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
	e.expandBraceSequences(f)
	e.analyzeVariables(f)
	syntax.Walk(f, func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.FuncDecl:
			if x.Name != nil {
				e.knownFuncs[x.Name.Value] = true
			}
		case *syntax.ParamExp:
			if x.Param != nil {
				if x.Param.Value == "OSTYPE" {
					e.needHelper(helperOSType)
				}
				if x.Param.Value == "IFS" {
					// fish's default IFS lists the same separator
					// characters in a different order; scripts that read
					// IFS compare its string value, so restore bash's
					// order once at the top of the script.
					e.needIFS = true
				}
			}
		case *syntax.CallExpr:
			normalizeCommandName(x)
			e.helperDispatch(x)
			if len(x.Args) > 0 && isLitWord(x.Args[0], "getopts") {
				if len(x.Args) >= 3 {
					if lit, ok := isBareLit(x.Args[2]); ok {
						x.Args[2] = litWord(e.varName(lit))
					}
				}
				if len(x.Args) == 3 {
					x.Args = append(x.Args, &syntax.Word{Parts: []syntax.WordPart{argvParam()}})
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
			for i := 1; i < len(x.Args); i++ {
				if e.arrayAllWords[x.Args[i]] {
					// "${arr[@]}" already passes every element as its own
					// argument; the normalized reference is the list.
					continue
				}
				pe, quoted, ok := bareParamOf(x.Args[i])
				if !ok {
					continue
				}
				name := pe.Param.Value
				if name == "@" || name == "*" || name == "argv" {
					continue
				}
				// A quoted word never word-splits in bash; an unquoted
				// multi-word scalar does, and __bait_words is IFS-aware.
				multi := e.multiWordScalars[name] && !quoted
				if !multi && e.listPolicy(name) == listScalar {
					// A scalar reference already passes exactly one
					// value; rewriteParams splices its name.
					continue
				}
				// This runs before rewriteParams splices names, so
				// resolve the target here: a reserved-name scalar such
				// as status maps to a mangled fish name that must be
				// applied before the reference is frozen as a literal.
				t, ok := e.resolveParamTarget(name)
				if !ok {
					continue
				}
				ref := e.paramTargetValueRef(t)
				if multi {
					e.needHelper(helperWords)
					ref = "(__bait_words " + ref + ")"
				}
				x.Args[i] = litWord(ref)
			}
		case *syntax.CmdSubst:
			// bash defines `$(<file)` as `$(cat file)`; materialize the
			// implicit cat so every emission path renders it.
			if len(x.Stmts) == 1 && !stmtHasCommand(x.Stmts[0]) &&
				len(x.Stmts[0].Redirs) == 1 &&
				x.Stmts[0].Redirs[0].Op == syntax.RdrIn &&
				x.Stmts[0].Redirs[0].Word != nil {
				x.Stmts[0].Cmd = &syntax.CallExpr{Args: []*syntax.Word{litWord("cat")}}
			}
			e.markSubstitutionUnserts(x.Stmts)
		case *syntax.ProcSubst:
			e.markSubstitutionUnserts(x.Stmts)
		case *syntax.Stmt:
			// A statement made only of redirections behaves like `true`
			// with those redirections in bash: files are opened or
			// truncated and the statement succeeds. Materialize the
			// implicit command here so all emission paths agree.
			if len(x.Redirs) > 0 {
				if x.Cmd == nil {
					x.Cmd = &syntax.CallExpr{Args: []*syntax.Word{litWord("true")}}
				} else if c, ok := x.Cmd.(*syntax.CallExpr); ok && len(c.Args) == 0 && len(c.Assigns) == 0 {
					c.Args = []*syntax.Word{litWord("true")}
				}
			}
			if c, ok := x.Cmd.(*syntax.CallExpr); ok {
				e.normalizeReadCmd(x, c)
				e.normalizeExecCmd(x, c)
			}
			if w, ok := x.Cmd.(*syntax.WhileClause); ok {
				e.normalizeLoopFD(x, w)
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

// stmtHasCommand reports whether a statement has a command word or an
// assignment. Statements made only of redirections (and empty statements)
// return false.
func stmtHasCommand(st *syntax.Stmt) bool {
	switch c := st.Cmd.(type) {
	case nil:
		return false
	case *syntax.CallExpr:
		return len(c.Args) > 0 || len(c.Assigns) > 0
	default:
		return true
	}
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
							w.Parts = e.spliceParts(w.Parts)
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
			if raw, isStar, quoted, ok := e.soleArrayAll(node); ok {
				t, rok := e.resolveParamTarget(raw)
				if !rok {
					t = paramTarget{name: e.varName(raw)}
				}
				if t.subst != nil {
					node.Parts = t.subst
					return true
				}
				whole := []syntax.WordPart{namedParam(t.name)}
				if quoted && isStar {
					whole = []syntax.WordPart{&syntax.DblQuoted{Parts: whole}}
				}
				node.Parts = whole
				e.arrayAllWords[node] = true
				return true
			}
			node.Parts = e.spliceParts(node.Parts)
			return true
		case *syntax.Redirect:
			if node.Op != syntax.DplOut {
				e.rewriteRedirectTarget(node)
			}
			return true
		}
		return true
	})
}

// helperDispatch marks the runtime helper a builtin needs. It runs once per
// call expression during normalize, so statements, conditions, and chain
// leaves all resolve helpers through the same decision.
func (e *emitter) helperDispatch(c *syntax.CallExpr) {
	if len(c.Args) == 0 {
		return
	}
	if isLitWord(c.Args[0], "unset") {
		// Registered even when the call is shadowed or carries a prefix
		// assignment: bodies printed verbatim (backquotes, here-document
		// pipelines, C-style loops) keep the bare call, and fish has no
		// unset of its own.
		e.needHelper(helperUnset)
		return
	}
	switch {
	case isLitWord(c.Args[0], "getopts"):
		e.needHelper(helperGetopts)
	case isLitWord(c.Args[0], "hash"):
		e.needHelper(helperHash)
		if len(c.Args) == 1 {
			e.warn(c.Args[0].Pos(), "hash without arguments prints bash's command hash table; fish has none")
		}
		for _, a := range c.Args[1:] {
			if lit, ok := isBareLit(a); ok && strings.HasPrefix(lit, "-") && strings.ContainsRune(lit, 't') {
				e.warn(a.Pos(), "hash -t prints a hashed path; fish has no command hash table")
			}
		}
	case isLitWord(c.Args[0], "unalias"):
		e.needHelper(helperUnalias)
	case isLitWord(c.Args[0], "source"):
		e.needHelper(helperSource)
	case isLitWord(c.Args[0], "."):
		e.needHelper(helperDot)
	}
}

// markSubstitutionUnserts marks variable unsets inside a substitution body so
// emission can shadow them instead of erasing the caller's variable. A nested
// substitution or function body is its own scope and is skipped.
func (e *emitter) markSubstitutionUnserts(stmts []*syntax.Stmt) {
	var walk func(n syntax.Node) bool
	walk = func(n syntax.Node) bool {
		switch n.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst, *syntax.FuncDecl:
			return false
		}
		if c, ok := n.(*syntax.CallExpr); ok && len(c.Args) > 0 && isLitWord(c.Args[0], "unset") {
			if names, rest := unsetShadowable(c); len(names) > 0 || rest {
				e.unsetShadows[c] = true
			}
		}
		return true
	}
	for _, st := range stmts {
		syntax.Walk(st, walk)
	}
}

// unsetShadowable splits an unset call's arguments: names are the plain
// variable names a block-local shadow can cover, and rest reports whether any
// argument (function mode, an array element, or a dynamic word) cannot be
// shadowed at all.
func unsetShadowable(c *syntax.CallExpr) (names []string, rest bool) {
	if len(c.Assigns) > 0 {
		// A prefix assignment cannot ride on a shadow, so the call is
		// skipped with a warning instead of losing the assignment.
		return nil, true
	}
	modeFunc := false
	inOptions := true
	for i, w := range c.Args {
		if i == 0 {
			continue
		}
		if inOptions {
			if lit, ok := isBareLit(w); ok {
				switch lit {
				case "--":
					inOptions = false
					continue
				case "-f":
					modeFunc = true
					continue
				case "-v", "-n":
					continue
				}
				if strings.HasPrefix(lit, "-") {
					continue
				}
			}
			inOptions = false
		}
		if modeFunc {
			rest = true
			continue
		}
		name, ok := staticWordValue(w)
		if !ok || !syntax.ValidName(name) {
			rest = true
			continue
		}
		names = append(names, name)
	}
	return names, rest
}

// statusDashText renders the $- emulation for contexts that need text
// rather than a word part.
func (e *emitter) statusDashText() string {
	return e.render(&syntax.Word{Parts: []syntax.WordPart{statusDashPart()}})
}

// rewriteRedirectTarget applies the shared list policy to a redirection
// target at normalize time, so every emission path (the printer for simple
// commands, tails for structural ones) sees the same word: a colon-split
// name stays joined, a bash array contributes element 0, and a
// dataflow-known multi-word scalar is split so fish fails like bash's
// ambiguous redirect instead of creating a differently named file.
func (e *emitter) rewriteRedirectTarget(r *syntax.Redirect) {
	if e.arrayAllWords[r.Word] {
		// "${arr[@]}" is several words in bash; fish rejects a
		// multi-element target, which matches bash's failure.
		return
	}
	pe, quoted, ok := bareParamOf(r.Word)
	if !ok {
		return
	}
	// This rewrite runs before the word walk splices names, so resolve the
	// target here.
	t, ok := e.resolveParamTarget(pe.Param.Value)
	if !ok {
		return
	}
	ref := e.paramTargetValueRef(t)
	switch {
	case r.Op == syntax.WordHdoc:
		// A herestring is a single string: quoting keeps a list joined.
		ref = quoteRef(ref)
	case !quoted && e.multiWordScalars[pe.Param.Value]:
		e.warn(r.Pos(), "bash rejects a redirect target that splits into several words (%s); emitted with word splitting", pe.Param.Value)
		ref = "(__bait_words " + ref + ")"
		e.needHelper(helperWords)
	}
	r.Word = litWord(ref)
}

func isNameChar(r byte) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

func needsFishBracing(parts []syntax.WordPart, i int) bool {
	if i+1 >= len(parts) {
		return false
	}
	lit, ok := parts[i+1].(*syntax.Lit)
	if !ok || len(lit.Value) == 0 {
		return false
	}
	return isNameChar(lit.Value[0])
}

func (e *emitter) spliceParts(parts []syntax.WordPart) []syntax.WordPart {
	out := make([]syntax.WordPart, 0, len(parts)+1)
	var prevEnd syntax.Pos
	prevExpand := false
	for i := range parts {
		switch p := parts[i].(type) {
		case *syntax.ParamExp:
			replaced := e.paramReplacements(p)
			if needsFishBracing(parts, i) && len(replaced) == 1 {
				if pe, ok := replaced[0].(*syntax.ParamExp); ok && bareParam(pe) && pe.Param != nil {
					out = append(out, litPart("{$"+pe.Param.Value+"}"))
					prevEnd = p.End()
					prevExpand = true
					continue
				}
			}
			out = append(out, replaced...)
			prevEnd = p.End()
			prevExpand = true
		case *syntax.ArithmExp:
			if txt, ok := e.arithmText(p.X); ok {
				out = append(out, e.mathSubst(txt))
			} else {
				e.warn(p.Pos(), "arithmetic uses an operator without a fish math equivalent; left untranslated")
				out = append(out, p)
			}
			prevEnd = p.End()
			prevExpand = true
		case *syntax.Lit:
			// bash never reads `[` after an unbraced expansion as an
			// index; fish would. Escape it so the literal survives. Only
			// source text is escaped; translator-generated index parts
			// share the synthesized position of their reference.
			if prevExpand && strings.HasPrefix(p.Value, "[") && sourceAdjacent(prevEnd, p.Pos()) {
				p.Value = escapeIndexBrackets(p.Value)
			}
			out = append(out, p)
			prevExpand = false
		case *syntax.DblQuoted:
			out = append(out, e.spliceQuoted(p)...)
			prevEnd = p.End()
			// A following source `[...]` must stay literal; fish would
			// otherwise read it as an index of the quoted value.
			prevExpand = true
		default:
			out = append(out, parts[i])
			prevEnd = parts[i].End()
			switch parts[i].(type) {
			case *syntax.CmdSubst, *syntax.ProcSubst:
				prevExpand = true
			default:
				prevExpand = false
			}
		}
	}
	return out
}

// sourceAdjacent reports whether ref and p are valid source positions with
// p at or after ref in (line, column) order. Synthesized nodes carry
// incomplete positions and are never treated as adjacent source text.
func sourceAdjacent(ref, p syntax.Pos) bool {
	if !ref.IsValid() || !p.IsValid() {
		return false
	}
	if p.Line() != ref.Line() {
		return p.Line() > ref.Line()
	}
	return p.Col() >= ref.Col()
}

// expansionEndsInName reports whether a replacement's last part is a bare
// fish variable reference, i.e. text following it in the same quoted
// segment would be absorbed into the variable name.
func expansionEndsInName(parts []syntax.WordPart) bool {
	if len(parts) == 0 {
		return false
	}
	pe, ok := parts[len(parts)-1].(*syntax.ParamExp)
	return ok && bareParam(pe) && pe.Param != nil
}

// spliceQuoted rewrites the parts of a double-quoted region. fish cannot
// delimit a variable name inside quotes, so the region is split into
// adjacent segments wherever a replacement is followed by source text that
// would otherwise be absorbed into the name or read as an index. The
// original node is reused when nothing splits, preserving its source
// positions.
func (e *emitter) spliceQuoted(dq *syntax.DblQuoted) []syntax.WordPart {
	if len(dq.Parts) == 0 {
		return []syntax.WordPart{dq}
	}
	var out []syntax.WordPart
	var cur []syntax.WordPart
	split := false
	flush := func() {
		if len(cur) > 0 {
			out = append(out, quotedSegment(cur))
			cur = nil
		}
	}
	for i := range dq.Parts {
		var appended []syntax.WordPart
		isExpansion := false
		endsInName := false
		switch p := dq.Parts[i].(type) {
		case *syntax.ParamExp:
			appended = e.paramReplacements(p)
			isExpansion = true
			endsInName = expansionEndsInName(appended)
		case *syntax.ArithmExp:
			txt, ok := e.arithmText(p.X)
			if !ok {
				e.warn(p.Pos(), "arithmetic uses an operator without a fish math equivalent; left untranslated")
				appended = []syntax.WordPart{p}
			} else {
				appended = []syntax.WordPart{e.mathSubst(txt)}
			}
			isExpansion = true
		case *syntax.CmdSubst, *syntax.ProcSubst:
			appended = []syntax.WordPart{p}
			isExpansion = true
		default:
			appended = []syntax.WordPart{dq.Parts[i]}
		}
		cur = append(cur, appended...)
		if isExpansion && quotedSplitNeeded(dq.Parts, i, endsInName) {
			flush()
			split = true
		}
	}
	flush()
	if !split || len(out) == 0 {
		if len(out) > 0 {
			dq.Parts = out[0].(*syntax.DblQuoted).Parts
		}
		return []syntax.WordPart{dq}
	}
	return out
}

// quotedSegment builds one adjacent double-quoted segment. Its span is
// derived from its content so the printer's line tracking stays valid.
func quotedSegment(parts []syntax.WordPart) *syntax.DblQuoted {
	seg := &syntax.DblQuoted{Parts: parts}
	if len(parts) > 0 {
		seg.Left = parts[0].Pos()
		seg.Right = parts[len(parts)-1].End()
	}
	return seg
}

// quotedSplitNeeded reports whether the quoted region must end after the
// expansion at index i: the next part is source text that fish would
// otherwise absorb into the replacement. terminatesInName says the
// replacement ends in a variable name, so identifier characters are
// absorbed; a following `[` is absorbed into any expansion's result.
func quotedSplitNeeded(parts []syntax.WordPart, i int, terminatesInName bool) bool {
	if i+1 >= len(parts) {
		return false
	}
	lit, ok := parts[i+1].(*syntax.Lit)
	if !ok || len(lit.Value) == 0 {
		return false
	}
	if !sourceAdjacent(parts[i].End(), lit.Pos()) {
		return false
	}
	if lit.Value[0] == '[' {
		return true
	}
	return terminatesInName && isNameChar(lit.Value[0])
}

// expandBraceSequences rewrites numeric bash brace sequences (`{1..3}`,
// `{1..9..2}`, `{3..1}`) into fish `seq` command substitutions, which
// produce the same word list. Heredoc bodies, quoted literals and
// parameter expansions are never touched: bash does not brace-expand
// there. Sequences fish cannot express (letter ranges, zero-padded
// bounds, mismatched step signs) are left in place with a warning.
func (e *emitter) expandBraceSequences(f *syntax.File) {
	syntax.Walk(f, func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.Redirect:
			return x.Hdoc == nil
		case *syntax.ParamExp:
			return false
		case *syntax.DblQuoted, *syntax.SglQuoted:
			return false
		case *syntax.Word:
			x.Parts = e.braceSeqParts(x.Parts)
			return true
		}
		return true
	})
}

func (e *emitter) braceSeqParts(parts []syntax.WordPart) []syntax.WordPart {
	out := make([]syntax.WordPart, 0, len(parts))
	changed := false
	for _, part := range parts {
		lit, ok := part.(*syntax.Lit)
		if !ok {
			out = append(out, part)
			continue
		}
		expanded, ok := e.braceLitParts(lit)
		if !ok {
			out = append(out, lit)
			continue
		}
		out = append(out, expanded...)
		changed = true
	}
	if !changed {
		return parts
	}
	return out
}

// braceLitParts splits one literal on numeric brace sequences. ok is false
// when nothing was translated; unsupported sequences are warned about and
// kept literal.
func (e *emitter) braceLitParts(lit *syntax.Lit) ([]syntax.WordPart, bool) {
	val := lit.Value
	if !strings.Contains(val, "{") || !strings.Contains(val, "..") {
		return nil, false
	}
	var out []syntax.WordPart
	var cur strings.Builder
	changed := false
	for i := 0; i < len(val); {
		if val[i] != '{' {
			cur.WriteByte(val[i])
			i++
			continue
		}
		closeIdx := strings.IndexByte(val[i:], '}')
		if closeIdx < 0 {
			cur.WriteByte(val[i])
			i++
			continue
		}
		closeIdx += i
		inner := val[i+1 : closeIdx]
		if !strings.Contains(inner, "..") {
			cur.WriteString(val[i : closeIdx+1])
			i = closeIdx + 1
			continue
		}
		args, ok := braceSequenceArgs(inner)
		if !ok {
			e.warn(lit.Pos(), "brace sequence %s cannot be translated; fish seq cannot express it", val[i:closeIdx+1])
			cur.WriteString(val[i : closeIdx+1])
			i = closeIdx + 1
			continue
		}
		if cur.Len() > 0 {
			out = append(out, litPart(cur.String()))
			cur.Reset()
		}
		out = append(out, substPart(append([]string{"seq"}, args...)...))
		changed = true
		i = closeIdx + 1
	}
	if !changed {
		return nil, false
	}
	if cur.Len() > 0 {
		out = append(out, litPart(cur.String()))
	}
	return out, true
}

// braceSequenceArgs converts the interior of a numeric brace sequence
// (`1..3`, `1..9..2`, `3..1`) into fish `seq` arguments. ok is false for
// content seq cannot reproduce: letter ranges, zero-padded bounds,
// mismatched step signs, and zero steps.
func braceSequenceArgs(inner string) ([]string, bool) {
	fields := strings.Split(inner, "..")
	if len(fields) != 2 && len(fields) != 3 {
		return nil, false
	}
	nums := make([]int64, 0, len(fields))
	for _, f := range fields {
		if !plainInt(f) {
			return nil, false
		}
		n, err := strconv.ParseInt(f, 10, 64)
		if err != nil {
			return nil, false
		}
		nums = append(nums, n)
	}
	start, end := nums[0], nums[1]
	if len(nums) == 2 {
		if end < start {
			// bash counts down; GNU seq needs an explicit step to do so.
			return []string{
				strconv.FormatInt(start, 10),
				"-1",
				strconv.FormatInt(end, 10),
			}, true
		}
		return []string{strconv.FormatInt(start, 10), strconv.FormatInt(end, 10)}, true
	}
	step := nums[2]
	if step == 0 || (end < start) != (step < 0) {
		return nil, false
	}
	return []string{
		strconv.FormatInt(start, 10),
		strconv.FormatInt(step, 10),
		strconv.FormatInt(end, 10),
	}, true
}

// plainInt reports whether s is a plain decimal integer whose printed form
// matches the value (no zero padding, which bash would preserve).
func plainInt(s string) bool {
	if s == "" {
		return false
	}
	body := s
	if body[0] == '-' || body[0] == '+' {
		body = body[1:]
	}
	if body == "" {
		return false
	}
	if len(body) > 1 && body[0] == '0' {
		return false
	}
	for i := range len(body) {
		if body[i] < '0' || body[i] > '9' {
			return false
		}
	}
	return true
}

// escapeIndexBrackets escapes unescaped `[` so fish cannot read it as a
// subscript of the preceding expansion. Idempotent.
func escapeIndexBrackets(s string) string {
	var b strings.Builder
	for i := range len(s) {
		if s[i] == '[' {
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
		before := len(e.warnings)
		if parts, ok := e.operatorExpansion(pe); ok {
			return parts
		}
		if len(e.warnings) == before {
			e.warn(pe.Pos(), "parameter expansion %s has no fish equivalent; left untranslated", e.render(pe))
		}
		return []syntax.WordPart{pe}
	}
	t, ok := e.resolveParamTarget(name)
	if !ok {
		return []syntax.WordPart{pe}
	}
	if name == "-" {
		e.warn(pe.Pos(), "$- has no fish equivalent; fish uses status subcommands (e.g. status is-interactive)")
	}
	if !t.mapped && t.subst == nil && t.index == "" && t.name == name && syntax.ValidName(name) {
		pe.Short = true // ${var}: drop the braces
		return []syntax.WordPart{pe}
	}
	return paramTargetParts(t)
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

func stringUnarySubst(subcmd string, ref *syntax.Word) syntax.WordPart {
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
	raw := pe.Param.Value
	t, ok := e.resolveParamTarget(raw)
	if !ok {
		return nil, false
	}
	name := t.name
	isPos := isDigits(raw)
	if raw == "-" {
		e.warn(pe.Pos(), "$- has no fish equivalent; fish uses status subcommands (e.g. status is-interactive)")
	}
	valuePlain := e.paramTargetText(t)
	valueArg := valuePlain
	if t.subst != nil {
		valueArg = "\"" + valuePlain + "\""
	}
	ref := litWord(e.paramTargetRefText(t))
	printVal := func(val *syntax.Word) *syntax.Stmt {
		return callStmt(litWord("printf"), litWord(`%s\n`), val)
	}

	switch {
	case pe.Length && pe.Index != nil:
		if tok, ok := e.indexToken(pe.Index); !ok || (tok != "@" && tok != "*") {
			return nil, false
		}
		if t.subst != nil {
			if raw == "FUNCNAME" {
				e.warn(pe.Pos(), "fish has no function stack to count; bash's ${#FUNCNAME[@]} includes its implicit script frame; emitted as count $FUNCNAME")
			} else {
				e.warn(pe.Pos(), "length of %s is not supported; emitted as count $%s", valuePlain, raw)
			}
			return []syntax.WordPart{substPart("count", "$"+raw)}, true
		}
		return []syntax.WordPart{substPart("count", "$"+name)}, true

	case pe.Index != nil && pe.Slice != nil:
		if t.subst != nil {
			e.warn(pe.Pos(), "slice of %s is not supported; left untranslated", valuePlain)
			return []syntax.WordPart{pe}, true
		}
		tok, ok := e.indexToken(pe.Index)
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
		if t.subst != nil {
			e.warn(pe.Pos(), "element operator on %s is not supported; left untranslated", valuePlain)
			return []syntax.WordPart{pe}, true
		}
		tok, ok := e.indexToken(pe.Index)
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
			fishSingleQuote(body), "''", "$"+name+"["+shiftIndex(tok, t.shift)+"]")}, true

	case pe.Index != nil && pe.Exp != nil:
		e.warn(pe.Pos(), "%s on an indexed array element is left untranslated",
			pe.Exp.Op.String())
		return []syntax.WordPart{pe}, true

	case pe.Index != nil:
		if t.subst != nil {
			tok, _ := e.indexToken(pe.Index)
			if tok != "" && tok != "0" && tok != "@" && tok != "*" {
				e.warn(pe.Pos(), "context array index %s beyond depth 0 is not supported in fish; emitted current context", tok)
			}
			return t.subst, true
		}
		tok, ok := e.indexToken(pe.Index)
		switch {
		case !ok:
			e.warn(pe.Pos(), "dynamic array index cannot be shifted; left untranslated")
			return []syntax.WordPart{pe}, true
		case tok == "@" || tok == "*":
			return []syntax.WordPart{namedParam(name)}, true
		default:
			return []syntax.WordPart{namedParam(name), litPart("[" + shiftIndex(tok, t.shift) + "]")}, true
		}

	case pe.Length:
		return []syntax.WordPart{stringUnarySubst("length", ref)}, true

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
		args = append(args, "--", "\""+valuePlain+"\"")
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
			pe.Repl.With.Parts = e.spliceParts(pe.Repl.With.Parts)
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
			valueArg)
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
		case syntax.DefaultUnsetOrNull, syntax.DefaultUnset:
			if t.subst != nil {
				// A substitution always yields a value, so only an empty
				// result reaches the default — and an empty default cannot
				// change it.
				if exp.Op == syntax.DefaultUnset || exp.Word == nil || isEmptyValue(exp.Word) {
					return t.subst, true
				}
				local := "__bait_value"
				setStmt := callStmt(litWord("set"), litWord("--local"), litWord(local), &syntax.Word{Parts: t.subst})
				cond := callStmt(litWord("test"), litWord("-n"), litWord("$"+local))
				chain := binCmd(syntax.OrStmt,
					binCmd(syntax.AndStmt, cond, printVal(litWord("$"+local))),
					printVal(e.argWord(exp.Word)))
				return []syntax.WordPart{e.synthCmdSubst(binCmd(syntax.AndStmt, setStmt, chain))}, true
			}
			if (exp.Word == nil || isEmptyValue(exp.Word)) && isPos {
				return []syntax.WordPart{argvParam(), litPart("[" + raw + "]")}, true
			}
			var cond *syntax.Stmt
			if exp.Op == syntax.DefaultUnsetOrNull {
				cond = callStmt(litWord("test"), litWord("-n"), ref)
			} else if isPos {
				cond = callStmt(litWord("test"), litWord("(count $argv)"), litWord("-ge"), litWord(raw))
			} else {
				cond = callStmt(litWord("set"), litWord("--query"), litWord(name))
			}
			chain := binCmd(syntax.OrStmt,
				binCmd(syntax.AndStmt,
					cond,
					printVal(ref)),
				printVal(e.argWord(exp.Word)))
			return []syntax.WordPart{e.synthCmdSubst(chain)}, true

		case syntax.AlternateUnsetOrNull, syntax.AlternateUnset:
			if t.subst != nil {
				// fish has no variable behind the emulation, so a set-ness
				// test cannot be answered: bash-only guards must not fire
				// here, and the divergence is warned about.
				e.warn(pe.Pos(), "%s cannot be applied to %s: fish has no such variable, so the test reads as unset", exp.Op.String(), valuePlain)
				return []syntax.WordPart{&syntax.DblQuoted{}}, true
			}
			var cond *syntax.Stmt
			if exp.Op == syntax.AlternateUnsetOrNull {
				cond = callStmt(litWord("test"), litWord("-n"), ref)
			} else if isPos {
				cond = callStmt(litWord("test"), litWord("(count $argv)"), litWord("-ge"), litWord(raw))
			} else {
				cond = callStmt(litWord("set"), litWord("--query"), litWord(name))
			}
			chain := binCmd(syntax.OrStmt,
				binCmd(syntax.AndStmt, cond, printVal(e.argWord(exp.Word))),
				callStmt(litWord("true")))
			return []syntax.WordPart{e.synthCmdSubst(chain)}, true

		case syntax.RemSmallPrefix, syntax.RemLargePrefix,
			syntax.RemSmallSuffix, syntax.RemLargeSuffix:
			if exp.Word == nil {
				// `${x%}` / `${x#}` strip an empty pattern: identity.
				return paramTargetParts(t), true
			}
			lazy := exp.Op == syntax.RemSmallPrefix || exp.Op == syntax.RemSmallSuffix
			isPrefix := exp.Op == syntax.RemSmallPrefix || exp.Op == syntax.RemLargePrefix
			regexArg, ok := e.renderDynamicRegex(exp.Word, isPrefix, !isPrefix, lazy)
			if !ok {
				return nil, false
			}
			return []syntax.WordPart{substPart("string", "replace", "--regex", "--",
				regexArg, "''", valueArg)}, true

		case syntax.UpperAll:
			if exp.Word != nil {
				e.warn(pe.Pos(), "case modification with pattern is not supported; left untranslated")
				return nil, false
			}
			return []syntax.WordPart{stringUnarySubst("upper", ref)}, true

		case syntax.LowerAll:
			if exp.Word != nil {
				e.warn(pe.Pos(), "case modification with pattern is not supported; left untranslated")
				return nil, false
			}
			return []syntax.WordPart{stringUnarySubst("lower", ref)}, true

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
