source $SOURCE_FISH

set script_path (mktemp)
echo 'GREETING="hello from bash"' > $script_path
echo 'ARG_CONCAT="${1}_${2}"' >> $script_path
echo 'COUNT=$#' >> $script_path
echo 'my_bash_calc() { local a=$1; local b=$2; echo $((a * b)); }' >> $script_path

source $script_path foo bar
test "$GREETING" = "hello from bash"; or begin; echo "greeting mismatch: $GREETING"; exit 1; end
test "$ARG_CONCAT" = "foo_bar"; or begin; echo "arg concat mismatch: $ARG_CONCAT"; exit 2; end
test "$COUNT" = "2"; or begin; echo "count mismatch: $COUNT"; exit 3; end
test (my_bash_calc 6 7) -eq 42; or begin; echo "calc mismatch"; exit 4; end
rm -f $script_path
