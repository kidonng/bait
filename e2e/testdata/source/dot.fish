source $SOURCE_FISH
source (dirname $SOURCE_FISH)/..fish

set script_path (mktemp)
echo 'DOT_VAR="${1}_${2}"' >$script_path

. $script_path hello dot
test "$DOT_VAR" = hello_dot; or exit 1
rm -f $script_path
