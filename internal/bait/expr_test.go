package bait

import (
	"strings"
	"testing"
)

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
			"{\n    set i $(math --scale=0 \"$i + 1\" 2> /dev/null || echo 0)\n    test $(math --scale=0 \"$i - 1\" 2> /dev/null || echo 0) -ne 0\n}\n",
		},
		{
			"pre-decrement",
			"((--i))\n",
			"{\n    set i $(math --scale=0 \"$i - 1\" 2> /dev/null || echo 0)\n    test $(math --scale=0 \"$i\" 2> /dev/null || echo 0) -ne 0\n}\n",
		},
		{
			"compound assign",
			"((n += 2))\n",
			"{\n    set n $(math --scale=0 \"$n + 2\" 2> /dev/null || echo 0)\n    test $(math --scale=0 \"$n\" 2> /dev/null || echo 0) -ne 0\n}\n",
		},
		{
			"assign expression",
			"((total = a * b + 1))\n",
			"{\n    set total $(math --scale=0 \"$a * $b + 1\" 2> /dev/null || echo 0)\n    test $(math --scale=0 \"$total\" 2> /dev/null || echo 0) -ne 0\n}\n",
		},
		{
			"unary minus value",
			"((d = -x))\n",
			"{\n    set d $(math --scale=0 \"-$x\" 2> /dev/null || echo 0)\n    test $(math --scale=0 \"$d\" 2> /dev/null || echo 0) -ne 0\n}\n",
		},
		{
			"comparison becomes test",
			"((n > 0))\n",
			"test $(math --scale=0 \"$n\" 2> /dev/null || echo 0) -gt $(math --scale=0 \"0\" 2> /dev/null || echo 0)\n",
		},
		{
			"truthiness of a variable",
			"((count))\n",
			"test $(math --scale=0 \"$count\" 2> /dev/null || echo 0) -ne 0\n",
		},
		{
			"in-word expansion",
			"echo $((1 + 2))\n",
			"echo $(math --scale=0 \"1 + 2\" 2> /dev/null || echo 0)\n",
		},
		{
			"variable expansion",
			"echo $((x * 2))\n",
			"echo $(math --scale=0 \"$x * 2\" 2> /dev/null || echo 0)\n",
		},
		{
			"compound comma assignment",
			"(( a = 1, b = 2 ))\n",
			"set a $(math --scale=0 \"1\" 2> /dev/null || echo 0)\n" +
				"{\n    set b $(math --scale=0 \"2\" 2> /dev/null || echo 0)\n" +
				"    test $(math --scale=0 \"$b\" 2> /dev/null || echo 0) -ne 0\n}\n",
		},
		{
			"compound comma increment and assign",
			"(( i++, n += 2, total = a * b ))\n",
			"set i $(math --scale=0 \"$i + 1\" 2> /dev/null || echo 0)\n" +
				"set n $(math --scale=0 \"$n + 2\" 2> /dev/null || echo 0)\n" +
				"{\n    set total $(math --scale=0 \"$a * $b\" 2> /dev/null || echo 0)\n" +
				"    test $(math --scale=0 \"$total\" 2> /dev/null || echo 0) -ne 0\n}\n",
		},
		{
			"compound comma parenthesized",
			"(( (a = 1, b = 2), c = 3 ))\n",
			"set a $(math --scale=0 \"1\" 2> /dev/null || echo 0)\n" +
				"set b $(math --scale=0 \"2\" 2> /dev/null || echo 0)\n" +
				"{\n    set c $(math --scale=0 \"3\" 2> /dev/null || echo 0)\n" +
				"    test $(math --scale=0 \"$c\" 2> /dev/null || echo 0) -ne 0\n}\n",
		},
		{
			"compound comma in if body",
			"if true; then\n  (( a = 1, b = 2 ))\nfi\n",
			"if true\n    set a $(math --scale=0 \"1\" 2> /dev/null || echo 0)\n" +
				"    {\n        set b $(math --scale=0 \"2\" 2> /dev/null || echo 0)\n" +
				"        test $(math --scale=0 \"$b\" 2> /dev/null || echo 0) -ne 0\n    }\nend\n",
		},
		{
			"compound comma with comments",
			"# comment before\n(( a = 1, b = 2 )) # comment after\n",
			"# comment before\nset a $(math --scale=0 \"1\" 2> /dev/null || echo 0)\n" +
				"{\n    set b $(math --scale=0 \"2\" 2> /dev/null || echo 0)\n" +
				"    test $(math --scale=0 \"$b\" 2> /dev/null || echo 0) -ne 0\n} # comment after\n",
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
			"{ test -n \"$a\" || test -n \"$b\"; } && test \"$c\" = \"d\"\n",
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

// TestDollarDashRecognized covers the patterns that are exactly an
// unanchored contains-i test; they translate to the interactivity
// predicate without warnings.
func TestDollarDashRecognized(t *testing.T) {
	tests := []string{
		"[[ $- == *i* ]]\n",
		"[[ $- == *\"i\"* ]]\n",
		"[[ $- != *i* ]]\n",
		"[[ $- =~ i ]]\n",
		"case $- in *i*) echo y;; esac\n",
	}
	for _, in := range tests {
		out, warns, err := Translate([]byte(in))
		if err != nil {
			t.Fatalf("Translate(%q) error: %v", in, err)
		}
		if !strings.Contains(string(out), "status is-interactive") {
			t.Errorf("%q should translate to the interactivity predicate, got %q", in, out)
		}
		if len(warns) != 0 {
			t.Errorf("%q should translate without warnings, got %v", in, warns)
		}
	}
}

// TestDollarDashOverreach guards against position- or case-insensitive
// patterns, multi-pattern items, and regex quantifiers being accepted as
// interactivity tests: bash's only flag translation is an unanchored
// lower-case contains-i, and any other pattern must fall back with a
// warning instead of silently changing branching.
func TestDollarDashOverreach(t *testing.T) {
	tests := []string{
		"[[ $- == i* ]]\n",
		"[[ $- == *i ]]\n",
		"[[ $- == i ]]\n",
		"[[ $- == *I* ]]\n",
		"[[ $- =~ i* ]]\n",
		"case $- in i*) echo y;; esac\n",
		"case $- in *I*) echo y;; esac\n",
		"case $- in *|*i*) echo y;; esac\n",
		"case $- in *i*|*x*) echo y;; esac\n",
	}
	for _, in := range tests {
		out, warns, err := Translate([]byte(in))
		if err != nil {
			t.Fatalf("Translate(%q) error: %v", in, err)
		}
		text := strings.TrimSpace(string(out))
		if text == "status is-interactive" || strings.HasPrefix(text, "status is-interactive\n") ||
			strings.HasPrefix(text, "! status is-interactive") ||
			strings.HasPrefix(text, "if status is-interactive") {
			t.Errorf("%q overreached to the interactivity predicate: %q", in, out)
		}
		if len(warns) == 0 {
			t.Errorf("%q should warn that the $ - test is not the interactive idiom, got %q", in, out)
		}
	}
}
