// Command bait translates bash scripts into fish scripts.
//
// Usage:
//
//	bait [flags] [file]
//	bait helper [flags] [name]
//
// With no file argument, bait reads from stdin and writes the fish
// translation to stdout. Constructs without a fish equivalent are
// passed through unchanged and reported as warnings on stderr.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kidonng/bait/internal/bait"
)

var version = ""

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

type exitError struct {
	code int
}

func (e exitError) Error() string {
	return fmt.Sprintf("exit code %d", e.code)
}

func newHelperCmd() *cobra.Command {
	var names bool

	cmd := &cobra.Command{
		Use:   "helper [name]",
		Short: "print the content of a runtime helper",
		Long:  "Print the content of a runtime helper, or list all available helper names.",
		Args: func(cmd *cobra.Command, args []string) error {
			if names {
				if len(args) > 0 {
					return errors.New("too many arguments for --names")
				}
				return nil
			}
			if len(args) == 0 {
				return errors.New("missing helper name")
			}
			if len(args) > 1 {
				return errors.New("too many arguments for helper command")
			}
			return nil
		},
		PreRun: func(cmd *cobra.Command, args []string) {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if names {
				for _, h := range bait.Helpers() {
					fmt.Fprintln(cmd.OutOrStdout(), h)
				}
				return nil
			}

			name := args[0]
			code, err := bait.Helper(name)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: unknown helper %q\n", name)
				return exitError{code: 1}
			}

			fmt.Fprint(cmd.OutOrStdout(), code)
			if !strings.HasSuffix(code, "\n") {
				fmt.Fprintln(cmd.OutOrStdout())
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&names, "names", false, "list all available helper names")

	return cmd
}

func newRootCmd(stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	var (
		quiet     bool
		noHelpers bool
	)

	cmd := &cobra.Command{
		Use:   "bait [flags] [file]",
		Short: "bait translates bash scripts into fish scripts",
		Long: `bait translates bash scripts into fish scripts.

With no file argument, bait reads from stdin and writes the fish
translation to stdout.`,
		Args: cobra.MaximumNArgs(1),
		PreRun: func(cmd *cobra.Command, args []string) {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				src  []byte
				err  error
				name string
			)
			if len(args) == 1 {
				name = args[0]
				src, err = os.ReadFile(name)
			} else {
				name = "<stdin>"
				src, err = io.ReadAll(cmd.InOrStdin())
			}
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				return exitError{code: 1}
			}

			out, warnings, err := bait.TranslateWithOptions(src, bait.Options{
				NoHelpers: noHelpers,
			})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", name, err)
				return exitError{code: 1}
			}

			if !quiet {
				colorPrefix, colorSuffix := "", ""
				if isColorEnabled() {
					colorPrefix, colorSuffix = colorYellow, colorReset
				}
				for _, w := range warnings {
					fmt.Fprintf(cmd.ErrOrStderr(), "%s%s:%d:%d: %s%s\n", colorPrefix, name, w.Line, w.Col, w.Text, colorSuffix)
				}
			}

			cmd.OutOrStdout().Write(out)
			return nil
		},
	}

	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	cmd.Version = getVersion()
	cmd.SetVersionTemplate("bait {{.Version}}\n")

	cmd.CompletionOptions.DisableDefaultCmd = true
	cmd.SetHelpCommand(&cobra.Command{Hidden: true})

	flags := cmd.Flags()
	flags.BoolVarP(&quiet, "quiet", "q", envQuiet(), "suppress translation warnings on stderr")
	flags.BoolVar(&noHelpers, "no-helpers", envNoHelpers(), "do not inject runtime helper functions")

	cmd.AddCommand(newHelperCmd())

	return cmd
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cmd := newRootCmd(stdin, stdout, stderr)
	if args == nil {
		args = []string{}
	}
	cmd.SetArgs(args)

	if err := cmd.Execute(); err != nil {
		var exitErr exitError
		if errors.As(err, &exitErr) {
			return exitErr.code
		}
		return 2
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

const (
	colorYellow = "\033[33m"
	colorReset  = "\033[0m"
)

func isColorEnabled() bool {
	val, ok := os.LookupEnv("NO_COLOR")
	return !ok || val == ""
}
