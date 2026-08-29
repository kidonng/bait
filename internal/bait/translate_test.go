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

// TestPassthrough pins down the translation policy: constructs that
// modern fish accepts natively must survive translation unchanged
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

// TestCompoundStatements covers the keyword-level rewrites: fish block
// structure, loops, case, functions, and grouping.
func TestCompoundStatements(t *testing.T) {
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
			"case dollar dash interactive check",
			"case $- in\n" +
				"\t*i*)\n" +
				"\t\treturn\n" +
				"\t\t;;\n" +
				"esac\n",
			"if status is-interactive\n" +
				"    return\n" +
				"end\n",
		},
		{
			"case dollar dash interactive with else",
			"case $- in\n" +
				"\t*i*)\n" +
				"\t\techo yes\n" +
				"\t\t;;\n" +
				"\t*)\n" +
				"\t\techo no\n" +
				"\t\t;;\n" +
				"esac\n",
			"if status is-interactive\n" +
				"    echo yes\n" +
				"else\n" +
				"    echo no\n" +
				"end\n",
		},
		{
			"case dollar dash non-interactive branch only",
			"case $- in\n" +
				"\t*i*)\n" +
				"\t\t;;\n" +
				"\t*)\n" +
				"\t\techo non-interactive\n" +
				"\t\t;;\n" +
				"esac\n",
			"if not status is-interactive\n" +
				"    echo non-interactive\n" +
				"end\n",
		},
		{
			"case dollar dash generic branch",
			"case $- in\n" +
				"\t*x*)\n" +
				"\t\techo xtrace\n" +
				"\t\t;;\n" +
				"esac\n",
			"switch $(status is-interactive && echo i || echo '')\n" +
				"case '*x*'\n" +
				"    echo xtrace\n" +
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
			"negated test clause",
			"if ! [[ -f foo ]]; then echo missing; fi\n",
			"if ! test -f foo\n" +
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
				t.Errorf("compound statement mismatch\n in:   %q\n got:  %q\n want: %q",
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
		{"env -S bash", "#!/usr/bin/env -S bash\necho hi\n", "#!/usr/bin/env fish\necho hi\n"},
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
			"subshell inside command substitution",
			"x=$( (cd /tmp && pwd); )\n",
			"subshell isolation",
		},
		{
			"bare set dropped",
			"set\n",
			"statement dropped",
		},
		{
			"single dash set dropped",
			"set -\n",
			"statement dropped",
		},
		{
			"dollar dash standalone warning",
			"echo \"$-\"\n",
			"status subcommands",
		},
		{
			"caller builtin warning",
			"caller 0\n",
			"status print-stack-trace",
		},
		{
			"eval passthrough warning",
			"eval \"echo hello\"\n",
			"eval executes fish syntax",
		},
		{
			"eval in condition warning",
			"if eval \"$cmd\"; then echo ok; fi\n",
			"eval executes fish syntax",
		},
		{
			"eval in command substitution warning",
			"x=$(eval \"$cmd\")\n",
			"eval executes fish syntax",
		},
		{
			"multiple heredocs warning",
			"cmd <<EOF1 <<EOF2\na\nEOF1\nb\nEOF2\n",
			"multiple here-documents on a single command are not supported",
		},
		{
			"high FD redirect on block",
			"{ echo hi; } 3>log\n",
			"redirection to file descriptor 3 on block is not supported in fish",
		},
		{
			"high FD redirect on function",
			"my_func() { echo hi; } 3>log\n",
			"redirection to file descriptor 3 on function is not supported in fish",
		},
		{
			"high FD redirect on builtin",
			"set 3>log\n",
			"redirection to file descriptor 3 on builtin is not supported in fish",
		},
		{
			"background function definition",
			"my_func() { echo hi; } &\n",
			"fish does not support running functions in the background",
		},
		{
			"background function call",
			"my_func() { echo hi; }\nmy_func &\n",
			"fish does not support running functions in the background",
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
// status.
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
			name: "for over command substitution empty output",
			src: "get_empty() { echo \"\"; }\n" +
				"for x in $(get_empty); do\n" +
				"\techo \"item:$x\"\n" +
				"done\n" +
				"echo done\n",
		},
		{
			name: "for over command substitution space-separated",
			src: "get_items() { echo \"item1 item2 item3\"; }\n" +
				"for x in $(get_items); do\n" +
				"\techo \"got:$x\"\n" +
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
			name: "epochseconds and srandom",
			src: "echo \"epoch_ok: $([ $EPOCHSECONDS -gt 1000000000 ] && echo yes)\"\n" +
				"echo \"srandom_ok: $([ $SRANDOM -ge 0 ] && echo yes)\"\n",
		},
		{
			name: "empty command prefix",
			src: "prefix=\"\"\n" +
				"$prefix echo running without prefix\n",
		},
		{
			name: "unset multiple variables with -v flag",
			src: "var1=val1\n" +
				"var2=val2\n" +
				"echo \"before: v1=${var1:-missing} v2=${var2:-missing}\"\n" +
				"unset -v var1 var2\n" +
				"echo \"after: v1=${var1:-missing} v2=${var2:-missing}\"\n",
		},
		{
			name: "set positional with single dash terminator",
			src: "set - first \"second param\"\n" +
				"echo \"$1|$2\"\n" +
				"echo \"count:$#\"\n",
		},
		{
			name: "case dollar dash interactive check in script",
			src: "case $- in\n" +
				"\t*i*) echo interactive ;;\n" +
				"\t*) echo non-interactive ;;\n" +
				"esac\n",
		},
		{
			name: "test clause dollar dash interactive check in script",
			src:  "[[ $- != *i* ]] && echo non-interactive\n",
		},
		{
			name: "getopts parsing arguments",
			src: "while getopts :hqy opt -y -q; do\n" +
				"\techo \"opt:$opt\"\n" +
				"done\n",
		},
		{
			name: "getopts without args uses argv",
			src: "parse() {\n" +
				"    while getopts \"ab:\" opt; do\n" +
				"        echo \"opt:$opt\"\n" +
				"    done\n" +
				"}\n" +
				"parse -a -b val\n" +
				"parse\n",
		},
		{
			name: "ifs inside function splits properly",
			src: "split_paths() {\n" +
				"    IFS=':'\n" +
				"    paths=\"/usr/bin:/bin:/opt/bin\"\n" +
				"    for p in $paths; do\n" +
				"        echo \"p:$p\"\n" +
				"    done\n" +
				"}\n" +
				"split_paths\n",
		},
		{
			name: "ifs inside function local",
			src: "split_local() {\n" +
				"    local IFS=':'\n" +
				"    paths=\"/usr/bin:/bin:/opt/bin\"\n" +
				"    for p in $paths; do\n" +
				"        echo \"local_p:$p\"\n" +
				"    done\n" +
				"}\n" +
				"split_local\n",
		},
		{
			name: "negated test clause condition",
			src: "if ! [[ \"a\" == \"b\" ]]; then\n" +
				"    echo \"different\"\n" +
				"fi\n",
		},
		{
			name: "export with expansion equivalence",
			src: "FOO=original\n" +
				"export FOO=\"${FOO:-fallback}\"\n" +
				"echo \"FOO:$FOO\"\n" +
				"export UNSET_VAR=\"${UNSET_VAR:-fallback}\"\n" +
				"echo \"UNSET:$UNSET_VAR\"\n",
		},
		{
			name: "undefined variable length is zero",
			src: "unset undef_var\n" +
				"echo \"len_undef: ${#undef_var}\"\n" +
				"empty_var=\"\"\n" +
				"echo \"len_empty: ${#empty_var}\"\n" +
				"non_empty_var=\"hello world\"\n" +
				"echo \"len_val: ${#non_empty_var}\"\n",
		},
		{
			name: "quoted star vs quoted at",
			src: "check_params() {\n" +
				"    echo \"count: $#\"\n" +
				"    echo \"first: $1\"\n" +
				"}\n" +
				"run_tests() {\n" +
				"    echo \"--- quoted at ---\"\n" +
				"    check_params \"$@\"\n" +
				"    echo \"--- quoted star ---\"\n" +
				"    check_params \"$*\"\n" +
				"}\n" +
				"run_tests \"first arg\" \"second arg\" \"third arg\"\n",
		},
		{
			name: "case modification uppercase and lowercase",
			src: "v=\"Hello, World! 123\"\n" +
				"echo \"upper: ${v^^}\"\n" +
				"echo \"lower: ${v,,}\"\n" +
				"unset undef\n" +
				"echo \"undef_upper: [${undef^^}]\"\n" +
				"echo \"undef_lower: [${undef,,}]\"\n" +
				"empty=\"\"\n" +
				"echo \"empty_upper: [${empty^^}]\"\n" +
				"echo \"empty_lower: [${empty,,}]\"\n",
		},
		{
			name: "complex positional parameter expansion",
			src: "test_params() {\n" +
				"    echo \"def:${1:-fallback}\"\n" +
				"    echo \"len:${#1}\"\n" +
				"    echo \"sub:${1:0:4}\"\n" +
				"    echo \"rep:${1/foo/bar}\"\n" +
				"}\n" +
				"test_params \"foobaz\"\n" +
				"test_params\n",
		},
		{
			name: "process substitution in double quotes",
			src: "file=\"<(printf 'psub_content\\n')\"\n" +
				"cat $file\n",
		},
		{
			name: "custom ifs multi delimiter with minus n",
			src: "IFS=\" :,\"\n" +
				"str=\"foo:bar -n,baz\"\n" +
				"for x in $str; do\n" +
				"    echo \"item:$x\"\n" +
				"done\n",
		},
		{
			name: "getopts in function with local opt",
			src: "parse_opts() {\n" +
				"    local opt\n" +
				"    while getopts \"ab:\" opt; do\n" +
				"        echo \"local_opt:$opt\"\n" +
				"    done\n" +
				"}\n" +
				"parse_opts -a -b hello\n",
		},
		{
			name: "shift 0 does not remove arguments",
			src: "test_shift() {\n" +
				"    shift 0\n" +
				"    echo \"first:$1\"\n" +
				"}\n" +
				"test_shift keep_me\n",
		},
		{
			name: "empty for in loop does not execute body",
			src: "for x in; do\n" +
				"    echo \"should not run $x\"\n" +
				"done\n" +
				"echo \"finished empty loop\"\n",
		},
		{
			name: "user variable pipestatus collision",
			src: "pipestatus=42\n" +
				"echo \"pipestatus:$pipestatus\"\n" +
				"true | false\n" +
				"echo \"system_pipestatus:${PIPESTATUS[1]}\"\n",
		},
		{
			name: "substring on empty variable does not hang",
			src: "s=\"\"\n" +
				"echo \"empty_sub:[${s:0:2}]\"\n",
		},
		{
			name: "negative substring and slice offset",
			src: "s=\"hello world\"\n" +
				"echo \"sub1:${s: -3}\"\n" +
				"echo \"sub2:${s: -5:2}\"\n" +
				"arr=(alpha beta gamma delta)\n" +
				"echo \"arr1:${arr[@]: -2}\"\n" +
				"echo \"arr2:${arr[@]: -3:1}\"\n",
		},
		{
			name: "quoted star preserves single string merge",
			src: "print_args() {\n" +
				"    echo \"count:$#\"\n" +
				"    for a in \"$@\"; do\n" +
				"        echo \"arg:[$a]\"\n" +
				"    done\n" +
				"}\n" +
				"items=(foo bar baz)\n" +
				"print_args \"${items[*]}\"\n" +
				"print_args \"${items[@]}\"\n",
		},
		{
			name: "shift 0 in condition and block",
			src: "test_shift0() {\n" +
				"    if true; then\n" +
				"        shift 0\n" +
				"    fi\n" +
				"    shift 0 && echo \"after shift 0\"\n" +
				"}\n" +
				"test_shift0\n",
		},
		{
			name: "single bracket var test -v",
			src: "FOO=bar\n" +
				"if [ -v FOO ]; then\n" +
				"    echo \"FOO is set\"\n" +
				"fi\n" +
				"if [ ! -v BAR ]; then\n" +
				"    echo \"BAR is not set\"\n" +
				"fi\n" +
				"test -v FOO && echo \"test FOO is set\"\n",
		},
		{
			name: "custom ifs with read builtin",
			src: "split_read() {\n" +
				"    IFS=':'\n" +
				"    read a b c <<<'one:two:three'\n" +
				"    echo \"a=$a b=$b c=$c\"\n" +
				"}\n" +
				"split_read\n",
		},
		{
			name: "backslash command alias bypass",
			src: "\\echo hello world\n" +
				"if ! \\which which >/dev/null 2>&1; then\n" +
				"\techo missing\n" +
				"else\n" +
				"\t\\echo ok\n" +
				"fi\n",
		},
		{
			name: "double quoted backticks",
			src: "echo \"Version 'lts' not found - try \\`nvm ls-remote\\` to browse available versions.\"\n" +
				"cmd=\"ls-remote\"\n" +
				"echo \"try \\`nvm $cmd\\`\"\n",
		},
		{
			name: "case with bracket character classes",
			src: "check_line() {\n" +
				"  case \"$1\" in\n" +
				"    *[![:space:]]*)\n" +
				"      echo \"non-empty: $1\"\n" +
				"      ;;\n" +
				"    *)\n" +
				"      echo \"empty or whitespace\"\n" +
				"      ;;\n" +
				"  esac\n" +
				"}\n" +
				"check_line 'hello'\n" +
				"check_line '  '\n" +
				"check_line ''\n" +
				"check_line 'a b'\n",
		},
		{
			name: "case with bracket negation pattern",
			src: "clean_dir() {\n" +
				"  case \"$1\" in\n" +
				"    *[!/]*/)\n" +
				"      echo \"trailing slash: $1\"\n" +
				"      ;;\n" +
				"    *)\n" +
				"      echo \"no trailing slash: $1\"\n" +
				"      ;;\n" +
				"  esac\n" +
				"}\n" +
				"clean_dir '/foo/bar/'\n" +
				"clean_dir '/foo/bar'\n" +
				"clean_dir '/'\n",
		},
		{
			name: "hash builtin command existence check",
			src: "if hash ls 2>/dev/null; then echo \"ls found\"; fi\n" +
				"if hash nonexistent_binary_xyz_123 2>/dev/null; then echo \"found\"; else echo \"not found\"; fi\n" +
				"hash ls 2>/dev/null && echo \"ls ok\"\n" +
				"hash nonexistent_binary_xyz_123 2>/dev/null || echo \"fallback ok\"\n" +
				"hash -r 2>/dev/null && echo \"hash -r ok\"\n" +
				"\\hash -r 2>/dev/null && echo \"backslash hash -r ok\"\n" +
				"hash ls nonexistent_binary_xyz_123 2>/dev/null || echo \"mixed hash failed ok\"\n",
		},
		{
			name: "unalias builtin command and pattern",
			src: "check_unalias() {\n" +
				"    unalias \"${1-}\" 2>/dev/null || true\n" +
				"    echo \"cleared\"\n" +
				"}\n" +
				"check_unalias \"ls\"\n" +
				"check_unalias \"\"\n" +
				"unalias -a 2>/dev/null || true\n" +
				"echo \"unalias done\"\n",
		},
		{
			name: "source and dot builtin commands",
			files: map[string]string{
				"sub.sh": "echo sourced_ok\n",
			},
			src: "source ./sub.sh\n" +
				". ./sub.sh\n",
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

// TestAssignments covers semantic rewrites of assignments and declaration
// clauses. Every successful path here must also be warning-free; the
// long-option policy applies to all generated set commands.
func TestAssignments(t *testing.T) {
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
			"export with parameter expansion and command substitution",
			"export FOO=\"${BAR:-default}\" BAZ=$(echo hi)\n",
			"export FOO=\"$(test -n \"$BAR\" && printf %s\\n \"$BAR\" || printf %s\\n default)\" BAZ=$(echo hi)\n",
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
			"declare in function with -g is global",
			"f() {\n\tdeclare -g n=2\n}\n",
			"function f\n    set --global n 2\nend\n",
		},
		{
			"self-referential accumulation preserves scalar string",
			"OPTS=\"--silent\"\nOPTS=\"$OPTS --netrc\"\n",
			"set OPTS \"--silent\"\nset OPTS \"$OPTS --netrc\"\n",
		},
		{
			"braced self-referential accumulation preserves scalar string",
			"OPTS=\"--silent\"\nOPTS=\"${OPTS} --netrc\"\n",
			"set OPTS \"--silent\"\nset OPTS \"$OPTS --netrc\"\n",
		},
		{
			"command substitution accumulation preserves scalar string",
			"ARGS=\"-a\"\nARGS=\"$ARGS $(get_flags)\"\n",
			"set ARGS \"-a\"\nset ARGS \"$ARGS $(get_flags)\"\n",
		},
		{
			"flag list literal preserves scalar string",
			"FLAGS=\"--retry 3 -C -\"\n",
			"set FLAGS \"--retry 3 -C -\"\n",
		},
		{
			"adjacent value concatenates in one word",
			"OPTS=x\nOPTS=\"$OPTS --file=$F\"\n",
			"set OPTS x\nset OPTS \"$OPTS --file=$F\"\n",
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
				t.Errorf("assignment mismatch\n in:   %q\n got:  %q\n want: %q",
					tc.in, got, tc.want)
			}
		})
	}
}
func TestVariableMangling(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"status assignment and expansion mangled to _status",
			"status=\"active\"\necho $status\n",
			"set _status \"active\"\necho $_status\n",
		},
		{
			"version assignment and expansion mangled to _version",
			"version=\"1.0\"\necho $version\n",
			"set _version \"1.0\"\necho $_version\n",
		},
		{
			"for loop underscore variable mangled to _unused",
			"for _ in a b; do echo ok; done\n",
			"for _unused in a b\n    echo ok\nend\n",
		},
		{
			"underscore assignment and expansion mangled to _unused",
			"_=\"foo\"\necho $_\n",
			"set _unused \"foo\"\necho $_unused\n",
		},
		{
			"bare underscore expansion mangled to _unused",
			"echo $_\n",
			"echo $_unused\n",
		},
		{
			"for loop status variable mangled to _status",
			"for status in a b; do echo $status; done\n",
			"for _status in a b\n    echo $_status\nend\n",
		},
		{
			"history variable mangled to _history",
			"history=\"cmd1\"\necho $history\n",
			"set _history \"cmd1\"\necho $_history\n",
		},
		{
			"hostname variable mangled to _hostname",
			"hostname=\"srv1\"\necho $hostname\n",
			"set _hostname \"srv1\"\necho $_hostname\n",
		},
		{
			"CMD_DURATION mangled to _CMD_DURATION",
			"CMD_DURATION=\"100\"\necho $CMD_DURATION\n",
			"set _CMD_DURATION \"100\"\necho $_CMD_DURATION\n",
		},
		{
			"status_generation mangled to _status_generation",
			"status_generation=1\necho $status_generation\n",
			"set _status_generation 1\necho $_status_generation\n",
		},
		{
			"mutable variables like HOME and USER are not mangled",
			"HOME=\"/tmp\"\nUSER=\"alice\"\necho $HOME $USER\n",
			"set HOME \"/tmp\"\nset USER \"alice\"\necho $HOME $USER\n",
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

func TestListValuedEnvVars(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		want     string
		contains string
	}{
		{
			name: "for loop over PATH untouched",
			in:   "for p in $PATH; do echo $p; done\n",
			want: "for p in $PATH\n    echo $p\nend\n",
		},
		{
			name: "for loop over CDPATH untouched",
			in:   "for p in $CDPATH; do echo $p; done\n",
			want: "for p in $CDPATH\n    echo $p\nend\n",
		},
		{
			name: "for loop over MANPATH untouched",
			in:   "for p in $MANPATH; do echo $p; done\n",
			want: "for p in $MANPATH\n    echo $p\nend\n",
		},
		{
			name: "for loop over custom PATH suffix variable untouched",
			in:   "for p in $PKG_CONFIG_PATH; do echo $p; done\n",
			want: "for p in $PKG_CONFIG_PATH\n    echo $p\nend\n",
		},
		{
			name:     "for loop over non-PATH variable wrapped in bait_words",
			in:       "for p in $DIR_LIST; do echo $p; done\n",
			contains: "for p in (__bait_words $DIR_LIST)",
		},
		{
			name: "switch on PATH quoted",
			in:   "case $PATH in *) echo match;; esac\n",
			want: "switch \"$PATH\"\ncase '*'\n    echo match\nend\n",
		},
		{
			name: "switch on CDPATH quoted",
			in:   "case $CDPATH in *) echo match;; esac\n",
			want: "switch \"$CDPATH\"\ncase '*'\n    echo match\nend\n",
		},
		{
			name: "switch on custom PATH suffix variable quoted",
			in:   "case $PKG_CONFIG_PATH in *) echo match;; esac\n",
			want: "switch \"$PKG_CONFIG_PATH\"\ncase '*'\n    echo match\nend\n",
		},
		{
			name: "switch on non-PATH variable unquoted",
			in:   "case $DIR_LIST in *) echo match;; esac\n",
			want: "switch $DIR_LIST\ncase '*'\n    echo match\nend\n",
		},
		{
			name: "for loop over LANGUAGE untouched",
			in:   "for l in $LANGUAGE; do echo $l; done\n",
			want: "for l in $LANGUAGE\n    echo $l\nend\n",
		},
		{
			name: "switch on LANGUAGE quoted",
			in:   "case $LANGUAGE in *) echo match;; esac\n",
			want: "switch \"$LANGUAGE\"\ncase '*'\n    echo match\nend\n",
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
			if tc.contains != "" && !strings.Contains(string(got), tc.contains) {
				t.Errorf("expected output to contain %q, got:\n%s", tc.contains, got)
			}
			if tc.want != "" && string(got) != tc.want {
				t.Errorf("mismatch\n in:   %q\n got:  %q\n want: %q",
					tc.in, got, tc.want)
			}
		})
	}
}

func TestPseudoArrayContextVars(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"BASH_SOURCE index 0",
			"echo ${BASH_SOURCE[0]}\n",
			"echo $(status filename)\n",
		},
		{
			"BASH_SOURCE index @",
			"echo ${BASH_SOURCE[@]}\n",
			"echo $(status filename)\n",
		},
		{
			"FUNCNAME index 0",
			"echo ${FUNCNAME[0]}\n",
			"echo $(status current-function)\n",
		},
		{
			"GROUPS index 0",
			"echo ${GROUPS[0]}\n",
			"echo $(id -g)\n",
		},
		{
			"regular array index untouched offset",
			"echo ${arr[0]}\n",
			"echo $arr[1]\n",
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

// TestDeclarationWarnings covers declaration forms without a faithful fish
// mapping; they pass through verbatim and must be reported.
func TestDeclarationWarnings(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantText string
	}{
		{"readonly clause", "readonly MAX=10\n", "readonly"},
		{"dynamic array index", "arr[$i]=x\n", "dynamic array index"},
		{"export flag", "export -n EDITOR\n", "not supported by fish's export"},
		{"assert assignment", "cmd ${x:=d}\n", "no fish equivalent"},
		{"dynamic array index in unset", "unset 'arr[$i]'\n", "dynamic array index"},
		{"dynamic array index in test -v", "[[ -v arr[$i] ]]\n", "dynamic array index"},
		{"case modification with pattern", "echo ${v^^[a-z]}\n", "case modification with pattern"},
		{"single character case modification", "echo ${v^}\n", "no fish equivalent"},
		{"declare readonly flag", "declare -r MAX=10\n", "readonly"},
		{"typeset readonly flag", "typeset -r MAX=10\n", "readonly"},
		{"local readonly flag in function", "f() { local -r x=1; }\n", "readonly"},
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

// TestParameterExpansions covers fish-side rewrites of parameter expansions.
func TestParameterExpansions(t *testing.T) {
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
		{
			"quoted star keeps quotes",
			"run \"$*\"\n",
			"run \"$argv\"\n",
		},
		{
			"quoted star braced keeps quotes",
			"run \"${*}\"\n",
			"run \"$argv\"\n",
		},
		{
			"quoted at braced drops quotes",
			"run \"${@}\"\n",
			"run $argv\n",
		},
		{"unquoted star becomes argv", "run $*\n", "run $argv\n"},
		{"UID maps to id -u and EUID is native", "echo $UID $EUID ${UID}\n", "echo $(id -u) $EUID $(id -u)\n"},
		{"GROUPS maps to id -g", "echo $GROUPS ${GROUPS[0]}\n", "echo $(id -g) $(id -g)\n"},
		{"HOSTNAME maps to hostname", "echo \"host: $HOSTNAME ${HOSTNAME}\"\n", "echo \"host: $hostname $hostname\"\n"},
		{"HOSTTYPE and MACHTYPE maps to uname -m", "echo $HOSTTYPE $MACHTYPE\n", "echo $(uname -m) $(uname -m)\n"},
		{"RANDOM maps to random", "echo $RANDOM ${RANDOM}\n", "echo $(random) $(random)\n"},
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
		{
			"BASH and BASH_ARGV0 maps to status",
			"echo $BASH $BASH_ARGV0\n",
			"echo $(status fish-path) $(status filename)\n",
		},
		{
			"BASH_COMMAND maps to status current-command",
			"echo $BASH_COMMAND\n",
			"echo $(status current-command)\n",
		},
		{
			"BASH_VERSION maps to echo 5.2.0",
			"echo $BASH_VERSION \"${BASH_VERSION}\"\n",
			"echo $(echo 5.2.0) \"$(echo 5.2.0)\"\n",
		},
		{
			"DIRSTACK maps to dirstack",
			"echo $DIRSTACK ${DIRSTACK[0]} ${DIRSTACK[@]} ${#DIRSTACK[@]}\n",
			"echo $dirstack $dirstack[1] $dirstack $(count $dirstack)\n",
		},
		{
			"EPOCHSECONDS and SRANDOM maps to date and random",
			"echo $EPOCHSECONDS $SRANDOM\n",
			"echo $(date +%s) $(random 0 4294967295)\n",
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

// TestArithmetic covers bash integer arithmetic mapped onto fish math
// and test.
func TestArithmetic(t *testing.T) {
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

// TestParameterOperators covers ${var...} operator rewrites into pure command
// substitutions.
func TestParameterOperators(t *testing.T) {
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
			"echo $(string replace --regex -- '\\.txt$' '' $f)\n",
		},
		{
			"greedy suffix strip",
			"echo ${f%%.*}\n",
			"echo $(string replace --regex -- '\\..*$' '' $f)\n",
		},
		{
			"greedy prefix strip",
			"echo ${p##*/}\n",
			"echo $(string replace --regex -- '^.*/' '' $p)\n",
		},
		{
			"replace first",
			"echo ${s/o/0}\n",
			"echo $(string replace --regex -- 'o' '0' $s)\n",
		},
		{
			"replace all",
			"echo ${s//o/0}\n",
			"echo $(string replace --regex --all -- 'o' '0' $s)\n",
		},
		{
			"substring",
			"echo ${s:2:3}\n",
			"echo $(string sub --start=3 --length=3 -- \"$s\")\n",
		},
		{
			"substring negative offset",
			"echo ${s: -3}\n",
			"echo $(string sub --start=-3 -- \"$s\")\n",
		},
		{
			"substring negative offset with length",
			"echo ${s: -3:2}\n",
			"echo $(string sub --start=-3 --length=2 -- \"$s\")\n",
		},
		{
			"replace bracket class",
			"echo ${s/[0-9]/d}\n",
			"echo $(string replace --regex -- '[0-9]' 'd' $s)\n",
		},
		{
			"prefix strip negated bracket class",
			"echo ${p#[!a-z]}\n",
			"echo $(string replace --regex -- '^[^a-z]' '' $p)\n",
		},
		{
			"replace with single quote",
			"echo ${s/foo/'bar'}\n",
			"echo $(string replace --regex -- 'foo' '\\'bar\\'' $s)\n",
		},
		{"length", "echo ${#s}\n", "echo $(string length -- \"$s\")\n"},
		{"uppercase all", "echo ${v^^}\n", "echo $(string upper -- \"$v\")\n"},
		{"lowercase all", "echo ${v,,}\n", "echo $(string lower -- \"$v\")\n"},
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

// TestArrays covers bash array operations mapped onto fish lists,
// including the 0-based to 1-based index shift.
func TestArrays(t *testing.T) {
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
		{"whole list star", "echo ${arr[*]}\n", "echo $arr\n"},
		{
			"quoted whole list star keeps quotes",
			"run \"${arr[*]}\"\n",
			"run \"$arr\"\n",
		},
		{"list slice negative offset", "echo ${arr[@]: -2:1}\n", "echo $arr[-2..-2]\n"},
		{"list slice negative offset to end", "echo ${arr[@]: -2}\n", "echo $arr[-2..-1]\n"},
		{"list slice star negative offset", "echo ${arr[*]: -3:2}\n", "echo $arr[-3..-2]\n"},
		{"list count star", "echo ${#arr[*]}\n", "echo $(count $arr)\n"},
		{"list slice star", "echo ${arr[*]:1:2}\n", "echo $arr[2..3]\n"},
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
		{"single dash terminator", "set - alpha beta\n", "set argv alpha beta\n"},
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
		{"shift 0 is no-op", "shift 0\n", "true\n"},
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
			"echo $(test -n \"$A\" && printf %s\\n \"$A\" || printf %s\\n \"x$(string replace --regex -- '/$' '' $B)y\")\n",
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
		{"unset negative array element", "unset 'arr[-1]'\n", "set --erase arr[-1]\n"},
		{"unset in chain", "test -n \"$x\" && unset x\n", "test -n \"$x\" && set --erase x\n"},
		{"unset mixed -f and -v", "unset -f f1 f2 -v v1 v2\n", "functions --erase f1 f2\nset --erase v1 v2\n"},
		{"unset mixed var and func", "unset v1 -f f1\n", "set --erase v1\nfunctions --erase f1\n"},
		{"unset mixed interleaved", "unset -f f1 -v v1 -f f2\n", "functions --erase f1\nset --erase v1\nfunctions --erase f2\n"},
		{"unset mixed in chain", "test -n \"$x\" && unset -f f1 -v v1\n", "test -n \"$x\" && begin; functions --erase f1; set --erase v1; end\n"},
		{"unset PIPESTATUS special var", "unset PIPESTATUS\n", "set --erase pipestatus\n"},
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

func TestSubshell(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"basic subshell", "(cd /tmp && ls)\n", "begin\n    cd /tmp && ls\nend\n"},
		{"multiple commands", "(cd /tmp; pwd)\n", "begin\n    cd /tmp\n    pwd\nend\n"},
		{"subshell with redirection", "(cd /tmp && pwd) > /tmp/out\n", "begin\n    cd /tmp && pwd\nend > /tmp/out\n"},
		{"subshell in chain", "(exit 0) && echo ok\n", "begin\n    exit 0\nend && echo ok\n"},
		{"pipe into subshell", "echo hello | (cat)\n", "echo hello | begin\n    cat\nend\n"},
		{"subshell inside command substitution", "x=$( (cd /tmp && pwd) )\n", "set x $(begin\n    cd /tmp && pwd\nend)\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := Translate([]byte(tc.in))
			if err != nil {
				t.Fatalf("Translate(%q) error: %v", tc.in, err)
			}
			if len(warnings) != 1 {
				t.Errorf("expected 1 warning, got: %v", warnings)
			}
			if string(got) != tc.want {
				t.Errorf("mismatch\n in: %q\n got: %q\n want: %q", tc.in, got, tc.want)
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
			"string match --quiet -- 'b*' $a\n",
		},
		{
			"glob no match",
			"[[ $a != b* ]]\n",
			"! string match --quiet -- 'b*' $a\n",
		},
		{
			"regex match",
			"[[ $str =~ ^[0-9]+$ ]]\n",
			"string match --regex --quiet -- '^[0-9]+$' $str\n",
		},
		{
			"interactive shell check glob",
			"[[ $- == *i* ]]\n",
			"status is-interactive\n",
		},
		{
			"interactive shell check quoted glob",
			"[[ $- == *\"i\"* ]]\n",
			"status is-interactive\n",
		},
		{
			"non-interactive shell check",
			"[[ $- != *i* ]]\n",
			"! status is-interactive\n",
		},
		{
			"interactive shell check regex",
			"[[ $- =~ i ]]\n",
			"status is-interactive\n",
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
			"set --query VAR\n",
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
		{
			"single bracket var test -v",
			"[ -v VAR ]\n",
			"set --query VAR\n",
		},
		{
			"single bracket negated var test -v",
			"[ ! -v VAR ]\n",
			"! set --query VAR\n",
		},
		{
			"builtin test var test -v",
			"test -v VAR\n",
			"set --query VAR\n",
		},
		{
			"builtin test negated var test -v",
			"test ! -v VAR\n",
			"! set --query VAR\n",
		},
		{
			"if with single bracket var test -v",
			"if [ -v VAR ]; then echo yes; fi\n",
			"if set --query VAR\n    echo yes\nend\n",
		},
		{
			"single bracket var test in chain",
			"[ -v VAR ] && echo yes\n",
			"set --query VAR && echo yes\n",
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
	for _, helperFunc := range []string{"function source", "function .", "function hash", "function getopts"} {
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

func TestEvalPassthroughWithWarning(t *testing.T) {
	src := "eval \"echo hello\"\n"
	got, warnings, err := Translate([]byte(src))
	if err != nil {
		t.Fatalf("Translate failed: %v", err)
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

func TestMultiWordScalarDeepPropagation(t *testing.T) {
	src := "A=\"a b c\"\n" +
		"B=\"$A\"\n" +
		"C=\"$B\"\n" +
		"D=\"$C\"\n" +
		"for x in $D; do\n" +
		"    echo \"$x\"\n" +
		"done\n"
	got, _, err := Translate([]byte(src))
	if err != nil {
		t.Fatalf("Translate failed: %v", err)
	}
	if !strings.Contains(string(got), "__bait_words $D") {
		t.Errorf("expected __bait_words $D in output, got:\n%s", string(got))
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

func TestBinaryChainAssignmentsAndCompound(t *testing.T) {
	src := "A=1 && [ -f x ] && B=2 && if true; then echo hi; fi\n"
	got, _, err := Translate([]byte(src))
	if err != nil {
		t.Fatalf("Translate failed: %v", err)
	}
	if !strings.Contains(string(got), "set A 1") || !strings.Contains(string(got), "set B 2") {
		t.Errorf("expected set commands for assignments in chain, got:\n%s", string(got))
	}
}

func TestDynamicPatternExpansion(t *testing.T) {
	src := "echo \"${1#\"$PREFIX\"-}\"\n"
	got, _, err := Translate([]byte(src))
	if err != nil {
		t.Fatalf("Translate failed: %v", err)
	}
	if !strings.Contains(string(got), `string escape --style=regex -- "$PREFIX"`) {
		t.Errorf("expected dynamic regex escape with quotes, got:\n%s", string(got))
	}

	// Test dynamic suffix strip
	suffixSrc := "echo \"${1%\"$SUFFIX\"}\"\n"
	gotSuffix, _, err := Translate([]byte(suffixSrc))
	if err != nil {
		t.Fatalf("Translate suffix failed: %v", err)
	}
	if !strings.Contains(string(gotSuffix), `string escape --style=regex -- "$SUFFIX"`) {
		t.Errorf("expected dynamic suffix regex escape, got:\n%s", string(gotSuffix))
	}

	// Test dynamic pattern replace
	replSrc := "echo \"${1/\"$PAT\"/repl}\"\n"
	gotRepl, _, err := Translate([]byte(replSrc))
	if err != nil {
		t.Fatalf("Translate repl failed: %v", err)
	}
	if !strings.Contains(string(gotRepl), `string escape --style=regex -- "$PAT"`) {
		t.Errorf("expected dynamic replace regex escape, got:\n%s", string(gotRepl))
	}
}

func TestDollarDashParameterExpansion(t *testing.T) {
	src := "if [ \"${-#*e}\" != \"$-\" ]; then echo errexit; fi\n"
	got, _, err := Translate([]byte(src))
	if err != nil {
		t.Fatalf("Translate failed: %v", err)
	}
	if strings.Contains(string(got), "$-") {
		t.Errorf("output should not contain raw $- variable in fish, got:\n%s", string(got))
	}
	if !strings.Contains(string(got), "status is-interactive") {
		t.Errorf("expected status is-interactive check in output, got:\n%s", string(got))
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

func TestExitCodeAssignmentNotMangled(t *testing.T) {
	src := "EXIT_CODE=\"$?\"\n"
	got, _, err := Translate([]byte(src))
	if err != nil {
		t.Fatalf("Translate failed: %v", err)
	}
	gotStr := string(got)
	if !strings.Contains(gotStr, "set EXIT_CODE $status") {
		t.Errorf("expected 'set EXIT_CODE $status', got:\n%s", gotStr)
	}
}

func TestVariableFollowedByIdentifierChar(t *testing.T) {
	t.Run("plain_variable", func(t *testing.T) {
		src := "echo -x${tar_compression_flag}f\n"
		got, _, err := Translate([]byte(src))
		if err != nil {
			t.Fatalf("Translate failed: %v", err)
		}
		gotStr := string(got)
		if !strings.Contains(gotStr, "echo -x{$tar_compression_flag}f") {
			t.Errorf("expected 'echo -x{$tar_compression_flag}f', got:\n%s", gotStr)
		}
	})

	t.Run("positional_parameter_does_not_emit_invalid_fish_var", func(t *testing.T) {
		src := "echo ${1}f\n"
		got, _, err := Translate([]byte(src))
		if err != nil {
			t.Fatalf("Translate failed: %v", err)
		}
		gotStr := string(got)
		if strings.Contains(gotStr, "{$1}") {
			t.Errorf("must not emit {$1} in fish, got:\n%s", gotStr)
		}
		if !strings.Contains(gotStr, "$argv[1]f") {
			t.Errorf("expected '$argv[1]f', got:\n%s", gotStr)
		}
	})

	t.Run("special_status_and_pid_variables", func(t *testing.T) {
		src := "echo ${?}x ${!}x ${PIPESTATUS}x ${status}x\n"
		got, _, err := Translate([]byte(src))
		if err != nil {
			t.Fatalf("Translate failed: %v", err)
		}
		gotStr := string(got)
		if !strings.Contains(gotStr, "{$status}x") {
			t.Errorf("expected '{$status}x', got:\n%s", gotStr)
		}
		if !strings.Contains(gotStr, "{$last_pid}x") {
			t.Errorf("expected '{$last_pid}x', got:\n%s", gotStr)
		}
		if !strings.Contains(gotStr, "{$pipestatus}x") {
			t.Errorf("expected '{$pipestatus}x', got:\n%s", gotStr)
		}
		if !strings.Contains(gotStr, "{$_status}x") {
			t.Errorf("expected '{$_status}x', got:\n%s", gotStr)
		}
	})
}

func TestExportQuotedValuePreserved(t *testing.T) {
	src := "export PATH=\"${NEWPATH}\"\n"
	got, _, err := Translate([]byte(src))
	if err != nil {
		t.Fatalf("Translate failed: %v", err)
	}
	gotStr := string(got)
	if !strings.Contains(gotStr, "export PATH=\"$NEWPATH\"") {
		t.Errorf("expected 'export PATH=\"$NEWPATH\"', got:\n%s", gotStr)
	}
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

func TestCaseWithBracketPatterns(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bracket character class converted to if",
			in: "case \"$line\" in\n" +
				"  *[![:space:]]*)\n" +
				"    echo yes\n" +
				"    ;;\n" +
				"  *)\n" +
				"    echo no\n" +
				"    ;;\n" +
				"esac\n",
			want: "if string match --regex --quiet -- '^.*[^[:space:]].*$' \"$line\"\n" +
				"    echo yes\n" +
				"else\n" +
				"    echo no\n" +
				"end\n",
		},
		{
			name: "bracket pattern without wildcard converted to if",
			in: "case \"$v\" in\n" +
				"  [0-9]) echo digit ;;\n" +
				"  [a-z]) echo lower ;;\n" +
				"  *) echo other ;;\n" +
				"esac\n",
			want: "if string match --regex --quiet -- '^[0-9]$' \"$v\"\n" +
				"    echo digit\n" +
				"else if string match --regex --quiet -- '^[a-z]$' \"$v\"\n" +
				"    echo lower\n" +
				"else\n" +
				"    echo other\n" +
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
				t.Errorf("mismatch\n in:   %q\n got:  %q\n want: %q", tc.in, got, tc.want)
			}
		})
	}
}
func TestCommentPreservation(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "single assignment comments",
			in: "# before assign\n" +
				"foo=bar # inline assign\n",
			want: "# before assign\n" +
				"set foo bar # inline assign\n",
		},
		{
			name: "multi assignment comments",
			in: "# before multi assign\n" +
				"a=1 b=2 # trailing assign\n",
			want: "# before multi assign\n" +
				"set a 1\n" +
				"set b 2 # trailing assign\n",
		},
		{
			name: "export and local comments",
			in: "# before export\n" +
				"export FOO=1 # trailing export\n" +
				"# before local\n" +
				"local BAR=2 # trailing local\n",
			want: "# before export\n" +
				"export FOO=1 # trailing export\n" +
				"# before local\n" +
				"set --function BAR 2 # trailing local\n",
		},
		{
			name: "function declaration comments",
			in: "# before func\n" +
				"myfunc() {\n" +
				"    # inside func\n" +
				"    echo 1 # inline echo\n" +
				"} # trailing func\n",
			want: "# before func\n" +
				"function myfunc\n" +
				"    # inside func\n" +
				"    echo 1 # inline echo\n" +
				"end # trailing func\n",
		},
		{
			name: "if statement comments",
			in: "# before if\n" +
				"if true; then\n" +
				"    # inside then\n" +
				"    echo a\n" +
				"fi # trailing fi\n",
			want: "# before if\n" +
				"if true\n" +
				"    # inside then\n" +
				"    echo a\n" +
				"end # trailing fi\n",
		},
		{
			name: "while loop comments",
			in: "# before while\n" +
				"while false; do\n" +
				"    # inside while\n" +
				"    echo b\n" +
				"done # trailing while\n",
			want: "# before while\n" +
				"while false\n" +
				"    # inside while\n" +
				"    echo b\n" +
				"end # trailing while\n",
		},
		{
			name: "for loop header and trailing comments",
			in: "# before for\n" +
				"for x in 1 2; do # header for\n" +
				"    echo $x\n" +
				"done # trailing for\n",
			want: "# before for\n" +
				"for x in 1 2 # header for\n" +
				"    echo $x\n" +
				"end # trailing for\n",
		},
		{
			name: "case statement comments",
			in: "# before case\n" +
				"case $x in\n" +
				"    a)\n" +
				"        echo match-a\n" +
				"        ;;\n" +
				"esac # trailing case\n",
			want: "# before case\n" +
				"switch $x\n" +
				"case a\n" +
				"    echo match-a\n" +
				"end # trailing case\n",
		},
		{
			name: "shift and unset comments",
			in: "# before shift\n" +
				"shift 2 # trailing shift\n" +
				"# before unset\n" +
				"unset myvar # trailing unset\n",
			want: "# before shift\n" +
				"set --erase argv[1..2] # trailing shift\n" +
				"# before unset\n" +
				"set --erase myvar # trailing unset\n",
		},
		{
			name: "heredoc pipeline comments",
			in: "# before hdoc\n" +
				"cat <<EOF # trailing hdoc\n" +
				"hello\n" +
				"EOF\n",
			want: "# before hdoc\n" +
				"printf '%s\\n' \"hello\" | cat # trailing hdoc\n",
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
