package bait

import (
	_ "embed"
	"fmt"
	"slices"
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
	name string
	code string
}

var allHelpers = []helperInfo{
	{kind: helperWords, name: "__bait_words", code: baitWordsHelper},
	{kind: helperExec, name: "__bait_exec", code: baitExecHelper},
	{kind: helperGetopts, name: "getopts", code: baitGetoptsHelper},
	{kind: helperHash, name: "hash", code: baitHashHelper},
	{kind: helperUnalias, name: "unalias", code: baitUnaliasHelper},
	{kind: helperSource, name: "source", code: baitSourceHelper},
	{kind: helperDot, name: ".", code: baitDotHelper},
	{kind: helperUnset, name: "unset", code: baitUnsetHelper},
	{kind: helperOSType, name: "__bait_ostype", code: baitOSTypeHelper},
	{kind: helperEval, name: "__bait_eval", code: baitEvalHelper},
	{kind: helperMath, name: "__bait_math", code: baitMathHelper},
	{kind: helperCmp, name: "__bait_cmp", code: baitCmpHelper},
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
var helperKindByName = func() map[string]helperKind {
	m := make(map[string]helperKind, len(allHelpers)+1)
	for _, h := range allHelpers {
		m[h.name] = h.kind
	}
	m["eval"] = helperEval
	return m
}()

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
	names := make([]string, len(allHelpers))
	for i, h := range allHelpers {
		names[i] = h.name
	}
	slices.Sort(names)
	return names
}
