package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-h", "-help"} {
		t.Run(flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exitCode := run([]string{flag}, strings.NewReader(""), &stdout, &stderr)

			if exitCode != 0 {
				t.Fatalf("expected exit code 0, got %d", exitCode)
			}
			if !strings.Contains(stdout.String(), "Usage:") {
				t.Errorf("expected stdout to contain 'Usage:', got %q", stdout.String())
			}
			if !strings.Contains(stdout.String(), "--help") {
				t.Errorf("expected stdout to contain '--help', got %q", stdout.String())
			}
			if !strings.Contains(stdout.String(), "--version") {
				t.Errorf("expected stdout to contain '--version', got %q", stdout.String())
			}
			if !strings.Contains(stdout.String(), "BAIT_QUIET") {
				t.Errorf("expected stdout to contain 'BAIT_QUIET', got %q", stdout.String())
			}
			if !strings.Contains(stdout.String(), "--no-helpers") {
				t.Errorf("expected stdout to contain '--no-helpers', got %q", stdout.String())
			}
			if !strings.Contains(stdout.String(), "BAIT_NO_HELPERS") {
				t.Errorf("expected stdout to contain 'BAIT_NO_HELPERS', got %q", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Errorf("expected empty stderr, got %q", stderr.String())
			}
		})
	}
}

func TestVersion(t *testing.T) {
	for _, flag := range []string{"--version", "-v", "-version"} {
		t.Run(flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exitCode := run([]string{flag}, strings.NewReader(""), &stdout, &stderr)

			if exitCode != 0 {
				t.Fatalf("expected exit code 0, got %d", exitCode)
			}
			out := stdout.String()
			if !strings.HasPrefix(out, "bait ") {
				t.Errorf("expected stdout to start with 'bait ', got %q", out)
			}
			if stderr.Len() != 0 {
				t.Errorf("expected empty stderr, got %q", stderr.String())
			}
		})
	}

	t.Run("custom version", func(t *testing.T) {
		oldVer := version
		defer func() { version = oldVer }()

		version = "1.2.3"
		if got := getVersion(); got != "1.2.3" {
			t.Errorf("expected getVersion() = '1.2.3', got %q", got)
		}

		var stdout, stderr bytes.Buffer
		exitCode := run([]string{"--version"}, strings.NewReader(""), &stdout, &stderr)
		if exitCode != 0 {
			t.Fatalf("expected exit code 0, got %d", exitCode)
		}
		if got := stdout.String(); got != "bait 1.2.3\n" {
			t.Errorf("expected 'bait 1.2.3\\n', got %q", got)
		}
	})
}

func TestTooManyArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"file1.sh", "file2.sh"}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 2 {
		t.Fatalf("expected exit code 2, got %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "error: too many arguments") {
		t.Errorf("expected stderr to contain 'error: too many arguments', got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("expected stderr to contain usage, got %q", stderr.String())
	}
}

func TestUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"--unknown-flag"}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 2 {
		t.Fatalf("expected exit code 2, got %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Errorf("expected stderr to mention undefined flag, got %q", stderr.String())
	}
}

func TestTranslateStdin(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{}, strings.NewReader("echo hello\n"), &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if got := stdout.String(); got != "echo hello\n" {
		t.Errorf("expected 'echo hello\\n', got %q", got)
	}
}

func TestTranslateFile(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.sh")
	if err := os.WriteFile(filePath, []byte("echo file_test\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{filePath}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if got := stdout.String(); got != "echo file_test\n" {
		t.Errorf("expected 'echo file_test\\n', got %q", got)
	}
}

func TestTranslateFileNotFound(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"nonexistent_file_12345.sh"}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if stderr.Len() == 0 {
		t.Errorf("expected non-empty stderr on file not found")
	}
}

func TestQuietFlag(t *testing.T) {
	// bash construct with warning: high file descriptor redirection or unsupported syntax
	// e.g. "set -e\n" produces a warning
	scriptWithWarning := "set -e\necho ok\n"

	t.Run("without quiet", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		exitCode := run([]string{}, strings.NewReader(scriptWithWarning), &stdout, &stderr)
		if exitCode != 0 {
			t.Fatalf("expected exit code 0, got %d", exitCode)
		}
		if stderr.Len() == 0 {
			t.Errorf("expected warnings on stderr without quiet flag")
		}
	})

	for _, flag := range []string{"--quiet", "-quiet", "-q"} {
		t.Run("with "+flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exitCode := run([]string{flag}, strings.NewReader(scriptWithWarning), &stdout, &stderr)
			if exitCode != 0 {
				t.Fatalf("expected exit code 0, got %d", exitCode)
			}
			if stderr.Len() != 0 {
				t.Errorf("expected empty stderr with %s, got %q", flag, stderr.String())
			}
		})
	}
}

func TestQuietEnv(t *testing.T) {
	scriptWithWarning := "set -e\necho ok\n"

	truthyVals := []string{"1", "true", "TRUE", "yes", "YES", "on"}
	for _, val := range truthyVals {
		t.Run("truthy_"+val, func(t *testing.T) {
			t.Setenv("BAIT_QUIET", val)
			var stdout, stderr bytes.Buffer
			exitCode := run([]string{}, strings.NewReader(scriptWithWarning), &stdout, &stderr)
			if exitCode != 0 {
				t.Fatalf("expected exit code 0, got %d", exitCode)
			}
			if stderr.Len() != 0 {
				t.Errorf("expected empty stderr with BAIT_QUIET=%s, got %q", val, stderr.String())
			}
		})
	}

	falsyVals := []string{"0", "false", "FALSE", "no", "off", ""}
	for _, val := range falsyVals {
		t.Run("falsy_"+val, func(t *testing.T) {
			t.Setenv("BAIT_QUIET", val)
			var stdout, stderr bytes.Buffer
			exitCode := run([]string{}, strings.NewReader(scriptWithWarning), &stdout, &stderr)
			if exitCode != 0 {
				t.Fatalf("expected exit code 0, got %d", exitCode)
			}
			if stderr.Len() == 0 {
				t.Errorf("expected warnings on stderr with BAIT_QUIET=%s", val)
			}
		})
	}

	t.Run("cli flag overrides env", func(t *testing.T) {
		t.Setenv("BAIT_QUIET", "1")
		var stdout, stderr bytes.Buffer
		exitCode := run([]string{"--quiet=false"}, strings.NewReader(scriptWithWarning), &stdout, &stderr)
		if exitCode != 0 {
			t.Fatalf("expected exit code 0, got %d", exitCode)
		}
		if stderr.Len() == 0 {
			t.Errorf("expected warnings on stderr when --quiet=false overrides BAIT_QUIET=1")
		}
	})
}

func TestNoHelpersFlag(t *testing.T) {
	script := "source ./sub.sh\n"
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"--no-helpers"}, strings.NewReader(script), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if strings.Contains(stdout.String(), "function source") {
		t.Errorf("expected stdout not to contain 'function source', got: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "source ./sub.sh") {
		t.Errorf("expected stdout to contain 'source ./sub.sh', got: %s", stdout.String())
	}
}

func TestNoHelpersEnv(t *testing.T) {
	script := "source ./sub.sh\n"
	truthyVals := []string{"1", "true", "TRUE", "yes", "YES", "on"}
	for _, val := range truthyVals {
		t.Run("truthy_"+val, func(t *testing.T) {
			t.Setenv("BAIT_NO_HELPERS", val)
			var stdout, stderr bytes.Buffer
			exitCode := run([]string{}, strings.NewReader(script), &stdout, &stderr)
			if exitCode != 0 {
				t.Fatalf("expected exit code 0, got %d", exitCode)
			}
			if strings.Contains(stdout.String(), "function source") {
				t.Errorf("expected no helpers with BAIT_NO_HELPERS=%s, got: %s", val, stdout.String())
			}
		})
	}

	t.Run("cli flag overrides env", func(t *testing.T) {
		t.Setenv("BAIT_NO_HELPERS", "1")
		var stdout, stderr bytes.Buffer
		exitCode := run([]string{"--no-helpers=false"}, strings.NewReader(script), &stdout, &stderr)
		if exitCode != 0 {
			t.Fatalf("expected exit code 0, got %d", exitCode)
		}
		if !strings.Contains(stdout.String(), "function source") {
			t.Errorf("expected helpers when --no-helpers=false overrides BAIT_NO_HELPERS=1")
		}
	})
}

func TestHelperCommand(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		contains string
	}{
		{"source", []string{"helper", "source"}, "function source"},
		{"dot", []string{"helper", "."}, "function ."},
		{"getopts", []string{"helper", "getopts"}, "function getopts"},
		{"hash", []string{"helper", "hash"}, "function hash"},
		{"unalias", []string{"helper", "unalias"}, "function unalias"},
		{"words", []string{"helper", "__bait_words"}, "function __bait_words"},
		{"exec", []string{"helper", "__bait_exec"}, "function __bait_exec"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exitCode := run(tc.args, strings.NewReader(""), &stdout, &stderr)
			if exitCode != 0 {
				t.Fatalf("expected exit code 0, got %d (stderr: %s)", exitCode, stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.contains) {
				t.Errorf("expected stdout to contain %q, got: %s", tc.contains, stdout.String())
			}
		})
	}
}

func TestHelperMissingName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"helper"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 2 {
		t.Fatalf("expected exit code 2, got %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "error: missing helper name") {
		t.Errorf("expected stderr to contain 'error: missing helper name', got: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("expected stderr to contain usage, got: %q", stderr.String())
	}
}

func TestHelperTooManyArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"helper", "source", "extra"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 2 {
		t.Fatalf("expected exit code 2, got %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "error: too many arguments for helper command") {
		t.Errorf("expected stderr to contain 'error: too many arguments for helper command', got: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("expected stderr to contain usage, got: %q", stderr.String())
	}
}

func TestHelperUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"helper", "nonexistent_helper"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if !strings.Contains(stderr.String(), `error: unknown helper "nonexistent_helper"`) {
		t.Errorf("expected stderr to contain unknown helper error, got: %q", stderr.String())
	}
}

func TestHelperNames(t *testing.T) {
	for _, flag := range []string{"--names", "-names"} {
		t.Run(flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exitCode := run([]string{"helper", flag}, strings.NewReader(""), &stdout, &stderr)
			if exitCode != 0 {
				t.Fatalf("expected exit code 0, got %d (stderr: %s)", exitCode, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Errorf("expected empty stderr, got %q", stderr.String())
			}
			lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
			expected := []string{"source", ".", "getopts", "hash", "unalias", "__bait_words", "__bait_exec"}
			if len(lines) != len(expected) {
				t.Fatalf("expected %d names, got %d: %v", len(expected), len(lines), lines)
			}
			for i, exp := range expected {
				if lines[i] != exp {
					t.Errorf("line %d: expected %q, got %q", i, exp, lines[i])
				}
			}
		})
	}
}

func TestHelperNamesTooManyArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"helper", "--names", "extra"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 2 {
		t.Fatalf("expected exit code 2, got %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "error: too many arguments for --names") {
		t.Errorf("expected stderr to contain 'error: too many arguments for --names', got: %q", stderr.String())
	}
}

func TestHelperHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exitCode := run([]string{"helper", flag}, strings.NewReader(""), &stdout, &stderr)
			if exitCode != 0 {
				t.Fatalf("expected exit code 0, got %d", exitCode)
			}
			if !strings.Contains(stdout.String(), "Usage:") {
				t.Errorf("expected stdout to contain 'Usage:', got %q", stdout.String())
			}
			if !strings.Contains(stdout.String(), "helper <name>") {
				t.Errorf("expected stdout to contain 'helper <name>', got %q", stdout.String())
			}
			if !strings.Contains(stdout.String(), "--names") {
				t.Errorf("expected stdout to contain '--names', got %q", stdout.String())
			}
			if !strings.Contains(stdout.String(), "Available helpers:") {
				t.Errorf("expected stdout to contain 'Available helpers:', got %q", stdout.String())
			}
			for _, h := range []string{"source", ".", "getopts", "hash", "unalias", "__bait_words", "__bait_exec"} {
				if !strings.Contains(stdout.String(), h) {
					t.Errorf("expected stdout to contain helper %q, got %q", h, stdout.String())
				}
			}
		})
	}
}
