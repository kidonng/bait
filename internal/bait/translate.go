// Package bait translates bash scripts into fish scripts.
//
// The translation strategy is minimal-diff: constructs that are already
// valid fish are emitted verbatim ("passthrough"), keyword-level
// differences are rewritten mechanically, and constructs without a fish
// equivalent are passed through unchanged while a Warning is reported.
package bait

import (
	"bytes"
	"fmt"

	"mvdan.cc/sh/v3/syntax"
)

// Warning describes a construct that could not be translated and was
// therefore emitted verbatim. Line and Col refer to the original bash
// source and are 1-based.
type Warning struct {
	Line int
	Col  int
	Text string
}

func (w Warning) Error() string {
	return fmt.Sprintf("%d:%d: %s", w.Line, w.Col, w.Text)
}

// Translate parses src as a bash script and returns its fish translation.
//
// Constructs that bait cannot translate are emitted unchanged and reported
// through the returned warnings. The result is a best-effort translation
// and should be reviewed before execution.
func Translate(src []byte) ([]byte, []Warning, error) {
	src = translateShebang(src)
	if len(bytes.TrimSpace(src)) == 0 {
		return src, nil, nil
	}
	parser := syntax.NewParser(
		syntax.KeepComments(true),
		syntax.Variant(syntax.LangBash),
	)
	file, err := parser.Parse(bytes.NewReader(src), "")
	if err != nil {
		return nil, nil, fmt.Errorf("parse bash: %w", err)
	}
	em := newEmitter()
	em.normalize(file)
	em.file(file)
	if em.err != nil {
		return nil, nil, fmt.Errorf("print fish: %w", em.err)
	}
	return em.buf.Bytes(), em.warnings, nil
}
