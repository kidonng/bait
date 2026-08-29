set --unexport --prepend fish_function_path $FUNCTIONS_DIR

set script_path (mktemp)
echo 'AUTOLOAD_VAR="autoloaded_ok"' > $script_path

source $script_path
test "$AUTOLOAD_VAR" = "autoloaded_ok"; or exit 1
rm -f $script_path
