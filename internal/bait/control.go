package bait

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// condText renders an if/while condition: a single statement verbatim,
// several statements wrapped in an inline brace block (fish conditions
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
	return "{ " + strings.Join(parts, "; ") + "; }"
}

func (e *emitter) ifClause(s *syntax.Stmt, f *syntax.IfClause) {
	sc := e.prepareStmt(s)
	tail := e.tails(s)
	e.printf("if %s", e.condText(f.Cond))
	e.advanceLine(f.Pos().Line())
	for _, st := range f.Cond {
		e.advanceLine(stmtEndLine(st))
	}

	pending := f.Last
	e.body(f.Then, f.ThenLast)
	for els := f.Else; els != nil; {
		for _, c := range pending {
			e.newline(c.Pos())
			e.comment(c)
		}
		pending = els.Last
		if len(els.Cond) == 0 {
			e.newline(els.Pos())
			e.printf("else")
			e.advanceLine(els.Pos().Line())
			e.body(els.Then, els.ThenLast)
			break
		}
		e.newline(els.Pos())
		e.printf("else if %s", e.condText(els.Cond))
		e.advanceLine(els.Pos().Line())
		for _, st := range els.Cond {
			e.advanceLine(stmtEndLine(st))
		}
		e.body(els.Then, els.ThenLast)
		els = els.Else
	}
	for _, c := range pending {
		e.newline(c.Pos())
		e.comment(c)
	}
	e.printEnd(tail, sc.trailing)
}

func (e *emitter) whileClause(s *syntax.Stmt, w *syntax.WhileClause) {
	sc := e.prepareStmt(s)
	tail := e.tails(s)
	cond := e.condText(w.Cond)
	if w.Until {
		cond = "not " + cond
	}
	e.printf("while %s", cond)
	e.advanceLine(w.Pos().Line())
	for _, st := range w.Cond {
		e.advanceLine(stmtEndLine(st))
	}
	e.body(w.Do, w.DoLast)
	e.printEnd(tail, sc.trailing)
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
	sc := e.prepareStmt(s)
	headerSuffix := ""
	if sc.headerTrailing != nil {
		headerSuffix = trailingCommentSuffix(*sc.headerTrailing)
	}
	e.printf("for %s in %s%s", e.varName(iter.Name.Value), strings.Join(items, " "), headerSuffix)
	e.advanceLine(f.Pos().Line())
	if f.DoPos.Line() > 0 {
		e.advanceLine(f.DoPos.Line())
	}
	e.body(f.Do, f.DoLast)
	e.printEnd(tail, sc.trailing)
}

func (e *emitter) caseClause(s *syntax.Stmt, cl *syntax.CaseClause) {
	tail := e.tails(s)
	sc := e.prepareStmt(s)
	if pred, ifItem, elseItem, ok := evalInteractiveCase(cl); ok {
		if elseItem == nil {
			e.printf("if %s", pred)
			e.advanceLine(ifItem.Pos().Line())
			for _, p := range ifItem.Patterns {
				e.advanceLine(p.End().Line())
			}
			e.body(ifItem.Stmts, ifItem.Last)
			e.advanceLine(ifItem.End().Line())
			if ifItem.OpPos.IsValid() {
				e.advanceLine(ifItem.OpPos.Line())
			}
			for _, c := range cl.Last {
				e.newline(c.Pos())
				e.comment(c)
			}
			e.printEnd(tail, sc.trailing)
			return
		}
		if len(ifItem.Stmts) == 0 && len(ifItem.Last) == 0 {
			e.printf("if not %s", pred)
			e.advanceLine(elseItem.Pos().Line())
			for _, p := range elseItem.Patterns {
				e.advanceLine(p.End().Line())
			}
			e.body(elseItem.Stmts, elseItem.Last)
			e.advanceLine(elseItem.End().Line())
			if elseItem.OpPos.IsValid() {
				e.advanceLine(elseItem.OpPos.Line())
			}
			for _, c := range cl.Last {
				e.newline(c.Pos())
				e.comment(c)
			}
			e.printEnd(tail, sc.trailing)
			return
		}
		e.printf("if %s", pred)
		e.advanceLine(ifItem.Pos().Line())
		for _, p := range ifItem.Patterns {
			e.advanceLine(p.End().Line())
		}
		e.body(ifItem.Stmts, ifItem.Last)
		e.advanceLine(ifItem.End().Line())
		if ifItem.OpPos.IsValid() {
			e.advanceLine(ifItem.OpPos.Line())
		}
		if len(elseItem.Stmts) > 0 || len(elseItem.Last) > 0 {
			e.newline(elseItem.Pos())
			e.printf("else")
			e.advanceLine(elseItem.Pos().Line())
			for _, p := range elseItem.Patterns {
				e.advanceLine(p.End().Line())
			}
			e.body(elseItem.Stmts, elseItem.Last)
			e.advanceLine(elseItem.End().Line())
			if elseItem.OpPos.IsValid() {
				e.advanceLine(elseItem.OpPos.Line())
			}
		}
		for _, c := range cl.Last {
			e.newline(c.Pos())
			e.comment(c)
		}
		e.printEnd(tail, sc.trailing)
		return
	}
	if caseHasBracketClass(cl) {
		e.caseClauseAsIf(s, cl, tail, sc.trailing)
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
	e.advanceLine(cl.Pos().Line())
	for _, item := range cl.Items {
		for _, c := range item.Comments {
			e.newline(c.Pos())
			e.comment(c)
		}
		e.newline(item.Pos())
		if item.Op != syntax.Break {
			e.warn(item.Pos(), "case fallthrough (%s) has no fish equivalent; converted to a plain case", item.Op)
		}
		patterns := make([]string, len(item.Patterns))
		for i, p := range item.Patterns {
			patterns[i] = e.renderCasePattern(p)
		}
		e.printf("case %s", strings.Join(patterns, " "))
		e.advanceLine(item.Pos().Line())
		for _, p := range item.Patterns {
			e.advanceLine(p.End().Line())
		}
		e.body(item.Stmts, item.Last)
		e.advanceLine(item.End().Line())
		if item.OpPos.IsValid() {
			e.advanceLine(item.OpPos.Line())
		}
	}
	for _, c := range cl.Last {
		e.newline(c.Pos())
		e.comment(c)
	}
	e.printEnd(tail, sc.trailing)
}

func (e *emitter) caseClauseAsIf(s *syntax.Stmt, cl *syntax.CaseClause, tail string, trailing []syntax.Comment) {
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
			e.newline(item.Pos())
			e.printf("else")
			e.advanceLine(item.Pos().Line())
			e.body(item.Stmts, item.Last)
			e.advanceLine(item.End().Line())
			if item.OpPos.IsValid() {
				e.advanceLine(item.OpPos.Line())
			}
			continue
		}
		var conds []string
		for _, p := range item.Patterns {
			re, _ := globToRegex(e.render(p), false)
			conds = append(conds, fmt.Sprintf("string match --regex --quiet -- %s %s", fishSingleQuote("^"+re+"$"), target))
		}
		cond := strings.Join(conds, "; or ")
		e.newline(item.Pos())
		if first {
			e.printf("if %s", cond)
			first = false
		} else {
			e.printf("else if %s", cond)
		}
		e.advanceLine(item.Pos().Line())
		for _, p := range item.Patterns {
			e.advanceLine(p.End().Line())
		}
		e.body(item.Stmts, item.Last)
		e.advanceLine(item.End().Line())
		if item.OpPos.IsValid() {
			e.advanceLine(item.OpPos.Line())
		}
	}
	for _, c := range cl.Last {
		e.comment(c)
	}
	e.printEnd(tail, trailing)
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
