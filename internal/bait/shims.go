package bait

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"
)

//go:embed shims/__bait_words.fish
var baitWordsShim string

//go:embed shims/__bait_exec.fish
var baitExecShim string

//go:embed shims/__bait_eval.fish
var baitEvalShim string

//go:embed shims/getopts.fish
var baitGetoptsShim string

//go:embed shims/hash.fish
var baitHashShim string

//go:embed shims/unalias.fish
var baitUnaliasShim string

//go:embed shims/unset.fish
var baitUnsetShim string

//go:embed shims/source.fish
var baitSourceShim string

//go:embed shims/..fish
var baitDotShim string

//go:embed shims/__bait_ostype.fish
var baitOSTypeShim string

//go:embed shims/__bait_math.fish
var baitMathShim string

//go:embed shims/__bait_cmp.fish
var baitCmpShim string

type shimKind int

const (
	shimWords shimKind = iota
	shimExec
	shimGetopts
	shimHash
	shimUnalias
	shimSource
	shimDot
	shimUnset
	shimOSType
	shimEval
	shimMath
	shimCmp
	numShims
)

type shimInfo struct {
	kind shimKind
	name string
	code string
}

var allShims = []shimInfo{
	{kind: shimWords, name: "__bait_words", code: baitWordsShim},
	{kind: shimExec, name: "__bait_exec", code: baitExecShim},
	{kind: shimGetopts, name: "getopts", code: baitGetoptsShim},
	{kind: shimHash, name: "hash", code: baitHashShim},
	{kind: shimUnalias, name: "unalias", code: baitUnaliasShim},
	{kind: shimSource, name: "source", code: baitSourceShim},
	{kind: shimDot, name: ".", code: baitDotShim},
	{kind: shimUnset, name: "unset", code: baitUnsetShim},
	{kind: shimOSType, name: "__bait_ostype", code: baitOSTypeShim},
	{kind: shimEval, name: "__bait_eval", code: baitEvalShim},
	{kind: shimMath, name: "__bait_math", code: baitMathShim},
	{kind: shimCmp, name: "__bait_cmp", code: baitCmpShim},
}

// shimDeps lists the shims a shim's own code calls: emitting or
// printing a shim must include everything it depends on.
var shimDeps = map[shimKind][]shimKind{
	shimDot:  {shimSource},
	shimCmp:  {shimMath},
	shimExec: {shimWords},
}

func (e *emitter) needShim(h shimKind) {
	e.neededShims[h] = true
	for _, dep := range shimDeps[h] {
		e.needShim(dep)
	}
}

// shimKindByName maps the names the CLI and tests use to shim kinds.
var shimKindByName = func() map[string]shimKind {
	m := make(map[string]shimKind, len(allShims)+1)
	for _, h := range allShims {
		m[h.name] = h.kind
	}
	m["eval"] = shimEval
	return m
}()

func shimCode(k shimKind) string {
	for _, h := range allShims {
		if h.kind == k {
			return h.code
		}
	}
	return ""
}

// Shim returns the fish script content of the named runtime shim,
// followed by the shims it depends on.
func Shim(name string) (string, error) {
	kind, ok := shimKindByName[name]
	if !ok {
		return "", fmt.Errorf("unknown shim %q", name)
	}
	var b strings.Builder
	var write func(shimKind)
	write = func(k shimKind) {
		b.WriteString(shimCode(k))
		for _, dep := range shimDeps[k] {
			write(dep)
		}
	}
	write(kind)
	return b.String(), nil
}

// Shims returns the canonical names of available runtime shims.
func Shims() []string {
	names := make([]string, len(allShims))
	for i, h := range allShims {
		names[i] = h.name
	}
	slices.Sort(names)
	return names
}
