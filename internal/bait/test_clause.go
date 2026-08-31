package bait

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

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
	res := "set --query " + shifted
	if negated {
		res = "! " + res
	}
	return res
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

func (e *emitter) testClause(s *syntax.Stmt, tc *syntax.TestClause) {
	sc := classifyComments(s)
	e.leadingComments(sc.leading)
	tail := e.tails(s)
	res := e.renderTestExpr(tc.X)
	var line string
	if s.Negated {
		line = fmt.Sprintf("! %s%s", res, tail)
	} else {
		line = fmt.Sprintf("%s%s", res, tail)
	}
	e.printLineWithTrailing(line, sc.trailing)
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
			return "set --query " + shifted
		}
		return "set --query " + e.render(u.X)
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
		return fmt.Sprintf("string match --regex --quiet -- %s %s", pat, target)
	case "==", "=":
		xWord, _ := b.X.(*syntax.Word)
		yWord, _ := b.Y.(*syntax.Word)
		target := e.renderWordSmart(xWord)
		if hasUnquotedWildcard(yWord) {
			pat := e.renderPatternLiteral(yWord)
			return fmt.Sprintf("string match --quiet -- %s %s", pat, target)
		}
		yStr := e.renderWordSmart(yWord)
		return fmt.Sprintf("test %s = %s", target, yStr)
	case "!=":
		xWord, _ := b.X.(*syntax.Word)
		yWord, _ := b.Y.(*syntax.Word)
		target := e.renderWordSmart(xWord)
		if hasUnquotedWildcard(yWord) {
			pat := e.renderPatternLiteral(yWord)
			return fmt.Sprintf("! string match --quiet -- %s %s", pat, target)
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
