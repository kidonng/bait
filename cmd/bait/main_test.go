package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetVersion(t *testing.T) {
	oldVer := version
	defer func() { version = oldVer }()

	version = "1.2.3"
	if got := getVersion(); got != "1.2.3" {
		t.Errorf("expected getVersion() = '1.2.3', got %q", got)
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

	for _, flag := range []string{"--quiet", "-q"} {
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
		{"unset", []string{"helper", "unset"}, "function unset"},
		{"words", []string{"helper", "__bait_words"}, "function __bait_words"},
		{"exec", []string{"helper", "__bait_exec"}, "function __bait_exec"},
		{"ostype", []string{"helper", "__bait_ostype"}, "function __bait_ostype"},
		{"eval", []string{"helper", "__bait_eval"}, "function __bait_eval"},
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
	if !strings.Contains(stderr.String(), "missing helper name") {
		t.Errorf("expected stderr to contain 'missing helper name', got: %q", stderr.String())
	}
}

func TestHelperTooManyArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"helper", "source", "extra"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 2 {
		t.Fatalf("expected exit code 2, got %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "too many arguments for helper command") {
		t.Errorf("expected stderr to contain 'too many arguments for helper command', got: %q", stderr.String())
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

func TestHelperNamesTooManyArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"helper", "--names", "extra"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 2 {
		t.Fatalf("expected exit code 2, got %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "too many arguments for --names") {
		t.Errorf("expected stderr to contain 'too many arguments for --names', got: %q", stderr.String())
	}
}

func TestWarningColor(t *testing.T) {
	scriptWithWarning := "set -e\necho ok\n"

	t.Run("colored by default", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		var stdout, stderr bytes.Buffer
		exitCode := run([]string{}, strings.NewReader(scriptWithWarning), &stdout, &stderr)
		if exitCode != 0 {
			t.Fatalf("expected exit code 0, got %d", exitCode)
		}
		got := stderr.String()
		if !strings.Contains(got, "\033[33m") || !strings.Contains(got, "\033[0m") {
			t.Errorf("expected ANSI color codes in stderr, got %q", got)
		}
	})

	t.Run("NO_COLOR disables color", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		var stdout, stderr bytes.Buffer
		exitCode := run([]string{}, strings.NewReader(scriptWithWarning), &stdout, &stderr)
		if exitCode != 0 {
			t.Fatalf("expected exit code 0, got %d", exitCode)
		}
		got := stderr.String()
		if strings.Contains(got, "\033[") {
			t.Errorf("expected NO_COLOR to disable color, got %q", got)
		}
	})

	t.Run("empty NO_COLOR does not disable color", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		var stdout, stderr bytes.Buffer
		exitCode := run([]string{}, strings.NewReader(scriptWithWarning), &stdout, &stderr)
		if exitCode != 0 {
			t.Fatalf("expected exit code 0, got %d", exitCode)
		}
		got := stderr.String()
		if !strings.Contains(got, "\033[33m") {
			t.Errorf("expected empty NO_COLOR to allow color, got %q", got)
		}
	})
}
