// Command bait translates bash scripts into fish scripts.
//
// Usage:
//
//	bait [options] [file]
//
// With no file argument, bait reads from stdin and writes the fish
// translation to stdout, cat-style. Constructs without a fish equivalent
// are passed through unchanged and reported as warnings on stderr;
// pass -quiet (or -q) to suppress them.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/kidonng/bait/internal/bait"
)

var version = ""

const usageText = `bait translates bash scripts into fish scripts.

Usage:
  bait [options] [file]

With no file argument, bait reads from stdin and writes the fish
translation to stdout.

Options:
  -q, --quiet    suppress translation warnings on stderr
  -h, --help     show this help message
  -v, --version  show version information

Environment variables:
  BAIT_QUIET     suppress translation warnings on stderr
`

func getVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
	}
	return "dev"
}
func envQuiet() bool {
	val := strings.TrimSpace(os.Getenv("BAIT_QUIET"))
	if val == "" {
		return false
	}
	if b, err := strconv.ParseBool(val); err == nil {
		return b
	}
	switch strings.ToLower(val) {
	case "yes", "y", "on":
		return true
	case "no", "n", "off":
		return false
	}
	return false
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bait", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		quiet   bool
		help    bool
		showVer bool
	)

	defaultQuiet := envQuiet()
	fs.BoolVar(&quiet, "quiet", defaultQuiet, "suppress translation warnings on stderr")
	fs.BoolVar(&quiet, "q", defaultQuiet, "suppress translation warnings on stderr")
	fs.BoolVar(&help, "help", false, "show this help message")
	fs.BoolVar(&help, "h", false, "show this help message")
	fs.BoolVar(&showVer, "version", false, "show version information")
	fs.BoolVar(&showVer, "v", false, "show version information")

	fs.Usage = func() {
		fmt.Fprint(stderr, usageText)
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) || help {
			fmt.Fprint(stdout, usageText)
			return 0
		}
		return 2
	}

	if help {
		fmt.Fprint(stdout, usageText)
		return 0
	}

	if showVer {
		fmt.Fprintf(stdout, "bait %s\n", getVersion())
		return 0
	}

	if fs.NArg() > 1 {
		fmt.Fprintln(stderr, "error: too many arguments")
		fs.Usage()
		return 2
	}

	var (
		src  []byte
		err  error
		name string
	)
	if fs.NArg() == 1 {
		name = fs.Arg(0)
		src, err = os.ReadFile(name)
	} else {
		name = "<stdin>"
		src, err = io.ReadAll(stdin)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	out, warnings, err := bait.Translate(src)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}

	if !quiet {
		for _, w := range warnings {
			fmt.Fprintf(stderr, "%s:%d:%d: %s\n", name, w.Line, w.Col, w.Text)
		}
	}

	stdout.Write(out)
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
