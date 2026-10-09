package bait

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// hasStructuralCmdSubst reports whether any command substitution or process
// substitution inside n contains statements that require translation.
func (e *emitter) hasStructuralCmdSubst(n syntax.Node) bool {
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
				if hasStructural(st) || e.stmtNeedsRewrite(st) || stmtHasHighFDTargetRedir(st) {
					found = true
					return false
				}
			}
		}
		return true
	})
	return found
}

// stmtNeedsRewrite reports whether a statement contains something the
// translator must rewrite (assignments, set/shift builtins, test -v,
// command-name indirection, leading or high-fd redirections).
func (e *emitter) stmtNeedsRewrite(st *syntax.Stmt) bool {
	if st == nil || e.synthStmts[st] {
		return false
	}
	if hasLeadingRedir(st) || hasHighFDTargetRedir(st.Redirs) {
		return true
	}
	switch c := st.Cmd.(type) {
	case *syntax.BinaryCmd:
		return e.chainNeedsRewrite(c)
	case *syntax.CallExpr:
		if len(c.Args) == 0 && len(c.Assigns) > 0 {
			return true
		}
		if len(c.Args) > 0 && isLitWord(c.Args[0], "shift") {
			return true
		}
		if len(c.Args) > 0 && isLitWord(c.Args[0], "unset") {
			if names, rest := unsetShadowable(c); len(names) > 0 || rest {
				return true
			}
		}
		if len(c.Assigns) == 0 && len(c.Args) > 0 && (e.isSetBuiltin(c) || e.isSynthesizedSet(c)) {
			return true
		}
		if len(c.Assigns) == 0 && len(c.Args) > 0 {
			if _, ok := singleBareParam(c.Args[0]); ok {
				return true
			}
		}
		if _, _, ok := matchTestDashV(c); ok {
			return true
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
	if !e.hasStructuralCmdSubst(w) {
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
		sub.newline(stmtStartPos(st))
		sub.stmt(st)
		sub.advanceLine(stmtEndLine(st))
	}
	for _, c := range cs.Last {
		sub.newline(c.Pos())
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
	if cmdSubstAssigns(cs.Stmts) || cmdSubstUnserts(cs.Stmts) {
		// bash scopes assignments and unsets to the substitution's
		// subshell; the block keeps fish's --local sets from outliving it.
		text = "begin\n" + text + "\nend"
	}
	pad := strings.Repeat(indentUnit, e.depth)
	lines := strings.Split(text, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = pad + lines[i]
	}
	return "$(" + strings.Join(lines, "\n") + ")", true
}

// cmdSubstAssigns reports whether a command substitution's body assigns a
// variable anywhere bash's subshell would scope to the substitution. A
// nested substitution or function body is its own scope and is skipped.
func cmdSubstAssigns(stmts []*syntax.Stmt) bool {
	found := false
	var walk func(syntax.Node) bool
	walk = func(n syntax.Node) bool {
		if found {
			return false
		}
		switch x := n.(type) {
		case *syntax.CmdSubst, *syntax.FuncDecl:
			return false
		case *syntax.CallExpr:
			if len(x.Assigns) > 0 {
				found = true
				return false
			}
		}
		return true
	}
	for _, st := range stmts {
		syntax.Walk(st, walk)
	}
	return found
}

// cmdSubstUnserts reports whether a command substitution's body unsets a
// variable, which emission shadows with a block-local set. A nested
// substitution or function body is its own scope and is skipped.
func cmdSubstUnserts(stmts []*syntax.Stmt) bool {
	found := false
	var walk func(syntax.Node) bool
	walk = func(n syntax.Node) bool {
		if found {
			return false
		}
		switch n.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst, *syntax.FuncDecl:
			return false
		}
		if c, ok := n.(*syntax.CallExpr); ok && len(c.Args) > 0 && isLitWord(c.Args[0], "unset") {
			if names, rest := unsetShadowable(c); len(names) > 0 || rest {
				found = true
				return false
			}
		}
		return true
	}
	for _, st := range stmts {
		syntax.Walk(st, walk)
	}
	return found
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
		sub.newline(stmtStartPos(st))
		sub.stmt(st)
		sub.advanceLine(stmtEndLine(st))
	}
	for _, c := range ps.Last {
		sub.newline(c.Pos())
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
	return "({\n" + strings.Join(lines, "\n") + "\n" + endPad + "} | psub)", true
}
