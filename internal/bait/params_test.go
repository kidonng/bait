package bait

import (
	"strings"
	"testing"
)

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
			"OSTYPE maps to __bait_ostype",
			"echo $OSTYPE \"${OSTYPE}\"\n",
			baitOSTypeHelper + "\necho $(__bait_ostype) \"$(__bait_ostype)\"\n",
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
