package bait

import (
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
			"{\n" +
				"    echo a\n" +
				"    echo b\n" +
				"} > out.txt\n",
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
			"{\n" +
				"    sleep 1\n" +
				"} &\n",
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
			"high FD redirect on builtin function like dirs",
			"dirs 3>log\n",
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

func TestExternalCommandHighFDNoWarning(t *testing.T) {
	// Commands that do not overlap with Fish builtins should not trigger builtin high FD warning.
	src := "vared 3>log\n"
	_, warnings, err := Translate([]byte(src))
	if err != nil {
		t.Fatalf("Translate failed: %v", err)
	}
	for _, w := range warnings {
		if strings.Contains(w.Text, "on builtin") {
			t.Errorf("unexpected builtin high FD warning for external command: %v", w)
		}
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

func TestSubshell(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"basic subshell", "(cd /tmp && ls)\n", "{\n    cd /tmp && ls\n}\n"},
		{"multiple commands", "(cd /tmp; pwd)\n", "{\n    cd /tmp\n    pwd\n}\n"},
		{"subshell with redirection", "(cd /tmp && pwd) > /tmp/out\n", "{\n    cd /tmp && pwd\n} > /tmp/out\n"},
		{"subshell in chain", "(exit 0) && echo ok\n", "{\n    exit 0\n} && echo ok\n"},
		{"pipe into subshell", "echo hello | (cat)\n", "echo hello | {\n    cat\n}\n"},
		{"subshell inside command substitution", "x=$( (cd /tmp && pwd) )\n", "set x $({\n    cd /tmp && pwd\n})\n"},
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
				"unset myvar # trailing unset\n",
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
			got, _, err := TranslateWithOptions([]byte(tc.in), Options{NoHelpers: true})
			if err != nil {
				t.Fatalf("Translate(%q) error: %v", tc.in, err)
			}
			if string(got) != tc.want {
				t.Errorf("mismatch\n in:   %q\n got:  %q\n want: %q", tc.in, got, tc.want)
			}
		})
	}
}
