package bait

import (
	_ "embed"
)

//go:embed helpers/__bait_words.fish
var baitWordsHelper string

//go:embed helpers/__bait_exec.fish
var baitExecHelper string

//go:embed helpers/getopts.fish
var baitGetoptsHelper string

//go:embed helpers/hash.fish
var baitHashHelper string

type helperKind int

const (
	helperWords helperKind = iota
	helperExec
	helperGetopts
	helperHash
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
}

func (e *emitter) needHelper(h helperKind) {
	e.neededHelpers[h] = true
}

func (e *emitter) hasHelper(h helperKind) bool {
	return e.neededHelpers[h]
}
