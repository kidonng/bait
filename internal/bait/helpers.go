package bait

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed helpers/__bait_words.fish
var baitWordsHelper string

//go:embed helpers/__bait_exec.fish
var baitExecHelper string

//go:embed helpers/__bait_eval.fish
var baitEvalHelper string

//go:embed helpers/getopts.fish
var baitGetoptsHelper string

//go:embed helpers/hash.fish
var baitHashHelper string

//go:embed helpers/unalias.fish
var baitUnaliasHelper string

//go:embed helpers/unset.fish
var baitUnsetHelper string

//go:embed helpers/source.fish
var baitSourceHelper string

//go:embed helpers/..fish
var baitDotHelper string

//go:embed helpers/__bait_ostype.fish
var baitOSTypeHelper string

//go:embed helpers/__bait_math.fish
var baitMathHelper string

//go:embed helpers/__bait_cmp.fish
var baitCmpHelper string

type helperKind int

const (
	helperWords helperKind = iota
	helperExec
	helperGetopts
	helperHash
	helperUnalias
	helperSource
	helperDot
	helperUnset
	helperOSType
	helperEval
	helperMath
	helperCmp
	numHelpers
)

type helperInfo struct {
	kind helperKind
	code string
}

var allHelpers = []helperInfo{
	{kind: helperWords, code: baitWordsHelper},
	{kind: helperExec, code: baitExecHelper},
	{kind: helperGetopts, code: baitGetoptsHelper},
	{kind: helperHash, code: baitHashHelper},
	{kind: helperUnalias, code: baitUnaliasHelper},
	{kind: helperSource, code: baitSourceHelper},
	{kind: helperDot, code: baitDotHelper},
	{kind: helperUnset, code: baitUnsetHelper},
	{kind: helperOSType, code: baitOSTypeHelper},
	{kind: helperEval, code: baitEvalHelper},
	{kind: helperMath, code: baitMathHelper},
	{kind: helperCmp, code: baitCmpHelper},
}

// helperDeps lists the helpers a helper's own code calls: emitting or
// printing a helper must include everything it depends on.
var helperDeps = map[helperKind][]helperKind{
	helperDot:  {helperSource},
	helperCmp:  {helperMath},
	helperExec: {helperWords},
}

func (e *emitter) needHelper(h helperKind) {
	e.neededHelpers[h] = true
	for _, dep := range helperDeps[h] {
		e.needHelper(dep)
	}
}

// helperKindByName maps the names the CLI and tests use to helper kinds.
var helperKindByName = map[string]helperKind{
	"source":        helperSource,
	".":             helperDot,
	"getopts":       helperGetopts,
	"hash":          helperHash,
	"unalias":       helperUnalias,
	"unset":         helperUnset,
	"__bait_words":  helperWords,
	"__bait_exec":   helperExec,
	"eval":          helperEval,
	"__bait_eval":   helperEval,
	"__bait_ostype": helperOSType,
	"__bait_math":   helperMath,
	"__bait_cmp":    helperCmp,
}

func helperCode(k helperKind) string {
	for _, h := range allHelpers {
		if h.kind == k {
			return h.code
		}
	}
	return ""
}

// Helper returns the fish script content of the named runtime helper,
// followed by the helpers it depends on.
func Helper(name string) (string, error) {
	kind, ok := helperKindByName[name]
	if !ok {
		return "", fmt.Errorf("unknown helper %q", name)
	}
	var b strings.Builder
	var write func(helperKind)
	write = func(k helperKind) {
		b.WriteString(helperCode(k))
		for _, dep := range helperDeps[k] {
			write(dep)
		}
	}
	write(kind)
	return b.String(), nil
}

// Helpers returns the canonical names of available runtime helpers.
func Helpers() []string {
	return []string{
		"source",
		".",
		"getopts",
		"hash",
		"unalias",
		"unset",
		"__bait_words",
		"__bait_exec",
		"__bait_ostype",
		"__bait_eval",
		"__bait_math",
		"__bait_cmp",
	}
}
