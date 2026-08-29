source $SOURCE_FISH

# 1. Native fish script execution
set script_path (mktemp)
echo 'set --global GREETING "hello from fish"' >$script_path
echo 'function fish_add; math $argv[1] + $argv[2]; end' >>$script_path

source $script_path
test "$GREETING" = "hello from fish"; or exit 1
test (fish_add 3 4) -eq 7; or exit 2

# 2. Bash translation with arguments and functions
echo 'GREETING="hello from bash"' >$script_path
echo 'ARG_CONCAT="${1}_${2}"' >>$script_path
echo 'COUNT=$#' >>$script_path
echo 'my_bash_calc() { local a=$1; local b=$2; echo $((a * b)); }' >>$script_path

source $script_path foo bar
test "$GREETING" = "hello from bash"; or begin
    echo "greeting mismatch: $GREETING"
    exit 3
end
test "$ARG_CONCAT" = foo_bar; or begin
    echo "arg concat mismatch: $ARG_CONCAT"
    exit 4
end
test "$COUNT" = 2; or begin
    echo "count mismatch: $COUNT"
    exit 5
end
test (my_bash_calc 6 7) -eq 42; or begin
    echo "calc mismatch"
    exit 6
end

# 3. Double dash argument handling
echo 'VAR="from double dash"' >$script_path
source -- $script_path
test "$VAR" = "from double dash"; or exit 7

# 4. Bash return code propagation and short-circuiting
echo 'FOO=bar' >$script_path
echo 'return 42' >>$script_path
echo 'FOO=baz' >>$script_path

source $script_path
set res $status
test $res -eq 42; or begin
    echo "expected status 42, got $res"
    exit 8
end
test "$FOO" = bar; or begin
    echo "expected FOO=bar, got $FOO"
    exit 9
end

rm -f $script_path
