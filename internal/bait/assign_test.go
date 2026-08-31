package bait

import (
	"strings"
	"testing"
)

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

// TestSetBuiltin covers rewrites of the set builtin: argument forms map
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
		{"shift dynamic", "shift $n\n", "test \"$n\" -gt 0 2>/dev/null; and set --erase argv[1..$n]\n"},
		{"shift 0 is no-op", "shift 0\n", "true\n"},
		{"shift dynamic unquoted", "shift \"$n\"\n", "test \"$n\" -gt 0 2>/dev/null; and set --erase argv[1..$n]\n"},
		{"shift dynamic in chain", "shift $n && echo done\n", "test \"$n\" -gt 0 2>/dev/null; and set --erase argv[1..$n] && echo done\n"},
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

func TestUnsetBuiltin(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"unset variable", "unset x\n", "unset x\n"},
		{"unset multiple variables", "unset a b c\n", "unset a b c\n"},
		{"unset with -v flag", "unset -v x y\n", "unset -v x y\n"},
		{"unset function", "unset -f my_func\n", "unset -f my_func\n"},
		{"unset multiple functions", "unset -f f1 f2\n", "unset -f f1 f2\n"},
		{"unset array element", "unset 'arr[0]'\n", "unset 'arr[0]'\n"},
		{"unset array element unquoted", "unset arr[2]\n", "unset arr[2]\n"},
		{"unset negative array element", "unset 'arr[-1]'\n", "unset 'arr[-1]'\n"},
		{"unset in chain", "test -n \"$x\" && unset x\n", "test -n \"$x\" && unset x\n"},
		{"unset mixed -f and -v", "unset -f f1 f2 -v v1 v2\n", "unset -f f1 f2 -v v1 v2\n"},
		{"unset mixed var and func", "unset v1 -f f1\n", "unset v1 -f f1\n"},
		{"unset mixed interleaved", "unset -f f1 -v v1 -f f2\n", "unset -f f1 -v v1 -f f2\n"},
		{"unset mixed in chain", "test -n \"$x\" && unset -f f1 -v v1\n", "test -n \"$x\" && unset -f f1 -v v1\n"},
		{"unset PIPESTATUS special var", "unset PIPESTATUS\n", "unset PIPESTATUS\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := TranslateWithOptions([]byte(tc.in), Options{NoHelpers: true})
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
