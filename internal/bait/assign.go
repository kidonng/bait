package bait

import (
	"fmt"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// assignStmt rewrites a pure assignment statement (assignments with no
// command) into fish set commands. Bash assignments made inside a
// function body are global, so they map to "set --global". Array
// literals flatten into list values; constant indices shift by one.
func (e *emitter) assignStmt(s *syntax.Stmt, c *syntax.CallExpr) {
	scope := ""
	if e.inFunction {
		scope = "--global"
	}
	var lines []string
	for _, a := range c.Assigns {
		curScope := scope
		if a.Name != nil {
			if e.inFunction && e.funcLocals != nil && (e.funcLocals[a.Name.Value] || e.funcLocals[e.varName(a.Name.Value)]) {
				curScope = ""
			}
		}
		if a.Name != nil && e.commandPrefixVars[a.Name.Value] && isEmptyValue(a.Value) {
			if curScope != "" {
				lines = append(lines, fmt.Sprintf("set %s %s", curScope, e.varName(a.Name.Value)))
			} else {
				lines = append(lines, fmt.Sprintf("set %s", e.varName(a.Name.Value)))
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
			lines = append(lines, fmt.Sprintf("set %s--append %s%s", scopePrefix(curScope), e.varName(a.Name.Value),
				e.arrayElemArgs(a.Array)))

		case a.Index != nil:
			tok, ok := e.arrayIndex(a.Index)
			if !ok {
				e.warn(s.Position, "dynamic array index cannot be shifted; emitted verbatim")
				e.lines(e.render(s))
				return
			}
			lines = append(lines, fmt.Sprintf("set %s%s[%s] %s", scopePrefix(curScope), e.varName(a.Name.Value), tok,
				e.assignValue(a)))

		case a.Array != nil:
			lines = append(lines, fmt.Sprintf("set %s%s%s", scopePrefix(curScope), e.varName(a.Name.Value),
				e.arrayElemArgs(a.Array)))

		default:
			lines = append(lines, setLineText(curScope, e.varName(a.Name.Value), e.assignValue(a)))
		}
	}
	sc := classifyComments(s)
	e.leadingComments(sc.leading)
	e.printLinesWithTrailing(lines, sc.trailing)
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
					val := e.renderWordSmart(a.Value)
					unquoted := unquoteArg(val)
					if mangled, ok := fishReservedVars[unquoted]; ok {
						args = append(args, mangled)
					} else {
						args = append(args, val)
					}
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
		sc := classifyComments(s)
		e.leadingComments(sc.leading)
		if len(args) == 0 {
			e.printLineWithTrailing("export", sc.trailing)
		} else {
			e.printLineWithTrailing(fmt.Sprintf("export %s", strings.Join(args, " ")), sc.trailing)
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
	var lines []string
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
				e.funcLocals[e.varName(a.Name.Value)] = true
			}
			if e.commandPrefixVars[a.Name.Value] && isEmptyValue(a.Value) {
				if scope != "" {
					lines = append(lines, fmt.Sprintf("set %s %s", scope, e.varName(a.Name.Value)))
				} else {
					lines = append(lines, fmt.Sprintf("set %s", e.varName(a.Name.Value)))
				}
				continue
			}
			lines = append(lines, setLineText(scope, e.varName(a.Name.Value), e.assignValue(a)))
		}
	}
	sc := classifyComments(s)
	e.leadingComments(sc.leading)
	e.printLinesWithTrailing(lines, sc.trailing)
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

func setLineText(scope, name, value string) string {
	if scope != "" {
		return fmt.Sprintf("set %s %s %s", scope, name, value)
	}
	return fmt.Sprintf("set %s %s", name, value)
}

// setLine emits one set command; generated fish always uses long options.
func (e *emitter) setLine(scope, name, value string) {
	if scope != "" {
		e.printf("set %s %s %s", scope, name, value)
		return
	}
	e.printf("set %s %s", name, value)
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

func isSynthesizedSet(c *syntax.CallExpr) bool {
	return c != nil && len(c.Args) == 3 && c.Args[0].Pos().Col() == 0 && isLitWord(c.Args[0], "set")
}

func (e *emitter) shiftCmd(s *syntax.Stmt, c *syntax.CallExpr) {
	sc := classifyComments(s)
	e.leadingComments(sc.leading)
	tail := e.tails(s)
	if len(c.Args) <= 1 {
		e.printLineWithTrailing("set --erase argv[1]"+tail, sc.trailing)
		return
	}
	arg := e.render(c.Args[1])
	if n, err := strconv.Atoi(arg); err == nil {
		switch {
		case n <= 0:
			e.printLineWithTrailing("true"+tail, sc.trailing)
		case n == 1:
			e.printLineWithTrailing("set --erase argv[1]"+tail, sc.trailing)
		default:
			e.printLineWithTrailing(fmt.Sprintf("set --erase argv[1..%d]%s", n, tail), sc.trailing)
		}
		return
	}
	argWord := e.renderWordSmart(c.Args[1])
	unquoted := unquoteArg(argWord)
	e.printLineWithTrailing(fmt.Sprintf("test \"%s\" -gt 0 2>/dev/null; and set --erase argv[1..%s]%s", unquoted, unquoted, tail), sc.trailing)
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
