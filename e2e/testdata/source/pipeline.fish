source $SOURCE_FISH

# 1. Native fish script via pipeline
echo 'set --global PIPE_FISH_VAL "ok"' | source
test "$PIPE_FISH_VAL" = ok; or exit 1

# 2. Bash script translation via pipeline
echo 'PIPE_BASH_VAL="translated"' | source
test "$PIPE_BASH_VAL" = translated; or exit 2

# 3. Pipeline with arguments
echo 'ARG_RESULT="${1}-${2}"' | source - first second
test "$ARG_RESULT" = first-second; or exit 3
