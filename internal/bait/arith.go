package bait

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// rewriteArithmetic replaces (( )) commands whose shape fish can express
// with set/test/math equivalents, so conditions and bodies alike see the
// translated node. Comma expressions are flattened into sequential set
// statements in statement lists.
func (e *emitter) rewriteArithmetic(f *syntax.File) {
	syntax.Walk(f, func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.File:
			x.Stmts = e.flattenStmts(x.Stmts)
		case *syntax.Block:
			x.Stmts = e.flattenStmts(x.Stmts)
		case *syntax.Subshell:
			x.Stmts = e.flattenStmts(x.Stmts)
		case *syntax.CaseItem:
			x.Stmts = e.flattenStmts(x.Stmts)
		case *syntax.IfClause:
			x.Cond = e.flattenStmts(x.Cond)
			x.Then = e.flattenStmts(x.Then)
		case *syntax.WhileClause:
			x.Cond = e.flattenStmts(x.Cond)
			x.Do = e.flattenStmts(x.Do)
		case *syntax.ForClause:
			x.Do = e.flattenStmts(x.Do)
		case *syntax.CmdSubst:
			x.Stmts = e.flattenStmts(x.Stmts)
		case *syntax.ProcSubst:
			x.Stmts = e.flattenStmts(x.Stmts)
		case *syntax.BinaryCmd:
			e.rewriteSingleStmt(x.X)
			e.rewriteSingleStmt(x.Y)
		case *syntax.FuncDecl:
			e.rewriteSingleStmt(x.Body)
		case *syntax.Stmt:
			if ac, ok := x.Cmd.(*syntax.ArithmCmd); ok {
				if repl := e.arithCommand(ac); repl != nil {
					x.Cmd = repl
				}
			}
		}
		return true
	})
}

func (e *emitter) flattenStmts(stmts []*syntax.Stmt) []*syntax.Stmt {
	var result []*syntax.Stmt
	for _, st := range stmts {
		if st == nil {
			continue
		}
		if ac, ok := st.Cmd.(*syntax.ArithmCmd); ok {
			if cmds := e.arithCommands(ac); len(cmds) > 0 {
				sc := classifyComments(st)
				for i, cmd := range cmds {
					newStmt := &syntax.Stmt{
						Cmd:        cmd,
						Position:   st.Position,
						Semicolon:  st.Semicolon,
						Negated:    st.Negated && (i == len(cmds)-1),
						Background: st.Background && (i == len(cmds)-1),
					}
					if i == 0 && len(sc.leading) > 0 {
						newStmt.Comments = append(newStmt.Comments, sc.leading...)
					}
					if i == len(cmds)-1 {
						if len(sc.trailing) > 0 {
							newStmt.Comments = append(newStmt.Comments, sc.trailing...)
						}
						newStmt.Redirs = st.Redirs
					}
					result = append(result, newStmt)
				}
				continue
			}
		}
		result = append(result, st)
	}
	return result
}

func (e *emitter) rewriteSingleStmt(s *syntax.Stmt) {
	if s == nil || s.Cmd == nil {
		return
	}
	if ac, ok := s.Cmd.(*syntax.ArithmCmd); ok {
		if repl := e.arithCommand(ac); repl != nil {
			s.Cmd = repl
		}
	}
}

var arithSymbols = map[syntax.BinAritOperator]string{
	syntax.Add: "+", syntax.Sub: "-", syntax.Mul: "*",
	syntax.Quo: "/", syntax.Rem: "%",
	syntax.Eql: "==", syntax.Neq: "!=",
	syntax.Lss: "<", syntax.Gtr: ">",
	syntax.Leq: "<=", syntax.Geq: ">=",
}

var arithAssignSymbols = map[syntax.BinAritOperator]string{
	syntax.AddAssgn: "+", syntax.SubAssgn: "-", syntax.MulAssgn: "*",
	syntax.QuoAssgn: "/", syntax.RemAssgn: "%",
}

var arithTestFlags = map[syntax.BinAritOperator]string{
	syntax.Eql: "-eq", syntax.Neq: "-ne",
	syntax.Lss: "-lt", syntax.Gtr: "-gt",
	syntax.Leq: "-le", syntax.Geq: "-ge",
}

func isAssignOp(op syntax.BinAritOperator) bool {
	return op == syntax.Assgn || op == syntax.PowAssgn || arithAssignSymbols[op] != ""
}

// arithCommands converts an (( )) command into a slice of fish equivalents, or
// nil when no faithful translation exists. Comma expressions yield multiple
// commands; only the last one carries bash's exit status.
func (e *emitter) arithCommands(ac *syntax.ArithmCmd) []syntax.Command {
	if ac == nil || ac.X == nil {
		return nil
	}
	parts := splitCommaArithm(ac.X)
	var cmds []syntax.Command
	for i, part := range parts {
		partCmds := e.arithExpr(part, i == len(parts)-1)
		if partCmds == nil {
			return nil
		}
		cmds = append(cmds, partCmds...)
	}
	return cmds
}

// arithCommand converts an (( )) command into its fish equivalent, or
// nil when no faithful translation exists (the caller then warns and
// emits verbatim through the default path).
func (e *emitter) arithCommand(ac *syntax.ArithmCmd) syntax.Command {
	cmds := e.arithCommands(ac)
	if len(cmds) == 0 {
		return nil
	}
	if len(cmds) == 1 {
		return cmds[0]
	}
	stmts := make([]*syntax.Stmt, len(cmds))
	for i, c := range cmds {
		stmts[i] = &syntax.Stmt{Cmd: c}
	}
	return &syntax.Block{Stmts: stmts}
}

func splitCommaArithm(expr syntax.ArithmExpr) []syntax.ArithmExpr {
	for {
		paren, ok := expr.(*syntax.ParenArithm)
		if !ok {
			break
		}
		expr = paren.X
	}
	if bin, ok := expr.(*syntax.BinaryArithm); ok && bin.Op == syntax.Comma {
		return append(splitCommaArithm(bin.X), splitCommaArithm(bin.Y)...)
	}
	return []syntax.ArithmExpr{expr}
}

// arithExpr converts one arithmetic expression into the fish commands that
// reproduce its side effects and value. withStatus is false for all but the
// last part of a comma expression, whose status alone bash reports.
func (e *emitter) arithExpr(expr syntax.ArithmExpr, withStatus bool) []syntax.Command {
	for {
		paren, ok := expr.(*syntax.ParenArithm)
		if !ok {
			break
		}
		expr = paren.X
	}
	switch x := expr.(type) {
	case *syntax.UnaryArithm:
		if x.Op != syntax.Inc && x.Op != syntax.Dec {
			return nil
		}
		name, ok := bareArithName(x.X)
		if !ok {
			return nil
		}
		mangled := e.bindName(x.Pos(), name)
		delta, inverse := "+", "-"
		if x.Op == syntax.Dec {
			delta, inverse = "-", "+"
		}
		set := e.setCall(mangled, e.mathSubst("$"+mangled+" "+delta+" 1"))
		if !withStatus {
			return []syntax.Command{set}
		}
		if x.Post {
			// ((x++)) evaluates to the old value: the new value ∓ 1.
			return []syntax.Command{set, e.cmpCall("-ne", "$"+mangled+" "+inverse+" 1", "0")}
		}
		return []syntax.Command{set, truthyCall("$" + mangled)}

	case *syntax.BinaryArithm:
		if name, ok := bareArithName(x.X); ok && isAssignOp(x.Op) {
			mangled := e.bindName(x.X.Pos(), name)
			rhs, rhsOK := e.arithmText(x.Y)
			var payload string
			var valid bool
			switch x.Op {
			case syntax.Assgn:
				payload, valid = rhs, rhsOK
			case syntax.PowAssgn:
				payload, valid = "pow($"+mangled+", "+rhs+")", rhsOK
			default:
				payload, valid = "$"+mangled+" "+arithAssignSymbols[x.Op]+" "+rhs, rhsOK
			}
			if !valid {
				return nil
			}
			set := e.setCall(mangled, e.mathSubst(payload))
			if !withStatus {
				return []syntax.Command{set}
			}
			// An assignment expression evaluates to the assigned value,
			// which the set command leaves in the variable.
			return []syntax.Command{set, truthyCall("$" + mangled)}
		}
		if flag, isCmp := arithTestFlags[x.Op]; isCmp {
			lhs, lok := e.arithmText(x.X)
			rhs, rok := e.arithmText(x.Y)
			if lok && rok {
				return []syntax.Command{e.cmpCall(flag, lhs, rhs)}
			}
		}

	case *syntax.Word:
		if txt, ok := e.arithmText(x); ok {
			return []syntax.Command{e.cmpCall("-ne", txt, "0")}
		}
	}
	return nil
}

// truthyCall reports whether a value bash arithmetic left in a variable is
// nonzero. The value is already coerced to an integer by __bait_math.
func truthyCall(payload string) syntax.Command {
	return &syntax.CallExpr{Args: []*syntax.Word{
		litWord("test"),
		litWord("\"" + payload + "\""),
		litWord("-ne"),
		litWord("0"),
	}}
}

// cmpCall builds `__bait_cmp <op> "<lhs>" "<rhs>"`, which evaluates both
// operands as bash arithmetic expressions before comparing them.
func (e *emitter) cmpCall(op, lhs, rhs string) syntax.Command {
	e.needShim(shimCmp)
	return &syntax.CallExpr{Args: []*syntax.Word{
		litWord("__bait_cmp"),
		litWord(op),
		litWord("\"" + lhs + "\""),
		litWord("\"" + rhs + "\""),
	}}
}

func wordOf(parts ...syntax.WordPart) *syntax.Word {
	return &syntax.Word{Parts: parts}
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
		if t, ok := e.resolveParamTarget(s); ok {
			return e.paramTargetText(t), true
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
		if a.Op == syntax.Pow {
			lx, lok := e.arithmText(a.X)
			ry, rok := e.arithmText(a.Y)
			return "pow(" + lx + ", " + ry + ")", lok && rok
		}
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

// mathSubst renders an arithmetic payload as a call to __bait_math, which
// applies bash's coercion rules: unset and non-numeric operands are zero.
func (e *emitter) mathSubst(payload string) syntax.WordPart {
	e.needShim(shimMath)
	return substPart("__bait_math", "\""+payload+"\"")
}

// setCall builds the `set name value` command the translator emits for
// arithmetic assignments, registering it so later passes can tell it apart
// from a user-written set builtin.
func (e *emitter) setCall(name string, value syntax.WordPart) syntax.Command {
	call := &syntax.CallExpr{Args: []*syntax.Word{
		{Parts: []syntax.WordPart{&syntax.Lit{Value: "set"}}},
		litWord(name),
		{Parts: []syntax.WordPart{value}},
	}}
	e.synthSets[call] = true
	return call
}
