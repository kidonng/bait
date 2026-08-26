package bait

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPassthrough pins down tier 0 of the translation policy: constructs
// that modern fish accepts natively must survive translation unchanged
// (modulo printer whitespace normalization).
func TestPassthrough(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty script", "", ""},
		{"simple command", "echo hello world", "echo hello world\n"},
		{
			"quoting",
			"echo \"two words\" 'single $quoted'",
			"echo \"two words\" 'single $quoted'\n",
		},
		{"pipe", "cat foo.txt | head -n 5", "cat foo.txt | head -n 5\n"},
		{
			"merge stderr into pipe",
			"make 2>&1 | less",
			"make 2>&1 | less\n",
		},
		{
			"and-or chain",
			"mkdir -p foo && cd foo || exit 1",
			"mkdir -p foo && cd foo || exit 1\n",
		},
		{
			"command substitution",
			"echo $(pwd)",
			"echo $(pwd)\n",
		},
		{
			"env override",
			"GIT_DIR=somerepo git status",
			"GIT_DIR=somerepo git status\n",
		},
		{
			"redirect stdout and stderr",
			"echo hello > out.txt 2>&1",
			"echo hello > out.txt 2>&1\n",
		},
		{
			"append redirect",
			"echo tick >> log.txt",
			"echo tick >> log.txt\n",
		},
		{"background job", "sleep 5m &", "sleep 5m &\n"},
		{
			"brace expansion",
			"mv *.{c,h} src/",
			"mv *.{c,h} src/\n",
		},
		{"tilde and glob", "ls ~/src/*.txt", "ls ~/src/*.txt\n"},
		{
			"bracket test",
			"[ -n \"$x\" ] && echo set",
			"[ -n \"$x\" ] && echo set\n",
		},
		{
			"comments kept",
			"# scaffold\necho hi # trailing",
			"# scaffold\necho hi # trailing\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := Translate([]byte(tc.in))
			if err != nil {
				t.Fatalf("Translate(%q) error: %v", tc.in, err)
			}
			for _, w := range warnings {
				t.Errorf("unexpected warning on passthrough input: %v", w)
			}
			if string(got) != tc.want {
				t.Errorf("passthrough mismatch\n in:   %q\n got:  %q\n want: %q",
					tc.in, got, tc.want)
			}
		})
	}
}

// TestTier1 covers the keyword-level rewrites: fish block structure,
// loops, case, functions, and grouping.
func TestTier1(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"if elif else",
			"if [ \"$a\" = 1 ]; then\n" +
				"\techo one\n" +
				"elif [ \"$a\" = 2 ]; then\n" +
				"\techo two\n" +
				"else\n" +
				"\techo many\n" +
				"fi\n",
			"if [ \"$a\" = 1 ]\n" +
				"    echo one\n" +
				"else if [ \"$a\" = 2 ]\n" +
				"    echo two\n" +
				"else\n" +
				"    echo many\n" +
				"end\n",
		},
		{
			"while reading a file",
			"while read -r line; do\n" +
				"\techo \"$line\"\n" +
				"done < input.txt\n",
			"while read line\n" +
				"    echo \"$line\"\n" +
				"end < input.txt\n",
		},
		{
			"until becomes while not",
			"until grep -q ready status.log; do\n" +
				"\tsleep 1\n" +
				"done\n",
			"while not grep -q ready status.log\n" +
				"    sleep 1\n" +
				"end\n",
		},
		{
			"for over glob",
			"for f in *.txt; do\n" +
				"\tcp \"$f\" backup/\n" +
				"done\n",
			"for f in *.txt\n" +
				"    cp \"$f\" backup/\n" +
				"end\n",
		},
		{
			"bare for iterates argv",
			"for arg; do echo \"$arg\"; done\n",
			"for arg in $argv\n" +
				"    echo \"$arg\"\n" +
				"end\n",
		},
		{
			"case becomes switch",
			"case $(uname) in\n" +
				"\tLinux)\n" +
				"\t\techo tux\n" +
				"\t\t;;\n" +
				"\tDarwin|'*BSD')\n" +
				"\t\techo bsd\n" +
				"\t\t;;\n" +
				"\t*)\n" +
				"\t\techo other\n" +
				"\t\t;;\n" +
				"esac\n",
			"switch $(uname)\n" +
				"case Linux\n" +
				"    echo tux\n" +
				"case Darwin '*BSD'\n" +
				"    echo bsd\n" +
				"case '*'\n" +
				"    echo other\n" +
				"end\n",
		},
		{
			"case with escaped tilde patterns",
			"case \"$PIXI_HOME\" in\n" +
				"\t\\~ | \\~/*)\n" +
				"\t\techo home\n" +
				"\t\t;;\n" +
				"esac\n",
			"switch \"$PIXI_HOME\"\n" +
				"case '~' '~/*'\n" +
				"    echo home\n" +
				"end\n",
		},
		{
			"function definition",
			"greet() {\n" +
				"\techo \"hello $1\"\n" +
				"}\n",
			"function greet\n" +
				"    echo \"hello $argv[1]\"\n" +
				"end\n",
		},
		{
			"function keyword form",
			"function foo { echo hi; }\n",
			"function foo\n" +
				"    echo hi\n" +
				"end\n",
		},
		{
			"brace group with redirect",
			"{ echo a; echo b; } > out.txt\n",
			"begin\n" +
				"    echo a\n" +
				"    echo b\n" +
				"end > out.txt\n",
		},
		{
			"negated condition",
			"if ! grep -q foo bar; then echo missing; fi\n",
			"if ! grep -q foo bar\n" +
				"    echo missing\n" +
				"end\n",
		},
		{
			"backgrounded group",
			"{ sleep 1; } &\n",
			"begin\n" +
				"    sleep 1\n" +
				"end &\n",
		},
		{
			"comments inside if body",
			"if true; then\n" +
				"\t# about to echo\n" +
				"\techo hi # trailing\n" +
				"fi\n",
			"if true\n" +
				"    # about to echo\n" +
				"    echo hi # trailing\n" +
				"end\n",
		},
		{
			"nested blocks",
			"for d in */; do\n" +
				"if [ -f \"$d/Makefile\" ]; then\n" +
				"\techo \"$d\"\n" +
				"fi\n" +
				"done\n",
			"for d in */\n" +
				"    if [ -f \"$d/Makefile\" ]\n" +
				"        echo \"$d\"\n" +
				"    end\n" +
				"end\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := Translate([]byte(tc.in))
			if err != nil {
				t.Fatalf("Translate(%q) error: %v", tc.in, err)
			}
			if string(got) != tc.want {
				t.Errorf("tier1 mismatch\n in:   %q\n got:  %q\n want: %q",
					tc.in, got, tc.want)
			}
		})
	}
}

func TestShebang(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bin bash", "#!/bin/bash\necho hi\n", "#!/usr/bin/env fish\necho hi\n"},
		{"env bash", "#!/usr/bin/env bash\necho hi\n", "#!/usr/bin/env fish\necho hi\n"},
		{"bash flags dropped", "#!/bin/bash -eu\necho hi\n", "#!/usr/bin/env fish\necho hi\n"},
		{"sh replaced", "#!/bin/sh\necho hi\n", "#!/usr/bin/env fish\necho hi\n"},
		{"dash replaced", "#!/bin/dash\necho hi\n", "#!/usr/bin/env fish\necho hi\n"},
		{"no shebang untouched", "echo hi\n", "echo hi\n"},
		{"fish shebang untouched", "#!/usr/bin/env fish\necho hi\n", "#!/usr/bin/env fish\necho hi\n"},
		{"body preserved byte-exact", "#!/bin/bash\nif true; then\n\techo a\nfi\n", "#!/usr/bin/env fish\nif true\n    echo a\nend\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := Translate([]byte(tc.in))
			if err != nil {
				t.Fatalf("Translate(%q) error: %v", tc.in, err)
			}
			if string(got) != tc.want {
				t.Errorf("shebang mismatch\n in:   %q\n got:  %q\n want: %q",
					tc.in, got, tc.want)
			}
		})
	}
}

// TestWarnings verifies that constructs without a fish equivalent are
// emitted verbatim and reported.
func TestWarnings(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantText string // substring expected in some warning
	}{
		{
			"c-style for loop",
			"for ((i = 0; i < 3; i++)); do echo $i; done\n",
			"C-style for loop",
		},
		{
			"select loop",
			"select v in a b; do echo $v; done\n",
			"select loop",
		},
		{
			"arithmetic command",
			"((y = x > 1 ? 2 : 3))\n",
			"arithmetic command",
		},
		{
			"subshell loses isolation",
			"(cd /tmp && ls)\n",
			"subshell isolation",
		},
		{
			"case fallthrough",
			"case $x in\na) echo one ;&\nb) echo two ;;\nesac\n",
			"fallthrough",
		},
		{
			"set flags dropped",
			"set -e\n",
			"set flags",
		},
		{
			"let passthrough",
			"let a=1+2\n",
			"let statement",
		},
		{
			"hash passthrough",
			"hash curl 2>/dev/null\n",
			"bash builtin",
		},
		{
			"hash in condition",
			"if hash curl 2>/dev/null; then echo y; fi\n",
			"bash builtin",
		},
		{
			"subshell inside command substitution",
			"x=$( (cd /tmp && pwd); )\n",
			"subshell isolation",
		},
		{
			"bare set dropped",
			"set\n",
			"statement dropped",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := Translate([]byte(tc.in))
			if err != nil {
				t.Fatalf("Translate(%q) error: %v", tc.in, err)
			}
			if len(warnings) == 0 {
				t.Fatalf("Translate(%q) produced no warnings, want one containing %q (output: %q)",
					tc.in, tc.wantText, got)
			}
			found := false
			for _, w := range warnings {
				if strings.Contains(w.Text, tc.wantText) {
					found = true
					if w.Line <= 0 || w.Col <= 0 {
						t.Errorf("warning %v has invalid position", w)
					}
				}
			}
			if !found {
				t.Errorf("no warning contains %q; got %v", tc.wantText, warnings)
			}
		})
	}
}

func TestParseError(t *testing.T) {
	src := "if true; then\n"
	_, warnings, err := Translate([]byte(src))
	if err == nil {
		t.Fatalf("Translate(%q) = no error, want parse failure", src)
	}
	if !strings.Contains(err.Error(), "parse bash") {
		t.Errorf("error %q does not identify the parse stage", err)
	}
	if warnings != nil {
		t.Errorf("warnings = %v, want nil on parse failure", warnings)
	}
}

// TestNonShellScriptFails documents that scripts which are not shell
// (e.g. Python) fail at the parse stage instead of passing through.
func TestNonShellScriptFails(t *testing.T) {
	src := "#!/usr/bin/env python3\nprint(1)\n"
	_, _, err := Translate([]byte(src))
	if err == nil {
		t.Fatalf("Translate(%q) = no error, want parse failure", src)
	}
}

// TestBashFishEquivalence executes each fixture under bash and executes
// its bait translation under fish, requiring identical stdout and exit
// status. Fixtures must avoid tier-2 constructs (assignments, positional
// parameters); inputs are injected through the environment instead.
func TestBashFishEquivalence(t *testing.T) {
	if _, err := exec.LookPath("fish"); err != nil {
		t.Skip("fish not found in PATH")
	}

	tests := []struct {
		name  string
		env   []string
		files map[string]string
		args  []string
		src   string
	}{
		{
			name: "if elif else",
			env:  []string{"A=2"},
			src: "if [ \"$A\" = 1 ]; then\n" +
				"\techo one\n" +
				"elif [ \"$A\" = 2 ]; then\n" +
				"\techo two\n" +
				"else\n" +
				"\techo many\n" +
				"fi\n",
		},
		{
			name:  "while read loop",
			files: map[string]string{"data.txt": "one\ntwo\nthree\n"},
			src: "while read -r line; do\n" +
				"\techo \"got:$line\"\n" +
				"done < data.txt\n",
		},
		{
			name:  "for over glob",
			files: map[string]string{"a.txt": "", "b.txt": ""},
			src:   "for f in *.txt; do\n\techo \"file:$f\"\ndone\n",
		},
		{
			name: "switch on uname",
			src: "case $(uname) in\n" +
				"\tLinux)\n" +
				"\t\techo tux\n" +
				"\t\t;;\n" +
				"\tDarwin)\n" +
				"\t\techo apple\n" +
				"\t\t;;\n" +
				"esac\n",
		},
		{
			name:  "switch wildcard with files present",
			files: map[string]string{"foo.txt": "", "bar.txt": "", "other.dat": ""},
			src: "target=something_else\n" +
				"case \"$target\" in\n" +
				"\t*.txt)\n" +
				"\t\techo txt\n" +
				"\t\t;;\n" +
				"\t*)\n" +
				"\t\techo wildcard_matched\n" +
				"\t\t;;\n" +
				"esac\n",
		},
		{
			name: "function definition and call",
			env:  []string{"WHO=world"},
			src:  "greet() {\n\techo \"hello $WHO\"\n}\ngreet\n",
		},
		{
			name: "group with redirect",
			src:  "{ echo a; echo b; } > out.txt\ncat out.txt\n",
		},
		{
			name: "assignment and use",
			src:  "NAME=bait\necho \"using $NAME\"\n",
		},
		{
			name: "function assignment is global like bash",
			src: "bump() {\n" +
				"\tCOUNTER=99\n" +
				"\techo \"inside:$COUNTER\"\n" +
				"}\n" +
				"bump\necho \"after:$COUNTER\"\n",
		},
		{
			name: "chain assignment fallback",
			src:  "false || GREET=hi\necho \"got:$GREET\"\n",
		},
		{
			name: "flag accumulation stays splittable",
			env:  []string{"NETRC=/tmp/netrc"},
			src: "FLAGS=\"--silent\"\n" +
				"FLAGS=\"$FLAGS --netrc-file $NETRC\"\n" +
				"printf '%s\\n' $FLAGS\n",
		},
		{
			name: "if inside command substitution",
			src: "V=$(if [ 1 -eq 1 ]; then echo match; else echo mismatch; fi)\n" +
				"echo \"got:$V\"\n",
		},
		{
			name: "positional parameters",
			args: []string{"alpha", "beta"},
			src:  "echo \"first:$1 second:$2 count:$#\"\n",
		},
		{
			name: "iterate quoted dollar-at",
			args: []string{"x", "y"},
			src:  "for v in \"$@\"; do echo item:$v; done\n",
		},
		{
			name: "default via parameter operator",
			src:  "echo \"hi, ${1:-stranger}\"\n",
		},
		{
			name:  "suffix strip in loop",
			files: map[string]string{"a.txt": "", "b.txt": ""},
			src: "for f in *.txt; do\n" +
				"\techo \"${f%.txt}\"\n" +
				"done\n",
		},
		{
			name: "shift in while loop",
			args: []string{"a", "b", "c"},
			src: "while [ $# -gt 0 ]; do\n" +
				"\techo \"arg:$1\"\n" +
				"\tshift\n" +
				"done\n",
		},
		{
			name: "counter loop with arithmetic",
			src: "COUNT=0\n" +
				"while [ \"$COUNT\" -lt 3 ]; do\n" +
				"\techo tick:$COUNT\n" +
				"\t((COUNT += 1))\n" +
				"done\n",
		},
		{
			name: "integer division",
			src:  "X=7\necho \"half:$((X / 2))\"\n",
		},
		{
			name: "array lifecycle",
			src: "fruits=(apple banana cherry)\n" +
				"for f in \"${fruits[@]}\"; do\n" +
				"\techo fruit:$f\n" +
				"done\n" +
				"echo \"count:${#fruits[@]}\"\n" +
				"echo \"second:${fruits[1]}\"\n" +
				"fruits+=(date)\n" +
				"echo \"after:${#fruits[@]}\"\n",
		},
		{
			name: "set positional params",
			src: "set -- alpha \"beta gamma\"\n" +
				"echo \"$1|$2\"\n" +
				"echo \"count:$#\"\n",
		},
		{
			name: "for over space-separated variable",
			src: "TARGETS=\"target1 target2 target3\"\n" +
				"for t in $TARGETS; do\n" +
				"\techo \"got:$t\"\n" +
				"done\n",
		},
		{
			name: "custom ifs splitting",
			src: "IFS=':'\n" +
				"PATHS=\"/usr/bin:/bin:/opt/bin\"\n" +
				"for p in $PATHS; do\n" +
				"\techo \"path:$p\"\n" +
				"done\n",
		},
		{
			name: "dynamic command execution",
			src: "cmd=\"printf [%s]\\n hello world\"\n" +
				"$cmd\n",
		},
		{
			name: "herestring",
			src:  "cat <<< \"hello from herestring\"\n",
		},
		{
			name: "herestring with variable expansion",
			src: "MSG=\"world\"\n" +
				"cat <<< \"hello $MSG\"\n",
		},
		{
			name: "heredoc with variable",
			src: "VAL=\"123\"\n" +
				"cat <<EOF\n" +
				"val is $VAL\n" +
				"EOF\n",
		},
		{
			name: "unset variable and function",
			src: "FOO=\"hello\"\n" +
				"unset FOO\n" +
				"echo \"foo:${FOO:-empty}\"\n" +
				"f() { echo hi; }\n" +
				"unset -f f\n",
		},
		{
			name: "unset array element",
			src: "items=(first second third)\n" +
				"unset 'items[1]'\n" +
				"echo \"count:${#items[@]}\"\n" +
				"for x in \"${items[@]}\"; do\n" +
				"\techo \"item:$x\"\n" +
				"done\n",
		},
		{
			name: "double bracket glob and equality",
			src: "val=\"apple_pie\"\n" +
				"if [[ $val == apple* ]]; then echo 'starts with apple'; fi\n" +
				"if [[ $val != orange* ]]; then echo 'not orange'; fi\n" +
				"if [[ \"$val\" == \"apple_pie\" ]]; then echo 'exact match'; fi\n",
		},
		{
			name: "double bracket regex match",
			src: "str=\"version_123_release\"\n" +
				"if [[ $str =~ [0-9]+ ]]; then echo 'has digits'; fi\n" +
				"if [[ $str =~ ^version_ ]]; then echo 'starts with version'; fi\n",
		},
		{
			name: "double bracket compound condition",
			src: "a=\"one\"\nb=\"two\"\n" +
				"if [[ ( -n \"$a\" || -n \"$b\" ) && \"$a\" == \"one\" ]]; then echo 'compound ok'; fi\n",
		},
		{
			name: "double bracket var test -v",
			src: "DEFINED=\"yes\"\n" +
				"if [[ -v DEFINED ]]; then echo 'DEFINED is set'; fi\n" +
				"if [[ ! -v UNDEFINED_VAR ]]; then echo 'UNDEFINED is not set'; fi\n",
		},
		{
			name: "diff with process substitution",
			src: "diff -u <(printf \"alpha\\nbeta\\n\") <(printf \"alpha\\ngamma\\n\") >/dev/null || true\n" +
				"echo 'diff completed'\n",
		},
		{
			name: "while loop reading from process substitution",
			src: "while read -r line; do\n" +
				"\techo \"got:$line\"\n" +
				"done < <(printf \"one\\ntwo\\n\")\n",
		},
		{
			name: "special variables uid euid hostname funcname",
			src: "echo \"uid_positive: $([ $UID -gt 0 ] && echo yes)\"\n" +
				"echo \"euid_positive: $([ $EUID -gt 0 ] && echo yes)\"\n" +
				"echo \"host_set: $([ -n \"$HOSTNAME\" ] && echo yes)\"\n" +
				"echo \"arch_set: $([ -n \"$HOSTTYPE\" ] && echo yes)\"\n" +
				"my_func() {\n" +
				"\techo \"func: $FUNCNAME, ${FUNCNAME[0]}\"\n" +
				"}\n" +
				"my_func\n",
		},
		{
			name: "pipestatus across multiple commands",
			src: "true | false | true\n" +
				"echo \"pipe: ${PIPESTATUS[0]} ${PIPESTATUS[1]} ${PIPESTATUS[2]}\"\n",
		},
		{
			name: "ostype lowercase check",
			src: "case \"$OSTYPE\" in\n" +
				"darwin*|linux*|*bsd*)\n" +
				"\techo 'recognized os'\n" +
				"\t;;\n" +
				"*)\n" +
				"\techo 'unrecognized os'\n" +
				"\t;;\n" +
				"esac\n",
		},
		{
			name: "empty command prefix",
			src: "prefix=\"\"\n" +
				"$prefix echo running without prefix\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			bashPath := filepath.Join(dir, "script.sh")
			fishPath := filepath.Join(dir, "script.fish")

			out, warnings, err := Translate([]byte(tc.src))
			if err != nil {
				t.Fatalf("Translate error: %v", err)
			}
			if len(warnings) > 0 {
				t.Fatalf("fixture should translate without warnings, got %v", warnings)
			}
			if err := os.WriteFile(bashPath, []byte(tc.src), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fishPath, out, 0o755); err != nil {
				t.Fatal(err)
			}

			run := func(shell, path string) (string, int) {
				cmd := exec.Command(shell, append([]string{path}, tc.args...)...)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), tc.env...)
				stdout, err := cmd.Output()
				code := 0
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					code = exitErr.ExitCode()
				} else if err != nil {
					t.Fatalf("%s %s: %v", shell, path, err)
				}
				return string(stdout), code
			}

			bashOut, bashCode := run("bash", bashPath)
			fishOut, fishCode := run("fish", fishPath)
			if fishOut != bashOut {
				t.Errorf("stdout mismatch\n bash: %q\n fish: %q", bashOut, fishOut)
			}
			if fishCode != bashCode {
				t.Errorf("exit status mismatch: bash=%d fish=%d", bashCode, fishCode)
			}
		})
	}
}

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

// TestTier2 covers semantic rewrites of assignments and declaration
// clauses. Every successful path here must also be warning-free; the
// long-option policy applies to all generated set commands.
func TestTier2(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"simple assignment", "x=1\n", "set x 1\n"},
		{
			"quoted value preserved",
			"msg=\"hello world\"\n",
			"set msg \"hello world\"\n",
		},
		{"empty assignment becomes empty string", "x=\n", "set x \"\"\n"},
		{"same-line pair", "a=1 b=2\n", "set a 1\nset b 2\n"},
		{"export passes through", "export EDITOR=vim\n", "export EDITOR=vim\n"},
		{
			"export multiple passes through",
			"export A=1 B=2\n",
			"export A=1 B=2\n",
		},
		{
			"local maps to set --function",
			"f() {\n\tlocal x=1\n}\n",
			"function f\n    set --function x 1\nend\n",
		},
		{
			"bare local becomes empty string",
			"f() {\n\tlocal x\n}\n",
			"function f\n    set --function x \"\"\nend\n",
		},
		{
			"function assignment gets --global",
			"f() {\n\tTOTAL=5\n}\n",
			"function f\n    set --global TOTAL 5\nend\n",
		},
		{"declare at top level", "declare greeting=hi\n", "set greeting hi\n"},
		{
			"declare in function is local",
			"f() {\n\tdeclare n=2\n}\n",
			"function f\n    set --function n 2\nend\n",
		},
		{
			"self-referential accumulation becomes list append",
			"OPTS=\"--silent\"\nOPTS=\"$OPTS --netrc\"\n",
			"set OPTS \"--silent\"\nset OPTS $OPTS --netrc\n",
		},
		{
			"adjacent value concatenates in one word",
			"OPTS=x\nOPTS=\"$OPTS --file=$F\"\n",
			"set OPTS x\nset OPTS $OPTS --file=$F\n",
		},
		{
			"non-self reference untouched",
			"LINE=\"set PATH \\\"$BIN\\\" \\$PATH\"\n",
			"set LINE \"set PATH \\\"$BIN\\\" \\$PATH\"\n",
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
				t.Errorf("tier2 mismatch\n in:   %q\n got:  %q\n want: %q",
					tc.in, got, tc.want)
			}
		})
	}
}

// TestTier2Warnings covers declaration forms without a faithful fish
// mapping; they pass through verbatim and must be reported.
func TestTier2Warnings(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantText string
	}{
		{"readonly clause", "readonly MAX=10\n", "readonly"},
		{"dynamic array index", "arr[$i]=x\n", "dynamic array index"},
		{"export flag", "export -n EDITOR\n", "not supported by fish's export"},
		{"assert assignment", "cmd ${x:=d}\n", "no fish equivalent"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := Translate([]byte(tc.in))
			if err != nil {
				t.Fatalf("Translate(%q) error: %v", tc.in, err)
			}
			found := false
			for _, w := range warnings {
				if strings.Contains(w.Text, tc.wantText) {
					found = true
				}
			}
			if !found {
				t.Errorf("no warning contains %q; got %v (output: %q)",
					tc.wantText, warnings, got)
			}
		})
	}
}

// TestTier2Params covers fish-side rewrites of parameter expansions.
func TestTier2Params(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"braces stripped from plain var",
			"echo ${HOME}/bin\n",
			"echo $HOME/bin\n",
		},
		{"question maps to status", "echo failed:$?\n", "echo failed:$status\n"},
		{"dollar maps to fish_pid", "echo pid:$$\n", "echo pid:$fish_pid\n"},
		{"bang maps to last_pid", "wait $!\n", "wait $last_pid\n"},
		{"hash maps to count argv", "echo $# args\n", "echo $(count $argv) args\n"},
		{
			"dollar-zero maps to status filename",
			"echo \"usage: $0\"\n",
			"echo \"usage: $(status filename)\"\n",
		},
		{"positional short", "echo $1\n", "echo $argv[1]\n"},
		{"positional braced", "echo ${2}\n", "echo $argv[2]\n"},
		{
			"quoted dollar-at drops quotes",
			"run \"$@\"\n",
			"run $argv\n",
		},
		{"unquoted star becomes argv", "run $*\n", "run $argv\n"},
		{"UID and EUID maps to id -u", "echo $UID $EUID ${UID}\n", "echo $(id -u) $(id -u) $(id -u)\n"},
		{"GROUPS maps to id -g", "echo $GROUPS ${GROUPS[0]}\n", "echo $(id -g) $(id -g)\n"},
		{"HOSTNAME maps to hostname", "echo \"host: $HOSTNAME ${HOSTNAME}\"\n", "echo \"host: $hostname $hostname\"\n"},
		{"HOSTTYPE and MACHTYPE maps to uname -m", "echo $HOSTTYPE $MACHTYPE\n", "echo $(uname -m) $(uname -m)\n"},
		{"RANDOM maps to random", "echo $RANDOM ${RANDOM}\n", "echo $(random 0 32767) $(random 0 32767)\n"},
		{"BASH_SOURCE maps to status filename", "echo \"${BASH_SOURCE[0]}\" $BASH_SOURCE\n", "echo \"$(status filename)\" $(status filename)\n"},
		{"FUNCNAME maps to status current-function", "echo \"$FUNCNAME\" \"${FUNCNAME[0]}\"\n", "echo \"$(status current-function)\" \"$(status current-function)\"\n"},
		{"BASHPID maps to fish_pid", "echo $BASHPID\n", "echo $fish_pid\n"},
		{
			"PIPESTATUS maps to pipestatus",
			"echo $PIPESTATUS ${PIPESTATUS[0]} ${PIPESTATUS[1]} ${PIPESTATUS[@]} ${#PIPESTATUS[@]}\n",
			"echo $pipestatus $pipestatus[1] $pipestatus[2] $pipestatus $(count $pipestatus)\n",
		},
		{
			"OSTYPE maps to uname -s lower",
			"echo $OSTYPE \"${OSTYPE}\"\n",
			"echo $(uname -s | string lower) \"$(uname -s | string lower)\"\n",
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
				t.Errorf("param mismatch\n in:   %q\n got:  %q\n want: %q",
					tc.in, got, tc.want)
			}
		})
	}
}

// TestTier2Arith covers bash integer arithmetic mapped onto fish math
// and test.
func TestTier2Arith(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"increment",
			"((i++))\n",
			"set i $(math --scale=0 \"$i + 1\")\n",
		},
		{
			"pre-decrement",
			"((--i))\n",
			"set i $(math --scale=0 \"$i - 1\")\n",
		},
		{
			"compound assign",
			"((n += 2))\n",
			"set n $(math --scale=0 \"$n + 2\")\n",
		},
		{
			"assign expression",
			"((total = a * b + 1))\n",
			"set total $(math --scale=0 \"$a * $b + 1\")\n",
		},
		{
			"unary minus value",
			"((d = -x))\n",
			"set d $(math --scale=0 \"-$x\")\n",
		},
		{
			"comparison becomes test",
			"((n > 0))\n",
			"test \"$n\" -gt 0\n",
		},
		{
			"truthiness of a variable",
			"((count))\n",
			"test \"$count\" -ne 0\n",
		},
		{
			"in-word expansion",
			"echo $((1 + 2))\n",
			"echo $(math --scale=0 \"1 + 2\")\n",
		},
		{
			"variable expansion",
			"echo $((x * 2))\n",
			"echo $(math --scale=0 \"$x * 2\")\n",
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
				t.Errorf("arith mismatch\n in:   %q\n got:  %q\n want: %q",
					tc.in, got, tc.want)
			}
		})
	}
}

// TestTier2Ops covers ${var...} operator rewrites into pure command
// substitutions.
func TestTier2Ops(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"default when unset or empty",
			"echo ${NAME:-world}\n",
			"echo $(test -n \"$NAME\" && printf %s\\n \"$NAME\" || printf %s\\n world)\n",
		},
		{
			"default inside quotes",
			"echo \"hi, ${NAME:-dear user}\"\n",
			"echo \"hi, $(test -n \"$NAME\" && printf %s\\n \"$NAME\" || printf %s\\n \"dear user\")\"\n",
		},
		{
			"default when unset only",
			"cmd ${OPT-fb}\n",
			"cmd $(set --query OPT && printf %s\\n \"$OPT\" || printf %s\\n fb)\n",
		},
		{
			"alternate value",
			"run ${V:+alt}\n",
			"run $(test -n \"$V\" && printf %s\\n alt || true)\n",
		},
		{
			"shortest suffix strip",
			"echo ${f%.txt}\n",
			"echo $(string replace --regex '\\.txt$' '' -- $f)\n",
		},
		{
			"greedy suffix strip",
			"echo ${f%%.*}\n",
			"echo $(string replace --regex '\\..*$' '' -- $f)\n",
		},
		{
			"greedy prefix strip",
			"echo ${p##*/}\n",
			"echo $(string replace --regex '^.*/' '' -- $p)\n",
		},
		{
			"replace first",
			"echo ${s/o/0}\n",
			"echo $(string replace --regex 'o' '0' -- $s)\n",
		},
		{
			"replace all",
			"echo ${s//o/0}\n",
			"echo $(string replace --regex --all 'o' '0' -- $s)\n",
		},
		{
			"substring",
			"echo ${s:2:3}\n",
			"echo $(string sub --start=3 --length=3 -- $s)\n",
		},
		{"length", "echo ${#s}\n", "echo $(string length -- $s)\n"},
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
				t.Errorf("ops mismatch\n in:   %q\n got:  %q\n want: %q",
					tc.in, got, tc.want)
			}
		})
	}
}

// TestTier2Arrays covers bash array operations mapped onto fish lists,
// including the 0-based to 1-based index shift.
func TestTier2Arrays(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"array literal flattens", "colors=(red blue)\n", "set colors red blue\n"},
		{"indexed read shifts", "echo ${arr[0]}\n", "echo $arr[1]\n"},
		{"indexed write shifts", "arr[2]=x\n", "set arr[3] x\n"},
		{"whole list", "echo ${arr[@]}\n", "echo $arr\n"},
		{
			"quoted whole list drops quotes",
			"run \"${arr[@]}\"\n",
			"run $arr\n",
		},
		{"list count", "echo ${#arr[@]}\n", "echo $(count $arr)\n"},
		{"list slice", "echo ${arr[@]:1:2}\n", "echo $arr[2..3]\n"},
		{"open slice to end", "echo ${arr[@]:2}\n", "echo $arr[3..-1]\n"},
		{"negative index unchanged", "echo ${arr[-1]}\n", "echo $arr[-1]\n"},
		{"append element", "arr+=(tail)\n", "set --append arr tail\n"},
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
				t.Errorf("array mismatch\n in:   %q\n got:  %q\n want: %q",
					tc.in, got, tc.want)
			}
		})
	}
}

// TestSetBuiltin covers bash's set builtin: positional assignment maps
// onto fish's argv list, while option forms are dropped with a warning.
func TestSetBuiltin(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"positional assignment", "set -- alpha beta\n", "set argv alpha beta\n"},
		{"quoted value", "set -- \"a b\" c\n", "set argv \"a b\" c\n"},
		{"clear positionals", "set --\n", "set argv\n"},
		{"flagless form", "set alpha beta\n", "set argv alpha beta\n"},
		{"command substitution value", "set -- $(pwd)\n", "set argv $(pwd)\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := Translate([]byte(tc.in))
			if err != nil {
				t.Fatalf("Translate(%q) error: %v", tc.in, err)
			}
			if len(warnings) != 0 {
				t.Errorf("unexpected warnings: %v", warnings)
			}
			if string(got) != tc.want {
				t.Errorf("set mismatch\n in: %q\n got: %q\n want: %q",
					tc.in, got, tc.want)
			}
		})
	}
}

func TestShiftBuiltin(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare shift", "shift\n", "set --erase argv[1]\n"},
		{"shift 1", "shift 1\n", "set --erase argv[1]\n"},
		{"shift 2", "shift 2\n", "set --erase argv[1..2]\n"},
		{"shift dynamic", "shift $n\n", "set --erase argv[1..$n]\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := Translate([]byte(tc.in))
			if err != nil {
				t.Fatalf("Translate(%q) error: %v", tc.in, err)
			}
			if len(warnings) != 0 {
				t.Errorf("unexpected warnings: %v", warnings)
			}
			if string(got) != tc.want {
				t.Errorf("shift mismatch\n in: %q\n got: %q\n want: %q",
					tc.in, got, tc.want)
			}
		})
	}
}

// TestExpansionRegression pins behaviors exposed by real-world scripts:
// empty-default expansions, function-definition combiners, and nested
// expansions inside default values.
func TestExpansionRegression(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"empty default unset",
			"echo ${x-}\n",
			"echo $(set --query x && printf %s\\n \"$x\" || printf %s\\n '')\n",
		},
		{
			"empty default unset or null",
			"echo ${x:-}\n",
			"echo $(test -n \"$x\" && printf %s\\n \"$x\" || printf %s\\n '')\n",
		},
		{
			"empty pattern strip is identity",
			"echo ${x%}\n",
			"echo $x\n",
		},
		{
			"function definition combined with call",
			"f() { echo hi; } && f\n",
			"function f\n    echo hi\nend\nf\n",
		},
		{
			"nested expansion in default stays live",
			"echo ${A:-x${B%/}y}\n",
			"echo $(test -n \"$A\" && printf %s\\n \"$A\" || printf %s\\n \"x$(string replace --regex '/$' '' -- $B)y\")\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := Translate([]byte(tc.in))
			if err != nil {
				t.Fatalf("Translate(%q) error: %v", tc.in, err)
			}
			if len(warnings) != 0 {
				t.Errorf("unexpected warnings: %v", warnings)
			}
			if string(got) != tc.want {
				t.Errorf("mismatch\n in: %q\n got: %q\n want: %q",
					tc.in, got, tc.want)
			}
		})
	}
}

// TestChainAssignments pins assignment leaves inside &&/||/| chains:
// they become set commands while plain command leaves stay verbatim.
func TestChainAssignments(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"assignment as fallback",
			"false || X=fall\n",
			"false || set X fall\n",
		},
		{
			"assignment leading a chain",
			"X=init && cmd\n",
			"set X init && cmd\n",
		},
		{
			"assignment with command substitution",
			"HTTP_CODE=$(curl -s example.com) || CURL_ERR=$?\n",
			"set HTTP_CODE $(curl -s example.com) || set CURL_ERR $status\n",
		},
		{
			"pipe with assignment leaf",
			"true | X=1\n",
			"true | set X 1\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := Translate([]byte(tc.in))
			if err != nil {
				t.Fatalf("Translate(%q) error: %v", tc.in, err)
			}
			if len(warnings) != 0 {
				t.Errorf("unexpected warnings: %v", warnings)
			}
			if string(got) != tc.want {
				t.Errorf("mismatch\n in: %q\n got: %q\n want: %q",
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
			"set x $(begin\n    echo hi\nend)\n",
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
			"if [ \"$(echo a | begin\n    cat\nend)\" = a ]\n    echo match\nend\n",
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

func TestUnsetBuiltin(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"unset variable", "unset x\n", "set --erase x\n"},
		{"unset multiple variables", "unset a b c\n", "set --erase a b c\n"},
		{"unset with -v flag", "unset -v x y\n", "set --erase x y\n"},
		{"unset function", "unset -f my_func\n", "functions --erase my_func\n"},
		{"unset multiple functions", "unset -f f1 f2\n", "functions --erase f1 f2\n"},
		{"unset array element", "unset 'arr[0]'\n", "set --erase arr[1]\n"},
		{"unset array element unquoted", "unset arr[2]\n", "set --erase arr[3]\n"},
		{"unset in chain", "test -n \"$x\" && unset x\n", "test -n \"$x\" && set --erase x\n"},
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

func TestTestClause(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"non-empty test",
			"[[ -n \"$x\" ]]\n",
			"test -n \"$x\"\n",
		},
		{
			"empty test",
			"[[ -z \"$x\" ]]\n",
			"test -z \"$x\"\n",
		},
		{
			"string equality",
			"[[ \"$a\" == \"b\" ]]\n",
			"test \"$a\" = \"b\"\n",
		},
		{
			"string inequality",
			"[[ \"$a\" != \"b\" ]]\n",
			"test \"$a\" != \"b\"\n",
		},
		{
			"glob match",
			"[[ $a == b* ]]\n",
			"string match -q -- 'b*' $a\n",
		},
		{
			"glob no match",
			"[[ $a != b* ]]\n",
			"! string match -q -- 'b*' $a\n",
		},
		{
			"regex match",
			"[[ $str =~ ^[0-9]+$ ]]\n",
			"string match -r -q -- '^[0-9]+$' $str\n",
		},
		{
			"and combiner",
			"[[ -f /etc/hosts && -r /etc/hosts ]]\n",
			"test -f /etc/hosts && test -r /etc/hosts\n",
		},
		{
			"or combiner with not",
			"[[ -d dir || ! -e file ]]\n",
			"test -d dir || ! test -e file\n",
		},
		{
			"parenthesized group",
			"[[ ( -n \"$a\" || -n \"$b\" ) && \"$c\" == \"d\" ]]\n",
			"begin test -n \"$a\" || test -n \"$b\"; end && test \"$c\" = \"d\"\n",
		},
		{
			"integer comparison",
			"[[ 5 -gt 3 ]]\n",
			"test 5 -gt 3\n",
		},
		{
			"variable set test",
			"[[ -v VAR ]]\n",
			"set -q VAR\n",
		},
		{
			"if with double bracket",
			"if [[ -n \"$x\" ]]; then echo yes; fi\n",
			"if test -n \"$x\"\n    echo yes\nend\n",
		},
		{
			"while with double bracket",
			"while [[ $i -lt 5 ]]; do echo $i; done\n",
			"while test $i -lt 5\n    echo $i\nend\n",
		},
		{
			"double bracket in chain",
			"[[ -n \"$x\" ]] && echo yes\n",
			"test -n \"$x\" && echo yes\n",
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
			"source (curl -fsSL https://example.com/install.sh | psub)\n",
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
