package main

import (
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/kidonng/bait/internal/bait"
)

func envBool(key string) bool {
	val, ok := os.LookupEnv(key)
	if !ok {
		return false
	}
	b, err := strconv.ParseBool(val)
	if err != nil {
		return false
	}
	return b
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	quiet := envBool("BAIT_QUIET")
	noShims := envBool("BAIT_NO_SHIMS")

	for _, arg := range args {
		switch arg {
		case "-q", "--quiet":
			quiet = true
		case "--no-shims":
			noShims = true
		}
	}

	src, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	out, warnings, err := bait.TranslateWithOptions(src, bait.Options{
		NoShims: noShims,
	})
	if err != nil {
		fmt.Fprintf(stderr, "<stdin>: %v\n", err)
		return 1
	}

	if !quiet {
		for _, w := range warnings {
			fmt.Fprintf(stderr, "<stdin>:%d:%d: %s\n", w.Line, w.Col, w.Text)
		}
	}

	stdout.Write(out)
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
