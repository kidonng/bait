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
			if x.Op == syntax.Hdoc || x.Op == syntax.DashHdoc || x.Op == syntax.WordHdoc {
				return false
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
