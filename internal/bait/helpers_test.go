package bait

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadFlags pins the read-flag rewrite: fish's read builtin has no
// -r option and would treat it as a variable name.
func TestReadFlags(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"read raw flag dropped",
			"while read -r line; do echo $line; done < f.txt\n",
			"while read line\n    echo $line\nend < f.txt\n",
		},
		{
			"other read flags kept",
			"read -n 1 -r ch < key\n",
			"read -n 1 ch < key\n",
		},
		{
			"grep -r untouched",
			"grep -r pattern dir\n",
			"grep -r pattern dir\n",
		},
		{
			"read underscore variable sanitized",
			"echo a b c | read -r _ v _\n",
			"echo a b c | read _unused v _unused\n",
		},
		{
			"read reserved variables mangled",
			"read -r status version code\n",
			"read _status _version code\n",
		},
		{
			"read with prompt preserved and variable mangled",
			"read -p \"status: \" status\n",
			"read -p \"status: \" _status\n",
		},
		{
			"read with fd preserved and variable mangled",
			"read -u 3 status\n",
			"read -u 3 _status\n",
		},
		{
			"read -r with bare prompt preserved and variable mangled",
			"read -r -p prompt status\n",
			"read -p prompt _status\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := Translate([]byte(tc.in))
			if err != nil {
				t.Fatalf("Translate(%q) error: %v", tc.in, err)
			}
			for _, w := range warnings {
				t.Errorf("unexpected warning: %v", w)
			}
			if string(got) != tc.want {
				t.Errorf("mismatch\n in:   %q\n got:  %q\n want: %q",
					tc.in, got, tc.want)
			}
		})
	}
}

// TestCmdSubstFish pins command substitutions whose bodies contain
// structural constructs (if, subshells, combiners over compounds):
// their inner statements are translated into valid fish.
func TestCmdSubstFish(t *testing.T) {
	tests := []struct {
		name         string
		in           string
		want         string
		wantWarnings int
	}{
		{
			"simple command substitution unchanged",
			"x=$(pwd)\n",
			"set x $(pwd)\n",
			0,
		},
		{
			"subshell inside substitution becomes begin block",
			"x=$( (echo hi); )\n",
			"set x $({\n    echo hi\n})\n",
			1,
		},
		{
			"if inside substitution",
			"x=$(if true; then echo yes; fi)\n",
			"set x $(if true\n    echo yes\nend)\n",
			0,
		},
		{
			"pipe into subshell in condition position",
			"if [ \"$(echo a | (cat))\" = a ]; then echo match; fi\n",
			"if [ \"$(echo a | {\n    cat\n})\" = a ]\n    echo match\nend\n",
			1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := Translate([]byte(tc.in))
			if err != nil {
				t.Fatalf("Translate(%q) error: %v", tc.in, err)
			}
			if len(warnings) != tc.wantWarnings {
				t.Errorf("warning count mismatch: got %d, want %d (%v)",
					len(warnings), tc.wantWarnings, warnings)
			}
			if string(got) != tc.want {
				t.Errorf("mismatch\n in: %q\n got: %q\n want: %q",
					tc.in, got, tc.want)
			}
		})
	}
}

func TestHeredocAndHerestring(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"simple herestring",
			"cat <<< \"hello\"\n",
			"printf '%s\\n' \"hello\" | cat\n",
		},
		{
			"herestring with variable",
			"cat <<< \"$FOO\"\n",
			"printf '%s\\n' \"$FOO\" | cat\n",
		},
		{
			"herestring unquoted",
			"cat <<< hello\n",
			"printf '%s\\n' hello | cat\n",
		},
		{
			"quoted heredoc",
			"cat <<'EOF'\nline 1 $NOEXPAND\nEOF\n",
			"printf '%s\\n' 'line 1 $NOEXPAND' | cat\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := Translate([]byte(tc.in))
			if err != nil {
				t.Fatalf("Translate(%q) error: %v", tc.in, err)
			}
			if len(warnings) > 0 {
				t.Errorf("unexpected warnings: %v", warnings)
			}
			if string(got) != tc.want {
				t.Errorf("mismatch\n in: %q\n got: %q\n want: %q",
					tc.in, got, tc.want)
			}
		})
	}
}

func TestProcSubst(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"diff with two input process substitutions",
			"diff -u <(sort a.txt) <(sort b.txt)\n",
			"diff -u (sort a.txt | psub) (sort b.txt | psub)\n",
		},
		{
			"source from process substitution",
			"source <(curl -fsSL https://example.com/install.sh)\n",
			baitSourceHelper + "\nsource (curl -fsSL https://example.com/install.sh | psub)\n",
		},
		{
			"process substitution with variable expansion",
			"cat <(echo \"$MSG\")\n",
			"cat (echo \"$MSG\" | psub)\n",
		},
		{
			"while loop reading from process substitution",
			"while read -r line; do echo \"$line\"; done < <(find . -name \"*.go\")\n",
			"while read line\n    echo \"$line\"\nend < (find . -name \"*.go\" | psub)\n",
		},
		{
			"multiline process substitution",
			"cat <(\n  echo a\n  echo b\n)\n",
			"cat ({\n    echo a\n    echo b\n} | psub)\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := Translate([]byte(tc.in))
			if err != nil {
				t.Fatalf("Translate(%q) error: %v", tc.in, err)
			}
			if len(warnings) > 0 {
				t.Errorf("unexpected warnings: %v", warnings)
			}
			if string(got) != tc.want {
				t.Errorf("mismatch\n in: %q\n got: %q\n want: %q",
					tc.in, got, tc.want)
			}
		})
	}
}

func TestRustupSupportFeatures(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		want     string
		contains string
	}{
		{
			name: "literal dollar escaping",
			in:   "echo 404$\n",
			want: "echo 404\\$\n",
		},
		{
			name: "function locals remain function scoped",
			in: "main() {\n" +
				"    local need_tty=yes\n" +
				"    echo $need_tty\n" +
				"}\n",
			want: "function main\n" +
				"    set --function need_tty yes\n" +
				"    echo $need_tty\n" +
				"end\n",
		},
		{
			name: "helper function locals remain function scoped",
			in: "helper() {\n" +
				"    local val=123\n" +
				"    echo $val\n" +
				"}\n",
			want: "function helper\n" +
				"    set --function val 123\n" +
				"    echo $val\n" +
				"end\n",
		},
		{
			name: "path concatenation does not split into list",
			in:   "_file=\"$_dir/rustup-init$_ext\"\n",
			want: "set _file \"$_dir/rustup-init$_ext\"\n",
		},
		{
			name: "options variable passes through without helper injection",
			in:   "curl $_retry $url\n",
			want: "curl $_retry $url\n",
		},
		{
			name:     "word split applied to for loop command substitution",
			in:       "for x in $(get_items); do echo \"$x\"; done\n",
			contains: "for x in (__bait_words $(get_items))",
		},
		{
			name: "getopts helper emitted",
			in: "while getopts :hqy opt \"$arg\"; do\n" +
				"    echo $opt\n" +
				"done\n",
			contains: "function getopts",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := Translate([]byte(tc.in))
			if err != nil {
				t.Fatalf("Translate(%q) error: %v", tc.in, err)
			}
			if tc.want != "" && string(got) != tc.want {
				t.Errorf("mismatch\n in: %q\n got: %q\n want: %q", tc.in, got, tc.want)
			}
			if tc.contains != "" && !strings.Contains(string(got), tc.contains) {
				t.Errorf("expected output to contain %q, got: %q", tc.contains, string(got))
			}
		})
	}
}

func TestSubEmitterLocalsIsolation(t *testing.T) {
	e := newEmitter()
	e.inFunction = true
	e.funcLocals = map[string]bool{"parent_var": true}

	sub := e.newSubEmitter()
	sub.funcLocals["child_var"] = true

	if e.funcLocals["child_var"] {
		t.Errorf("parent funcLocals was polluted by sub-emitter mutation")
	}
	if !sub.funcLocals["parent_var"] {
		t.Errorf("sub-emitter did not inherit parent funcLocals")
	}
}

func TestUnaliasHelperEmitted(t *testing.T) {
	in := `unalias foo 2>/dev/null || true`
	out, warnings, err := Translate([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if !strings.Contains(string(out), "function unalias") {
		t.Errorf("expected unalias helper to be emitted, got: %s", string(out))
	}
}

func TestUnsetHelperEmitted(t *testing.T) {
	in := `unset foo`
	out, warnings, err := Translate([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if !strings.Contains(string(out), "function unset") {
		t.Errorf("expected unset helper to be emitted, got: %s", string(out))
	}
}

func TestSourceHelperEmitted(t *testing.T) {
	in := `source ./sub.sh`
	out, warnings, err := Translate([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	outStr := string(out)
	if !strings.Contains(outStr, "function source") {
		t.Errorf("expected source helper to be emitted, got: %s", outStr)
	}
	if strings.Contains(outStr, "function .") {
		t.Errorf("did not expect dot helper to be emitted when only source is used, got: %s", outStr)
	}
}

func TestDotHelperEmitted(t *testing.T) {
	in := `. ./sub.sh`
	out, warnings, err := Translate([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	outStr := string(out)
	if !strings.Contains(outStr, "function source") {
		t.Errorf("expected source helper to be emitted for dot command, got: %s", outStr)
	}
	if !strings.Contains(outStr, "function .") {
		t.Errorf("expected dot helper to be emitted, got: %s", outStr)
	}
	srcIdx := strings.Index(outStr, "function source")
	dotIdx := strings.Index(outStr, "function .")
	if srcIdx > dotIdx {
		t.Errorf("expected function source to be defined before function ., got srcIdx=%d, dotIdx=%d", srcIdx, dotIdx)
	}
}

func TestTranslateWithOptionsNoHelpers(t *testing.T) {
	in := "source ./sub.sh\nhash ls\nwhile getopts :a opt \"$arg\"; do echo $opt; done\n"
	outWithHelpers, _, err := Translate([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(outWithHelpers), "function source") {
		t.Errorf("expected function source to be emitted by default")
	}

	outNoHelpers, _, err := TranslateWithOptions([]byte(in), Options{NoHelpers: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	outStr := string(outNoHelpers)
	for _, helperFunc := range []string{"function source", "function .", "function hash", "function getopts", "function unset", "function __bait_eval"} {
		if strings.Contains(outStr, helperFunc) {
			t.Errorf("did not expect %q to be emitted with NoHelpers=true, got: %s", helperFunc, outStr)
		}
	}
	if !strings.Contains(outStr, "source ./sub.sh") {
		t.Errorf("expected source ./sub.sh in output, got: %s", outStr)
	}
}

func TestHelperAPI(t *testing.T) {
	tests := []struct {
		name     string
		contains string
	}{
		{"source", "function source"},
		{".", "function ."},
		{"getopts", "function getopts"},
		{"hash", "function hash"},
		{"unalias", "function unalias"},
		{"__bait_words", "function __bait_words"},
		{"__bait_exec", "function __bait_exec"},
		{"unset", "function unset"},
		{"__bait_ostype", "function __bait_ostype"},
		{"__bait_eval", "function __bait_eval"},
		{"eval", "function __bait_eval"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			content, err := Helper(tc.name)
			if err != nil {
				t.Fatalf("Helper(%q) returned unexpected error: %v", tc.name, err)
			}
			if !strings.Contains(content, tc.contains) {
				t.Errorf("Helper(%q) does not contain %q; got: %s", tc.name, tc.contains, content)
			}
		})
	}

	for _, invalid := range []string{"source.fish", "..fish", "getopts.fish", "words", "exec", "nonexistent"} {
		if _, err := Helper(invalid); err == nil {
			t.Errorf("expected error for Helper(%q), got nil", invalid)
		}
	}

	helpers := Helpers()
	if len(helpers) == 0 {
		t.Errorf("expected non-empty Helpers list")
	}
}

func TestBaitExecDoesNotEvalMetacharacters(t *testing.T) {
	if _, err := exec.LookPath("fish"); err != nil {
		t.Skip("fish not installed")
	}
	dir := t.TempDir()
	outFile := filepath.Join(dir, "should_not_exist")
	fishScript := fmt.Sprintf("%s\n__bait_exec \"printf [%%s] a | b > %s\"\n", baitExecHelper, outFile)

	cmd := exec.Command("fish", "-c", fishScript)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fish failed: %v, output: %s", err, string(out))
	}
	if _, err := os.Stat(outFile); !os.IsNotExist(err) {
		t.Errorf("file %s should not have been created by redirection", outFile)
	}
	outStr := string(out)
	if !strings.Contains(outStr, "[a]") || !strings.Contains(outStr, "[|]") || !strings.Contains(outStr, "[>]") {
		t.Errorf("expected metacharacters to be passed as literal arguments, got: %s", outStr)
	}
}

func TestEvalHelperEmitted(t *testing.T) {
	in := "eval \"echo hello\"\n"
	got, warnings, err := Translate([]byte(in))
	if err != nil {
		t.Fatalf("Translate failed: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	gotStr := string(got)
	if !strings.Contains(gotStr, "function __bait_eval") {
		t.Errorf("expected __bait_eval helper to be emitted, got: %s", gotStr)
	}
	if !strings.Contains(gotStr, "__bait_eval \"echo hello\"") {
		t.Errorf("expected '__bait_eval \"echo hello\"' in output, got: %s", gotStr)
	}
}

func TestBaitEvalHelperExecution(t *testing.T) {
	if _, err := exec.LookPath("fish"); err != nil {
		t.Skip("fish not installed")
	}
	t.Run("missing_bait_returns_127", func(t *testing.T) {
		fishScript := fmt.Sprintf("%s\n__bait_eval \"echo 1\"\n", baitEvalHelper)
		cmd := exec.Command("fish", "-c", fishScript)
		cmd.Env = []string{"PATH=/nonexistent"}
		out, err := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.ExitCode() != 127 {
				t.Errorf("expected exit code 127, got %d", exitErr.ExitCode())
			}
		} else {
			t.Errorf("expected exit code 127, got %v (output: %s)", err, string(out))
		}
		if !strings.Contains(string(out), "eval: 'bait' is required") {
			t.Errorf("expected missing bait error message, got: %s", string(out))
		}
	})

	t.Run("empty_arguments_is_noop", func(t *testing.T) {
		fishScript := fmt.Sprintf("%s\n__bait_eval\n", baitEvalHelper)
		cmd := exec.Command("fish", "-c", fishScript)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("expected 0 exit status, got %v (output: %s)", err, string(out))
		}
	})

	t.Run("evaluates_translated_code_with_mock_bait", func(t *testing.T) {
		tmpDir := t.TempDir()
		mockBait := filepath.Join(tmpDir, "bait")
		script := "#!/bin/sh\nread -r line\ncase \"$line\" in\n  \"FOO=bar\") echo 'set FOO bar' ;;\n  *) echo \"$line\" ;;\nesac\n"
		if err := os.WriteFile(mockBait, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		fishScript := fmt.Sprintf("%s\n__bait_eval \"FOO=bar\"\necho \"FOO:$FOO\"\n", baitEvalHelper)
		cmd := exec.Command("fish", "-c", fishScript)
		cmd.Env = []string{"PATH=" + tmpDir + ":" + os.Getenv("PATH")}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fish execution failed: %v (output: %s)", err, string(out))
		}
		if !strings.Contains(string(out), "FOO:bar") {
			t.Errorf("expected FOO:bar in output, got: %s", string(out))
		}
	})
}

func TestEvalPassthroughWithWarningWhenNoHelpers(t *testing.T) {
	src := "eval \"echo hello\"\n"
	got, warnings, err := TranslateWithOptions([]byte(src), Options{NoHelpers: true})
	if err != nil {
		t.Fatalf("TranslateWithOptions failed: %v", err)
	}
	if string(got) != src {
		t.Errorf("expected verbatim output %q, got %q", src, string(got))
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	w := warnings[0]
	if w.Line != 1 || w.Col != 1 {
		t.Errorf("expected position 1:1, got %d:%d", w.Line, w.Col)
	}
	if !strings.Contains(w.Text, "eval executes fish syntax") {
		t.Errorf("expected warning text to mention eval executes fish syntax, got: %s", w.Text)
	}
}

func TestEvalNoHelpersWarnings(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"eval in condition", "if eval \"$cmd\"; then echo ok; fi\n"},
		{"eval in command substitution", "x=$(eval \"$cmd\")\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := TranslateWithOptions([]byte(tc.src), Options{NoHelpers: true})
			if err != nil {
				t.Fatalf("TranslateWithOptions(%q) error: %v", tc.src, err)
			}
			if len(warnings) == 0 {
				t.Fatalf("TranslateWithOptions(%q) produced no warnings, want one containing 'eval executes fish syntax' (output: %q)", tc.src, got)
			}
			found := false
			for _, w := range warnings {
				if strings.Contains(w.Text, "eval executes fish syntax") {
					found = true
				}
			}
			if !found {
				t.Errorf("expected warning to contain 'eval executes fish syntax', got: %v", warnings)
			}
		})
	}
}

func TestLeadingRedirection(t *testing.T) {
	src := ">&2 echo hello\n"
	got, _, err := Translate([]byte(src))
	if err != nil {
		t.Fatalf("Translate failed: %v", err)
	}
	want := "echo hello >& 2\n"
	if string(got) != want {
		t.Errorf("expected %q, got %q", want, string(got))
	}
}

func TestCompoundHeredoc(t *testing.T) {
	src := "while IFS= read -r line; do\n" +
		"    unpaired_line=\"$line\"\n" +
		"done <<EOF\n" +
		"test-content\n" +
		"EOF\n"
	got, _, err := Translate([]byte(src))
	if err != nil {
		t.Fatalf("Translate failed: %v", err)
	}
	if !strings.Contains(string(got), "printf '%s\\n' \"test-content\" | while IFS= read line") {
		t.Errorf("expected pipeline into while read, got:\n%s", string(got))
	}
	if !strings.Contains(string(got), "set unpaired_line $line") {
		t.Errorf("expected assignment translated to set, got:\n%s", string(got))
	}
}

func TestHighFDRedirectionInSubshell(t *testing.T) {
	t.Run("paired_block_redirection_eliminated_and_mapped_to_stderr", func(t *testing.T) {
		src := "{ provided_version=\"$(nvm_rc_version 3>&1 1>&4)\"; } 4>&1\n"
		got, warnings, err := Translate([]byte(src))
		if err != nil {
			t.Fatalf("Translate failed: %v", err)
		}
		if len(warnings) != 0 {
			t.Errorf("expected 0 warnings for paired high-FD redirection, got %d: %v", len(warnings), warnings)
		}
		gotStr := string(got)
		if strings.Contains(gotStr, "4>&") || strings.Contains(gotStr, "1>&4") {
			t.Errorf("output should not contain uninherited FD 4 in fish, got:\n%s", gotStr)
		}
		if !strings.Contains(gotStr, "1>& 2") && !strings.Contains(gotStr, "1>&2") {
			t.Errorf("expected 1>&4 to be mapped to stderr in subshell, got:\n%s", gotStr)
		}
	})

	t.Run("paired_binary_chain_redirection", func(t *testing.T) {
		src := "{ NVM_RC_VERSION=\"$(NVM_SILENT=\"${NVM_SILENT:-0}\" nvm_rc_version 3>&1 1>&4)\"; } 4>&1 && has_checked_nvmrc=1\n"
		got, warnings, err := Translate([]byte(src))
		if err != nil {
			t.Fatalf("Translate failed: %v", err)
		}
		if len(warnings) != 0 {
			t.Errorf("expected 0 warnings for paired high-FD redirection, got %d: %v", len(warnings), warnings)
		}
		gotStr := string(got)
		if strings.Contains(gotStr, "4>&") {
			t.Errorf("output should not contain redundant 4>&1 on block, got:\n%s", gotStr)
		}
		if !strings.Contains(gotStr, "has_checked_nvmrc 1") {
			t.Errorf("expected binary chain operand in output, got:\n%s", gotStr)
		}
	})

	t.Run("unpaired_high_fd_still_warns", func(t *testing.T) {
		src := "{ echo hi; } 4>&1\n"
		_, warnings, err := Translate([]byte(src))
		if err != nil {
			t.Fatalf("Translate failed: %v", err)
		}
		if len(warnings) == 0 {
			t.Errorf("expected warning for unpaired high-FD redirection on block")
		}
	})
}

func TestLeadingRedirectionWithDynamicCommand(t *testing.T) {
	src := ">&2 $cmd hello\n"
	got, _, err := Translate([]byte(src))
	if err != nil {
		t.Fatalf("Translate failed: %v", err)
	}
	gotStr := string(got)
	if strings.Contains(gotStr, ">&2 $cmd") || strings.Contains(gotStr, ">& 2 $cmd") {
		t.Errorf("leading redirection was not normalized to tail, got:\n%s", gotStr)
	}
	if !strings.Contains(gotStr, "__bait_exec $cmd hello >& 2") {
		t.Errorf("expected '__bait_exec $cmd hello >& 2', got:\n%s", gotStr)
	}
}

func TestDynamicExecDoesNotEmitBaitWords(t *testing.T) {
	src := "cmd=\"echo hi\"\n$cmd\n"
	got, _, err := Translate([]byte(src))
	if err != nil {
		t.Fatalf("Translate failed: %v", err)
	}
	gotStr := string(got)
	if !strings.Contains(gotStr, "function __bait_exec") {
		t.Errorf("expected __bait_exec helper, got:\n%s", gotStr)
	}
	if strings.Contains(gotStr, "__bait_words") {
		t.Errorf("expected no __bait_words helper emitted, got:\n%s", gotStr)
	}
}

func TestDynamicExecInlineDoesNotEmitBaitWords(t *testing.T) {
	src := "true && $cmd foo\n"
	got, _, err := Translate([]byte(src))
	if err != nil {
		t.Fatalf("Translate failed: %v", err)
	}
	gotStr := string(got)
	if !strings.Contains(gotStr, "__bait_exec") {
		t.Errorf("expected __bait_exec, got:\n%s", gotStr)
	}
	if strings.Contains(gotStr, "__bait_words") {
		t.Errorf("expected no __bait_words helper emitted, got:\n%s", gotStr)
	}
}

func TestBaitExecCommandAndBuiltin(t *testing.T) {
	if _, err := exec.LookPath("fish"); err != nil {
		t.Skip("fish not installed")
	}

	t.Run("dispatches_command_prefix", func(t *testing.T) {
		fishScript := fmt.Sprintf("%s\n__bait_exec \"command echo\" \"hello from command\"\n", baitExecHelper)
		cmd := exec.Command("fish", "-c", fishScript)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fish failed: %v, output: %s", err, string(out))
		}
		if strings.TrimSpace(string(out)) != "hello from command" {
			t.Errorf("expected 'hello from command', got: %q", string(out))
		}
	})

	t.Run("dispatches_builtin_prefix", func(t *testing.T) {
		fishScript := fmt.Sprintf("%s\n__bait_exec \"builtin echo\" \"hello from builtin\"\n", baitExecHelper)
		cmd := exec.Command("fish", "-c", fishScript)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fish failed: %v, output: %s", err, string(out))
		}
		if strings.TrimSpace(string(out)) != "hello from builtin" {
			t.Errorf("expected 'hello from builtin', got: %q", string(out))
		}
	})
}

func TestBackslashCommand(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "simple backslash command",
			in:   `\cd "$@"`,
			want: "cd $argv\n",
		},
		{
			name: "backslash alias",
			in:   `\alias foo`,
			want: "alias foo\n",
		},
		{
			name: "backslash pwd",
			in:   `\pwd`,
			want: "pwd\n",
		},
		{
			name: "command builtin with backslash",
			in:   `\command -v foo`,
			want: "command -v foo\n",
		},
		{
			name: "builtin keyword with backslash",
			in:   `\builtin echo hi`,
			want: "builtin echo hi\n",
		},
		{
			name: "arguments with backslash untouched",
			in:   `echo \foo \bar`,
			want: "echo \\foo \\bar\n",
		},
		{
			name: "and-or chain with backslash command",
			in:   `mkdir -p dir && \cd dir`,
			want: "mkdir -p dir && cd dir\n",
		},
		{
			name: "negated backslash command",
			in:   `! \which node`,
			want: "! which node\n",
		},
		{
			name: "command substitution with backslash command",
			in:   `DIR="$(nvm_cd && \pwd)"`,
			want: "set DIR \"$(nvm_cd && pwd)\"\n",
		},
		{
			name: "double backslash preserved",
			in:   `\\cd foo`,
			want: "\\\\cd foo\n",
		},
		{
			name: "escaped wildcards and dollars preserved",
			in:   `echo \* \$foo \ bar`,
			want: "echo \\* \\$foo \\ bar\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := Translate([]byte(tc.in))
			if err != nil {
				t.Fatalf("Translate(%q) error: %v", tc.in, err)
			}
			if string(got) != tc.want {
				t.Errorf("mismatch\n in:   %q\n got:  %q\n want: %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDoubleQuotedBackticks(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "simple double quoted backtick",
			in:   "echo \"try \\`foo\\`\"",
			want: "echo \"try `foo`\"\n",
		},
		{
			name: "double quoted backtick with variable",
			in:   "echo \"try \\`${REMOTE_CMD}\\` to browse\"",
			want: "echo \"try `$REMOTE_CMD` to browse\"\n",
		},
		{
			name: "escaped backslash before backtick",
			in:   "echo \"try \\\\\\`foo\\\\\\`\"",
			want: "echo \"try \\\\`foo\\\\`\"\n",
		},
		{
			name: "active backtick command substitution inside double quotes",
			in:   "echo \"try `echo foo`\"",
			want: "echo \"try $(echo foo)\"\n",
		},
		{
			name: "assignment with double quoted backtick",
			in:   "MSG=\"try \\`foo\\`\"",
			want: "set MSG \"try `foo`\"\n",
		},
		{
			name: "heredoc with escaped backtick",
			in:   "cat <<EOF\ntry \\`foo\\`\nEOF\n",
			want: "printf '%s\\n' \"try `foo`\" | cat\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := Translate([]byte(tc.in))
			if err != nil {
				t.Fatalf("Translate(%q) error: %v", tc.in, err)
			}
			if string(got) != tc.want {
				t.Errorf("mismatch\n in:   %q\n got:  %q\n want: %q", tc.in, got, tc.want)
			}
		})
	}
}
