package bait

import (
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

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
//
// FUNCNAME maps to `status current-function`, which fish exposes only for the
// innermost frame: indices beyond 0 and the array count have no fish
// equivalent and are warned about (the pinned fish has no $funcstack list).
var bashContextVars = map[string][]string{
	"0":            {"status", "filename"},
	"BASH_SOURCE":  {"status", "filename"},
	"BASH_ARGV0":   {"status", "filename"},
	"BASH":         {"status", "fish-path"},
	"BASH_VERSION": {"echo", "5.2.0"},
	"BASH_COMMAND": {"status", "current-command"},
	"FUNCNAME":     {"status", "current-function"},
	"UID":          {"id", "-u"},
	"GROUPS":       {"id", "-g"},
}

// paramTarget describes the fish reference a bash parameter name resolves
// to. Exactly one of subst and name is meaningful:
//
//   - subst != nil: the reference is a command substitution. Operators that
//     consume a value apply to the substitution result; operators fish cannot
//     express over it are left untranslated with a warning.
//   - subst == nil: a plain fish variable reference. name is the fish
//     variable (already mangled). index, when non-empty, is the fish subscript
//     used for a bare expansion; shift is added to an explicit bash subscript.
//
// mapped marks names whose bash semantics differ from a plain user variable,
// so assignments to them have no faithful fish equivalent.
type paramTarget struct {
	name   string
	subst  []syntax.WordPart
	index  string
	shift  int
	mapped bool
}

// resolveParamTarget maps a bash parameter name onto its fish reference. It is
// the single name-to-reference mapping consulted by word expansion, parameter
// operators, arithmetic, and the binding side.
func (e *emitter) resolveParamTarget(name string) (paramTarget, bool) {
	if cv, ok := bashContextVars[name]; ok {
		return paramTarget{subst: []syntax.WordPart{substPart(cv...)}, mapped: true}, true
	}
	if mangled, ok := fishReservedVars[name]; ok {
		return paramTarget{name: mangled}, true
	}
	switch name {
	case "@", "*":
		return paramTarget{name: "argv", mapped: true}, true
	case "?":
		return paramTarget{name: "status"}, true
	case "!":
		return paramTarget{name: "last_pid"}, true
	case "$", "BASHPID":
		return paramTarget{name: "fish_pid"}, true
	case "#":
		return paramTarget{subst: []syntax.WordPart{substPart("count", "$argv")}, mapped: true}, true
	case "HOSTNAME":
		return paramTarget{name: "hostname", mapped: true}, true
	case "HOSTTYPE", "MACHTYPE":
		return paramTarget{subst: []syntax.WordPart{substPart("uname", "-m")}, mapped: true}, true
	case "PIPESTATUS":
		return paramTarget{name: "pipestatus", shift: 1, mapped: true}, true
	case "DIRSTACK":
		return paramTarget{name: "dirstack", shift: 1, mapped: true}, true
	case "OSTYPE":
		e.needHelper(helperOSType)
		return paramTarget{subst: []syntax.WordPart{substPart("__bait_ostype")}, mapped: true}, true
	case "RANDOM":
		return paramTarget{subst: []syntax.WordPart{substPart("random")}, mapped: true}, true
	case "SRANDOM":
		return paramTarget{subst: []syntax.WordPart{substPart("random", "0", "4294967295")}, mapped: true}, true
	case "EPOCHSECONDS":
		return paramTarget{subst: []syntax.WordPart{substPart("date", "+%s")}, mapped: true}, true
	case "-":
		return paramTarget{subst: []syntax.WordPart{statusDashPart()}, mapped: true}, true
	}
	if isDigits(name) {
		return paramTarget{name: "argv", index: name}, true
	}
	if syntax.ValidName(name) {
		return paramTarget{name: name, shift: 1}, true
	}
	return paramTarget{}, false
}

// paramTargetParts renders a bare reference to the target.
func paramTargetParts(t paramTarget) []syntax.WordPart {
	if t.subst != nil {
		return t.subst
	}
	if t.index != "" {
		return []syntax.WordPart{namedParam(t.name), litPart("[" + t.index + "]")}
	}
	return []syntax.WordPart{namedParam(t.name)}
}

// paramTargetText renders a bare reference as fish source text, embedding a
// substitution-backed target as "$(cmd)".
func (e *emitter) paramTargetText(t paramTarget) string {
	if t.subst != nil {
		return e.render(&syntax.Word{Parts: t.subst})
	}
	if t.index != "" {
		return "$" + t.name + "[" + t.index + "]"
	}
	return "$" + t.name
}

// paramTargetRefText renders the double-quoted reference operators use when
// they consume one value. It is literal text, not a parameter node: the name
// has already been resolved, and a node would be resolved a second time.
func (e *emitter) paramTargetRefText(t paramTarget) string {
	if t.subst != nil {
		return `"` + e.paramTargetText(t) + `"`
	}
	if t.index != "" {
		return `"$` + t.name + `[` + t.index + `]"`
	}
	return `"$` + t.name + `"`
}

// paramTargetValueRef renders a resolved target as one value, applying the
// list policy: colon-split names are quoted and bash arrays contribute
// element 0.
func (e *emitter) paramTargetValueRef(t paramTarget) string {
	if t.subst != nil {
		return `"` + e.paramTargetText(t) + `"`
	}
	if t.index != "" {
		return `"$` + t.name + `[` + t.index + `]"`
	}
	switch e.listPolicy(t.name) {
	case listFishListEnv:
		return `"$` + t.name + `"`
	case listBashArray:
		return `"$` + t.name + `[1]"`
	}
	return "$" + t.name
}

// listKind classifies how a bash variable's value maps onto fish's list
// semantics.
type listKind int

const (
	// listScalar: fish stores the value as a single element.
	listScalar listKind = iota
	// listFishListEnv: fish splits the name on ':' when it is imported and
	// joins it back on ':' when the reference is quoted.
	listFishListEnv
	// listBashArray: a bash array tracked by the dataflow pass; fish stores
	// it as a 1-based list, so bash's element 0 is fish's [1].
	listBashArray
)

// listPolicy reports how a bash variable expands. It is the single policy
// consulted by for items, case subjects, assignment values, redirect
// targets, and argument injection.
func (e *emitter) listPolicy(name string) listKind {
	if e.knownLists[name] {
		return listBashArray
	}
	if isFishListEnvVar(name) {
		return listFishListEnv
	}
	return listScalar
}

// bareParamOf returns the parameter a word expands to, unwrapping one level
// of double quotes: `$v` and `"$v"` expand to the same value, and quoted
// reports which form it was (quoting suppresses bash word splitting).
func bareParamOf(w *syntax.Word) (pe *syntax.ParamExp, quoted, ok bool) {
	if w == nil || len(w.Parts) != 1 {
		return nil, false, false
	}
	part := w.Parts[0]
	if q, isQ := part.(*syntax.DblQuoted); isQ {
		if len(q.Parts) != 1 {
			return nil, false, false
		}
		part, quoted = q.Parts[0], true
	}
	pe, ok = part.(*syntax.ParamExp)
	if !ok || !bareParam(pe) || pe.Param == nil {
		return nil, false, false
	}
	return pe, quoted, true
}

// paramValueRef renders a bare parameter as a fish reference that yields
// exactly one value: bash arrays contribute element 0 and colon-split
// environment variables are quoted so fish joins them on ':' again. Callers
// that need bash's word splitting wrap the result in __bait_words.
//
// The name has already been through rewriteParams, so it is a fish name:
// context variables were replaced with their substitutions there.
func (e *emitter) paramValueRef(pe *syntax.ParamExp) (string, bool) {
	if pe == nil || pe.Param == nil {
		return "", false
	}
	return e.paramTargetValueRef(paramTarget{name: pe.Param.Value}), true
}

// arrayAllRef returns the whole-list reference for a word that
// rewriteParams normalized from a "${arr[@]}"-style expansion, whose value
// is every element rather than a scalar. Callers that need one value quote
// the reference (fish joins a quoted list on spaces, like bash's IFS join).
func (e *emitter) arrayAllRef(w *syntax.Word, quoted bool) (string, bool) {
	if !e.arrayAllWords[w] {
		return "", false
	}
	pe, _, ok := bareParamOf(w)
	if !ok {
		return "", false
	}
	ref := "$" + pe.Param.Value
	if quoted {
		ref = `"` + ref + `"`
	}
	return ref, true
}

// quoteRef makes a single-value reference a single fish argument even when
// it expands to nothing: an unquoted reference vanishes, and fish's switch
// rejects a subject with no arguments.
func quoteRef(ref string) string {
	if strings.HasPrefix(ref, `"`) {
		return ref
	}
	return `"` + ref + `"`
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

func (e *emitter) escapeLiteralDollars(f *syntax.File) {
	var walk func(n syntax.Node) bool
	walk = func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.Redirect:
			switch x.Op {
			case syntax.Hdoc, syntax.DashHdoc:
				// A quoted here-document body is emitted as literal text;
				// an unquoted one is emitted double-quoted, where a
				// literal dollar must be escaped to survive fish.
				if x.Hdoc != nil && !isQuotedHdocWord(x.Word) {
					syntax.Walk(x.Hdoc, walk)
				}
				return false
			case syntax.WordHdoc:
				// `<<<` words are ordinary words; walk them.
				return true
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

func (e *emitter) varName(name string) string {
	if name == "PIPESTATUS" {
		return "pipestatus"
	}
	if name == "DIRSTACK" {
		return "dirstack"
	}
	return mangleVarName(name)
}
