package bait

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// normalize applies mechanical AST fixes before emission: read-flag
// removal, fish-side parameter rewrites, and arithmetic translation.
func (e *emitter) normalize(f *syntax.File) {
	e.escapeLiteralDollars(f)
	e.analyzeVariables(f)
	syntax.Walk(f, func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.FuncDecl:
			if x.Name != nil {
				e.knownFuncs[x.Name.Value] = true
			}
		case *syntax.ParamExp:
			if x.Param != nil && x.Param.Value == "OSTYPE" {
				e.needHelper(helperOSType)
			}
		case *syntax.CallExpr:
			normalizeCommandName(x)
			if len(x.Args) > 0 {
				if isLitWord(x.Args[0], "getopts") {
					e.needHelper(helperGetopts)
					if len(x.Args) >= 3 {
						if lit, ok := isBareLit(x.Args[2]); ok {
							x.Args[2] = litWord(e.varName(lit))
						}
					}
					if len(x.Args) == 3 {
						x.Args = append(x.Args, &syntax.Word{Parts: []syntax.WordPart{argvParam()}})
					}
				} else if isLitWord(x.Args[0], "hash") {
					e.needHelper(helperHash)
				} else if isLitWord(x.Args[0], "unalias") {
					e.needHelper(helperUnalias)
				} else if isLitWord(x.Args[0], "source") {
					e.needHelper(helperSource)
				} else if isLitWord(x.Args[0], ".") {
					e.needHelper(helperDot)
				} else if isLitWord(x.Args[0], "unset") {
					e.needHelper(helperUnset)
				}
			}
			if len(x.Args) > 0 && isLitWord(x.Args[0], "eval") {
				if e.noHelpers {
					if x.Args[0].Pos().Col() > 0 {
						e.warn(x.Args[0].Pos(), "eval executes fish syntax; incompatible bash syntax will fail at runtime; emitted verbatim")
					}
				} else {
					e.needHelper(helperEval)
					x.Args[0] = litWord("__bait_eval")
				}
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
		e.needHelper(helperOSType)
		return []syntax.WordPart{substPart("__bait_ostype")}
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
		var replArg string
		if pe.Repl.With == nil {
			replArg = "''"
		} else if containsParam(pe.Repl.With) {
			pe.Repl.With.Parts = e.spliceParts(pe.Repl.With.Parts, false)
			repl := e.renderWordSmart(pe.Repl.With)
			if !strings.HasPrefix(repl, `"`) && !strings.HasPrefix(repl, "'") {
				repl = `"` + repl + `"`
			}
			replArg = repl
		} else {
			replArg = fishSingleQuote(e.render(pe.Repl.With))
		}
		args = append(args, "--",
			regexArg,
			replArg,
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
	type segment struct {
		isStatic bool
		text     string
	}
	var segments []segment
	if isPrefix {
		segments = append(segments, segment{isStatic: true, text: "^"})
	}

	var walkParts func(parts []syntax.WordPart, inQuotes bool) bool
	walkParts = func(parts []syntax.WordPart, inQuotes bool) bool {
		for _, p := range parts {
			switch pt := p.(type) {
			case *syntax.Lit:
				if inQuotes {
					segments = append(segments, segment{isStatic: true, text: regexp.QuoteMeta(pt.Value)})
				} else {
					re, ok := globToRegex(pt.Value, lazy)
					if !ok {
						return false
					}
					if re != "" {
						segments = append(segments, segment{isStatic: true, text: re})
					}
				}
			case *syntax.SglQuoted:
				segments = append(segments, segment{isStatic: true, text: regexp.QuoteMeta(pt.Value)})
			case *syntax.DblQuoted:
				if !walkParts(pt.Parts, true) {
					return false
				}
			case *syntax.ParamExp:
				rendered := e.renderWordSmart(&syntax.Word{Parts: []syntax.WordPart{pt}})
				segments = append(segments, segment{
					isStatic: false,
					text:     fmt.Sprintf("(string escape --style=regex -- \"%s\")", rendered),
				})
			default:
				rendered := e.renderWordSmart(&syntax.Word{Parts: []syntax.WordPart{pt}})
				if strings.HasPrefix(rendered, "$") {
					segments = append(segments, segment{
						isStatic: false,
						text:     fmt.Sprintf("(string escape --style=regex -- \"%s\")", rendered),
					})
				} else {
					segments = append(segments, segment{
						isStatic: false,
						text:     fmt.Sprintf("(string escape --style=regex -- %s)", rendered),
					})
				}
			}
		}
		return true
	}

	if w != nil && !walkParts(w.Parts, false) {
		return "", false
	}
	if isSuffix {
		segments = append(segments, segment{isStatic: true, text: "$"})
	}
	if len(segments) == 0 {
		return "''", true
	}

	var parts []string
	var staticBuf strings.Builder
	flushStatic := func() {
		if staticBuf.Len() > 0 {
			parts = append(parts, fishSingleQuote(staticBuf.String()))
			staticBuf.Reset()
		}
	}
	for _, seg := range segments {
		if seg.isStatic {
			staticBuf.WriteString(seg.text)
		} else {
			flushStatic()
			parts = append(parts, seg.text)
		}
	}
	flushStatic()
	if len(parts) == 0 {
		return "''", true
	}
	return strings.Join(parts, ""), true
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
