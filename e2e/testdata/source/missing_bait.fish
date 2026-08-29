source $SOURCE_FISH

set script_path (mktemp)
echo 'VAR="should fail without bait"' >$script_path

source $script_path
set res $status
rm -f $script_path
exit $res
