package bait

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

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
	return "({\n" + strings.Join(lines, "\n") + "\n" + endPad + "} | psub)", true
}
