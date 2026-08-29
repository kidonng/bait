source $SOURCE_FISH

set script_path (mktemp)
echo 'set --global GREETING "hello from fish"' > $script_path
echo 'function fish_add; math $argv[1] + $argv[2]; end' >> $script_path

source $script_path
test "$GREETING" = "hello from fish"; or exit 1
test (fish_add 3 4) -eq 7; or exit 2
rm -f $script_path
