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

	t.Run("with quiet", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		exitCode := run([]string{"--quiet"}, strings.NewReader(scriptWithWarning), &stdout, &stderr)
		if exitCode != 0 {
			t.Fatalf("expected exit code 0, got %d", exitCode)
		}
		if stderr.Len() != 0 {
			t.Errorf("expected empty stderr with --quiet, got %q", stderr.String())
		}
	})
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
}

func TestHelperCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"helper", "source"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %s)", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "function source") {
		t.Errorf("expected stdout to contain 'function source', got: %s", stdout.String())
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

func TestHelperList(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"helper", "list"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %s)", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("expected empty stderr, got %q", stderr.String())
	}
	output := stdout.String()
	for _, expected := range []string{"source", "getopts", "__bait_eval"} {
		if !strings.Contains(output, expected) {
			t.Errorf("expected output to contain %q, got: %s", expected, output)
		}
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
