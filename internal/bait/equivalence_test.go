package bait

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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
		{
			name: "unset dynamic array element and negative index",
			src: "items=(first second third fourth)\n" +
				"idx=1\n" +
				"unset \"items[$idx]\"\n" +
				"echo \"after_idx1:${items[*]}\"\n" +
				"unset 'items[-1]'\n" +
				"echo \"after_neg:${items[*]}\"\n",
		},
		{
			name: "function arithmetic assignment is global like bash",
			src: "incr_global() {\n" +
				"    ((count = 42))\n" +
				"    ((count++))\n" +
				"}\n" +
				"count=0\n" +
				"incr_global\n" +
				"echo \"count:$count\"\n",
		},
		{
			name: "function arithmetic assignment with local",
			src: "incr_local() {\n" +
				"    local count=10\n" +
				"    ((count++))\n" +
				"    echo \"local_count:$count\"\n" +
				"}\n" +
				"count=1\n" +
				"incr_local\n" +
				"echo \"outer_count:$count\"\n",
		},
		{
			name: "reserved variable status arithmetic and getopts",
			src: "status=10\n" +
				"echo \"arith:$((status + 5))\"\n" +
				"((status++))\n" +
				"echo \"status_after:$status\"\n",
		},
		{
			name: "pattern replacement with variable",
			src: "orig=\"hello 123 world\"\n" +
				"rep=\"456\"\n" +
				"echo \"rep:${orig/123/$rep}\"\n",
		},
		{
			name: "pattern strip with quoted wildcard",
			src: "s=\"*.txt\"\n" +
				"echo \"stripped:[${s#\"*.txt\"}]\"\n" +
				"s2=\"foo.txt\"\n" +
				"echo \"kept:[${s2#\"*.txt\"}]\"\n",
		},
		{
			name: "dynamic shift with zero",
			src: "test_shift() {\n" +
				"    n=0\n" +
				"    shift $n\n" +
				"    echo \"args:$*\"\n" +
				"}\n" +
				"test_shift a b c\n",
		},
		{
			name: "dynamic shift with negative number",
			src: "test_shift() {\n" +
				"    n=-1\n" +
				"    shift $n 2>/dev/null || true\n" +
				"    echo \"args:$*\"\n" +
				"}\n" +
				"test_shift a b c\n",
		},
		{
			name: "dynamic shift with positive number",
			src: "test_shift() {\n" +
				"    n=2\n" +
				"    shift $n\n" +
				"    echo \"args:$*\"\n" +
				"}\n" +
				"test_shift a b c d\n",
		},
		{
			name: "custom ifs empty fields",
			src: "IFS=\":\"\n" +
				"val=\"a::b\"\n" +
				"for x in $val; do\n" +
				"    echo \"f:[$x]\"\n" +
				"done\n",
		},
		{
			name: "ostype helper matches bash platform identifier",
			src: "if [[ \"$OSTYPE\" == linux-gnu* || \"$OSTYPE\" == darwin* || \"$OSTYPE\" == linux* ]]; then\n" +
				"    echo 'matched ostype prefix'\n" +
				"fi\n" +
				"case \"$OSTYPE\" in\n" +
				"    darwin*)\n" +
				"        echo 'matched darwin branch'\n" +
				"        ;;\n" +
				"    linux-gnu*|linux-musl*|linux*)\n" +
				"        echo 'matched linux branch'\n" +
				"        ;;\n" +
				"    *)\n" +
				"        echo 'matched other branch'\n" +
				"        ;;\n" +
				"esac\n",
		},
		{
			name: "arithmetic compound comma expressions",
			src: "a=0\nb=0\nc=0\n" +
				"(( a = 1, b = 2, c = a + b ))\n" +
				"echo \"a:$a b:$b c:$c\"\n" +
				"(( a++, b += 10, c *= 2 ))\n" +
				"echo \"a:$a b:$b c:$c\"\n",
		},
		{
			name: "arithmetic compound comma in function with local and global",
			src: "f() {\n" +
				"    local x=0\n" +
				"    (( x = 5, y = 10, z = x + y ))\n" +
				"    echo \"inner: x=$x y=$y z=$z\"\n" +
				"}\n" +
				"x=1\ny=2\nz=3\n" +
				"f\n" +
				"echo \"outer: x=$x y=$y z=$z\"\n",
		},
		{
			name: "arithmetic compound comma in loop",
			src: "sum=0\n" +
				"for i in 1 2 3; do\n" +
				"    (( a = i * 2, sum += a ))\n" +
				"done\n" +
				"echo \"sum:$sum a:$a\"\n",
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
