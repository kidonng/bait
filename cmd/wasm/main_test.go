package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestTranslateStdin(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{}, strings.NewReader("echo hello\n"), &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if got := stdout.String(); got != "echo hello\n" {
		t.Errorf("expected 'echo hello\\n', got %q", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("expected empty stderr, got %q", stderr.String())
	}
}

func TestTranslateSyntaxError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{}, strings.NewReader("if; then\n"), &stdout, &stderr)

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "<stdin>:") {
		t.Errorf("expected stderr to contain '<stdin>:', got %q", stderr.String())
	}
}

func TestTranslateWarnings(t *testing.T) {
	var stdout, stderr bytes.Buffer
	// ${!foo} produces an unsupported warning for indirect expansion
	exitCode := run([]string{}, strings.NewReader("echo ${!foo}\n"), &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "<stdin>:1:") {
		t.Errorf("expected stderr warning, got %q", stderr.String())
	}
}

func TestQuietFlag(t *testing.T) {
	for _, flag := range []string{"-q", "--quiet"} {
		var stdout, stderr bytes.Buffer
		exitCode := run([]string{flag}, strings.NewReader("echo ${!foo}\n"), &stdout, &stderr)

		if exitCode != 0 {
			t.Fatalf("expected exit code 0, got %d", exitCode)
		}
		if stderr.Len() != 0 {
			t.Errorf("expected quiet stderr for %s, got %q", flag, stderr.String())
		}
	}
}

func TestQuietEnv(t *testing.T) {
	t.Setenv("BAIT_QUIET", "true")
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{}, strings.NewReader("echo ${!foo}\n"), &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if stderr.Len() != 0 {
		t.Errorf("expected quiet stderr with BAIT_QUIET=true, got %q", stderr.String())
	}
}

func TestNoShimsFlag(t *testing.T) {
	// unset requires unset.fish shim unless --no-shims is passed
	var stdoutWithout, stderrWithout bytes.Buffer
	run([]string{"--no-shims"}, strings.NewReader("unset foo\n"), &stdoutWithout, &stderrWithout)

	var stdoutWith, stderrWith bytes.Buffer
	run([]string{}, strings.NewReader("unset foo\n"), &stdoutWith, &stderrWith)

	if !strings.Contains(stdoutWith.String(), "function unset") {
		t.Errorf("expected default to inject unset shim, got %q", stdoutWith.String())
	}
	if strings.Contains(stdoutWithout.String(), "function unset") {
		t.Errorf("expected --no-shims not to inject unset shim, got %q", stdoutWithout.String())
	}
}

func TestNoShimsEnv(t *testing.T) {
	t.Setenv("BAIT_NO_SHIMS", "1")
	var stdout, stderr bytes.Buffer
	run([]string{}, strings.NewReader("unset foo\n"), &stdout, &stderr)

	if strings.Contains(stdout.String(), "function unset") {
		t.Errorf("expected BAIT_NO_SHIMS=1 not to inject unset shim, got %q", stdout.String())
	}
}
