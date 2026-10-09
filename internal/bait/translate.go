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
	"strings"

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

// Options specifies translation options.
type Options struct {
	// NoHelpers suppresses emitting runtime helper functions.
	NoHelpers bool
}

// Translate parses src as a bash script and returns its fish translation.
//
// Constructs that bait cannot translate are emitted unchanged and reported
// through the returned warnings. The result is a best-effort translation
// and should be reviewed before execution.
func Translate(src []byte) ([]byte, []Warning, error) {
	return TranslateWithOptions(src, Options{})
}

// TranslateWithOptions translates src using the provided options.
func TranslateWithOptions(src []byte, opts Options) ([]byte, []Warning, error) {
	src = translateShebang(src)
	if len(bytes.TrimSpace(src)) == 0 {
		return src, nil, nil
	}
	parser := syntax.NewParser(
		syntax.KeepComments(true),
		syntax.Variant(syntax.LangBash),
	)
	file, err := parser.Parse(bytes.NewReader(src), "")
	var repaired []string
	// mvdan rejects a here-document whose terminator is swallowed by a
	// backslash-newline continuation; bash warns and takes the rest of the
	// input as the body. Appending the delimiter the error names makes the
	// parse match bash's reading, so the translation stays faithful.
	for i := 0; err != nil && i < 10; i++ {
		delim, ok := unclosedHereDocDelim(err)
		if !ok {
			break
		}
		src = appendHereDocTerminator(src, delim)
		repaired = append(repaired, delim)
		file, err = parser.Parse(bytes.NewReader(src), "")
	}
	if err != nil {
		return nil, nil, fmt.Errorf("parse bash: %w", err)
	}
	em := newEmitter()
	em.noHelpers = opts.NoHelpers
	em.normalize(file)
	em.file(file)
	if len(repaired) > 0 {
		delims := make(map[string]bool, len(repaired))
		for _, d := range repaired {
			delims[d] = true
		}
		w := Warning{Text: fmt.Sprintf("here-document %s is delimited by end-of-file; the rest of the input is its body, matching bash's warning", strings.Join(repaired, ", "))}
		syntax.Walk(file, func(n syntax.Node) bool {
			if w.Line != 0 {
				return false
			}
			if r, ok := n.(*syntax.Redirect); ok && (r.Op == syntax.Hdoc || r.Op == syntax.DashHdoc) && r.Word != nil {
				if lit, ok := staticWordValue(r.Word); ok && delims[lit] {
					w.Line, w.Col = int(r.Pos().Line()), int(r.Pos().Col())
				}
			}
			return true
		})
		if w.Line == 0 {
			w.Line, w.Col = 1, 1
		}
		em.warnings = append(em.warnings, w)
	}
	if em.err != nil {
		return nil, nil, fmt.Errorf("print fish: %w", em.err)
	}
	return em.buf.Bytes(), em.warnings, nil
}

// unclosedHereDocDelim extracts the delimiter from mvdan's "unclosed
// here-document `EOF`" parse error.
func unclosedHereDocDelim(err error) (string, bool) {
	const marker = "unclosed here-document `"
	msg := err.Error()
	i := strings.Index(msg, marker)
	if i < 0 {
		return "", false
	}
	rest := msg[i+len(marker):]
	j := strings.IndexByte(rest, '`')
	if j <= 0 {
		return "", false
	}
	delim := rest[:j]
	if strings.ContainsAny(delim, "`\\\n") {
		return "", false
	}
	return delim, true
}

// appendHereDocTerminator adds the delimiter as a final line, so the parser
// reads the body exactly as bash does when it warns about end-of-file.
func appendHereDocTerminator(src []byte, delim string) []byte {
	if len(src) > 0 && src[len(src)-1] != '\n' {
		src = append(src, '\n')
	}
	src = append(src, delim...)
	return append(src, '\n')
}
