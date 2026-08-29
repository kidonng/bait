source $SOURCE_FISH

set script_path (mktemp)
echo 'VAR="from double dash"' > $script_path

source -- $script_path
test "$VAR" = "from double dash"; or exit 1
rm -f $script_path
