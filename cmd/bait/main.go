// Command bait translates bash scripts into fish scripts.
//
// Usage:
//
//	bait [options] [file]
//
// With no file argument, bait reads from stdin and writes the fish
// translation to stdout, cat-style. Constructs without a fish equivalent
// are passed through unchanged and reported as warnings on stderr;
// pass -quiet to suppress them.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/kidonng/bait/internal/bait"
)

func main() {
	quiet := flag.Bool("quiet", false, "suppress translation warnings on stderr")
	flag.Parse()

	if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: bait [-quiet] [file]")
		os.Exit(2)
	}

	var (
		src  []byte
		err  error
		name string
	)
	if flag.NArg() == 1 {
		name = flag.Arg(0)
		src, err = os.ReadFile(name)
	} else {
		name = "<stdin>"
		src, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	out, warnings, err := bait.Translate(src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		os.Exit(1)
	}

	if !*quiet {
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "%s:%d:%d: %s\n", name, w.Line, w.Col, w.Text)
		}
	}

	os.Stdout.Write(out)
}
