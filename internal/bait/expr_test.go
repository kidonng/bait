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
		{
			"compound comma assignment",
			"(( a = 1, b = 2 ))\n",
			"set a $(math --scale=0 \"1\")\nset b $(math --scale=0 \"2\")\n",
		},
		{
			"compound comma increment and assign",
			"(( i++, n += 2, total = a * b ))\n",
			"set i $(math --scale=0 \"$i + 1\")\nset n $(math --scale=0 \"$n + 2\")\nset total $(math --scale=0 \"$a * $b\")\n",
		},
		{
			"compound comma parenthesized",
			"(( (a = 1, b = 2), c = 3 ))\n",
			"set a $(math --scale=0 \"1\")\nset b $(math --scale=0 \"2\")\nset c $(math --scale=0 \"3\")\n",
		},
		{
			"compound comma in if body",
			"if true; then\n  (( a = 1, b = 2 ))\nfi\n",
			"if true\n    set a $(math --scale=0 \"1\")\n    set b $(math --scale=0 \"2\")\nend\n",
		},
		{
			"compound comma with comments",
			"# comment before\n(( a = 1, b = 2 )) # comment after\n",
			"# comment before\nset a $(math --scale=0 \"1\")\nset b $(math --scale=0 \"2\") # comment after\n",
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
