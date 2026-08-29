set --unexport --prepend fish_function_path $FUNCTIONS_DIR

set script_path (mktemp)
echo 'AUTOLOAD_DOT_VAR="dot_ok"' > $script_path

. $script_path
test "$AUTOLOAD_DOT_VAR" = "dot_ok"; or exit 1
rm -f $script_path
