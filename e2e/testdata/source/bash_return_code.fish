source $SOURCE_FISH

set script_path (mktemp)
echo 'FOO=bar' >$script_path
echo 'return 42' >>$script_path
echo 'FOO=baz' >>$script_path

source $script_path
set res $status
test $res -eq 42; or begin
    echo "expected status 42, got $res"
    exit 1
end
test "$FOO" = bar; or begin
    echo "expected FOO=bar, got $FOO"
    exit 2
end
rm -f $script_path
