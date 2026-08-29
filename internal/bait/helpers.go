package bait

import (
	_ "embed"
	"fmt"
)

//go:embed helpers/__bait_words.fish
var baitWordsHelper string

//go:embed helpers/__bait_exec.fish
var baitExecHelper string

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
}

func (e *emitter) needHelper(h helperKind) {
	e.neededHelpers[h] = true
	if h == helperDot {
		e.neededHelpers[helperSource] = true
	}
}

func (e *emitter) hasHelper(h helperKind) bool {
	return e.neededHelpers[h]
}

// Helper returns the fish script content of the named runtime helper.
func Helper(name string) (string, error) {
	switch name {
	case "source":
		return baitSourceHelper, nil
	case ".":
		return baitDotHelper, nil
	case "getopts":
		return baitGetoptsHelper, nil
	case "hash":
		return baitHashHelper, nil
	case "unalias":
		return baitUnaliasHelper, nil
	case "unset":
		return baitUnsetHelper, nil
	case "__bait_words":
		return baitWordsHelper, nil
	case "__bait_exec":
		return baitExecHelper, nil
	default:
		return "", fmt.Errorf("unknown helper %q", name)
	}
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
	}
}
