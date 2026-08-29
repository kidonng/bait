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
  bait helper <name>

With no file argument, bait reads from stdin and writes the fish
translation to stdout.

Commands:
  helper <name>  print the content of a runtime helper

Options:
  -q, --quiet     suppress translation warnings on stderr
  --no-helpers    do not inject runtime helper functions
  -h, --help      show this help message
  -v, --version   show version information

Environment variables:
  BAIT_QUIET       suppress translation warnings on stderr
  BAIT_NO_HELPERS  do not inject runtime helper functions
`

const helperUsageText = `Usage:
  bait helper <name>
  bait helper --names

Print the content of a runtime helper, or list all available helper names.

Available helpers:
  source
  .
  getopts
  hash
  unalias
  unset
  __bait_words
  __bait_exec

Options:
  --names     list all available helper names
  -h, --help  show this help message
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
func envNoHelpers() bool {
	val := strings.TrimSpace(os.Getenv("BAIT_NO_HELPERS"))
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

func runHelper(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bait helper", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		help  bool
		names bool
	)
	fs.BoolVar(&names, "names", false, "list all available helper names")
	fs.BoolVar(&help, "help", false, "show this help message")
	fs.BoolVar(&help, "h", false, "show this help message")
	fs.Usage = func() {
		fmt.Fprint(stderr, helperUsageText)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) || help {
			fmt.Fprint(stdout, helperUsageText)
			return 0
		}
		return 2
	}

	if help {
		fmt.Fprint(stdout, helperUsageText)
		return 0
	}
	if names {
		if fs.NArg() > 0 {
			fmt.Fprintln(stderr, "error: too many arguments for --names")
			fs.Usage()
			return 2
		}
		for _, h := range bait.Helpers() {
			fmt.Fprintln(stdout, h)
		}
		return 0
	}

	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "error: missing helper name")
		fs.Usage()
		return 2
	}

	if fs.NArg() > 1 {
		fmt.Fprintln(stderr, "error: too many arguments for helper command")
		fs.Usage()
		return 2
	}

	name := fs.Arg(0)
	code, err := bait.Helper(name)
	if err != nil {
		fmt.Fprintf(stderr, "error: unknown helper %q\n", name)
		return 1
	}

	fmt.Fprint(stdout, code)
	if !strings.HasSuffix(code, "\n") {
		fmt.Fprintln(stdout)
	}
	return 0
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "helper" {
		return runHelper(args[1:], stdout, stderr)
	}
	fs := flag.NewFlagSet("bait", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		quiet     bool
		noHelpers bool
		help      bool
		showVer   bool
	)

	defaultQuiet := envQuiet()
	defaultNoHelpers := envNoHelpers()
	fs.BoolVar(&quiet, "quiet", defaultQuiet, "suppress translation warnings on stderr")
	fs.BoolVar(&quiet, "q", defaultQuiet, "suppress translation warnings on stderr")
	fs.BoolVar(&noHelpers, "no-helpers", defaultNoHelpers, "do not inject runtime helper functions")
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

	out, warnings, err := bait.TranslateWithOptions(src, bait.Options{
		NoHelpers: noHelpers,
	})
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
