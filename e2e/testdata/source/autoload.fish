set --unexport --prepend fish_function_path $FUNCTIONS_DIR

set script_path (mktemp)

# 1. Autoload source via fish_function_path
echo 'AUTOLOAD_VAR="autoloaded_ok"' >$script_path
source $script_path
test "$AUTOLOAD_VAR" = autoloaded_ok; or exit 1

# 2. Autoload dot (.) via fish_function_path
echo 'AUTOLOAD_DOT_VAR="dot_ok"' >$script_path
. $script_path
test "$AUTOLOAD_DOT_VAR" = dot_ok; or exit 2

# 3. Dot (.) forwarding with arguments
echo 'DOT_VAR="${1}_${2}"' >$script_path
. $script_path hello dot
test "$DOT_VAR" = hello_dot; or exit 3

rm -f $script_path
