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
	sub.multiWordScalars = e.multiWordScalars
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
	}
}

func (e *emitter) printf(format string, args ...any) {
	e.buf.WriteString(strings.Repeat(indentUnit, e.depth))
	fmt.Fprintf(&e.buf, format, args...)
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

func (e *emitter) leadingComments(comments []syntax.Comment) {
	for _, c := range comments {
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
	for _, c := range trailing[1:] {
		e.comment(c)
	}
}

func (e *emitter) printLinesWithTrailing(lines []string, trailing []syntax.Comment) {
	if len(lines) == 0 {
		for _, c := range trailing {
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
	for _, c := range trailing[1:] {
		e.comment(c)
	}
}

func (e *emitter) comment(c syntax.Comment) {
	text := c.Text
	if !strings.HasPrefix(text, "#") {
		text = "#" + text
	}
	e.printf("%s", text)
}

func (e *emitter) wrapperComments(s *syntax.Stmt) {
	e.leadingComments(classifyComments(s).leading)
}

// body emits a nested statement list, flushing comments that dangle
// before the block's closer.
func (e *emitter) body(stmts []*syntax.Stmt, dangling []syntax.Comment) {
	e.depth++
	for _, st := range stmts {
		e.stmt(st)
	}
	for _, c := range dangling {
		e.comment(c)
	}
	e.depth--
}

func (e *emitter) file(f *syntax.File) {
	var body bytes.Buffer
	origBuf := e.buf
	e.buf = body
	for _, stmt := range f.Stmts {
		e.stmt(stmt)
	}
	for _, c := range f.Last {
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

	if !e.noHelpers {
		for _, h := range allHelpers {
			if e.neededHelpers[h.kind] {
				e.buf.WriteString(h.code)
				if !strings.HasSuffix(h.code, "\n") {
					e.buf.WriteByte('\n')
				}
				e.buf.WriteByte('\n')
			}
		}
	}
	e.buf.Write(bodyBytes)
}

func (e *emitter) stmt(s *syntax.Stmt) {
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
		e.warn(cmd.Lparen, "subshell isolation is lost: (...) translated to a begin block")
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
		e.buf.WriteString(pad)
		e.buf.WriteString(l)
		e.buf.WriteByte('\n')
	}
}

// simple emits a plain command statement. The whole statement is rendered
// by the mvdan printer, which replays attached comments, negation,
// redirections, and the background marker -- all of which are also valid
// fish.
func (e *emitter) simple(s *syntax.Stmt) {
	if c, ok := s.Cmd.(*syntax.CallExpr); ok {
		if operand, negated, ok := matchTestDashV(c); ok {
			sc := classifyComments(s)
			e.leadingComments(sc.leading)
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
		if len(c.Args) > 0 {
			if isLitWord(c.Args[0], "getopts") {
				e.needHelper(helperGetopts)
			} else if isLitWord(c.Args[0], "hash") {
				e.needHelper(helperHash)
			} else if isLitWord(c.Args[0], "unalias") {
				e.needHelper(helperUnalias)
			} else if isLitWord(c.Args[0], "source") {
				e.needHelper(helperSource)
			} else if isLitWord(c.Args[0], ".") {
				e.needHelper(helperDot)
			} else if isLitWord(c.Args[0], "unset") {
				e.needHelper(helperUnset)
			}
		}
		e.warnBashOnlyBuiltin(s, c)
		if isSynthesizedSet(c) {
			sc := classifyComments(s)
			e.leadingComments(sc.leading)
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
		if len(c.Assigns) == 0 && e.isSetBuiltin(c) {
			if e.setCmd(s, c) {
				return
			}
		}
		if hasStructuralCmdSubst(s) || (e.inSubshell && hasHighFDTargetRedir(s.Redirs)) {
			e.emitSimpleFish(s, c)
			return
		}
		if len(c.Assigns) == 0 && len(c.Args) > 0 {
			if _, ok := singleBareParam(c.Args[0]); ok {
				e.needHelper(helperExec)
				if hasLeadingRedir(s) || (e.inSubshell && hasHighFDTargetRedir(s.Redirs)) {
					sc := classifyComments(s)
					e.leadingComments(sc.leading)
					line := e.render(s.Cmd)
					if s.Negated {
						line = "! " + line
					}
					line += e.tails(s)
					e.printLineWithTrailing("__bait_exec "+line, sc.trailing)
					return
				}
				e.lines("__bait_exec " + e.render(s))
				return
			}
		}
		if hasLeadingRedir(s) || (e.inSubshell && hasHighFDTargetRedir(s.Redirs)) {
			sc := classifyComments(s)
			e.leadingComments(sc.leading)
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
	sc := classifyComments(s)
	e.leadingComments(sc.leading)
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

// inlineStmtText renders one statement through the translator into a
// text chunk for embedding into a surrounding line.
func (e *emitter) inlineStmtText(s *syntax.Stmt) string {
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

// binary emits &&/||/pipe chains. Chains made only of simple commands are
// valid fish verbatim; a structural operand (rare) falls back.
func (e *emitter) binary(s *syntax.Stmt) {
	bcmd := s.Cmd.(*syntax.BinaryCmd)
	// A function definition always succeeds, so `f() { … } && cmd` is a
	// plain sequence in both shells; emit the two statements in order.
	if _, ok := bcmd.X.Cmd.(*syntax.FuncDecl); ok && bcmd.Op == syntax.AndStmt {
		e.stmt(bcmd.X)
		e.stmt(bcmd.Y)
		return
	}
	if hasStructural(bcmd.X) || hasStructural(bcmd.Y) {
		sc := classifyComments(s)
		e.leadingComments(sc.leading)
		line := fmt.Sprintf("%s %s %s%s", e.chainSideText(bcmd.X), binOpText(bcmd.Op),
			e.chainSideText(bcmd.Y), e.tails(s))
		e.printLineWithTrailing(line, sc.trailing)
		return
	}
	if chainNeedsRewrite(bcmd) {
		e.emitChain(s, bcmd)
		return
	}
	e.simple(s)
}

func chainNeedsRewrite(b *syntax.BinaryCmd) bool {
	found := false
	syntax.Walk(b, func(n syntax.Node) bool {
		if st, ok := n.(*syntax.Stmt); ok && (hasLeadingRedir(st) || hasHighFDTargetRedir(st.Redirs)) {
			found = true
			return false
		}
		c, ok := n.(*syntax.CallExpr)
		if !ok {
			return true
		}
		if len(c.Args) == 0 && len(c.Assigns) > 0 {
			found = true
			return false
		}
		if len(c.Args) > 0 && isLitWord(c.Args[0], "shift") {
			found = true
			return false
		}
		if len(c.Assigns) == 0 && len(c.Args) > 0 {
			if _, ok := singleBareParam(c.Args[0]); ok {
				found = true
				return false
			}
		}
		if _, _, ok := matchTestDashV(c); ok {
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
	sc := classifyComments(s)
	e.leadingComments(sc.leading)
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
// become translated fish blocks (multi-line), plain sides render as
// chain leaves.
func (e *emitter) chainSideText(st *syntax.Stmt) string {
	if !hasStructural(st) {
		if b, ok := st.Cmd.(*syntax.BinaryCmd); ok && chainNeedsRewrite(b) {
			return e.inlineStmtText(st)
		}
		return e.chainLeaf(st)
	}
	return e.inlineStmtText(st)
}

// chainLeaf renders one leaf of a combiner chain: pure assignments
// become set commands, everything else renders verbatim.
func (e *emitter) chainLeaf(st *syntax.Stmt) string {
	c, ok := st.Cmd.(*syntax.CallExpr)
	if isSynthesizedSet(c) {
		varName := c.Args[1].Parts[0].(*syntax.Lit).Value
		val := e.renderWordSmart(c.Args[2])
		scope := ""
		if e.inFunction && (e.funcLocals == nil || !e.funcLocals[varName]) {
			scope = "--global"
		}
		return setLineText(scope, varName, val) + e.tails(st)
	}
	if !ok || len(c.Args) != 0 || len(c.Assigns) == 0 {
		if operand, negated, ok := matchTestDashV(c); ok {
			line := e.renderTestDashV(operand, negated)
			if st.Negated {
				line = "! " + line
			}
			return line
		}
		if c != nil && len(c.Args) > 0 && isLitWord(c.Args[0], "shift") {
			txt := e.inlineStmtText(st)
			if strings.Contains(txt, "\n") {
				var parts []string
				for _, line := range strings.Split(txt, "\n") {
					if trimmed := strings.TrimSpace(line); trimmed != "" {
						parts = append(parts, trimmed)
					}
				}
				return "begin; " + strings.Join(parts, "; ") + "; end"
			}
			return txt
		}
		if c != nil && len(c.Assigns) == 0 && len(c.Args) > 0 {
			if _, ok := singleBareParam(c.Args[0]); ok {
				e.needHelper(helperExec)
				return "__bait_exec " + e.render(st)
			}
		}
		if hasStructuralCmdSubst(st) {
			return e.inlineStmtText(st)
		}
		if hasLeadingRedir(st) || (e.inSubshell && hasHighFDTargetRedir(st.Redirs)) {
			line := e.render(st.Cmd)
			if st.Negated {
				line = "! " + line
			}
			line += e.tails(st)
			return line
		}
		return e.render(st)
	}
	scope := ""
	if e.inFunction {
		scope = "--global"
	}
	var parts []string
	for _, a := range c.Assigns {
		if a.Name == nil || a.Append || a.Index != nil || a.Array != nil {
			e.warn(st.Position, "assignment in a combiner chain cannot be translated; emitted verbatim")
			return e.render(st)
		}
		curScope := scope
		if e.inFunction && e.funcLocals != nil && (e.funcLocals[a.Name.Value] || e.funcLocals[e.varName(a.Name.Value)]) {
			curScope = ""
		}
		parts = append(parts, fmt.Sprintf("set %s%s %s",
			scopePrefix(curScope), e.varName(a.Name.Value), e.assignValue(a)))
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "begin; " + strings.Join(parts, "; ") + "; end"
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
	sc := classifyComments(s)
	e.leadingComments(sc.leading)
	tail := e.tails(s)
	if fd.Body != nil && len(fd.Body.Redirs) > 0 {
		tail += e.tails(fd.Body)
	}
	e.printf("function %s", fd.Name.Value)
	saved := e.inFunction
	savedLocals := e.funcLocals
	e.inFunction = true
	e.funcLocals = make(map[string]bool)
	if bcmd, ok := fd.Body.Cmd.(*syntax.BinaryCmd); ok && bcmd.Op == syntax.AndStmt {
		if blk, ok := bcmd.X.Cmd.(*syntax.Block); ok {
			e.body(blk.Stmts, blk.Last)
			e.inFunction = saved
			e.funcLocals = savedLocals
			e.printEnd(tail, sc.trailing)
			e.stmt(bcmd.Y)
			return
		}
	}
	if blk, ok := fd.Body.Cmd.(*syntax.Block); ok {
		e.body(blk.Stmts, blk.Last)
	} else {
		e.body([]*syntax.Stmt{fd.Body}, nil)
	}
	e.inFunction = saved
	e.funcLocals = savedLocals
	e.printEnd(tail, sc.trailing)
}

func (e *emitter) group(s *syntax.Stmt, stmts []*syntax.Stmt, last []syntax.Comment) {
	sc := classifyComments(s)
	e.leadingComments(sc.leading)
	tail := e.tails(s)
	if s.Negated {
		e.printf("! begin")
	} else {
		e.printf("begin")
	}
	e.body(stmts, last)
	e.printEnd(tail, sc.trailing)
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
