package bait

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

const indentUnit = "    "

// emitter walks a parsed bash file and writes its fish translation.
//
// Structural nodes (if/while/for/case/function/blocks) are rewritten into
// fish syntax. Simple command statements are delegated to the mvdan
// printer verbatim, which preserves tier-0 constructs unchanged.
// Unsupported statements are emitted verbatim and reported as warnings.
type emitter struct {
	buf               bytes.Buffer
	depth             int
	printer           *syntax.Printer
	warnings          []Warning
	err               error
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
// result is verbatim bash text; callers rely on it being fish-compatible
// for tier-0 constructs.
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

// redirect renders a single redirection manually: the mvdan printer
// cannot print a bare *Redirect node.
func isHighFDTarget(r *syntax.Redirect) (int, bool) {
	if r.Op != syntax.DplOut && r.Op != syntax.DplIn {
		return 0, false
	}
	if r.Word == nil || len(r.Word.Parts) != 1 {
		return 0, false
	}
	lit, ok := r.Word.Parts[0].(*syntax.Lit)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(lit.Value)
	if err != nil || n <= 2 {
		return 0, false
	}
	return n, true
}

func hasHighFDTargetRedir(redirs []*syntax.Redirect) bool {
	for _, r := range redirs {
		if _, ok := isHighFDTarget(r); ok {
			return true
		}
	}
	return false
}

func stmtHasHighFDTargetRedir(st *syntax.Stmt) bool {
	if st == nil {
		return false
	}
	if hasHighFDTargetRedir(st.Redirs) {
		return true
	}
	found := false
	syntax.Walk(st.Cmd, func(n syntax.Node) bool {
		if s, ok := n.(*syntax.Stmt); ok {
			if hasHighFDTargetRedir(s.Redirs) {
				found = true
				return false
			}
		}
		return !found
	})
	return found
}

func stmtHasTargetFD(st *syntax.Stmt, target int) bool {
	if st == nil {
		return false
	}
	for _, r := range st.Redirs {
		if n, ok := isHighFDTarget(r); ok && n == target {
			return true
		}
	}
	found := false
	syntax.Walk(st.Cmd, func(n syntax.Node) bool {
		if s, ok := n.(*syntax.Stmt); ok {
			for _, r := range s.Redirs {
				if n, ok := isHighFDTarget(r); ok && n == target {
					found = true
					return false
				}
			}
		}
		return !found
	})
	return found
}

func (e *emitter) isPairedHighFDRedir(s *syntax.Stmt, r *syntax.Redirect) bool {
	if r.Op != syntax.DplOut || r.N == nil {
		return false
	}
	fd, err := strconv.Atoi(r.N.Value)
	if err != nil || fd <= 2 {
		return false
	}
	if r.Word == nil || len(r.Word.Parts) != 1 {
		return false
	}
	lit, ok := r.Word.Parts[0].(*syntax.Lit)
	if !ok || lit.Value != "1" {
		return false
	}
	found := false
	syntax.Walk(s.Cmd, func(n syntax.Node) bool {
		if cs, ok := n.(*syntax.CmdSubst); ok {
			for _, st := range cs.Stmts {
				if stmtHasTargetFD(st, fd) {
					found = true
					return false
				}
			}
		}
		return !found
	})
	return found
}

// redirect renders a single redirection manually: the mvdan printer
// cannot print a bare *Redirect node.
func (e *emitter) redirect(r *syntax.Redirect) string {
	s := ""
	if r.N != nil {
		s += r.N.Value
	}
	s += r.Op.String()
	word := e.renderWordSmart(r.Word)
	if e.inSubshell && r.Op == syntax.DplOut {
		if _, ok := isHighFDTarget(r); ok {
			word = "2"
		}
	}
	return s + " " + word
}

// tails renders the redirections and background marker that trail a
// structural statement, for appending to its closing line.
func (e *emitter) tails(s *syntax.Stmt) string {
	out := ""
	for _, r := range s.Redirs {
		if e.isPairedHighFDRedir(s, r) {
			continue
		}
		out += " " + e.redirect(r)
	}
	if s.Background {
		out += " &"
	}
	return out
}

func (e *emitter) checkHighFDRedir(s *syntax.Stmt, kind string) {
	for _, r := range s.Redirs {
		if e.isPairedHighFDRedir(s, r) {
			continue
		}
		if r.N != nil {
			if n, err := strconv.Atoi(r.N.Value); err == nil && n > 2 {
				e.warn(r.Pos(), "redirection to file descriptor %d on %s is not supported in fish; emitted verbatim", n, kind)
			}
		}
	}
}

var fishBuiltins = map[string]bool{
	"alias": true, "and": true, "argparse": true, "begin": true, "bg": true,
	"bind": true, "block": true, "breakpoint": true, "builtin": true, "case": true,
	"cd": true, "command": true, "commandline": true, "complete": true, "contains": true,
	"continue": true, "count": true, "disown": true, "echo": true, "else": true,
	"emit": true, "end": true, "eval": true, "exec": true, "exit": true,
	"false": true, "fg": true, "fish": true, "fish_indent": true, "fish_opt": true,
	"for": true, "function": true, "functions": true, "history": true, "if": true,
	"jobs": true, "math": true, "not": true, "or": true, "path": true,
	"printf": true, "pwd": true, "random": true, "read": true, "realpath": true,
	"return": true, "set": true, "set_color": true, "shift": true, "source": true,
	".": true, "status": true, "string": true, "switch": true, "test": true,
	"[": true, "time": true, "true": true, "type": true, "ulimit": true,
	"umask": true, "unset": true, "vared": true, "wait": true, "while": true,
}

func isFishBuiltin(name string) bool {
	return fishBuiltins[name]
}
func (e *emitter) comment(c syntax.Comment) {
	text := c.Text
	if !strings.HasPrefix(text, "#") {
		text = "#" + text
	}
	e.printf("%s", text)
}

func (e *emitter) wrapperComments(s *syntax.Stmt) {
	for _, c := range s.Comments {
		e.comment(c)
	}
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

// condText renders an if/while condition: a single statement verbatim,
// several statements wrapped in an inline begin block (fish conditions
// take a single job).
func (e *emitter) condText(cond []*syntax.Stmt) string {
	if len(cond) == 1 {
		st := cond[0]
		if hasStructural(st) {
			return e.inlineStmtText(st)
		}
		if c, ok := st.Cmd.(*syntax.CallExpr); ok {
			e.warnBashOnlyBuiltin(st, c)
			if _, _, ok := matchTestDashV(c); ok {
				return e.inlineStmtText(st)
			}
			if len(c.Args) == 0 && len(c.Assigns) > 0 {
				assignTxt := e.inlineStmtText(st)
				if st.Negated {
					return "! " + assignTxt
				}
				return assignTxt
			}
			if hasStructuralCmdSubst(st) {
				return e.inlineStmtText(st)
			}
		}
		return e.render(st)
	}
	parts := make([]string, len(cond))
	for i, st := range cond {
		parts[i] = e.render(st)
	}
	return "begin " + strings.Join(parts, "; ") + "; end"
}

func (e *emitter) file(f *syntax.File) {
	syntax.Walk(f, func(n syntax.Node) bool {
		if fd, ok := n.(*syntax.FuncDecl); ok && fd.Name != nil {
			e.knownFuncs[fd.Name.Value] = true
		}
		if c, ok := n.(*syntax.CallExpr); ok && len(c.Assigns) == 0 && len(c.Args) > 0 {
			if isLitWord(c.Args[0], "hash") {
				e.needHelper(helperHash)
			} else if isLitWord(c.Args[0], "unalias") {
				e.needHelper(helperUnalias)
			}
		}
		return true
	})
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

	for _, h := range allHelpers {
		if e.neededHelpers[h.kind] {
			e.buf.WriteString(h.code)
			if !strings.HasSuffix(h.code, "\n") {
				e.buf.WriteByte('\n')
			}
			e.buf.WriteByte('\n')
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
func matchTestDashV(c *syntax.CallExpr) (*syntax.Word, bool, bool) {
	if c == nil || len(c.Args) == 0 {
		return nil, false, false
	}
	isBracket := isLitWord(c.Args[0], "[")
	isTest := isLitWord(c.Args[0], "test")
	if !isBracket && !isTest {
		return nil, false, false
	}
	args := c.Args[1:]
	if isBracket {
		if len(args) == 0 || !isLitWord(args[len(args)-1], "]") {
			return nil, false, false
		}
		args = args[:len(args)-1]
	}
	if len(args) == 2 && isLitWord(args[0], "-v") {
		return args[1], false, true
	}
	if len(args) == 3 && isLitWord(args[0], "!") && isLitWord(args[1], "-v") {
		return args[2], true, true
	}
	return nil, false, false
}

func (e *emitter) renderTestDashV(operand *syntax.Word, negated bool) string {
	raw := e.render(operand)
	unquoted := unquoteArg(raw)
	shifted := e.shiftArrayIndex(operand.Pos(), unquoted)
	res := "set -q " + shifted
	if negated {
		res = "! " + res
	}
	return res
}

func (e *emitter) simple(s *syntax.Stmt) {
	if c, ok := s.Cmd.(*syntax.CallExpr); ok {
		if operand, negated, ok := matchTestDashV(c); ok {
			line := e.renderTestDashV(operand, negated)
			if s.Negated {
				line = "! " + line
			}
			line += e.tails(s)
			e.lines(line)
			return
		}
		if len(c.Assigns) == 0 && len(c.Args) > 0 && isLitWord(c.Args[0], "shift") {
			e.shiftCmd(s, c)
			return
		}
		if len(c.Assigns) == 0 && len(c.Args) > 0 && isLitWord(c.Args[0], "unset") {
			e.unsetCmd(s, c)
			return
		}
		if len(c.Assigns) == 0 && len(c.Args) > 0 {
			if isLitWord(c.Args[0], "getopts") {
				e.needHelper(helperGetopts)
			} else if isLitWord(c.Args[0], "hash") {
				e.needHelper(helperHash)
			} else if isLitWord(c.Args[0], "unalias") {
				e.needHelper(helperUnalias)
			}
		}
		e.warnBashOnlyBuiltin(s, c)
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
				e.needHelper(helperWords)
				if hasLeadingRedir(s) || (e.inSubshell && hasHighFDTargetRedir(s.Redirs)) {
					line := e.render(s.Cmd)
					if s.Negated {
						line = "! " + line
					}
					line += e.tails(s)
					e.lines("__bait_exec " + line)
					return
				}
				e.lines("__bait_exec " + e.render(s))
				return
			}
		}
		if hasLeadingRedir(s) || (e.inSubshell && hasHighFDTargetRedir(s.Redirs)) {
			line := e.render(s.Cmd)
			if s.Negated {
				line = "! " + line
			}
			line += e.tails(s)
			e.lines(line)
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

// warnBashOnlyBuiltin flags bash builtins that fish lacks entirely (or
// whose fish counterpart differs fatally). They are emitted verbatim
// with a warning: silent passthrough would only fail at runtime.
var bashOnlyBuiltins = map[string]string{
	"let":     "use 'math' instead",
	"shopt":   "fish has no shell options",
	"caller":  "use 'status print-stack-trace' instead",
	"compgen": "use 'complete' instead",
	"compopt": "use 'complete' instead",
	"enable":  "fish does not support enabling/disabling builtins",
	"fc":      "use 'history' instead",
}

func (e *emitter) warnBashOnlyBuiltin(s *syntax.Stmt, c *syntax.CallExpr) {
	if len(c.Args) == 0 || len(c.Assigns) != 0 || c.Args[0].Pos().Col() == 0 {
		return
	}
	name := e.render(c.Args[0])
	if hint, ok := bashOnlyBuiltins[name]; ok {
		e.warn(s.Position, "%s is a bash builtin with no fish equivalent (%s); emitted verbatim", name, hint)
	}
}

// isSetBuiltin reports whether the command invokes bash's set builtin,
// including the bare argument-less form.
func (e *emitter) isSetBuiltin(c *syntax.CallExpr) bool {
	if len(c.Args) == 0 {
		return true
	}
	// Synthesized `set` commands produced by the translator's own
	// rewrites (arithmetic statements, assignments) carry no source
	// position; only user-written set builtins are rewritten.
	if c.Args[0].Pos().Col() == 0 {
		return false
	}
	return e.render(c.Args[0]) == "set"
}

func (e *emitter) shiftCmd(s *syntax.Stmt, c *syntax.CallExpr) {
	tail := e.tails(s)
	if len(c.Args) <= 1 {
		e.printf("set --erase argv[1]%s", tail)
		return
	}
	arg := e.render(c.Args[1])
	if n, err := strconv.Atoi(arg); err == nil {
		switch {
		case n <= 0:
			e.printf("true%s", tail)
		case n == 1:
			e.printf("set --erase argv[1]%s", tail)
		default:
			e.printf("set --erase argv[1..%d]%s", n, tail)
		}
		return
	}
	e.printf("set --erase argv[1..%s]%s", arg, tail)
}
func (e *emitter) unsetCmd(s *syntax.Stmt, c *syntax.CallExpr) {
	tail := e.tails(s)
	isFunc := false
	stopFlags := false

	type unsetChunk struct {
		isFunc bool
		names  []string
	}
	var chunks []unsetChunk

	for _, argWord := range c.Args[1:] {
		arg := e.render(argWord)
		unquoted := unquoteArg(arg)
		if !stopFlags && strings.HasPrefix(arg, "-") && len(arg) > 1 {
			if arg == "--" {
				stopFlags = true
				continue
			}
			if strings.Contains(arg, "f") {
				isFunc = true
			}
			if strings.Contains(arg, "v") || strings.Contains(arg, "n") {
				isFunc = false
			}
			continue
		}
		name := unquoted
		if !isFunc {
			name = e.shiftArrayIndex(s.Position, unquoted)
		}
		if len(chunks) > 0 && chunks[len(chunks)-1].isFunc == isFunc {
			chunks[len(chunks)-1].names = append(chunks[len(chunks)-1].names, name)
		} else {
			chunks = append(chunks, unsetChunk{isFunc: isFunc, names: []string{name}})
		}
	}

	for _, chunk := range chunks {
		if len(chunk.names) == 0 {
			continue
		}
		if chunk.isFunc {
			e.printf("functions --erase %s%s", strings.Join(chunk.names, " "), tail)
		} else {
			e.printf("set --erase %s%s", strings.Join(chunk.names, " "), tail)
		}
	}
}

func unquoteArg(arg string) string {
	if (strings.HasPrefix(arg, "'") && strings.HasSuffix(arg, "'")) ||
		(strings.HasPrefix(arg, `"`) && strings.HasSuffix(arg, `"`)) {
		if len(arg) >= 2 {
			return arg[1 : len(arg)-1]
		}
	}
	return arg
}

func (e *emitter) shiftArrayIndex(pos syntax.Pos, name string) string {
	idxStart := strings.IndexByte(name, '[')
	idxEnd := strings.LastIndexByte(name, ']')
	if idxStart != -1 && idxEnd > idxStart {
		arrName := e.varName(name[:idxStart])
		sub := name[idxStart+1 : idxEnd]
		if n, err := strconv.Atoi(sub); err == nil {
			if n >= 0 {
				return fmt.Sprintf("%s[%d]", arrName, n+1)
			}
			return fmt.Sprintf("%s[%d]", arrName, n)
		}
		e.warn(pos, "dynamic array index cannot be shifted; emitted verbatim")
		return fmt.Sprintf("%s[%s]", arrName, sub)
	}
	return e.varName(name)
}
// `set args...` form assign fish's argv list; option forms and the bare
// `set` state dump have no fish equivalent and are dropped. Reports
// whether the statement was fully handled; when false, c.Args has been
// rewritten to the `set argv ...` form for normal rendering.
func (e *emitter) setCmd(s *syntax.Stmt, c *syntax.CallExpr) bool {
	args := c.Args[1:] // drop the command word
	if len(args) == 0 {
		e.warn(s.Position, "set with no arguments prints shell state; statement dropped")
		return true
	}
	head := e.render(args[0])
	if head == "--" {
		args = args[1:]
	} else if head == "-" && len(args) > 1 {
		args = args[1:]
	} else if strings.HasPrefix(head, "-") || strings.HasPrefix(head, "+") {
		e.warn(s.Position, "set flags have no fish equivalent; statement dropped")
		return true
	}
	out := make([]*syntax.Word, 0, len(args)+2)
	out = append(out, litWord("set"), litWord("argv"))
	out = append(out, args...)
	c.Args = out
	return false
}

// emitSimpleFish renders a simple command whose arguments or env-prefix
// assignments contain structural command substitutions.
func (e *emitter) emitSimpleFish(s *syntax.Stmt, c *syntax.CallExpr) {
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
	e.lines(line)
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

// hasStructuralCmdSubst reports whether any command substitution or process
// substitution inside n contains structural statements or requires translation.
func hasStructuralCmdSubst(n syntax.Node) bool {
	if n == nil {
		return false
	}
	found := false
	syntax.Walk(n, func(inner syntax.Node) bool {
		switch ps := inner.(type) {
		case *syntax.ProcSubst:
			found = true
			return false
		case *syntax.CmdSubst:
			for _, st := range ps.Stmts {
				if hasStructural(st) || stmtNeedsRewrite(st) || stmtHasHighFDTargetRedir(st) {
					found = true
					return false
				}
			}
		}
		return true
	})
	return found
}

func stmtNeedsRewrite(st *syntax.Stmt) bool {
	if st == nil {
		return false
	}
	if hasLeadingRedir(st) || hasHighFDTargetRedir(st.Redirs) {
		return true
	}
	if b, ok := st.Cmd.(*syntax.BinaryCmd); ok && chainNeedsRewrite(b) {
		return true
	}
	if c, ok := st.Cmd.(*syntax.CallExpr); ok {
		if len(c.Assigns) == 0 && len(c.Args) > 0 {
			if _, ok := singleBareParam(c.Args[0]); ok {
				return true
			}
		}
	}
	return false
}

// renderWordSmart renders a word as fish text. Words whose command
// substitutions contain structural statements are emitted through the
// translator; everything else keeps the printer's verbatim output.
func (e *emitter) renderWordSmart(w *syntax.Word) string {
	if w == nil {
		return ""
	}
	if !hasStructuralCmdSubst(w) {
		return e.render(w)
	}
	if out, ok := e.renderWordFish(w); ok {
		return out
	}
	return e.render(w)
}

// renderWordFish renders word parts manually so that command
// substitutions can carry translated fish bodies. ok is false for part
// combinations without a fish rendering; callers fall back to the
// printer.
func (e *emitter) renderWordFish(w *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, p := range w.Parts {
		switch p := p.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			if p.Dollar {
				return "", false
			}
			b.WriteString("'" + p.Value + "'")
		case *syntax.DblQuoted:
			if p.Dollar {
				return "", false
			}
			b.WriteString("\"")
			for _, ip := range p.Parts {
				switch ip := ip.(type) {
				case *syntax.Lit:
					b.WriteString(ip.Value)
				case *syntax.CmdSubst:
					s, ok := e.cmdSubstText(ip)
					if !ok {
						return "", false
					}
					b.WriteString(s)
				case *syntax.ProcSubst:
					s, ok := e.procSubstText(ip)
					if !ok {
						return "", false
					}
					if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
						s = "$" + s
					}
					b.WriteString(s)
				case *syntax.ParamExp:
					b.WriteString(e.render(ip))
				default:
					return "", false
				}
			}
			b.WriteString("\"")
		case *syntax.CmdSubst:
			s, ok := e.cmdSubstText(p)
			if !ok {
				return "", false
			}
			b.WriteString(s)
		case *syntax.ProcSubst:
			s, ok := e.procSubstText(p)
			if !ok {
				return "", false
			}
			b.WriteString(s)
		case *syntax.ParamExp:
			b.WriteString(e.render(p))
		default:
			return "", false
		}
	}
	return b.String(), true
}

// cmdSubstText renders a command substitution as $( ... ) with the body
// emitted through the statement translator.
func (e *emitter) cmdSubstText(cs *syntax.CmdSubst) (string, bool) {
	if cs.Backquotes {
		return "", false
	}
	sub := e.newSubEmitter()
	sub.inSubshell = true
	for _, st := range cs.Stmts {
		sub.stmt(st)
	}
	for _, c := range cs.Last {
		sub.comment(c)
	}
	e.inheritSub(sub)
	if sub.err != nil {
		if e.err == nil {
			e.err = sub.err
		}
		return "", false
	}
	text := strings.TrimRight(sub.buf.String(), "\n")
	if text == "" {
		return "", true
	}
	pad := strings.Repeat(indentUnit, e.depth)
	lines := strings.Split(text, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = pad + lines[i]
	}
	return "$(" + strings.Join(lines, "\n") + ")", true
}

// procSubstText renders a process substitution <( ... ) as ( ... | psub ).
func (e *emitter) procSubstText(ps *syntax.ProcSubst) (string, bool) {
	if ps.Op == syntax.CmdOut {
		e.warn(ps.OpPos, "output process substitution >(...) has no direct fish equivalent; emitted verbatim")
		return e.render(ps), true
	}
	if len(ps.Stmts) == 0 {
		return "(true | psub)", true
	}
	sub := e.newSubEmitter()
	sub.inSubshell = true
	for _, st := range ps.Stmts {
		sub.stmt(st)
	}
	for _, c := range ps.Last {
		sub.comment(c)
	}
	e.inheritSub(sub)
	if sub.err != nil {
		if e.err == nil {
			e.err = sub.err
		}
		return "", false
	}
	text := strings.TrimRight(sub.buf.String(), "\n")
	if text == "" {
		return "(true | psub)", true
	}
	if len(ps.Stmts) == 1 && !strings.Contains(text, "\n") && !hasStructural(ps.Stmts[0]) {
		return "(" + text + " | psub)", true
	}
	pad := strings.Repeat(indentUnit, e.depth+1)
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	endPad := strings.Repeat(indentUnit, e.depth)
	return "(begin\n" + strings.Join(lines, "\n") + "\n" + endPad + "end | psub)", true
}

// fishReservedVars maps user variable names from Bash that collide with
// Fish built-in read-only variables to safe mangled names.
// Fish-internal variables (e.g. fish_pid, fish_killring) and mutable variables
// (e.g. HOME, USER, IFS, argv) are excluded.
var fishReservedVars = map[string]string{
	"_":                 "_unused",
	"status":            "_status",
	"version":           "_version",
	"history":           "_history",
	"hostname":          "_hostname",
	"pipestatus":        "_pipestatus",
	"CMD_DURATION":      "_CMD_DURATION",
	"status_generation": "_status_generation",
}

// mangleVarName returns the safe mangled variable name if name collides with
// a Fish reserved or read-only variable.
func mangleVarName(name string) string {
	if mangled, ok := fishReservedVars[name]; ok {
		return mangled
	}
	return name
}

// fishPathVarRegex matches unquoted variable expansions whose names end in PATH.
var fishPathVarRegex = regexp.MustCompile(`\$(\{[A-Za-z0-9_]*PATH\}|[A-Za-z0-9_]*PATH\b|\{LANGUAGE\}|LANGUAGE\b)`)

// isFishListEnvVar reports whether an environment variable is automatically
// created as a list by fish. Fish automatically splits all environment
// variables whose name ends in "PATH" (like PATH, CDPATH, MANPATH, PKG_CONFIG_PATH)
// and LANGUAGE on colons into native lists.
func isFishListEnvVar(name string) bool {
	return strings.HasSuffix(name, "PATH") || name == "LANGUAGE"
}

func containsUnquotedFishListVar(s string) bool {
	return fishPathVarRegex.MatchString(s)
}

// bashContextVars defines Bash context introspection variables that map to
// Fish command substitutions instead of normal variables.
var bashContextVars = map[string][]string{
	"0":            {"status", "filename"},
	"BASH_SOURCE":  {"status", "filename"},
	"BASH_ARGV0":   {"status", "filename"},
	"BASH":         {"status", "fish-path"},
	"BASH_COMMAND": {"status", "current-command"},
	"FUNCNAME":     {"status", "current-function"},
	"UID":          {"id", "-u"},
	"GROUPS":       {"id", "-g"},
}

// normalizeReadCmd adapts a bash read command to fish:
// 1. Removes the -r flag (fish read never processes backslashes, so raw mode is default).
// 2. Applies variable name mangling for target variable names to prevent colliding with
//    fish read-only or reserved variables (e.g. "_" -> "_unused", "status" -> "_status").
func normalizeReadCmd(c *syntax.CallExpr) {
	if len(c.Args) == 0 || !isLitWord(c.Args[0], "read") {
		return
	}
	filtered := c.Args[:1]
	inOptions := true
	for i := 1; i < len(c.Args); i++ {
		w := c.Args[i]
		if inOptions {
			lit, isLit := isBareLit(w)
			if isLit && lit == "--" {
				inOptions = false
				filtered = append(filtered, w)
				continue
			}
			if isLit && strings.HasPrefix(lit, "-") && len(lit) > 1 {
				// Strip 'r' from the flag if present
				cleaned := "-" + strings.ReplaceAll(lit[1:], "r", "")
				optChar := rune(lit[len(lit)-1])
				// Options taking an argument: p (prompt), u (fd), t (timeout),
				// n/N (nchars), d (delim), i (initial text), a (array name)
				takesArg := strings.ContainsRune("putnNdia", optChar)
				consumesNext := takesArg && (len(lit) == 2 || (len(lit) > 2 && lit[len(lit)-1] == byte(optChar) && !strings.ContainsRune("putnNdia", rune(lit[1]))))

				if cleaned != "-" {
					filtered = append(filtered, litWord(cleaned))
				}
				if consumesNext && i+1 < len(c.Args) {
					i++
					argWord := c.Args[i]
					if optChar == 'a' {
						if argLit, ok := isBareLit(argWord); ok {
							filtered = append(filtered, litWord(mangleVarName(argLit)))
						} else {
							filtered = append(filtered, argWord)
						}
					} else {
						// Non-variable option arguments (prompt, fd, timeout, etc.) preserved verbatim
						filtered = append(filtered, argWord)
					}
				}
				continue
			}
			inOptions = false
		}
		// Positional variable name
		if lit, ok := isBareLit(w); ok {
			filtered = append(filtered, litWord(mangleVarName(lit)))
		} else {
			filtered = append(filtered, w)
		}
	}
	c.Args = filtered
}

func isBareLit(w *syntax.Word) (string, bool) {
	if len(w.Parts) == 1 {
		if lit, ok := w.Parts[0].(*syntax.Lit); ok {
			return lit.Value, true
		}
	}
	return "", false
}
func isLitWord(w *syntax.Word, val string) bool {
	if len(w.Parts) != 1 {
		return false
	}
	lit, ok := w.Parts[0].(*syntax.Lit)
	return ok && lit.Value == val
}

func isCmdChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
		c == '_' || c == '.' || c == '/'
}

// normalizeCommandName translates Bash's \cmd alias-bypass syntax to plain cmd.
// In Bash, prefixing the command word with a backslash (\cd, \alias, \pwd) quotes
// the first character to prevent alias expansion. In Fish, alias expansion does
// not exist and backslashes before alphanumeric characters trigger control escapes
// (\c -> Ctrl-D, \a -> BEL) or unknown command errors. Subsequent arguments
// are passed to the command itself and are not alias-expanded by the shell.
func normalizeCommandName(c *syntax.CallExpr) {
	if len(c.Args) == 0 || len(c.Args[0].Parts) == 0 {
		return
	}
	lit, ok := c.Args[0].Parts[0].(*syntax.Lit)
	if !ok {
		return
	}
	val := lit.Value
	if strings.HasPrefix(val, `\`) && !strings.HasPrefix(val, `\\`) && len(val) > 1 {
		if isCmdChar(val[1]) {
			lit.Value = val[1:]
		}
	}
}

// unescapeBackticks removes backslashes escaping backticks in double-quoted strings.
// In Bash double quotes, \` is required to escape backticks from command substitution,
// and quote removal reduces \` to `. In Fish double quotes, backticks are not command
// substitutions, and \` is preserved literally as \`. Unescaping \` to ` preserves
// faithful string output.
func unescapeBackticks(s string) string {
	if !strings.Contains(s, "`") || !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := range len(s) {
		if s[i] == '`' {
			bs := 0
			for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
				bs++
			}
			if bs%2 == 1 {
				str := b.String()
				b.Reset()
				b.WriteString(str[:len(str)-1])
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func (e *emitter) analyzeVariables(f *syntax.File) {
	syntax.Walk(f, func(n syntax.Node) bool {
		if c, ok := n.(*syntax.CallExpr); ok {
			if len(c.Assigns) == 0 && len(c.Args) > 1 {
				if pe, ok := singleBareParam(c.Args[0]); ok && pe.Param != nil {
					e.commandPrefixVars[pe.Param.Value] = true
				}
			}
			for _, a := range c.Assigns {
				if a.Name == nil {
					continue
				}
				if a.Array != nil {
					e.knownLists[a.Name.Value] = true
				}
			}
		}
		if d, ok := n.(*syntax.DeclClause); ok {
			for _, a := range d.Args {
				if a.Name != nil && a.Array != nil {
					e.knownLists[a.Name.Value] = true
				}
			}
		}
		return true
	})

	// Iteratively propagate multi-word scalar tracking until convergence
	for {
		changed := false
		syntax.Walk(f, func(n syntax.Node) bool {
			if c, ok := n.(*syntax.CallExpr); ok {
				for _, a := range c.Assigns {
					if a.Name == nil || e.knownLists[a.Name.Value] {
						continue
					}
					if !e.multiWordScalars[a.Name.Value] && isMultiWordAssign(a, e.multiWordScalars) {
						e.multiWordScalars[a.Name.Value] = true
						changed = true
					}
				}
			}
			return true
		})
		if !changed {
			break
		}
	}
}

func isMultiWordAssign(a *syntax.Assign, multiWordScalars map[string]bool) bool {
	if a.Value == nil {
		return false
	}
	hasCmdSubst := false
	syntax.Walk(a.Value, func(n syntax.Node) bool {
		switch n.(type) {
		case *syntax.CmdSubst:
			hasCmdSubst = true
			return false
		}
		return true
	})
	if hasCmdSubst {
		return true
	}

	for _, p := range a.Value.Parts {
		switch pt := p.(type) {
		case *syntax.Lit:
			if strings.ContainsAny(pt.Value, " \t\n") {
				return true
			}
		case *syntax.SglQuoted:
			if strings.ContainsAny(pt.Value, " \t\n") {
				return true
			}
		case *syntax.DblQuoted:
			for _, dp := range pt.Parts {
				switch dpt := dp.(type) {
				case *syntax.Lit:
					if strings.ContainsAny(dpt.Value, " \t\n") {
						return true
					}
				case *syntax.ParamExp:
					if dpt.Param != nil {
						if a.Name != nil && dpt.Param.Value == a.Name.Value {
							return true
						}
						if multiWordScalars[dpt.Param.Value] {
							return true
						}
					}
				case *syntax.CmdSubst:
					return true
				}
			}
		case *syntax.ParamExp:
			if pt.Param != nil {
				if a.Name != nil && pt.Param.Value == a.Name.Value {
					return true
				}
				if multiWordScalars[pt.Param.Value] {
					return true
				}
			}
		}
	}
	return false
}

// normalize applies mechanical AST fixes before emission: read-flag
// removal, fish-side parameter rewrites, and arithmetic translation.
func (e *emitter) normalize(f *syntax.File) {
	e.escapeLiteralDollars(f)
	e.analyzeVariables(f)
	syntax.Walk(f, func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.CallExpr:
			normalizeCommandName(x)
			if len(x.Assigns) == 0 && len(x.Args) > 0 {
				if isLitWord(x.Args[0], "getopts") {
					e.needHelper(helperGetopts)
					if len(x.Args) == 3 {
						x.Args = append(x.Args, &syntax.Word{Parts: []syntax.WordPart{argvParam()}})
					}
				} else if isLitWord(x.Args[0], "hash") {
					e.needHelper(helperHash)
				} else if isLitWord(x.Args[0], "unalias") {
					e.needHelper(helperUnalias)
				}
			}
			if len(x.Args) > 0 && isLitWord(x.Args[0], "eval") && x.Args[0].Pos().Col() > 0 {
				e.warn(x.Args[0].Pos(), "eval executes fish syntax; incompatible bash syntax will fail at runtime; emitted verbatim")
			}
			normalizeReadCmd(x)
			for i := 1; i < len(x.Args); i++ {
				if pe, ok := singleBareParam(x.Args[i]); ok && pe.Param != nil {
					name := pe.Param.Value
					if e.multiWordScalars[name] && !e.knownLists[name] &&
						name != "@" && name != "*" && name != "argv" &&
						!isFishListEnvVar(name) {
						e.needHelper(helperWords)
						x.Args[i] = &syntax.Word{
							Parts: []syntax.WordPart{
								substPart("__bait_words", "$"+e.varName(name)),
							},
						}
					}
				}
			}
		case *syntax.DblQuoted:
			for _, p := range x.Parts {
				if lit, ok := p.(*syntax.Lit); ok {
					lit.Value = unescapeBackticks(lit.Value)
				}
			}
		}
		return true
	})
	e.rewriteParams(f)
	e.rewriteArithmetic(f)
}

func (e *emitter) escapeLiteralDollars(f *syntax.File) {
	var walk func(n syntax.Node) bool
	walk = func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.Redirect:
			if x.Op == syntax.Hdoc || x.Op == syntax.DashHdoc || x.Op == syntax.WordHdoc {
				return false
			}
		case *syntax.TestClause, *syntax.CaseClause:
			return false
		case *syntax.ParamExp:
			if x.Exp != nil && x.Exp.Word != nil {
				syntax.Walk(x.Exp.Word, walk)
			}
			if x.Repl != nil {
				if x.Repl.Orig != nil {
					syntax.Walk(x.Repl.Orig, walk)
				}
				if x.Repl.With != nil {
					syntax.Walk(x.Repl.With, walk)
				}
			}
			return false
		case *syntax.Lit:
			x.Value = escapeLiteralDollar(x.Value)
		}
		return true
	}
	syntax.Walk(f, walk)
}

func escapeLiteralDollar(s string) string {
	if !strings.Contains(s, "$") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '$' {
			bs := 0
			for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
				bs++
			}
			if bs%2 == 0 {
				b.WriteByte('\\')
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
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
		e.printf("%s %s %s%s", e.chainSideText(bcmd.X), binOpText(bcmd.Op),
			e.chainSideText(bcmd.Y), e.tails(s))
		return
	}
	if chainNeedsRewrite(bcmd) {
		e.emitChain(bcmd)
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
		if len(c.Args) > 0 && (isLitWord(c.Args[0], "shift") || isLitWord(c.Args[0], "unset")) {
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
func (e *emitter) emitChain(b *syntax.BinaryCmd) {
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
	e.lines(strings.Join(parts, " "))
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
	if !ok || len(c.Args) != 0 || len(c.Assigns) == 0 {
		if operand, negated, ok := matchTestDashV(c); ok {
			line := e.renderTestDashV(operand, negated)
			if st.Negated {
				line = "! " + line
			}
			return line
		}
		if c != nil && len(c.Args) > 0 && (isLitWord(c.Args[0], "shift") || isLitWord(c.Args[0], "unset")) {
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
				e.needHelper(helperWords)
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
		if e.inFunction && e.funcLocals != nil && e.funcLocals[a.Name.Value] {
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

func (e *emitter) ifClause(s *syntax.Stmt, f *syntax.IfClause) {
	tail := e.tails(s)
	e.wrapperComments(s)
	e.printf("if %s", e.condText(f.Cond))

	pending := f.Last
	e.body(f.Then, f.ThenLast)
	for els := f.Else; els != nil; {
		for _, c := range pending {
			e.comment(c)
		}
		pending = els.Last
		if len(els.Cond) == 0 {
			e.printf("else")
			e.body(els.Then, els.ThenLast)
			break
		}
		e.printf("else if %s", e.condText(els.Cond))
		e.body(els.Then, els.ThenLast)
		els = els.Else
	}
	for _, c := range pending {
		e.comment(c)
	}
	e.printf("end%s", tail)
}

func (e *emitter) whileClause(s *syntax.Stmt, w *syntax.WhileClause) {
	tail := e.tails(s)
	e.wrapperComments(s)
	cond := e.condText(w.Cond)
	if w.Until {
		cond = "not " + cond
	}
	e.printf("while %s", cond)
	e.body(w.Do, w.DoLast)
	e.printf("end%s", tail)
}

func (e *emitter) forClause(s *syntax.Stmt, f *syntax.ForClause) {
	iter, ok := f.Loop.(*syntax.WordIter)
	if !ok || f.Select {
		e.warn(f.ForPos, "%s has no fish equivalent; emitted verbatim",
			describeSelect(f))
		e.printf("%s", e.render(s))
		return
	}
	items := make([]string, len(iter.Items))
	for i, w := range iter.Items {
		if pe, ok := singleBareParam(w); ok {
			name := pe.Param.Value
			if name == "@" || name == "*" || name == "argv" {
				items[i] = "$argv"
				continue
			}
			if isFishListEnvVar(name) || e.knownLists[name] {
				items[i] = "$" + e.varName(name)
				continue
			}
			name = e.varName(name)
			items[i] = "(__bait_words $" + name + ")"
			e.needHelper(helperWords)
			continue
		}
		if _, ok := singleBareCmdSubst(w); ok {
			items[i] = "(__bait_words " + e.renderWordSmart(w) + ")"
			e.needHelper(helperWords)
			continue
		}
		items[i] = e.renderWordSmart(w)
	}
	if !iter.InPos.IsValid() {
		// bash iterates the positional parameters when "in" is omitted.
		items = []string{"$argv"}
	} else if len(items) == 0 {
		items = []string{"(true)"}
	}
	tail := e.tails(s)
	e.wrapperComments(s)
	e.printf("for %s in %s", e.varName(iter.Name.Value), strings.Join(items, " "))
	e.body(f.Do, f.DoLast)
	e.printf("end%s", tail)
}

func (e *emitter) caseClause(s *syntax.Stmt, cl *syntax.CaseClause) {
	tail := e.tails(s)
	e.wrapperComments(s)
	if pred, ifItem, elseItem, ok := evalInteractiveCase(cl); ok {
		if elseItem == nil {
			e.printf("if %s", pred)
			e.body(ifItem.Stmts, ifItem.Last)
			for _, c := range cl.Last {
				e.comment(c)
			}
			e.printf("end%s", tail)
			return
		}
		if len(ifItem.Stmts) == 0 && len(ifItem.Last) == 0 {
			e.printf("if not %s", pred)
			e.body(elseItem.Stmts, elseItem.Last)
			for _, c := range cl.Last {
				e.comment(c)
			}
			e.printf("end%s", tail)
			return
		}
		e.printf("if %s", pred)
		e.body(ifItem.Stmts, ifItem.Last)
		if len(elseItem.Stmts) > 0 || len(elseItem.Last) > 0 {
			e.printf("else")
			e.body(elseItem.Stmts, elseItem.Last)
		}
		for _, c := range cl.Last {
			e.comment(c)
		}
		e.printf("end%s", tail)
		return
	}
	if caseHasBracketClass(cl) {
		e.caseClauseAsIf(s, cl, tail)
		return
	}
	wTxt := e.render(cl.Word)
	if isDollarDash(cl.Word) {
		e.warn(cl.Word.Pos(), "$- has no fish equivalent; fish uses status subcommands (e.g. status is-interactive)")
		wTxt = "$(status is-interactive && echo i || echo '')"
	}
	if containsUnquotedFishListVar(wTxt) && !strings.HasPrefix(wTxt, `"`) && !strings.HasPrefix(wTxt, "'") {
		wTxt = `"` + wTxt + `"`
	}
	e.printf("switch %s", wTxt)
	for _, item := range cl.Items {
		if item.Op != syntax.Break {
			e.warn(item.Pos(), "case fallthrough (%s) has no fish equivalent; converted to a plain case", item.Op)
		}
		patterns := make([]string, len(item.Patterns))
		for i, p := range item.Patterns {
			patterns[i] = e.renderCasePattern(p)
		}
		e.printf("case %s", strings.Join(patterns, " "))
		e.body(item.Stmts, item.Last)
	}
	for _, c := range cl.Last {
		e.comment(c)
	}
	e.printf("end%s", tail)
}

func (e *emitter) caseClauseAsIf(s *syntax.Stmt, cl *syntax.CaseClause, tail string) {
	hasCmdSubst := false
	syntax.Walk(cl.Word, func(n syntax.Node) bool {
		if _, ok := n.(*syntax.CmdSubst); ok {
			hasCmdSubst = true
			return false
		}
		return true
	})
	target := e.renderWordSmart(cl.Word)
	if !strings.HasPrefix(target, `"`) && !strings.HasPrefix(target, `'`) {
		target = `"` + target + `"`
	}
	if hasCmdSubst {
		e.printf("set --local __bait_case_target %s", target)
		target = "$__bait_case_target"
	}

	first := true
	for i, item := range cl.Items {
		if !first && i == len(cl.Items)-1 && len(item.Patterns) == 1 && isCatchAll(item.Patterns[0]) {
			e.printf("else")
			e.body(item.Stmts, item.Last)
			continue
		}
		var conds []string
		for _, p := range item.Patterns {
			re, _ := globToRegex(e.render(p), false)
			conds = append(conds, fmt.Sprintf("string match -r -q -- %s %s", fishSingleQuote("^"+re+"$"), target))
		}
		cond := strings.Join(conds, "; or ")
		if first {
			e.printf("if %s", cond)
			first = false
		} else {
			e.printf("else if %s", cond)
		}
		e.body(item.Stmts, item.Last)
	}
	for _, c := range cl.Last {
		e.comment(c)
	}
	e.printf("end%s", tail)
}

func isCatchAll(w *syntax.Word) bool {
	if w == nil || len(w.Parts) != 1 {
		return false
	}
	lit, ok := w.Parts[0].(*syntax.Lit)
	return ok && lit.Value == "*"
}

func caseHasBracketClass(cl *syntax.CaseClause) bool {
	if cl == nil {
		return false
	}
	for _, item := range cl.Items {
		for _, p := range item.Patterns {
			if wordHasBracketClass(p) {
				return true
			}
		}
	}
	return false
}

func wordHasBracketClass(w *syntax.Word) bool {
	if w == nil {
		return false
	}
	for _, p := range w.Parts {
		if lit, ok := p.(*syntax.Lit); ok {
			runes := []rune(lit.Value)
			for i := 0; i < len(runes); i++ {
				if runes[i] == '[' {
					if findClosingBracket(runes, i) != -1 {
						return true
					}
				}
			}
		}
	}
	return false
}

func isDollarDash(w *syntax.Word) bool {
	if w == nil {
		return false
	}
	if len(w.Parts) == 1 {
		switch p := w.Parts[0].(type) {
		case *syntax.ParamExp:
			return p.Param != nil && p.Param.Value == "-" && bareParam(p)
		case *syntax.DblQuoted:
			if len(p.Parts) == 1 {
				if pe, ok := p.Parts[0].(*syntax.ParamExp); ok {
					return pe.Param != nil && pe.Param.Value == "-" && bareParam(pe)
				}
			}
		}
	}
	return false
}

type patToken struct {
	val        string
	isWildcard bool
	isQuoted   bool
}

func tokenizePattern(w *syntax.Word) ([]patToken, bool) {
	if w == nil {
		return nil, false
	}
	var tokens []patToken
	for _, part := range w.Parts {
		switch pt := part.(type) {
		case *syntax.Lit:
			val := pt.Value
			for i := 0; i < len(val); {
				if val[i] == '\\' && i+1 < len(val) {
					tokens = append(tokens, patToken{
						val:      string(val[i+1]),
						isQuoted: true,
					})
					i += 2
				} else if val[i] == '*' || val[i] == '?' {
					tokens = append(tokens, patToken{
						val:        string(val[i]),
						isWildcard: true,
					})
					i++
				} else {
					tokens = append(tokens, patToken{
						val: string(val[i]),
					})
					i++
				}
			}
		case *syntax.SglQuoted:
			for _, r := range pt.Value {
				tokens = append(tokens, patToken{
					val:      string(r),
					isQuoted: true,
				})
			}
		case *syntax.DblQuoted:
			for _, qp := range pt.Parts {
				if lit, ok := qp.(*syntax.Lit); ok {
					for _, r := range lit.Value {
						tokens = append(tokens, patToken{
							val:      string(r),
							isQuoted: true,
						})
					}
				} else {
					return nil, false
				}
			}
		default:
			return nil, false
		}
	}
	return tokens, true
}

func matchFlagRule(p *syntax.Word) (pred string, matched bool, isWildcard bool) {
	if p == nil {
		return "", false, false
	}
	tokens, ok := tokenizePattern(p)
	if !ok {
		raw := ""
		for _, part := range p.Parts {
			switch pt := part.(type) {
			case *syntax.Lit:
				raw += pt.Value
			case *syntax.DblQuoted:
				for _, dqp := range pt.Parts {
					if dqlit, ok := dqp.(*syntax.Lit); ok {
						raw += dqlit.Value
					}
				}
			case *syntax.SglQuoted:
				raw += pt.Value
			}
		}
		trimmed := strings.Trim(raw, "*?.^$")
		if strings.EqualFold(trimmed, "i") {
			return "status is-interactive", true, false
		}
		if raw == "*" {
			return "", false, true
		}
		return "", false, false
	}
	if len(tokens) == 1 && tokens[0].val == "*" && tokens[0].isWildcard {
		return "", false, true
	}
	var meaningful []string
	for _, tok := range tokens {
		if tok.isWildcard {
			continue
		}
		meaningful = append(meaningful, tok.val)
	}
	candidate := strings.Join(meaningful, "")
	if strings.EqualFold(candidate, "i") {
		return "status is-interactive", true, false
	}
	return "", false, false
}

func evalInteractiveCase(cl *syntax.CaseClause) (pred string, ifItem, elseItem *syntax.CaseItem, ok bool) {
	if !isDollarDash(cl.Word) {
		return "", nil, nil, false
	}
	if len(cl.Items) == 1 {
		item0 := cl.Items[0]
		for _, p := range item0.Patterns {
			if pr, matched, _ := matchFlagRule(p); matched {
				return pr, item0, nil, true
			}
		}
	} else if len(cl.Items) == 2 {
		item0, item1 := cl.Items[0], cl.Items[1]
		var matchedPred string
		for _, p := range item0.Patterns {
			if pr, matched, _ := matchFlagRule(p); matched {
				matchedPred = pr
				break
			}
		}
		var wildcard bool
		if len(item1.Patterns) == 1 {
			_, _, wildcard = matchFlagRule(item1.Patterns[0])
		}
		if matchedPred != "" && wildcard {
			return matchedPred, item0, item1, true
		}
	}
	return "", nil, nil, false
}

func evalInteractiveTest(b *syntax.BinaryTest) (pred string, ok bool) {
	xWord, okX := b.X.(*syntax.Word)
	yWord, okY := b.Y.(*syntax.Word)
	if !okX || !okY {
		return "", false
	}
	dashX := isDollarDash(xWord)
	dashY := isDollarDash(yWord)
	if !dashX && !dashY {
		return "", false
	}
	var pat *syntax.Word
	if dashX {
		pat = yWord
	} else {
		pat = xWord
	}
	flagPred, matched, _ := matchFlagRule(pat)
	if !matched {
		return "", false
	}
	opStr := b.Op.String()
	switch opStr {
	case "==", "=", "=~":
		return flagPred, true
	case "!=", "!~":
		return "! " + flagPred, true
	}
	return "", false
}

func (e *emitter) renderCasePattern(p *syntax.Word) string {
	tokens, ok := tokenizePattern(p)
	if !ok {
		rendered := e.renderWordSmart(p)
		if hasUnquotedWildcard(p) && !strings.HasPrefix(rendered, `"`) && !strings.HasPrefix(rendered, `'`) {
			var sb strings.Builder
			sb.WriteByte('"')
			for _, part := range p.Parts {
				switch pt := part.(type) {
				case *syntax.Lit:
					val := strings.ReplaceAll(pt.Value, `"`, `\"`)
					sb.WriteString(val)
				case *syntax.DblQuoted:
					for _, qp := range pt.Parts {
						sb.WriteString(e.renderWordSmart(&syntax.Word{Parts: []syntax.WordPart{qp}}))
					}
				case *syntax.SglQuoted:
					val := pt.Value
					val = strings.ReplaceAll(val, `\`, `\\`)
					val = strings.ReplaceAll(val, `"`, `\"`)
					val = strings.ReplaceAll(val, `$`, `\$`)
					sb.WriteString(val)
				default:
					sb.WriteString(e.renderWordSmart(&syntax.Word{Parts: []syntax.WordPart{pt}}))
				}
			}
			sb.WriteByte('"')
			return sb.String()
		}
		return rendered
	}
	if len(tokens) == 0 {
		return `""`
	}

	hasLeadingTilde := tokens[0].val == "~"
	hasWildcard := false
	hasSlash := false
	hasQuotedSpecial := false
	needsQuoting := hasLeadingTilde

	for _, tok := range tokens {
		if tok.isWildcard {
			hasWildcard = true
		}
		if tok.isQuoted {
			hasQuotedSpecial = true
		}
		if tok.val == "/" {
			hasSlash = true
		}
		if strings.ContainsAny(tok.val, " \t'\"$;&|()[]<>`") {
			needsQuoting = true
		}
	}
	if hasWildcard || hasSlash || hasLeadingTilde || hasQuotedSpecial {
		needsQuoting = true
	}

	if !needsQuoting {
		var sb strings.Builder
		for _, tok := range tokens {
			sb.WriteString(tok.val)
		}
		return sb.String()
	}

	var sb strings.Builder
	sb.WriteByte('\'')
	for _, tok := range tokens {
		if tok.val == "'" {
			sb.WriteString(`\'`)
		} else {
			sb.WriteString(tok.val)
		}
	}
	sb.WriteByte('\'')
	return sb.String()
}

func (e *emitter) testClause(s *syntax.Stmt, tc *syntax.TestClause) {
	tail := e.tails(s)
	e.wrapperComments(s)
	res := e.renderTestExpr(tc.X)
	if s.Negated {
		e.printf("! %s%s", res, tail)
	} else {
		e.printf("%s%s", res, tail)
	}
}

func (e *emitter) renderTestExpr(expr syntax.TestExpr) string {
	switch x := expr.(type) {
	case nil:
		return "test -n ''"
	case *syntax.ParenTest:
		return "begin " + e.renderTestExpr(x.X) + "; end"
	case *syntax.UnaryTest:
		return e.renderUnaryTest(x)
	case *syntax.BinaryTest:
		return e.renderBinaryTest(x)
	case *syntax.Word:
		return "test -n " + e.renderWordSmart(x)
	default:
		return "test " + e.render(expr)
	}
}

func (e *emitter) renderUnaryTest(u *syntax.UnaryTest) string {
	opStr := u.Op.String()
	if opStr == "!" {
		inner := e.renderTestExpr(u.X)
		return "! " + inner
	}
	if opStr == "-v" {
		if w, ok := u.X.(*syntax.Word); ok {
			raw := e.render(w)
			unquoted := unquoteArg(raw)
			shifted := e.shiftArrayIndex(u.X.Pos(), unquoted)
			return "set -q " + shifted
		}
		return "set -q " + e.render(u.X)
	}
	return fmt.Sprintf("test %s %s", opStr, e.renderTestOperand(u.X))
}

func (e *emitter) renderBinaryTest(b *syntax.BinaryTest) string {
	if pred, ok := evalInteractiveTest(b); ok {
		return pred
	}
	opStr := b.Op.String()
	switch opStr {
	case "&&":
		return fmt.Sprintf("%s && %s", e.renderTestExpr(b.X), e.renderTestExpr(b.Y))
	case "||":
		return fmt.Sprintf("%s || %s", e.renderTestExpr(b.X), e.renderTestExpr(b.Y))
	case "=~":
		xWord, _ := b.X.(*syntax.Word)
		yWord, _ := b.Y.(*syntax.Word)
		target := e.renderWordSmart(xWord)
		pat := e.renderPatternLiteral(yWord)
		return fmt.Sprintf("string match -r -q -- %s %s", pat, target)
	case "==", "=":
		xWord, _ := b.X.(*syntax.Word)
		yWord, _ := b.Y.(*syntax.Word)
		target := e.renderWordSmart(xWord)
		if hasUnquotedWildcard(yWord) {
			pat := e.renderPatternLiteral(yWord)
			return fmt.Sprintf("string match -q -- %s %s", pat, target)
		}
		yStr := e.renderWordSmart(yWord)
		return fmt.Sprintf("test %s = %s", target, yStr)
	case "!=":
		xWord, _ := b.X.(*syntax.Word)
		yWord, _ := b.Y.(*syntax.Word)
		target := e.renderWordSmart(xWord)
		if hasUnquotedWildcard(yWord) {
			pat := e.renderPatternLiteral(yWord)
			return fmt.Sprintf("! string match -q -- %s %s", pat, target)
		}
		yStr := e.renderWordSmart(yWord)
		return fmt.Sprintf("test %s != %s", target, yStr)
	case "-eq", "-ne", "-lt", "-le", "-gt", "-ge", "-nt", "-ot", "-ef":
		return fmt.Sprintf("test %s %s %s", e.renderTestOperand(b.X), opStr, e.renderTestOperand(b.Y))
	case "<":
		return fmt.Sprintf("test %s \\< %s", e.renderTestOperand(b.X), e.renderTestOperand(b.Y))
	case ">":
		return fmt.Sprintf("test %s \\> %s", e.renderTestOperand(b.X), e.renderTestOperand(b.Y))
	default:
		return fmt.Sprintf("test %s %s %s", e.renderTestOperand(b.X), opStr, e.renderTestOperand(b.Y))
	}
}

func (e *emitter) renderTestOperand(expr syntax.TestExpr) string {
	if w, ok := expr.(*syntax.Word); ok {
		return e.renderWordSmart(w)
	}
	return e.render(expr)
}

func (e *emitter) renderPatternLiteral(w *syntax.Word) string {
	if w == nil {
		return "''"
	}
	if hasParamOrSubst(w) {
		return e.renderWordSmart(w)
	}
	raw := e.render(w)
	if (strings.HasPrefix(raw, "'") && strings.HasSuffix(raw, "'")) ||
		(strings.HasPrefix(raw, `"`) && strings.HasSuffix(raw, `"`)) {
		return raw
	}
	return "'" + strings.ReplaceAll(raw, "'", `\'`) + "'"
}

func hasParamOrSubst(w *syntax.Word) bool {
	if w == nil {
		return false
	}
	found := false
	syntax.Walk(w, func(n syntax.Node) bool {
		switch n.(type) {
		case *syntax.ParamExp, *syntax.CmdSubst:
			found = true
			return false
		}
		return true
	})
	return found
}

func hasUnquotedWildcard(w *syntax.Word) bool {
	if w == nil {
		return false
	}
	for _, p := range w.Parts {
		switch p := p.(type) {
		case *syntax.Lit:
			if strings.ContainsAny(p.Value, "*?[") {
				return true
			}
		}
	}
	return false
}


func casePatternString(p *syntax.Word) (string, bool) {
	var sb strings.Builder
	for _, part := range p.Parts {
		switch pt := part.(type) {
		case *syntax.Lit:
			sb.WriteString(pt.Value)
		case *syntax.SglQuoted:
			sb.WriteString(pt.Value)
		case *syntax.DblQuoted:
			for _, qp := range pt.Parts {
				if lit, ok := qp.(*syntax.Lit); ok {
					sb.WriteString(lit.Value)
				} else {
					return "", false
				}
			}
		default:
			return "", false
		}
	}
	return sb.String(), true
}

func (e *emitter) funcDecl(s *syntax.Stmt, fd *syntax.FuncDecl) {
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
			e.printf("end%s", tail)
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
	e.printf("end%s", tail)
}

func (e *emitter) group(s *syntax.Stmt, stmts []*syntax.Stmt, last []syntax.Comment) {
	tail := e.tails(s)
	e.wrapperComments(s)
	if s.Negated {
		e.printf("! begin")
	} else {
		e.printf("begin")
	}
	e.body(stmts, last)
	e.printf("end%s", tail)
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

// assignStmt rewrites a pure assignment statement (assignments with no
// command) into fish set commands. Bash assignments made inside a
// function body are global, so they map to "set --global". Array
// literals flatten into list values; constant indices shift by one.
func (e *emitter) assignStmt(s *syntax.Stmt, c *syntax.CallExpr) {
	scope := ""
	if e.inFunction {
		scope = "--global"
	}
	for _, a := range c.Assigns {
		curScope := scope
		if a.Name != nil {
			if e.inFunction && e.funcLocals != nil && e.funcLocals[a.Name.Value] {
				curScope = ""
			}
		}
		if a.Name != nil && e.commandPrefixVars[a.Name.Value] && isEmptyValue(a.Value) {
			if curScope != "" {
				e.printf("set %s %s", curScope, e.varName(a.Name.Value))
			} else {
				e.printf("set %s", e.varName(a.Name.Value))
			}
			continue
		}
		switch {
		case a.Name == nil:
			e.warn(s.Position, "unnamed assignment cannot be translated; emitted verbatim")
			e.lines(e.render(s))
			return

		case a.Append:
			if a.Array == nil {
				e.warn(s.Position, "non-array append cannot be translated; emitted verbatim")
				e.lines(e.render(s))
				return
			}
			e.printf("set %s--append %s%s", scopePrefix(curScope), e.varName(a.Name.Value),
				e.arrayElemArgs(a.Array))

		case a.Index != nil:
			tok, ok := e.arrayIndex(a.Index)
			if !ok {
				e.warn(s.Position, "dynamic array index cannot be shifted; emitted verbatim")
				e.lines(e.render(s))
				return
			}
			e.printf("set %s%s[%s] %s", scopePrefix(curScope), e.varName(a.Name.Value), tok,
				e.assignValue(a))

		case a.Array != nil:
			e.printf("set %s%s%s", scopePrefix(curScope), e.varName(a.Name.Value),
				e.arrayElemArgs(a.Array))

		default:
			e.setLine(curScope, e.varName(a.Name.Value), e.assignValue(a))
		}
	}
}

func isEmptyValue(w *syntax.Word) bool {
	if w == nil || len(w.Parts) == 0 {
		return true
	}
	if len(w.Parts) == 1 {
		switch p := w.Parts[0].(type) {
		case *syntax.Lit:
			return p.Value == ""
		case *syntax.SglQuoted:
			return p.Value == ""
		case *syntax.DblQuoted:
			if len(p.Parts) == 0 {
				return true
			}
			if len(p.Parts) == 1 {
				if lit, ok := p.Parts[0].(*syntax.Lit); ok {
					return lit.Value == ""
				}
			}
		}
	}
	return false
}

func scopePrefix(scope string) string {
	if scope == "" {
		return ""
	}
	return scope + " "
}

// arrayElemArgs renders array literal elements as set arguments, quoting
// empty elements so they survive as list items.
func (e *emitter) arrayElemArgs(a *syntax.ArrayExpr) string {
	var b strings.Builder
	for _, el := range a.Elems {
		b.WriteByte(' ')
		if el.Value == nil {
			b.WriteString(`""`)
			continue
		}
		s := e.render(el.Value)
		if s == "" {
			s = `""`
		}
		b.WriteString(s)
	}
	return b.String()
}

// arrayIndex maps a bash array subscript to its fish token: "@"/"*"
// become the whole-list marker "@" and non-negative constants shift up
// by one; negative indices already match fish and pass through. ok is
// false for dynamic expressions, which cannot shift statically.
func (e *emitter) arrayIndex(idx syntax.ArithmExpr) (string, bool) {
	if u, ok := idx.(*syntax.UnaryArithm); ok && u.Op == syntax.Minus && !u.Post {
		s := strings.TrimSpace(e.render(u.X))
		if isDigits(s) {
			return "-" + s, true
		}
		return "", false
	}
	s := strings.TrimSpace(e.render(idx))
	switch {
	case s == "@" || s == "*":
		return s, true
	case isDigits(s):
		n, err := strconv.Atoi(s)
		if err != nil {
			return "", false
		}
		return strconv.Itoa(n + 1), true
	}
	return "", false
}

// soleArrayAll reports whether the word is exactly "${arr[@]}" or
// "${arr[*]}" (optionally quoted) and returns the variable name, whether
// the subscript token was "*", and whether the word was double-quoted.
func (e *emitter) soleArrayAll(w *syntax.Word) (name string, isStar bool, quoted bool, ok bool) {
	if len(w.Parts) != 1 {
		return "", false, false, false
	}
	part := w.Parts[0]
	isQuoted := false
	if q, ok := part.(*syntax.DblQuoted); ok {
		if len(q.Parts) != 1 {
			return "", false, false, false
		}
		part = q.Parts[0]
		isQuoted = true
	}
	pe, ok := part.(*syntax.ParamExp)
	if !ok || pe.Param == nil || pe.Length || pe.Slice != nil ||
		pe.Repl != nil || pe.Exp != nil || pe.Index == nil {
		return "", false, false, false
	}
	tok, ok := e.arrayIndex(pe.Index)
	if !ok || (tok != "@" && tok != "*") {
		return "", false, false, false
	}
	return e.varName(pe.Param.Value), tok == "*", isQuoted, true
}
func (e *emitter) assignValue(a *syntax.Assign) string {
	// A missing value is a typed-nil *Word, which must not reach the
	// printer; bash "x=" and fish 'set x ""' both mean an empty string.
	if a.Value == nil {
		return `""`
	}
	if len(a.Value.Parts) == 1 {
		if q, ok := a.Value.Parts[0].(*syntax.DblQuoted); ok && len(q.Parts) == 1 {
			if pe, ok := q.Parts[0].(*syntax.ParamExp); ok && bareParam(pe) && pe.Param != nil {
				return "$" + pe.Param.Value
			}
		}
	}
	if r := e.renderWordSmart(a.Value); r != "" {
		return r
	}
	return `""`
}

func untranslatableAssign(a *syntax.Assign) string {
	if a.Name == nil {
		return "unnamed assignment"
	}
	return ""
}

// declClause rewrites declare-family clauses. export passes through:
// fish ships an export wrapper accepting NAME=VALUE arguments. local and
// declare map onto set with long scope flags.
func (e *emitter) declClause(s *syntax.Stmt, d *syntax.DeclClause) {
	if d.Variant.Value == "export" {
		if e.hasOptionArg(d.Args) {
			e.warn(d.Variant.Pos(), "export flag is not supported by fish's export; emitted verbatim")
			e.printf("%s", e.render(s))
			return
		}
		args := make([]string, 0, len(d.Args))
		for _, a := range d.Args {
			if a.Naked {
				if a.Name != nil {
					args = append(args, e.varName(a.Name.Value))
				} else if a.Value != nil {
					args = append(args, e.renderWordSmart(a.Value))
				}
				continue
			}
			if a.Name != nil {
				val := e.assignValue(a)
				if a.Value != nil && len(a.Value.Parts) == 1 {
					if _, ok := a.Value.Parts[0].(*syntax.DblQuoted); ok {
						val = e.renderWordSmart(a.Value)
					}
				}
				args = append(args, fmt.Sprintf("%s=%s", e.varName(a.Name.Value), val))
			}
		}
		if len(args) == 0 {
			e.printf("export")
		} else {
			e.printf("export %s", strings.Join(args, " "))
		}
		return
	}
	if e.hasReadonlyFlag(d.Args) {
		e.warn(s.Position, "readonly attribute has no fish equivalent; emitted verbatim")
		e.printf("%s", e.render(s))
		return
	}
	scope := ""
	switch d.Variant.Value {
	case "local":
		scope = "--function"
	case "declare", "typeset":
		if e.hasGlobalFlag(d.Args) {
			if e.inFunction {
				scope = "--global"
			}
		} else if e.inFunction {
			scope = "--function"
		}
	default: // readonly, nameref
		e.warn(s.Position, "%s has no fish equivalent; emitted verbatim", d.Variant.Value)
		e.printf("%s", e.render(s))
		return
	}
	for _, a := range d.Args {
		if e.isOptionArg(a) {
			continue
		}
		if a.Name != nil {
			if scope == "--function" {
				if e.funcLocals == nil {
					e.funcLocals = make(map[string]bool)
				}
				e.funcLocals[a.Name.Value] = true
			}
			if e.commandPrefixVars[a.Name.Value] && isEmptyValue(a.Value) {
				if scope != "" {
					e.printf("set %s %s", scope, e.varName(a.Name.Value))
				} else {
					e.printf("set %s", e.varName(a.Name.Value))
				}
				continue
			}
			e.setLine(scope, e.varName(a.Name.Value), e.assignValue(a))
		}
	}
}

func (e *emitter) hasGlobalFlag(args []*syntax.Assign) bool {
	for _, a := range args {
		if a.Naked && e.argText(a) == "-g" {
			return true
		}
	}
	return false
}

func (e *emitter) hasReadonlyFlag(args []*syntax.Assign) bool {
	for _, a := range args {
		if a.Naked {
			txt := e.argText(a)
			if strings.HasPrefix(txt, "-") && strings.ContainsRune(txt[1:], 'r') {
				return true
			}
		}
	}
	return false
}

// isOptionArg reports whether a naked declaration argument is an option
// token like -r. mvdan models options as either Name or Value text.
func (e *emitter) isOptionArg(a *syntax.Assign) bool {
	if !a.Naked {
		return false
	}
	return strings.HasPrefix(e.argText(a), "-")
}

func (e *emitter) hasOptionArg(args []*syntax.Assign) bool {
	for _, a := range args {
		if e.isOptionArg(a) {
			return true
		}
	}
	return false
}

func (e *emitter) argText(a *syntax.Assign) string {
	if a.Name != nil {
		return a.Name.Value
	}
	return e.render(a.Value)
}

// setLine emits one set command; generated fish always uses long options.
func (e *emitter) setLine(scope, name, value string) {
	if scope != "" {
		e.printf("set %s %s %s", scope, name, value)
		return
	}
	e.printf("set %s %s", name, value)
}

// rewriteParams rewrites parameter expansions that fish spells
// differently: special parameters ($? $$ $! $# $0), positional
// parameters, and redundant braces around plain variables.
func (e *emitter) rewriteParams(f *syntax.File) {
	syntax.Walk(f, func(n syntax.Node) bool {
		switch node := n.(type) {
		case *syntax.CaseClause:
			if isDollarDash(node.Word) {
				for _, item := range node.Items {
					syntax.Walk(item, func(cn syntax.Node) bool {
						if w, ok := cn.(*syntax.Word); ok {
							w.Parts = e.spliceParts(w.Parts, false)
						}
						return true
					})
				}
				return false
			}
		case *syntax.TestClause:
			if b, ok := node.X.(*syntax.BinaryTest); ok {
				if _, isInter := evalInteractiveTest(b); isInter {
					return false
				}
			}
		case *syntax.Word:
			if isBareSpecialParam(node, "@", "*") || isQuotedSpecialParam(node, "@") {
				node.Parts = []syntax.WordPart{argvParam()}
				return true
			}
			if name, isStar, quoted, ok := e.soleArrayAll(node); ok {
				if cv, isCtx := bashContextVars[name]; isCtx {
					node.Parts = []syntax.WordPart{substPart(cv...)}
					return true
				}
				if quoted && isStar {
					node.Parts = []syntax.WordPart{&syntax.DblQuoted{Parts: []syntax.WordPart{namedParam(name)}}}
					return true
				}
				node.Parts = []syntax.WordPart{namedParam(name)}
				return true
			}
			node.Parts = e.spliceParts(node.Parts, false)
			return true
		}
		return true
	})
}
func needsFishBracing(parts []syntax.WordPart, i int) bool {
	if i+1 >= len(parts) {
		return false
	}
	lit, ok := parts[i+1].(*syntax.Lit)
	if !ok || len(lit.Value) == 0 {
		return false
	}
	r := lit.Value[0]
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

func (e *emitter) spliceParts(parts []syntax.WordPart, inDblQuotes bool) []syntax.WordPart {
	out := make([]syntax.WordPart, 0, len(parts)+1)
	for i := range parts {
		part := parts[i]
		switch p := part.(type) {
		case *syntax.ParamExp:
			replaced := e.paramReplacements(p)
			if !inDblQuotes && needsFishBracing(parts, i) && len(replaced) == 1 {
				if pe, ok := replaced[0].(*syntax.ParamExp); ok && bareParam(pe) && pe.Param != nil {
					out = append(out, litPart("{$"+pe.Param.Value+"}"))
					continue
				}
			}
			out = append(out, replaced...)
		case *syntax.ArithmExp:
			if txt, ok := e.arithmText(p.X); ok {
				out = append(out, mathSubst(txt))
			} else {
				e.warn(p.Pos(), "arithmetic uses an operator without a fish math equivalent; left untranslated")
				out = append(out, p)
			}
		case *syntax.DblQuoted:
			p.Parts = e.spliceParts(p.Parts, true)
			out = append(out, p)
		default:
			out = append(out, p)
		}
	}
	return out
}

func (e *emitter) paramReplacements(pe *syntax.ParamExp) []syntax.WordPart {
	name := ""
	if pe.Param != nil {
		name = pe.Param.Value
	}
	if !bareParam(pe) {
		if name == "@" || name == "*" {
			e.warn(pe.Pos(), "$%s inside a larger word or expression is left untranslated", name)
			return []syntax.WordPart{pe}
		}
		if parts, ok := e.operatorExpansion(pe); ok {
			return parts
		}
		return []syntax.WordPart{pe}
	}
	if cv, ok := bashContextVars[name]; ok {
		return []syntax.WordPart{substPart(cv...)}
	}
	if mangled, ok := fishReservedVars[name]; ok {
		return []syntax.WordPart{namedParam(mangled)}
	}
	switch name {
	case "@", "*":
		return []syntax.WordPart{argvParam()}
	case "?":
		return []syntax.WordPart{namedParam("status")}
	case "!":
		return []syntax.WordPart{namedParam("last_pid")}
	case "$", "BASHPID":
		return []syntax.WordPart{namedParam("fish_pid")}
	case "#":
		return []syntax.WordPart{substPart("count", "$argv")}
	case "HOSTNAME":
		return []syntax.WordPart{namedParam("hostname")}
	case "HOSTTYPE", "MACHTYPE":
		return []syntax.WordPart{substPart("uname", "-m")}
	case "PIPESTATUS":
		return []syntax.WordPart{namedParam("pipestatus")}
	case "DIRSTACK":
		return []syntax.WordPart{namedParam("dirstack")}
	case "OSTYPE":
		return []syntax.WordPart{pipeSubstPart([]string{"uname", "-s"}, []string{"string", "lower"})}
	case "RANDOM":
		return []syntax.WordPart{substPart("random")}
	case "SRANDOM":
		return []syntax.WordPart{substPart("random", "0", "4294967295")}
	case "EPOCHSECONDS":
		return []syntax.WordPart{substPart("date", "+%s")}
	case "-":
		e.warn(pe.Pos(), "$- has no fish equivalent; fish uses status subcommands (e.g. status is-interactive)")
		return []syntax.WordPart{statusDashPart()}
	}
	if isDigits(name) {
		// bash positional params are 1-based like fish list indices;
		// a Lit suffix keeps the printed form $argv[N], never braced.
		return []syntax.WordPart{argvParam(), litPart("[" + name + "]")}
	}
	if syntax.ValidName(name) {
		pe.Short = true // ${var}: drop the braces
	}
	return []syntax.WordPart{pe}
}

func bareParam(pe *syntax.ParamExp) bool {
	return !pe.Length && !pe.Excl && !pe.Width && !pe.IsSet &&
		pe.Index == nil && pe.Slice == nil && pe.Repl == nil &&
		pe.Exp == nil && len(pe.Modifiers) == 0 &&
		pe.Names == 0 && pe.Flags == nil && pe.NestedParam == nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func namedParam(name string) *syntax.ParamExp {
	return &syntax.ParamExp{Short: true, Param: lit(name)}
}

func argvParam() *syntax.ParamExp { return namedParam("argv") }

func lit(s string) *syntax.Lit {
	return &syntax.Lit{ValuePos: syntax.NewPos(1, 1, 1), Value: s}
}

func litPart(s string) syntax.WordPart { return lit(s) }

func litWord(s string) *syntax.Word {
	return &syntax.Word{Parts: []syntax.WordPart{litPart(s)}}
}

// substPart builds a $(cmd arg...) command substitution word part.
func substPart(args ...string) syntax.WordPart {
	call := &syntax.CallExpr{}
	for _, a := range args {
		call.Args = append(call.Args, litWord(a))
	}
	return &syntax.CmdSubst{Stmts: []*syntax.Stmt{{Cmd: call}}}
}
func stringUnarySubst(subcmd, name string) syntax.WordPart {
	var ref *syntax.Word
	if name == "-" {
		ref = dq(statusDashPart())
	} else if isDigits(name) {
		ref = dq(argvParam(), litPart("["+name+"]"))
	} else {
		ref = dq(namedParam(name))
	}
	call := &syntax.CallExpr{
		Args: []*syntax.Word{
			litWord("string"),
			litWord(subcmd),
			litWord("--"),
			ref,
		},
	}
	return &syntax.CmdSubst{Stmts: []*syntax.Stmt{{Cmd: call}}}
}


// pipeSubstPart builds a $(cmd1 args... | cmd2 args...) command substitution word part.
func pipeSubstPart(xArgs, yArgs []string) syntax.WordPart {
	xCall := &syntax.CallExpr{}
	for _, a := range xArgs {
		xCall.Args = append(xCall.Args, litWord(a))
	}
	yCall := &syntax.CallExpr{}
	for _, a := range yArgs {
		yCall.Args = append(yCall.Args, litWord(a))
	}
	bin := &syntax.BinaryCmd{
		Op: syntax.Pipe,
		X:  &syntax.Stmt{Cmd: xCall},
		Y:  &syntax.Stmt{Cmd: yCall},
	}
	return &syntax.CmdSubst{Stmts: []*syntax.Stmt{{Cmd: bin}}}
}

func statusDashPart() syntax.WordPart {
	cmd1 := &syntax.CallExpr{Args: []*syntax.Word{litWord("status"), litWord("is-interactive")}}
	cmd2 := &syntax.CallExpr{Args: []*syntax.Word{litWord("echo"), litWord("i")}}
	cmd3 := &syntax.CallExpr{Args: []*syntax.Word{litWord("echo"), litWord("''")}}
	bin1 := &syntax.BinaryCmd{
		Op: syntax.AndStmt,
		X:  &syntax.Stmt{Cmd: cmd1},
		Y:  &syntax.Stmt{Cmd: cmd2},
	}
	bin2 := &syntax.BinaryCmd{
		Op: syntax.OrStmt,
		X:  &syntax.Stmt{Cmd: bin1},
		Y:  &syntax.Stmt{Cmd: cmd3},
	}
	return &syntax.CmdSubst{Stmts: []*syntax.Stmt{{Cmd: bin2}}}
}

func isBareSpecialParam(w *syntax.Word, names ...string) bool {
	if len(w.Parts) != 1 {
		return false
	}
	pe, ok := w.Parts[0].(*syntax.ParamExp)
	return ok && matchesAnySpecial(pe, names)
}

func isQuotedSpecialParam(w *syntax.Word, names ...string) bool {
	if len(w.Parts) != 1 {
		return false
	}
	q, ok := w.Parts[0].(*syntax.DblQuoted)
	if !ok || len(q.Parts) != 1 {
		return false
	}
	pe, ok := q.Parts[0].(*syntax.ParamExp)
	return ok && matchesAnySpecial(pe, names)
}

func matchesAnySpecial(pe *syntax.ParamExp, names []string) bool {
	if pe.Param == nil || !bareParam(pe) {
		return false
	}
	for _, n := range names {
		if pe.Param.Value == n {
			return true
		}
	}
	return false
}

// --- arithmetic translation ---
// bash arithmetic is integer-only; fish math matches it under
// --scale=0 (truncation toward zero, verified empirically). Comparison
// and truthiness shapes become test invocations because fish math does
// not support logical operators and always exits zero on success.

// rewriteArithmetic replaces (( )) commands whose shape fish can express
// with set/test/math equivalents, so conditions and bodies alike see the
// translated node.
func (e *emitter) rewriteArithmetic(f *syntax.File) {
	syntax.Walk(f, func(n syntax.Node) bool {
		stmt, ok := n.(*syntax.Stmt)
		if !ok || stmt.Cmd == nil {
			return true
		}
		ac, ok := stmt.Cmd.(*syntax.ArithmCmd)
		if !ok {
			return true
		}
		if repl := e.arithCommand(ac); repl != nil {
			stmt.Cmd = repl
		}
		return true
	})
}

var arithSymbols = map[syntax.BinAritOperator]string{
	syntax.Add: "+", syntax.Sub: "-", syntax.Mul: "*",
	syntax.Quo: "/", syntax.Rem: "%", syntax.Pow: "**",
	syntax.Eql: "==", syntax.Neq: "!=",
	syntax.Lss: "<", syntax.Gtr: ">",
	syntax.Leq: "<=", syntax.Geq: ">=",
}

var arithAssignSymbols = map[syntax.BinAritOperator]string{
	syntax.AddAssgn: "+", syntax.SubAssgn: "-", syntax.MulAssgn: "*",
	syntax.QuoAssgn: "/", syntax.RemAssgn: "%", syntax.PowAssgn: "**",
}

var arithTestFlags = map[syntax.BinAritOperator]string{
	syntax.Eql: "-eq", syntax.Neq: "-ne",
	syntax.Lss: "-lt", syntax.Gtr: "-gt",
	syntax.Leq: "-le", syntax.Geq: "-ge",
}

func isAssignOp(op syntax.BinAritOperator) bool {
	return op == syntax.Assgn || arithAssignSymbols[op] != ""
}

// arithCommand converts an (( )) command into its fish equivalent, or
// nil when no faithful translation exists (the caller then warns and
// emits verbatim through the default path).
func (e *emitter) arithCommand(ac *syntax.ArithmCmd) syntax.Command {
	switch x := ac.X.(type) {
	case *syntax.UnaryArithm:
		if x.Op != syntax.Inc && x.Op != syntax.Dec {
			return nil
		}
		name, ok := bareArithName(x.X)
		if !ok {
			return nil
		}
		op := "+"
		if x.Op == syntax.Dec {
			op = "-"
		}
		return setCall(name, mathSubst("$"+name+" "+op+" 1"))

	case *syntax.BinaryArithm:
		if name, ok := bareArithName(x.X); ok && isAssignOp(x.Op) {
			var payload string
			var valid bool
			if x.Op == syntax.Assgn {
				payload, valid = e.arithmText(x.Y)
			} else {
				rhs, rhsOK := e.arithmText(x.Y)
				payload, valid = "$"+name+" "+arithAssignSymbols[x.Op]+" "+rhs, rhsOK
			}
			if valid {
				return setCall(name, mathSubst(payload))
			}
			return nil
		}
		if flag, isCmp := arithTestFlags[x.Op]; isCmp {
			lhs, lok := e.operandValue(x.X)
			rhs, rok := e.operandValue(x.Y)
			if lok && rok {
				return &syntax.CallExpr{Args: []*syntax.Word{
					litWord("test"), lhs, litWord(flag), rhs,
				}}
			}
		}

	case *syntax.Word:
		val, ok := e.operandValue(x)
		if ok {
			return &syntax.CallExpr{Args: []*syntax.Word{
				litWord("test"), val, litWord("-ne"), litWord("0"),
			}}
		}
	}
	return nil
}

// arithmText renders an arithmetic expression as fish math payload text,
// rewriting bare variable names to $references. ok reports whether every
// operator has a math equivalent.
func (e *emitter) arithmText(n syntax.ArithmExpr) (string, bool) {
	switch a := n.(type) {
	case *syntax.Word:
		s := e.render(a)
		if isDigits(s) || strings.HasPrefix(s, "$") {
			return s, true
		}
		if syntax.ValidName(s) {
			return "$" + s, true
		}
		return "", false
	case *syntax.ParenArithm:
		inner, ok := e.arithmText(a.X)
		return "(" + inner + ")", ok
	case *syntax.UnaryArithm:
		if a.Op == syntax.Minus && !a.Post {
			x, ok := e.arithmText(a.X)
			return "-" + x, ok
		}
		return "", false
	case *syntax.BinaryArithm:
		sym, ok := arithSymbols[a.Op]
		if !ok {
			return "", false
		}
		lx, lok := e.arithmText(a.X)
		ry, rok := e.arithmText(a.Y)
		return lx + " " + sym + " " + ry, lok && rok
	}
	return "", false
}

// operandValue renders an arithmetic leaf as a test-safe argument word:
// digits unquoted, variable references double-quoted against emptiness.
func (e *emitter) operandValue(n syntax.ArithmExpr) (*syntax.Word, bool) {
	w, ok := n.(*syntax.Word)
	if !ok {
		return nil, false
	}
	s := e.render(w)
	switch {
	case isDigits(s):
		return litWord(s), true
	case strings.HasPrefix(s, "$"):
		return dq(litPart(s)), true
	case syntax.ValidName(s):
		return dq(namedParam(s)), true
	}
	return nil, false
}

func bareArithName(n syntax.ArithmExpr) (string, bool) {
	w, ok := n.(*syntax.Word)
	if !ok || len(w.Parts) != 1 {
		return "", false
	}
	l, ok := w.Parts[0].(*syntax.Lit)
	if !ok || !syntax.ValidName(l.Value) {
		return "", false
	}
	return l.Value, true
}

func mathSubst(payload string) syntax.WordPart {
	return substPart("math", "--scale=0", "\""+payload+"\"")
}

func setCall(name string, value syntax.WordPart) syntax.Command {
	// The zero position on the command word marks this command as
	// synthesized; source positions always have a column >= 1.
	return &syntax.CallExpr{Args: []*syntax.Word{
		{Parts: []syntax.WordPart{&syntax.Lit{Value: "set"}}},
		litWord(name),
		{Parts: []syntax.WordPart{value}},
	}}
}

func (e *emitter) operatorExpansion(pe *syntax.ParamExp) ([]syntax.WordPart, bool) {
	if pe.Param == nil {
		return nil, false
	}
	name := e.varName(pe.Param.Value)
	var ref *syntax.Word
	varPlain := "$" + name
	varQuoted := "\"" + varPlain + "\""
	if pe.Param.Value == "-" {
		e.warn(pe.Pos(), "$- has no fish equivalent; fish uses status subcommands (e.g. status is-interactive)")
		ref = dq(statusDashPart())
		varPlain = "\"$(status is-interactive && echo i || echo '')\""
		varQuoted = varPlain
	} else if isDigits(name) {
		ref = dq(argvParam(), litPart("["+name+"]"))
		varPlain = "$argv[" + name + "]"
		varQuoted = "\"" + varPlain + "\""
	} else {
		ref = dq(namedParam(name))
	}
	printVal := func(val *syntax.Word) *syntax.Stmt {
		return callStmt(litWord("printf"), litWord(`%s\n`), val)
	}

	switch {
	case pe.Length && pe.Index != nil:
		if tok, ok := e.arrayIndex(pe.Index); !ok || (tok != "@" && tok != "*") {
			return nil, false
		}
		return []syntax.WordPart{substPart("count", "$"+name)}, true

	case pe.Index != nil && pe.Slice != nil:
		tok, ok := e.arrayIndex(pe.Index)
		if !ok || (tok != "@" && tok != "*") {
			return nil, false
		}
		start, ok := intLiteral(pe.Slice.Offset)
		if !ok {
			e.warn(pe.Pos(), "slice offsets must be plain integers; left untranslated")
			return nil, false
		}
		end := "-1"
		if pe.Slice.Length != nil {
			n, ok := intLiteral(pe.Slice.Length)
			if !ok {
				e.warn(pe.Pos(), "slice lengths must be plain integers; left untranslated")
				return nil, false
			}
			if start < 0 {
				endVal := start + n - 1
				if endVal >= 0 {
					end = "-1"
				} else {
					end = strconv.Itoa(endVal)
				}
			} else {
				end = strconv.Itoa(start + n)
			}
		}
		startStr := strconv.Itoa(start + 1)
		if start < 0 {
			startStr = strconv.Itoa(start)
		}
		return []syntax.WordPart{namedParam(name),
			litPart(fmt.Sprintf("[%s..%s]", startStr, end))}, true

	case pe.Index != nil && pe.Exp != nil &&
		(pe.Exp.Op == syntax.RemSmallSuffix || pe.Exp.Op == syntax.RemLargeSuffix ||
			pe.Exp.Op == syntax.RemSmallPrefix || pe.Exp.Op == syntax.RemLargePrefix):
		tok, ok := e.arrayIndex(pe.Index)
		if !ok || tok == "@" || tok == "*" {
			return nil, false
		}
		lazy := pe.Exp.Op == syntax.RemSmallSuffix || pe.Exp.Op == syntax.RemSmallPrefix
		body, ok := globToRegex(e.render(pe.Exp.Word), lazy)
		if !ok {
			return nil, false
		}
		if pe.Exp.Op == syntax.RemSmallPrefix || pe.Exp.Op == syntax.RemLargePrefix {
			body = "^" + body
		} else {
			body += "$"
		}
		return []syntax.WordPart{substPart("string", "replace", "--regex", "--",
			fishSingleQuote(body), "''", "$"+name+"["+tok+"]")}, true

	case pe.Index != nil && pe.Exp != nil:
		e.warn(pe.Pos(), "%s on an indexed array element is left untranslated",
			pe.Exp.Op.String())
		return []syntax.WordPart{pe}, true

	case pe.Index != nil:
		if cv, ok := bashContextVars[name]; ok {
			tok, _ := e.arrayIndex(pe.Index)
			if tok != "1" && tok != "@" && tok != "" {
				e.warn(pe.Pos(), "context array index %s beyond depth 0 is not supported in fish; emitted current context", tok)
			}
			return []syntax.WordPart{substPart(cv...)}, true
		}

		tok, ok := e.arrayIndex(pe.Index)
		switch {
		case !ok:
			e.warn(pe.Pos(), "dynamic array index cannot be shifted; left untranslated")
			return []syntax.WordPart{pe}, true
		case tok == "@" || tok == "*":
			return []syntax.WordPart{namedParam(name)}, true
		default:
			return []syntax.WordPart{namedParam(name), litPart("[" + tok + "]")}, true
		}

	case pe.Length:
		return []syntax.WordPart{stringUnarySubst("length", name)}, true

	case pe.Slice != nil:
		start, ok := intLiteral(pe.Slice.Offset)
		if !ok {
			e.warn(pe.Pos(), "substring offsets must be plain integers; left untranslated")
			return nil, false
		}
		startArg := strconv.Itoa(start + 1)
		if start < 0 {
			startArg = strconv.Itoa(start)
		}
		args := []string{"string", "sub", "--start=" + startArg}
		if pe.Slice.Length != nil {
			n, ok := intLiteral(pe.Slice.Length)
			if !ok {
				e.warn(pe.Pos(), "substring lengths must be plain integers; left untranslated")
				return nil, false
			}
			args = append(args, "--length="+strconv.Itoa(n))
		}
		args = append(args, "--", varQuoted)
		return []syntax.WordPart{substPart(args...)}, true

	case pe.Repl != nil:
		regexArg, ok := e.renderDynamicRegex(pe.Repl.Orig, false, false, true)
		if !ok {
			return nil, false
		}
		args := []string{"string", "replace", "--regex"}
		if pe.Repl.All {
			args = append(args, "--all")
		}
		args = append(args, "--",
			regexArg,
			fishSingleQuote(e.render(pe.Repl.With)),
			varPlain)
		return []syntax.WordPart{substPart(args...)}, true

	case pe.Exp != nil:
		exp := pe.Exp
		// The default/alternate/pattern word may itself contain
		// parameter expansions; rewrite them before rendering, since the
		// outer walk visits this node before its nested words.
		if exp.Word != nil {
		exp.Word.Parts = e.spliceParts(exp.Word.Parts, false)
		}
		switch exp.Op {
		case syntax.DefaultUnsetOrNull, syntax.DefaultUnset:
			if (exp.Word == nil || isEmptyValue(exp.Word)) && isDigits(name) {
				return []syntax.WordPart{argvParam(), litPart("[" + name + "]")}, true
			}
			var cond *syntax.Stmt
			if exp.Op == syntax.DefaultUnsetOrNull {
				cond = callStmt(litWord("test"), litWord("-n"), ref)
			} else if isDigits(name) {
				cond = callStmt(litWord("test"), litWord("(count $argv)"), litWord("-ge"), litWord(name))
			} else {
				cond = callStmt(litWord("set"), litWord("--query"), litWord(name))
			}
			chain := binCmd(syntax.OrStmt,
				binCmd(syntax.AndStmt,
					cond,
					printVal(ref)),
				printVal(e.argWord(exp.Word)))
			return []syntax.WordPart{&syntax.CmdSubst{Stmts: []*syntax.Stmt{chain}}}, true

		case syntax.AlternateUnsetOrNull, syntax.AlternateUnset:
			var cond *syntax.Stmt
			if exp.Op == syntax.AlternateUnsetOrNull {
				cond = callStmt(litWord("test"), litWord("-n"), ref)
			} else if isDigits(name) {
				cond = callStmt(litWord("test"), litWord("(count $argv)"), litWord("-ge"), litWord(name))
			} else {
				cond = callStmt(litWord("set"), litWord("--query"), litWord(name))
			}
			chain := binCmd(syntax.OrStmt,
				binCmd(syntax.AndStmt, cond, printVal(e.argWord(exp.Word))),
				callStmt(litWord("true")))
			return []syntax.WordPart{&syntax.CmdSubst{Stmts: []*syntax.Stmt{chain}}}, true

		case syntax.RemSmallPrefix, syntax.RemLargePrefix,
			syntax.RemSmallSuffix, syntax.RemLargeSuffix:
			if exp.Word == nil {
				// `${x%}` / `${x#}` strip an empty pattern: identity.
				if isDigits(name) {
					return []syntax.WordPart{argvParam(), litPart("[" + name + "]")}, true
				}
				if name == "-" {
					return []syntax.WordPart{statusDashPart()}, true
				}
				return []syntax.WordPart{namedParam(name)}, true
			}
			lazy := exp.Op == syntax.RemSmallPrefix || exp.Op == syntax.RemSmallSuffix
			isPrefix := exp.Op == syntax.RemSmallPrefix || exp.Op == syntax.RemLargePrefix
			regexArg, ok := e.renderDynamicRegex(exp.Word, isPrefix, !isPrefix, lazy)
			if !ok {
				return nil, false
			}
			return []syntax.WordPart{substPart("string", "replace", "--regex", "--",
				regexArg, "''", varPlain)}, true
		case syntax.UpperAll:
			if exp.Word != nil {
				e.warn(pe.Pos(), "case modification with pattern is not supported; left untranslated")
				return nil, false
			}
			return []syntax.WordPart{stringUnarySubst("upper", name)}, true

		case syntax.LowerAll:
			if exp.Word != nil {
				e.warn(pe.Pos(), "case modification with pattern is not supported; left untranslated")
				return nil, false
			}
			return []syntax.WordPart{stringUnarySubst("lower", name)}, true

		default:
			e.warn(pe.Pos(), "parameter expansion %q has no fish equivalent; left untranslated",
				exp.Op.String())
		}
	}
	return nil, false
}

// fishSingleQuote escapes single quotes for use in fish single-quoted literals.
func fishSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `\'`) + "'"
}

// argWord renders a word for use as a single printf argument: bare when
// plain, double-quoted around expansions, single-quoted when it embeds
// double quotes but no expansions.
func (e *emitter) argWord(w *syntax.Word) *syntax.Word {
	if w == nil {
		// `${x-}`, `${x:-}`, `${x:+}` with an empty default parse with
		// a nil Word; render as an explicit empty argument.
		return litWord("''")
	}
	s := e.render(w)
	switch {
	case s == "":
		return litWord("''")
	case !strings.ContainsAny(s, " $\t\"'"):
		return litWord(s)
	case strings.Contains(s, "$"):
		if strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) && len(s) >= 2 {
			return litWord(s)
		}
		var b strings.Builder
		b.WriteByte('"')
		runes := []rune(s)
		depth := 0
		for i := 0; i < len(runes); i++ {
			if runes[i] == '$' && i+1 < len(runes) && runes[i+1] == '(' {
				depth++
				b.WriteString("$(")
				i++
				continue
			}
			if runes[i] == ')' && depth > 0 {
				depth--
				b.WriteRune(')')
				continue
			}
			if runes[i] == '\\' && i+1 < len(runes) {
				b.WriteRune('\\')
				b.WriteRune(runes[i+1])
				i++
				continue
			}
			if runes[i] == '"' {
				if depth == 0 {
					b.WriteString(`\"`)
				} else {
					b.WriteRune('"')
				}
				continue
			}
			b.WriteRune(runes[i])
		}
		b.WriteByte('"')
		return litWord(b.String())
	case strings.Contains(s, `"`):
		return litWord(fishSingleQuote(s))
	default:
		return dq(litPart(s))
	}
}

func callStmt(args ...*syntax.Word) *syntax.Stmt {
	return &syntax.Stmt{Position: syntax.NewPos(1, 1, 1), Cmd: &syntax.CallExpr{Args: args}}
}

func binCmd(op syntax.BinCmdOperator, x, y *syntax.Stmt) *syntax.Stmt {
	return &syntax.Stmt{Cmd: &syntax.BinaryCmd{Op: op, X: x, Y: y}}
}

// intLiteral extracts a plain integer (positive, zero, or negative) from an arithmetic
// leaf; dynamic expressions are not supported yet.
func intLiteral(n syntax.ArithmExpr) (int, bool) {
	if n == nil {
		return 0, false
	}
	switch a := n.(type) {
	case *syntax.ParenArithm:
		return intLiteral(a.X)
	case *syntax.UnaryArithm:
		if a.Op == syntax.Minus && !a.Post {
			sub, ok := intLiteral(a.X)
			if !ok {
				return 0, false
			}
			return -sub, true
		}
		if a.Op == syntax.Plus && !a.Post {
			return intLiteral(a.X)
		}
		return 0, false
	case *syntax.Word:
		if len(a.Parts) != 1 {
			return 0, false
		}
		l, ok := a.Parts[0].(*syntax.Lit)
		if !ok {
			return 0, false
		}
		v, err := strconv.Atoi(strings.TrimSpace(l.Value))
		return v, err == nil
	default:
		return 0, false
	}
}

// globToRegex converts a bash glob pattern to a Go regex body. lazy
// selects shortest-match quantifiers for the small %/# operator forms.
func globToRegex(pat string, lazy bool) (string, bool) {
	var b strings.Builder
	runes := []rune(pat)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch r {
		case '\\':
			if i+1 < len(runes) {
				i++
				b.WriteString(regexp.QuoteMeta(string(runes[i])))
			}
		case '*':
			if lazy {
				b.WriteString(".*?")
			} else {
				b.WriteString(".*")
			}
		case '?':
			b.WriteString(".")
		case '[':
			if closeIdx := findClosingBracket(runes, i); closeIdx != -1 {
				classContent := convertBracketClass(runes[i+1 : closeIdx])
				b.WriteString(classContent)
				i = closeIdx
			} else {
				b.WriteString(regexp.QuoteMeta(string(r)))
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	return b.String(), true
}

func containsParam(w *syntax.Word) bool {
	if w == nil {
		return false
	}
	found := false
	syntax.Walk(w, func(n syntax.Node) bool {
		if _, ok := n.(*syntax.ParamExp); ok {
			found = true
			return false
		}
		if _, ok := n.(*syntax.CmdSubst); ok {
			found = true
			return false
		}
		return true
	})
	return found
}

func (e *emitter) renderDynamicRegex(w *syntax.Word, isPrefix bool, isSuffix bool, lazy bool) (string, bool) {
	if !containsParam(w) {
		body, ok := globToRegex(e.render(w), lazy)
		if !ok {
			return "", false
		}
		if isPrefix {
			body = "^" + body
		}
		if isSuffix {
			body += "$"
		}
		return fishSingleQuote(body), true
	}

	var segments []string
	if isPrefix {
		segments = append(segments, fishSingleQuote("^"))
	}

	var walkParts func(parts []syntax.WordPart) bool
	walkParts = func(parts []syntax.WordPart) bool {
		for _, p := range parts {
			switch pt := p.(type) {
			case *syntax.Lit:
				re, ok := globToRegex(pt.Value, lazy)
				if !ok {
					return false
				}
				if re != "" {
					segments = append(segments, fishSingleQuote(re))
				}
			case *syntax.SglQuoted:
				re := regexp.QuoteMeta(pt.Value)
				if re != "" {
					segments = append(segments, fishSingleQuote(re))
				}
			case *syntax.DblQuoted:
				if !walkParts(pt.Parts) {
					return false
				}
			case *syntax.ParamExp:
				rendered := e.renderWordSmart(&syntax.Word{Parts: []syntax.WordPart{pt}})
				segments = append(segments, fmt.Sprintf("(string escape --style=regex -- \"%s\")", rendered))
			default:
				rendered := e.renderWordSmart(&syntax.Word{Parts: []syntax.WordPart{pt}})
				if strings.HasPrefix(rendered, "$") {
					segments = append(segments, fmt.Sprintf("(string escape --style=regex -- \"%s\")", rendered))
				} else {
					segments = append(segments, fmt.Sprintf("(string escape --style=regex -- %s)", rendered))
				}
			}
		}
		return true
	}

	if !walkParts(w.Parts) {
		return "", false
	}
	if isSuffix {
		segments = append(segments, fishSingleQuote("$"))
	}
	if len(segments) == 0 {
		return "''", true
	}
	return strings.Join(segments, ""), true
}

func findClosingBracket(runes []rune, start int) int {
	if start+1 >= len(runes) {
		return -1
	}
	j := start + 1
	if runes[j] == '!' || runes[j] == '^' {
		j++
	}
	if j < len(runes) && runes[j] == ']' {
		j++
	}
	for ; j < len(runes); j++ {
		if runes[j] == '\\' && j+1 < len(runes) {
			j++
			continue
		}
		if runes[j] == '[' && j+1 < len(runes) && (runes[j+1] == ':' || runes[j+1] == '.' || runes[j+1] == '=') {
			term := runes[j+1]
			j += 2
			for ; j+1 < len(runes); j++ {
				if runes[j] == term && runes[j+1] == ']' {
					j++
					break
				}
			}
			continue
		}
		if runes[j] == ']' {
			return j
		}
	}
	return -1
}

func convertBracketClass(content []rune) string {
	var b strings.Builder
	b.WriteByte('[')
	if len(content) == 0 {
		b.WriteByte(']')
		return b.String()
	}
	idx := 0
	if content[0] == '!' || content[0] == '^' {
		b.WriteByte('^')
		idx++
	}
	for ; idx < len(content); idx++ {
		c := content[idx]
		if c == '[' && idx+1 < len(content) && (content[idx+1] == ':' || content[idx+1] == '.' || content[idx+1] == '=') {
			term := content[idx+1]
			b.WriteRune(c)
			b.WriteRune(term)
			idx += 2
			for ; idx < len(content); idx++ {
				b.WriteRune(content[idx])
				if content[idx] == term && idx+1 < len(content) && content[idx+1] == ']' {
					idx++
					b.WriteRune(']')
					break
				}
			}
			continue
		}
		switch c {
		case '\\':
			if idx+1 < len(content) {
				idx++
				b.WriteString(regexp.QuoteMeta(string(content[idx])))
			} else {
				b.WriteString(`\\`)
			}
		case ']':
			b.WriteString(`\]`)
		default:
			b.WriteRune(c)
		}
	}
	b.WriteByte(']')
	return b.String()
}

func dq(parts ...syntax.WordPart) *syntax.Word {
	return &syntax.Word{Parts: []syntax.WordPart{&syntax.DblQuoted{Parts: parts}}}
}

func (e *emitter) varName(name string) string {
	if name == "PIPESTATUS" {
		return "pipestatus"
	}
	if name == "DIRSTACK" {
		return "dirstack"
	}
	return mangleVarName(name)
}

func singleBareParam(w *syntax.Word) (*syntax.ParamExp, bool) {
	if w == nil || len(w.Parts) != 1 {
		return nil, false
	}
	pe, ok := w.Parts[0].(*syntax.ParamExp)
	if !ok || !bareParam(pe) || pe.Param == nil {
		return nil, false
	}
	return pe, true
}
func singleBareCmdSubst(w *syntax.Word) (*syntax.CmdSubst, bool) {
	if w == nil || len(w.Parts) != 1 {
		return nil, false
	}
	cs, ok := w.Parts[0].(*syntax.CmdSubst)
	if !ok {
		return nil, false
	}
	return cs, true
}


func extractHdoc(redirs []*syntax.Redirect) (*syntax.Redirect, []*syntax.Redirect) {
	var hdoc *syntax.Redirect
	var rest []*syntax.Redirect
	for _, r := range redirs {
		if r.Op == syntax.Hdoc || r.Op == syntax.DashHdoc || r.Op == syntax.WordHdoc {
			if hdoc == nil {
				hdoc = r
			} else {
				rest = append(rest, r)
			}
		} else {
			rest = append(rest, r)
		}
	}
	return hdoc, rest
}

func (e *emitter) emitHdoc(s *syntax.Stmt, hdoc *syntax.Redirect, rest []*syntax.Redirect) {
	for _, r := range rest {
		if r.Op == syntax.Hdoc || r.Op == syntax.DashHdoc || r.Op == syntax.WordHdoc {
			e.warn(s.Position, "multiple here-documents on a single command are not supported; only one is converted to pipeline")
			break
		}
	}
	origRedirs := s.Redirs
	s.Redirs = rest
	var cmdText string
	if hasStructural(s) {
		cmdText = e.inlineStmtText(s)
	} else {
		cmdText = e.render(s)
	}
	s.Redirs = origRedirs

	var prefix string
	if hdoc.Op == syntax.WordHdoc {
		wordText := e.render(hdoc.Word)
		prefix = fmt.Sprintf("printf '%%s\\n' %s", wordText)
	} else {
		body := ""
		if hdoc.Hdoc != nil {
			body = e.render(hdoc.Hdoc)
		}
		if isQuotedHdocWord(hdoc.Word) {
			escaped := strings.ReplaceAll(body, "'", `\'`)
			prefix = fmt.Sprintf("printf '%%s\\n' '%s'", escaped)
		} else {
			body = unescapeBackticks(body)
			escaped := strings.ReplaceAll(body, `"`, `\"`)
			prefix = fmt.Sprintf("printf '%%s\\n' \"%s\"", escaped)
		}
	}

	if strings.Contains(cmdText, "\n") {
		lines := strings.Split(cmdText, "\n")
		e.printf("%s | %s", prefix, lines[0])
		for _, l := range lines[1:] {
			e.printf("%s", l)
		}
	} else {
		e.printf("%s | %s", prefix, cmdText)
	}
}

func isQuotedHdocWord(w *syntax.Word) bool {
	if w == nil {
		return false
	}
	for _, p := range w.Parts {
		switch p.(type) {
		case *syntax.SglQuoted, *syntax.DblQuoted:
			return true
		case *syntax.Lit:
			if strings.ContainsAny(p.(*syntax.Lit).Value, `\'"`) {
				return true
			}
		}
	}
	return false
}
