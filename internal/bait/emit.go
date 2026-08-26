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
	buf        bytes.Buffer
	depth      int
	printer    *syntax.Printer
	warnings   []Warning
	err        error
	inFunction bool
}

func newEmitter() *emitter {
	return &emitter{
		printer: syntax.NewPrinter(syntax.SpaceRedirects(true)),
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
func (e *emitter) redirect(r *syntax.Redirect) string {
	s := ""
	if r.N != nil {
		s += r.N.Value
	}
	s += r.Op.String()
	return s + " " + e.render(r.Word)
}

// tails renders the redirections and background marker that trail a
// structural statement, for appending to its closing line.
func (e *emitter) tails(s *syntax.Stmt) string {
	out := ""
	for _, r := range s.Redirs {
		out += " " + e.redirect(r)
	}
	if s.Background {
		out += " &"
	}
	return out
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
		if c, ok := cond[0].Cmd.(*syntax.CallExpr); ok {
			e.warnBashOnlyBuiltin(cond[0], c)
		}
		return e.render(cond[0])
	}
	parts := make([]string, len(cond))
	for i, st := range cond {
		parts[i] = e.render(st)
	}
	return "begin " + strings.Join(parts, "; ") + "; end"
}

func (e *emitter) file(f *syntax.File) {
	for _, stmt := range f.Stmts {
		e.stmt(stmt)
	}
	for _, c := range f.Last {
		e.comment(c)
	}
}

func (e *emitter) stmt(s *syntax.Stmt) {
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
		e.warnBashOnlyBuiltin(s, c)
		if len(c.Assigns) == 0 && e.isSetBuiltin(c) {
			if e.setCmd(s, c) {
				return
			}
		}
	}
	e.lines(e.render(s))
}

// warnBashOnlyBuiltin flags bash builtins that fish lacks entirely (or
// whose fish counterpart differs fatally). They are emitted verbatim
// with a warning: silent passthrough would only fail at runtime.
var bashOnlyBuiltins = map[string]string{
	"hash":    "use 'command -v' or 'type -q' instead",
	"trap":    "use function --on-event handlers instead",
	"unset":   "use 'set --erase NAME' instead",
	"shift":   "reassign $argv instead",
	"let":     "use 'math' instead",
	"getopts": "use 'argparse' inside functions instead",
	"pushd":   "fish has no directory stack",
	"popd":    "fish has no directory stack",
	"dirs":    "fish has no directory stack",
	"shopt":   "fish has no shell options",
	"ulimit":  "fish has no ulimit builtin",
	"unalias": "fish aliases are functions; remove the function instead",
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

// setCmd rewrites bash's set builtin. `set --` and bash's flagless
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
	if head != "--" && (strings.HasPrefix(head, "-") || strings.HasPrefix(head, "+")) {
		e.warn(s.Position, "set flags have no fish equivalent; statement dropped")
		return true
	}
	if head == "--" {
		args = args[1:]
	}
	out := make([]*syntax.Word, 0, len(args)+2)
	out = append(out, litWord("set"), litWord("argv"))
	out = append(out, args...)
	c.Args = out
	return false
}

// stripReadRawFlags removes bash's read -r flag. fish's read builtin has
// no -r option: it never processes backslashes on input, so bash's raw
// mode is already the default, and an unrecognized "-r" would be taken
// for a variable name.
func stripReadRawFlags(c *syntax.CallExpr) {
	if len(c.Args) == 0 || !isLitWord(c.Args[0], "read") {
		return
	}
	filtered := c.Args[:0]
	for i, w := range c.Args {
		if i > 0 && isLitWord(w, "-r") {
			continue
		}
		filtered = append(filtered, w)
	}
	c.Args = filtered
}

func isLitWord(w *syntax.Word, val string) bool {
	if len(w.Parts) != 1 {
		return false
	}
	lit, ok := w.Parts[0].(*syntax.Lit)
	return ok && lit.Value == val
}

// normalize applies mechanical AST fixes before emission: read-flag
// removal, fish-side parameter rewrites, and arithmetic translation.
func (e *emitter) normalize(f *syntax.File) {
	syntax.Walk(f, func(n syntax.Node) bool {
		if c, ok := n.(*syntax.CallExpr); ok {
			stripReadRawFlags(c)
		}
		return true
	})
	e.rewriteParams(f)
	e.rewriteArithmetic(f)
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
		e.warn(bcmd.OpPos, "combiner over a compound statement is not translated; emitted verbatim")
		e.printf("%s", e.render(s))
		return
	}
	if chainHasAssignment(bcmd) {
		e.emitChain(bcmd)
		return
	}
	e.simple(s)
}

func chainHasAssignment(b *syntax.BinaryCmd) bool {
	found := false
	syntax.Walk(b, func(n syntax.Node) bool {
		c, ok := n.(*syntax.CallExpr)
		if !ok {
			return true
		}
		if len(c.Args) == 0 && len(c.Assigns) > 0 {
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

// chainLeaf renders one leaf of a combiner chain: pure assignments
// become set commands, everything else renders verbatim.
func (e *emitter) chainLeaf(st *syntax.Stmt) string {
	c, ok := st.Cmd.(*syntax.CallExpr)
	if !ok || len(c.Args) != 0 || len(c.Assigns) == 0 {
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
		parts = append(parts, fmt.Sprintf("set %s%s %s",
			scopePrefix(scope), a.Name.Value, e.assignValue(a)))
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
	switch s.Cmd.(type) {
	case *syntax.CallExpr, *syntax.BinaryCmd:
		return false
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
		items[i] = e.render(w)
	}
	if !iter.InPos.IsValid() {
		// bash iterates the positional parameters when "in" is omitted.
		items = []string{"$argv"}
	}
	tail := e.tails(s)
	e.wrapperComments(s)
	e.printf("for %s in %s", iter.Name.Value, strings.Join(items, " "))
	e.body(f.Do, f.DoLast)
	e.printf("end%s", tail)
}

func (e *emitter) caseClause(s *syntax.Stmt, cl *syntax.CaseClause) {
	tail := e.tails(s)
	e.wrapperComments(s)
	e.printf("switch %s", e.render(cl.Word))
	for _, item := range cl.Items {
		if item.Op != syntax.Break {
			e.warn(item.Pos(), "case fallthrough (%s) has no fish equivalent; converted to a plain case", item.Op)
		}
		patterns := make([]string, len(item.Patterns))
		for i, p := range item.Patterns {
			patterns[i] = e.render(p)
		}
		e.printf("case %s", strings.Join(patterns, " "))
		e.body(item.Stmts, item.Last)
	}
	for _, c := range cl.Last {
		e.comment(c)
	}
	e.printf("end%s", tail)
}

func (e *emitter) funcDecl(s *syntax.Stmt, fd *syntax.FuncDecl) {
	tail := e.tails(s)
	e.wrapperComments(s)
	e.printf("function %s", fd.Name.Value)
	saved := e.inFunction
	e.inFunction = true
	body := fd.Body.Cmd
	if bcmd, ok := body.(*syntax.BinaryCmd); ok && bcmd.Op == syntax.AndStmt {
		if blk, ok := bcmd.X.Cmd.(*syntax.Block); ok {
			// mvdan parses `f() { … } && cmd` with the combiner inside
			// the body; bash applies it to the definition statement,
			// which always succeeds. Close the function, then run cmd.
			e.body(blk.Stmts, blk.Last)
			e.inFunction = saved
			e.printf("end%s", tail)
			e.stmt(bcmd.Y)
			return
		}
	}
	switch b := body.(type) {
	case *syntax.Block:
		e.body(b.Stmts, b.Last)
	default:
		e.body([]*syntax.Stmt{fd.Body}, nil)
	}
	e.inFunction = saved
	e.printf("end%s", tail)
}

func (e *emitter) group(s *syntax.Stmt, stmts []*syntax.Stmt, last []syntax.Comment) {
	tail := e.tails(s)
	e.wrapperComments(s)
	e.printf("begin")
	e.body(stmts, last)
	e.printf("end%s", tail)
}

// describe names a command node for diagnostics.
func describe(cmd syntax.Command) string {
	switch c := cmd.(type) {
	case *syntax.ArithmCmd:
		return "arithmetic command ((...))"
	case *syntax.DeclClause:
		return c.Variant.Value + " declaration"
	case *syntax.TestClause:
		return "[[ ]] test clause"
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
		if a.Name != nil {
			if args, ok := e.selfRefAccumulation(a); ok {
				e.printf("set %s%s %s", scopePrefix(scope),
					a.Name.Value, strings.Join(args, " "))
				continue
			}
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
			e.printf("set %s--append %s%s", scopePrefix(scope), a.Name.Value,
				e.arrayElemArgs(a.Array))

		case a.Index != nil:
			tok, ok := e.arrayIndex(a.Index)
			if !ok {
				e.warn(s.Position, "dynamic array index cannot be shifted; emitted verbatim")
				e.lines(e.render(s))
				return
			}
			e.printf("set %s%s[%s] %s", scopePrefix(scope), a.Name.Value, tok,
				e.assignValue(a))

		case a.Array != nil:
			e.printf("set %s%s%s", scopePrefix(scope), a.Name.Value,
				e.arrayElemArgs(a.Array))

		default:
			e.setLine(scope, a.Name.Value, e.assignValue(a))
		}
	}
}

// selfRefAccumulation detects `X="$X more words"`: bash accumulates a
// string that is word-split at unquoted use sites, while fish reaches
// the same observable behavior by accumulating a list. It returns the
// arguments for `set X $X <words...>`, splitting literal parts on
// whitespace and keeping adjacent fragments in one word.
func (e *emitter) selfRefAccumulation(a *syntax.Assign) ([]string, bool) {
	if a.Value == nil || len(a.Value.Parts) != 1 {
		return nil, false
	}
	q, ok := a.Value.Parts[0].(*syntax.DblQuoted)
	if !ok || len(q.Parts) < 2 {
		return nil, false
	}
	pe, ok := q.Parts[0].(*syntax.ParamExp)
	if !ok || pe.Param == nil || pe.Param.Value != a.Name.Value || !bareParam(pe) {
		return nil, false
	}
	var args []string
	cur := ""
	flush := func() {
		if cur != "" {
			args = append(args, cur)
			cur = ""
		}
	}
	for _, p := range q.Parts[1:] {
		switch p := p.(type) {
		case *syntax.Lit:
			v := p.Value
			if v != strings.TrimLeft(v, " \t") {
				flush()
			}
			for i, f := range strings.Fields(v) {
				if i > 0 {
					flush()
				}
				cur += f
			}
			if v != strings.TrimRight(v, " \t") {
				flush()
			}
		case *syntax.ParamExp:
			cur += e.render(&syntax.Word{Parts: []syntax.WordPart{p}})
		default:
			return nil, false
		}
	}
	flush()
	return append([]string{"$" + a.Name.Value}, args...), true
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
// "${arr[*]}" (optionally quoted) and returns the variable name.
func (e *emitter) soleArrayAll(w *syntax.Word) (string, bool) {
	if len(w.Parts) != 1 {
		return "", false
	}
	part := w.Parts[0]
	if q, ok := part.(*syntax.DblQuoted); ok {
		if len(q.Parts) != 1 {
			return "", false
		}
		part = q.Parts[0]
	}
	pe, ok := part.(*syntax.ParamExp)
	if !ok || pe.Param == nil || pe.Length || pe.Slice != nil ||
		pe.Repl != nil || pe.Exp != nil || pe.Index == nil {
		return "", false
	}
	tok, ok := e.arrayIndex(pe.Index)
	if !ok || tok != "@" {
		return "", false
	}
	return pe.Param.Value, true
}
func (e *emitter) assignValue(a *syntax.Assign) string {
	// A missing value is a typed-nil *Word, which must not reach the
	// printer; bash "x=" and fish 'set x ""' both mean an empty string.
	if a.Value == nil {
		return `""`
	}
	if r := e.render(a.Value); r != "" {
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
		}
		e.printf("%s", e.render(s))
		return
	}
	scope := ""
	switch d.Variant.Value {
	case "local":
		scope = "--local"
	case "declare", "typeset":
		if e.inFunction {
			scope = "--local" // bash declare defaults to local inside functions
		}
	default: // readonly, nameref
		e.warn(s.Position, "%s has no fish equivalent; emitted verbatim", d.Variant.Value)
		e.printf("%s", e.render(s))
		return
	}
	for _, a := range d.Args {
		if e.isOptionArg(a) {
			e.warn(s.Position, "declaration flag %s has no direct fish equivalent; emitted verbatim",
				e.argText(a))
			e.printf("%s", e.render(s))
			return
		}
		if reason := untranslatableAssign(a); reason != "" {
			e.warn(s.Position, "%s cannot be translated to set; emitted verbatim", reason)
			e.printf("%s", e.render(s))
			return
		}
		e.setLine(scope, a.Name.Value, e.assignValue(a))
	}
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
		w, ok := n.(*syntax.Word)
		if !ok {
			return true
		}
		if soleSpecialParam(w, "@", "*") {
			w.Parts = []syntax.WordPart{argvParam()}
			return true
		}
		if name, ok := e.soleArrayAll(w); ok {
			w.Parts = []syntax.WordPart{namedParam(name)}
			return true
		}
		w.Parts = e.spliceParts(w.Parts)
		return true
	})
}

func (e *emitter) spliceParts(parts []syntax.WordPart) []syntax.WordPart {
	out := make([]syntax.WordPart, 0, len(parts)+1)
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.ParamExp:
			out = append(out, e.paramReplacements(p)...)
		case *syntax.ArithmExp:
			if txt, ok := e.arithmText(p.X); ok {
				out = append(out, mathSubst(txt))
			} else {
				e.warn(p.Pos(), "arithmetic uses an operator without a fish math equivalent; left untranslated")
				out = append(out, p)
			}
		case *syntax.DblQuoted:
			p.Parts = e.spliceParts(p.Parts)
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
	switch name {
	case "?":
		return []syntax.WordPart{namedParam("status")}
	case "!":
		return []syntax.WordPart{namedParam("last_pid")}
	case "$":
		return []syntax.WordPart{namedParam("fish_pid")}
	case "#":
		return []syntax.WordPart{substPart("count", "$argv")}
	case "0":
		return []syntax.WordPart{substPart("status", "filename")}
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

func soleSpecialParam(w *syntax.Word, names ...string) bool {
	if len(w.Parts) != 1 {
		return false
	}
	switch p := w.Parts[0].(type) {
	case *syntax.ParamExp:
		return matchesAnySpecial(p, names)
	case *syntax.DblQuoted:
		if len(p.Parts) != 1 {
			return false
		}
		pe, ok := p.Parts[0].(*syntax.ParamExp)
		return ok && matchesAnySpecial(pe, names)
	}
	return false
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

// warnCmdSubstSubshells flags bash subshells nested inside command
// substitutions. Their contents never pass through the statement
// emitter (words print verbatim), and fish parses (...) as command
// substitution, so the raw parens change meaning or fail to parse.
func (e *emitter) warnCmdSubstSubshells(f *syntax.File) {
	syntax.Walk(f, func(n syntax.Node) bool {
		cs, ok := n.(*syntax.CmdSubst)
		if !ok {
			return true
		}
		syntax.Walk(cs, func(inner syntax.Node) bool {
			if p, ok := inner.(*syntax.Subshell); ok {
				e.warn(p.Lparen, "subshell inside a command substitution is emitted verbatim; fish would misparse it")
			}
			return true
		})
		return true
	})
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
	name := pe.Param.Value
	ref := dq(namedParam(name)) // "$name"
	printVal := func(val *syntax.Word) *syntax.Stmt {
		return callStmt(litWord("printf"), litWord(`%s\n`), val)
	}

	switch {
	case pe.Length && pe.Index != nil:
		if tok, ok := e.arrayIndex(pe.Index); !ok || tok != "@" {
			return nil, false
		}
		return []syntax.WordPart{substPart("count", "$"+name)}, true

	case pe.Index != nil && pe.Slice != nil:
		tok, ok := e.arrayIndex(pe.Index)
		if !ok || tok != "@" {
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
			end = strconv.Itoa(start + n)
		}
		return []syntax.WordPart{namedParam(name),
			litPart(fmt.Sprintf("[%d..%s]", start+1, end))}, true

	case pe.Index != nil && pe.Exp != nil &&
		(pe.Exp.Op == syntax.RemSmallSuffix || pe.Exp.Op == syntax.RemLargeSuffix ||
			pe.Exp.Op == syntax.RemSmallPrefix || pe.Exp.Op == syntax.RemLargePrefix):
		tok, ok := e.arrayIndex(pe.Index)
		if !ok || tok == "@" {
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
		return []syntax.WordPart{substPart("string", "replace", "--regex",
			"'"+body+"'", "''", "--", "$"+name+"["+tok+"]")}, true

	case pe.Index != nil && pe.Exp != nil:
		e.warn(pe.Pos(), "%s on an indexed array element is left untranslated",
			pe.Exp.Op.String())
		return []syntax.WordPart{pe}, true

	case pe.Index != nil:
		tok, ok := e.arrayIndex(pe.Index)
		switch {
		case !ok:
			e.warn(pe.Pos(), "dynamic array index cannot be shifted; left untranslated")
			return []syntax.WordPart{pe}, true
		case tok == "@":
			return []syntax.WordPart{namedParam(name)}, true
		default:
			return []syntax.WordPart{namedParam(name), litPart("[" + tok + "]")}, true
		}

	case pe.Length:
		return []syntax.WordPart{substPart("string", "length", "--", "$"+name)}, true

	case pe.Slice != nil:
		start, ok := intLiteral(pe.Slice.Offset)
		if !ok {
			e.warn(pe.Pos(), "substring offsets must be plain integers; left untranslated")
			return nil, false
		}
		args := []string{"string", "sub", "--start=" + strconv.Itoa(start+1)}
		if pe.Slice.Length != nil {
			n, ok := intLiteral(pe.Slice.Length)
			if !ok {
				e.warn(pe.Pos(), "substring lengths must be plain integers; left untranslated")
				return nil, false
			}
			args = append(args, "--length="+strconv.Itoa(n))
		}
		args = append(args, "--", "$"+name)
		return []syntax.WordPart{substPart(args...)}, true

	case pe.Repl != nil:
		re, ok := globToRegex(e.render(pe.Repl.Orig), true)
		if !ok {
			return nil, false
		}
		args := []string{"string", "replace", "--regex"}
		if pe.Repl.All {
			args = append(args, "--all")
		}
		args = append(args,
			"'"+re+"'",
			"'"+e.render(pe.Repl.With)+"'",
			"--", "$"+name)
		return []syntax.WordPart{substPart(args...)}, true

	case pe.Exp != nil:
		exp := pe.Exp
		// The default/alternate/pattern word may itself contain
		// parameter expansions; rewrite them before rendering, since the
		// outer walk visits this node before its nested words.
		if exp.Word != nil {
			exp.Word.Parts = e.spliceParts(exp.Word.Parts)
		}
		switch exp.Op {
		case syntax.DefaultUnsetOrNull:
			chain := binCmd(syntax.OrStmt,
				binCmd(syntax.AndStmt,
					callStmt(litWord("test"), litWord("-n"), ref),
					printVal(ref)),
				printVal(e.argWord(exp.Word)))
			return []syntax.WordPart{&syntax.CmdSubst{Stmts: []*syntax.Stmt{chain}}}, true

		case syntax.DefaultUnset:
			chain := binCmd(syntax.OrStmt,
				binCmd(syntax.AndStmt,
					callStmt(litWord("set"), litWord("--query"), litWord(name)),
					printVal(ref)),
				printVal(e.argWord(exp.Word)))
			return []syntax.WordPart{&syntax.CmdSubst{Stmts: []*syntax.Stmt{chain}}}, true

		case syntax.AlternateUnsetOrNull, syntax.AlternateUnset:
			var cond *syntax.Stmt
			if exp.Op == syntax.AlternateUnsetOrNull {
				cond = callStmt(litWord("test"), litWord("-n"), ref)
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
				return []syntax.WordPart{namedParam(name)}, true
			}
			lazy := exp.Op == syntax.RemSmallPrefix || exp.Op == syntax.RemSmallSuffix
			body, ok := globToRegex(e.render(exp.Word), lazy)
			if !ok {
				return nil, false
			}
			if exp.Op == syntax.RemSmallPrefix || exp.Op == syntax.RemLargePrefix {
				body = "^" + body
			} else {
				body += "$"
			}
			return []syntax.WordPart{substPart("string", "replace", "--regex",
				"'"+body+"'", "''", "--", "$"+name)}, true
		default:
			e.warn(pe.Pos(), "parameter expansion %q has no fish equivalent; left untranslated",
				exp.Op.String())
		}
	}
	return nil, false
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
	case !strings.ContainsAny(s, " $\t\""):
		return litWord(s)
	case strings.Contains(s, "$"):
		// Expansions must stay live, so keep double quotes and escape
		// the inner ones; fish treats \" inside "..." as a literal.
		return litWord(`"` + strings.ReplaceAll(s, `"`, `\"`) + `"`)
	case strings.Contains(s, `"`):
		return litWord("'" + s + "'")
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

// intLiteral extracts a plain non-negative integer from an arithmetic
// leaf; dynamic expressions are not supported yet.
func intLiteral(n syntax.ArithmExpr) (int, bool) {
	w, ok := n.(*syntax.Word)
	if !ok || len(w.Parts) != 1 {
		return 0, false
	}
	l, ok := w.Parts[0].(*syntax.Lit)
	if !ok || !isDigits(l.Value) {
		return 0, false
	}
	v, err := strconv.Atoi(l.Value)
	return v, err == nil
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
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	return b.String(), true
}

func dq(parts ...syntax.WordPart) *syntax.Word {
	return &syntax.Word{Parts: []syntax.WordPart{&syntax.DblQuoted{Parts: parts}}}
}
