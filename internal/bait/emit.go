package bait

import (
	"bytes"
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

const indentUnit = "    "

// emitter walks a parsed bash file and writes its fish translation.
//
// Structural nodes (if/while/for/case/function/blocks) are rewritten into
// fish syntax. Simple command statements are delegated to the mvdan
// printer verbatim, which preserves native fish constructs unchanged.
// Unsupported statements are emitted verbatim and reported as warnings.
type emitter struct {
	buf               bytes.Buffer
	depth             int
	printer           *syntax.Printer
	warnings          []Warning
	err               error
	noHelpers         bool
	inFunction        bool
	inSubshell        bool
	neededHelpers     [numHelpers]bool
	funcLocals        map[string]bool
	knownLists        map[string]bool
	multiWordScalars  map[string]bool
	commandPrefixVars map[string]bool
	knownFuncs        map[string]bool
	synthStmts        map[*syntax.Stmt]bool
	arrayAllWords     map[*syntax.Word]bool
	synthSets         map[*syntax.CallExpr]bool
	readTargets       map[*syntax.CallExpr][]string
	pairedFDRedirs    map[*syntax.Redirect]bool
	execRewrites      map[*syntax.Stmt]bool
	unsetShadows      map[*syntax.CallExpr]bool
	usedNames         map[string]syntax.Pos
	execFdPaths       map[int]string
	skipFDReads       map[*syntax.Stmt]bool
	needIFS           bool
	lastLine          uint
}

func (e *emitter) newSubEmitter() *emitter {
	sub := newEmitter()
	sub.noHelpers = e.noHelpers
	sub.inFunction = e.inFunction
	sub.inSubshell = e.inSubshell
	if e.funcLocals != nil {
		sub.funcLocals = make(map[string]bool, len(e.funcLocals))
		for k, v := range e.funcLocals {
			sub.funcLocals[k] = v
		}
	}
	sub.knownLists = e.knownLists
	sub.synthStmts = e.synthStmts
	sub.arrayAllWords = e.arrayAllWords
	sub.synthSets = e.synthSets
	sub.readTargets = e.readTargets
	sub.execRewrites = e.execRewrites
	sub.unsetShadows = e.unsetShadows
	sub.pairedFDRedirs = e.pairedFDRedirs
	sub.commandPrefixVars = e.commandPrefixVars
	sub.knownFuncs = e.knownFuncs
	return sub
}

func (e *emitter) inheritSub(sub *emitter) {
	for k, v := range sub.neededHelpers {
		if v {
			e.neededHelpers[k] = true
		}
	}
	e.warnings = append(e.warnings, sub.warnings...)
	if sub.err != nil && e.err == nil {
		e.err = sub.err
	}
}

func newEmitter() *emitter {
	return &emitter{
		printer:           syntax.NewPrinter(syntax.SpaceRedirects(true)),
		knownLists:        make(map[string]bool),
		multiWordScalars:  make(map[string]bool),
		commandPrefixVars: make(map[string]bool),
		knownFuncs:        make(map[string]bool),
		synthStmts:        make(map[*syntax.Stmt]bool),
		arrayAllWords:     make(map[*syntax.Word]bool),
		synthSets:         make(map[*syntax.CallExpr]bool),
		execRewrites:      make(map[*syntax.Stmt]bool),
		unsetShadows:      make(map[*syntax.CallExpr]bool),
		usedNames:         make(map[string]syntax.Pos),
		execFdPaths:       make(map[int]string),
		skipFDReads:       make(map[*syntax.Stmt]bool),
	}
}

func (e *emitter) printf(format string, args ...any) {
	if format != "" || len(args) > 0 {
		e.buf.WriteString(strings.Repeat(indentUnit, e.depth))
		fmt.Fprintf(&e.buf, format, args...)
	}
	e.buf.WriteByte('\n')
}

func (e *emitter) warn(pos syntax.Pos, format string, args ...any) {
	e.warnings = append(e.warnings, Warning{
		Line: int(pos.Line()),
		Col:  int(pos.Col()),
		Text: fmt.Sprintf(format, args...),
	})
}

// render prints any non-file node through the mvdan bash printer. The
// result is verbatim bash text; callers rely on it being fish-compatible.
func (e *emitter) render(node syntax.Node) string {
	if node == nil {
		return ""
	}
	var b bytes.Buffer
	if err := e.printer.Print(&b, node); err != nil && e.err == nil {
		e.err = err
	}
	return strings.TrimRight(b.String(), "\n")
}

type stmtComments struct {
	leading        []syntax.Comment
	headerTrailing *syntax.Comment
	trailing       []syntax.Comment
}

func classifyComments(s *syntax.Stmt) stmtComments {
	var sc stmtComments
	if s == nil || len(s.Comments) == 0 {
		return sc
	}
	startLine := s.Pos().Line()
	endLine := s.End().Line()
	for _, c := range s.Comments {
		cLine := c.Pos().Line()
		switch {
		case cLine < startLine:
			sc.leading = append(sc.leading, c)
		case startLine < endLine && cLine == startLine:
			if sc.headerTrailing == nil {
				cCopy := c
				sc.headerTrailing = &cCopy
			} else {
				sc.trailing = append(sc.trailing, c)
			}
		default:
			sc.trailing = append(sc.trailing, c)
		}
	}
	return sc
}

func (e *emitter) newline(pos syntax.Pos) {
	if !pos.IsValid() {
		return
	}
	line := pos.Line()
	if e.lastLine > 0 && line > e.lastLine+1 {
		e.buf.WriteByte('\n')
	}
	if line > e.lastLine {
		e.lastLine = line
	}
}

func (e *emitter) advanceLine(line uint) {
	if line > e.lastLine {
		e.lastLine = line
	}
}

func stmtStartPos(s *syntax.Stmt) syntax.Pos {
	if s == nil {
		return syntax.Pos{}
	}
	pos := s.Pos()
	if len(s.Comments) > 0 {
		cPos := s.Comments[0].Pos()
		if pos.After(cPos) {
			return cPos
		}
	}
	return pos
}

func stmtEndLine(s *syntax.Stmt) uint {
	if s == nil {
		return 0
	}
	end := s.End().Line()
	for _, c := range s.Comments {
		if c.End().Line() > end {
			end = c.End().Line()
		}
	}
	return end
}

func (e *emitter) prepareStmt(s *syntax.Stmt) stmtComments {
	sc := classifyComments(s)
	for _, c := range sc.leading {
		e.newline(c.Pos())
		e.comment(c)
	}
	e.newline(s.Pos())
	return sc
}

func (e *emitter) leadingComments(comments []syntax.Comment) {
	for _, c := range comments {
		e.newline(c.Pos())
		e.comment(c)
	}
}

func trailingCommentSuffix(c syntax.Comment) string {
	text := c.Text
	if !strings.HasPrefix(text, "#") {
		text = "#" + text
	}
	return " " + text
}

func (e *emitter) printLineWithTrailing(line string, trailing []syntax.Comment) {
	if len(trailing) == 0 {
		e.printf("%s", line)
		return
	}
	e.printf("%s%s", line, trailingCommentSuffix(trailing[0]))
	e.advanceLine(trailing[0].End().Line())
	for _, c := range trailing[1:] {
		e.newline(c.Pos())
		e.comment(c)
	}
}

func (e *emitter) printLinesWithTrailing(lines []string, trailing []syntax.Comment) {
	if len(lines) == 0 {
		for _, c := range trailing {
			e.newline(c.Pos())
			e.comment(c)
		}
		return
	}
	for i := range len(lines) - 1 {
		e.printf("%s", lines[i])
	}
	e.printLineWithTrailing(lines[len(lines)-1], trailing)
}

func (e *emitter) printEnd(tail string, trailing []syntax.Comment) {
	if len(trailing) == 0 {
		e.printf("end%s", tail)
		return
	}
	e.printf("end%s%s", tail, trailingCommentSuffix(trailing[0]))
	e.advanceLine(trailing[0].End().Line())
	for _, c := range trailing[1:] {
		e.newline(c.Pos())
		e.comment(c)
	}
}
func (e *emitter) printBraceClose(tail string, trailing []syntax.Comment) {
	if len(trailing) == 0 {
		e.printf("}%s", tail)
		return
	}
	e.printf("}%s%s", tail, trailingCommentSuffix(trailing[0]))
	e.advanceLine(trailing[0].End().Line())
	for _, c := range trailing[1:] {
		e.newline(c.Pos())
		e.comment(c)
	}
}

func (e *emitter) comment(c syntax.Comment) {
	text := c.Text
	if !strings.HasPrefix(text, "#") {
		text = "#" + text
	}
	e.printf("%s", text)
	e.advanceLine(c.End().Line())
}

func (e *emitter) wrapperComments(s *syntax.Stmt) {
	e.leadingComments(classifyComments(s).leading)
}

// body emits a nested statement list, flushing comments that dangle
// before the block's closer.
func (e *emitter) body(stmts []*syntax.Stmt, dangling []syntax.Comment) {
	e.depth++
	for _, st := range stmts {
		e.newline(stmtStartPos(st))
		e.stmt(st)
		e.advanceLine(stmtEndLine(st))
	}
	for _, c := range dangling {
		e.newline(c.Pos())
		e.comment(c)
	}
	e.depth--
}

func (e *emitter) file(f *syntax.File) {
	var body bytes.Buffer
	origBuf := e.buf
	e.buf = body
	for _, stmt := range f.Stmts {
		e.newline(stmtStartPos(stmt))
		e.stmt(stmt)
		e.advanceLine(stmtEndLine(stmt))
	}
	for _, c := range f.Last {
		e.newline(c.Pos())
		e.comment(c)
	}
	bodyBytes := e.buf.Bytes()
	e.buf = origBuf

	// If body begins with a shebang line (#!), write the shebang first.
	if bytes.HasPrefix(bodyBytes, []byte("#!")) {
		if idx := bytes.IndexByte(bodyBytes, '\n'); idx != -1 {
			e.buf.Write(bodyBytes[:idx+1])
			bodyBytes = bodyBytes[idx+1:]
		}
	}

	if e.needIFS {
		// fish's default IFS holds the same separators in a different
		// order; scripts that read or compare its value need bash's.
		e.printf("set --global IFS \" \t\n\"")
		e.printf("")
	}

	if !e.noHelpers {
		wroteHelper := false
		for _, h := range allHelpers {
			if e.neededHelpers[h.kind] {
				e.buf.WriteString(h.code)
				if !strings.HasSuffix(h.code, "\n") {
					e.buf.WriteByte('\n')
				}
				e.buf.WriteByte('\n')
				wroteHelper = true
			}
		}
		if wroteHelper {
			bodyBytes = bytes.TrimLeft(bodyBytes, "\n")
		}
	}
	e.buf.Write(bodyBytes)
}

func (e *emitter) stmt(s *syntax.Stmt) {
	e.markPairedFDRedirs(s)
	if s.Background {
		switch cmd := s.Cmd.(type) {
		case *syntax.FuncDecl:
			e.warn(s.Position, "fish does not support running functions in the background; emitted verbatim")
		case *syntax.CallExpr:
			if len(cmd.Args) > 0 {
				name := e.renderWordSmart(cmd.Args[0])
				if e.knownFuncs[name] {
					e.warn(s.Position, "fish does not support running functions in the background; emitted verbatim")
				}
			}
		}
	}
	switch cmd := s.Cmd.(type) {
	case *syntax.Block, *syntax.Subshell, *syntax.IfClause, *syntax.WhileClause, *syntax.ForClause, *syntax.CaseClause:
		e.checkHighFDRedir(s, "block")
	case *syntax.FuncDecl:
		e.checkHighFDRedir(s, "function")
		if cmd.Body != nil {
			e.checkHighFDRedir(cmd.Body, "function")
		}
	case *syntax.CallExpr:
		if len(cmd.Args) > 0 {
			name := e.renderWordSmart(cmd.Args[0])
			if e.knownFuncs[name] {
				e.checkHighFDRedir(s, "function")
			} else if isFishBuiltin(name) {
				e.checkHighFDRedir(s, "builtin")
			}
		}
	}
	if hdoc, rest := extractHdoc(s.Redirs); hdoc != nil {
		e.emitHdoc(s, hdoc, rest)
		return
	}
	switch cmd := s.Cmd.(type) {
	case nil:
		return
	case *syntax.CallExpr:
		e.applyReadScope(cmd)
		if len(cmd.Args) == 0 && len(cmd.Assigns) > 0 {
			e.assignStmt(s, cmd)
			break
		}
		e.simple(s)
	case *syntax.DeclClause:
		e.declClause(s, cmd)
	case *syntax.BinaryCmd:
		e.binary(s)
	case *syntax.IfClause:
		e.ifClause(s, cmd)
	case *syntax.WhileClause:
		e.whileClause(s, cmd)
	case *syntax.ForClause:
		e.forClause(s, cmd)
	case *syntax.CaseClause:
		e.caseClause(s, cmd)
	case *syntax.TestClause:
		e.testClause(s, cmd)
	case *syntax.FuncDecl:
		e.funcDecl(s, cmd)
	case *syntax.Block:
		e.group(s, cmd.Stmts, cmd.Last)
	case *syntax.Subshell:
		e.warn(cmd.Lparen, "subshell isolation is lost: (...) translated to a { ... } block")
		e.group(s, cmd.Stmts, cmd.Last)
	default:
		e.warn(s.Position, "%s has no fish equivalent; emitted verbatim", describe(cmd))
		e.lines(e.render(s))
	}
}

// lines writes s at the current indentation, indenting every line of a
// multi-line chunk (comments printed together with their statement).
func (e *emitter) lines(s string) {
	pad := strings.Repeat(indentUnit, e.depth)
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			e.buf.WriteString(pad)
			e.buf.WriteString(l)
		}
		e.buf.WriteByte('\n')
	}
}

// simple emits a plain command statement. The whole statement is rendered
// by the mvdan printer, which replays attached comments, negation,
// redirections, and the background marker -- all of which are also valid
// fish.
func (e *emitter) simple(s *syntax.Stmt) {
	if c, ok := s.Cmd.(*syntax.CallExpr); ok {
		if e.unsetShadows[c] {
			e.emitUnsetShadow(s, c)
			return
		}
		if operand, negated, ok := matchTestDashV(c); ok {
			sc := e.prepareStmt(s)
			line := e.renderTestDashV(operand, negated)
			if s.Negated {
				line = "! " + line
			}
			line += e.tails(s)
			e.printLineWithTrailing(line, sc.trailing)
			return
		}
		if len(c.Assigns) == 0 && len(c.Args) > 0 && isLitWord(c.Args[0], "shift") {
			e.shiftCmd(s, c)
			return
		}
		e.warnBashOnlyBuiltin(s, c)
		if e.isSynthesizedSet(c) {
			sc := e.prepareStmt(s)
			varName := c.Args[1].Parts[0].(*syntax.Lit).Value
			val := e.renderWordSmart(c.Args[2])
			scope := ""
			if e.inFunction && (e.funcLocals == nil || (!e.funcLocals[varName])) {
				scope = "--global"
			}
			line := setLineText(scope, varName, val) + e.tails(s)
			e.printLineWithTrailing(line, sc.trailing)
			return
		}
		if !e.synthStmts[s] && len(c.Assigns) == 0 && e.isSetBuiltin(c) {
			if e.setCmd(s, c) {
				return
			}
		}
		if e.hasStructuralCmdSubst(s) || (e.inSubshell && hasHighFDTargetRedir(s.Redirs)) {
			e.emitSimpleFish(s, c)
			return
		}
		if len(c.Assigns) == 0 && len(c.Args) > 0 {
			if _, ok := singleBareParam(c.Args[0]); ok {
				e.needHelper(helperExec)
				// Negation applies to the helper call, not inside the
				// dynamic command word: `! __bait_exec $cmd`.
				renderStmt := s
				prefix := ""
				if s.Negated {
					cp := *s
					cp.Negated = false
					renderStmt = &cp
					prefix = "! "
				}
				if hasLeadingRedir(s) || (e.inSubshell && hasHighFDTargetRedir(s.Redirs)) {
					sc := e.prepareStmt(s)
					line := e.render(renderStmt.Cmd) + e.tails(s)
					e.printLineWithTrailing(prefix+"__bait_exec "+line, sc.trailing)
					return
				}
				e.lines(prefix + "__bait_exec " + e.render(renderStmt))
				return
			}
		}
		if hasLeadingRedir(s) || (e.inSubshell && hasHighFDTargetRedir(s.Redirs)) {
			sc := e.prepareStmt(s)
			line := e.render(s.Cmd)
			if s.Negated {
				line = "! " + line
			}
			line += e.tails(s)
			e.printLineWithTrailing(line, sc.trailing)
			return
		}
	}
	e.lines(e.render(s))
}

// emitUnsetShadow renders an unset inside a substitution body. fish cannot
// erase a variable for one block only, so the caller's variable is preserved
// by emptying a block-local shadow instead: reads match bash (the name is
// empty), while set-ness tests still see the name as set. Function erasure,
// array elements, and dynamic names cannot be shadowed and are skipped so the
// caller's state stays intact.
func (e *emitter) emitUnsetShadow(s *syntax.Stmt, c *syntax.CallExpr) {
	names, rest := unsetShadowable(c)
	sc := e.prepareStmt(s)
	var parts []string
	for _, name := range names {
		parts = append(parts, "set --local "+mangleVarName(name))
	}
	if len(parts) == 0 {
		parts = append(parts, "true")
	}
	line := strings.Join(parts, "; ")
	if s.Negated {
		line = "! " + line
	}
	line += e.tails(s)
	if len(names) > 0 {
		msg := "unset inside a command substitution is emulated with a block-local empty variable; the caller's value is preserved, but set-ness tests still see the name as set"
		if e.depth > 0 {
			msg = "unset inside a nested block of a command substitution is only shadowed until the block ends; reads after it see the caller's value"
		}
		e.warn(c.Pos(), "%s", msg)
	}
	if rest {
		e.warn(c.Pos(), "unset of a function, an array element, a dynamic name, or a call with prefix assignments inside a command substitution cannot be scoped in fish; it is skipped so the caller's state is preserved")
	}
	e.printLineWithTrailing(line, sc.trailing)
}

func hasLeadingRedir(s *syntax.Stmt) bool {
	if s == nil || s.Cmd == nil || len(s.Redirs) == 0 {
		return false
	}
	cmdPos := s.Cmd.Pos()
	for _, r := range s.Redirs {
		rp := r.Pos()
		if rp.Line() < cmdPos.Line() || (rp.Line() == cmdPos.Line() && rp.Col() < cmdPos.Col()) {
			return true
		}
	}
	return false
}

// emitSimpleFish renders a simple command whose arguments or env-prefix
// assignments contain structural command substitutions.
func (e *emitter) emitSimpleFish(s *syntax.Stmt, c *syntax.CallExpr) {
	sc := e.prepareStmt(s)
	var parts []string
	if s.Negated {
		parts = append(parts, "!")
	}
	for _, a := range c.Assigns {
		parts = append(parts, a.Name.Value+"="+e.assignValue(a))
	}
	for _, w := range c.Args {
		parts = append(parts, e.renderWordSmart(w))
	}
	line := strings.Join(parts, " ") + e.tails(s)
	e.printLineWithTrailing(line, sc.trailing)
}

// stmtText renders one statement through the translator into a text chunk
// for embedding into a surrounding line. Every context that needs a
// statement's fish text (chain leaves, conditions, command substitutions)
// uses this function so all rewrites apply uniformly.
func (e *emitter) stmtText(s *syntax.Stmt) string {
	sub := e.newSubEmitter()
	sub.stmt(s)
	e.inheritSub(sub)
	text := strings.TrimRight(sub.buf.String(), "\n")
	pad := strings.Repeat(indentUnit, e.depth)
	lines := strings.Split(text, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

// nonEmptyLines splits text into trimmed, non-empty lines.
func nonEmptyLines(text string) []string {
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(l); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

// synthCmdSubst wraps statements the translator generated inside a command
// substitution. Generated commands are already fish text; emission-time
// rewrites (the set builtin in particular) must not treat them as source.
func (e *emitter) synthCmdSubst(st *syntax.Stmt) *syntax.CmdSubst {
	e.markSynth(st)
	return &syntax.CmdSubst{Stmts: []*syntax.Stmt{st}}
}

// markSynth records a generated statement and everything nested in it.
func (e *emitter) markSynth(st *syntax.Stmt) {
	syntax.Walk(st, func(n syntax.Node) bool {
		if inner, ok := n.(*syntax.Stmt); ok {
			e.synthStmts[inner] = true
		}
		return true
	})
}

// binary emits &&/||/pipe chains. Chains made only of simple commands are
// valid fish verbatim; a structural operand (rare) falls back.
func (e *emitter) binary(s *syntax.Stmt) {
	bcmd := s.Cmd.(*syntax.BinaryCmd)
	if hasStructural(bcmd.X) || hasStructural(bcmd.Y) {
		sc := e.prepareStmt(s)
		line := fmt.Sprintf("%s %s %s%s", e.chainSideText(bcmd.X), binOpText(bcmd.Op),
			e.chainSideText(bcmd.Y), e.tails(s))
		e.printLineWithTrailing(line, sc.trailing)
		return
	}
	if e.chainNeedsRewrite(bcmd) {
		e.emitChain(s, bcmd)
		return
	}
	e.simple(s)
}

// chainNeedsRewrite reports whether a combiner chain contains a statement
// the translator must rewrite. It walks through stmtNeedsRewrite so the
// chain and statement entry points decide identically.
func (e *emitter) chainNeedsRewrite(b *syntax.BinaryCmd) bool {
	found := false
	syntax.Walk(b, func(n syntax.Node) bool {
		if found {
			return false
		}
		st, ok := n.(*syntax.Stmt)
		if !ok {
			return true
		}
		if e.stmtNeedsRewrite(st) || e.hasStructuralCmdSubst(st) {
			found = true
			return false
		}
		return true
	})
	return found
}

// emitChain renders an &&/||/| chain leaf by leaf so that assignment
// leaves can become set commands; plain command leaves stay verbatim.
func (e *emitter) emitChain(s *syntax.Stmt, b *syntax.BinaryCmd) {
	sc := e.prepareStmt(s)
	var leaves []*syntax.Stmt
	var ops []syntax.BinCmdOperator
	var walk func(*syntax.BinaryCmd)
	walk = func(b *syntax.BinaryCmd) {
		if xb, ok := b.X.Cmd.(*syntax.BinaryCmd); ok {
			walk(xb)
		} else {
			leaves = append(leaves, b.X)
		}
		ops = append(ops, b.Op)
		if yb, ok := b.Y.Cmd.(*syntax.BinaryCmd); ok {
			walk(yb)
		} else {
			leaves = append(leaves, b.Y)
		}
	}
	walk(b)
	parts := make([]string, 0, 2*len(leaves))
	for i, leaf := range leaves {
		if i > 0 {
			parts = append(parts, binOpText(ops[i-1]))
		}
		parts = append(parts, e.chainLeaf(leaf))
	}
	e.printLineWithTrailing(strings.Join(parts, " "), sc.trailing)
}

func binOpText(op syntax.BinCmdOperator) string {
	switch op {
	case syntax.AndStmt:
		return "&&"
	case syntax.OrStmt:
		return "||"
	case syntax.Pipe:
		return "|"
	case syntax.PipeAll:
		return "|&"
	}
	return ";"
}

// chainSideText renders one side of a combiner/pipe: structural sides
// become translated fish blocks, everything else renders as a chain leaf.
func (e *emitter) chainSideText(st *syntax.Stmt) string {
	if hasStructural(st) {
		return e.stmtText(st)
	}
	return e.chainLeaf(st)
}

// chainLeaf renders one leaf of a combiner chain through the statement
// translator, collapsing a multi-line translation into an inline block so
// the surrounding `&&`/`||`/`|` operators keep their binding. Comments
// force a multi-line block, since a `#` cannot share a line.
func (e *emitter) chainLeaf(st *syntax.Stmt) string {
	text := e.stmtText(st)
	lines := nonEmptyLines(text)
	if len(lines) == 0 {
		return "true"
	}
	if len(lines) == 1 {
		return lines[0]
	}
	if lines[0] == "{" && lines[len(lines)-1] == "}" {
		return text
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "#") {
			pad := strings.Repeat(indentUnit, e.depth+1)
			endPad := strings.Repeat(indentUnit, e.depth)
			var b strings.Builder
			b.WriteString("{\n")
			for _, l := range lines {
				b.WriteString(pad)
				b.WriteString(l)
				b.WriteByte('\n')
			}
			b.WriteString(endPad)
			b.WriteString("}")
			return b.String()
		}
	}
	return "{ " + strings.Join(lines, "; ") + "; }"
}

func hasStructural(s *syntax.Stmt) bool {
	if s == nil {
		return false
	}
	switch cmd := s.Cmd.(type) {
	case *syntax.CallExpr:
		return false
	case *syntax.BinaryCmd:
		return hasStructural(cmd.X) || hasStructural(cmd.Y)
	default:
		return true
	}
}

func (e *emitter) funcDecl(s *syntax.Stmt, fd *syntax.FuncDecl) {
	sc := e.prepareStmt(s)
	bodyStmts := []*syntax.Stmt{fd.Body}
	var bodyLast []syntax.Comment
	bodyTails := ""
	if fd.Body != nil {
		bodyTails = e.tails(fd.Body)
	}
	var op syntax.BinCmdOperator
	var rhs *syntax.Stmt
	switch cmd := fd.Body.Cmd.(type) {
	case *syntax.Block:
		bodyStmts, bodyLast = cmd.Stmts, cmd.Last
	case *syntax.BinaryCmd:
		// mvdan attaches a trailing `&&`/`||`/`|` to the function body.
		// The operator applies to the definition command, which always
		// succeeds, so it is emitted after `end`, never inside the body.
		if blk, ok := cmd.X.Cmd.(*syntax.Block); ok {
			bodyStmts, bodyLast = blk.Stmts, blk.Last
			bodyTails = e.tails(cmd.X)
			op, rhs = cmd.Op, cmd.Y
		}
	}
	e.printf("function %s", fd.Name.Value)
	e.advanceLine(fd.Pos().Line())
	saved := e.inFunction
	savedLocals := e.funcLocals
	e.inFunction = true
	e.funcLocals = make(map[string]bool)
	if bodyTails != "" {
		// Redirections attached to a definition apply whenever the
		// function is invoked, so they wrap the body, not the definition.
		e.printf("{")
		e.body(bodyStmts, bodyLast)
		e.printBraceClose(bodyTails, nil)
	} else {
		e.body(bodyStmts, bodyLast)
	}
	e.inFunction = saved
	e.funcLocals = savedLocals
	e.printEnd("", sc.trailing)
	if op == 0 || rhs == nil {
		return
	}
	e.newline(stmtStartPos(rhs))
	switch op {
	case syntax.AndStmt:
		// The definition always succeeds, so the `&&` branch runs
		// unconditionally.
		e.stmt(rhs)
	default:
		if op == syntax.Pipe || op == syntax.PipeAll {
			e.warn(fd.Pos(), "function defined in a pipeline stays defined in the current shell; bash defines it in a subshell")
		}
		e.printLineWithTrailing("true "+binOpText(op)+" "+e.chainLeaf(rhs), nil)
	}
	e.advanceLine(stmtEndLine(rhs))
}

func (e *emitter) group(s *syntax.Stmt, stmts []*syntax.Stmt, last []syntax.Comment) {
	sc := e.prepareStmt(s)
	tail := e.tails(s)
	if s.Negated {
		e.printf("! {")
	} else {
		e.printf("{")
	}
	e.advanceLine(s.Pos().Line())
	e.body(stmts, last)
	e.printBraceClose(tail, sc.trailing)
}

func describe(cmd syntax.Command) string {
	switch c := cmd.(type) {
	case *syntax.ArithmCmd:
		return "arithmetic command ((...))"
	case *syntax.DeclClause:
		return c.Variant.Value + " declaration"
	case *syntax.TestClause:
		return "[[ ]] test clause"
	case *syntax.LetClause:
		return "let statement"
	case *syntax.TimeClause:
		return "time clause"
	case *syntax.CoprocClause:
		return "coproc clause"
	default:
		return fmt.Sprintf("%T", cmd)
	}
}

func describeSelect(f *syntax.ForClause) string {
	if f.Select {
		return "select loop"
	}
	return "C-style for loop"
}
