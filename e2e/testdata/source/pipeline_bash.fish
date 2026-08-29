source $SOURCE_FISH

echo 'PIPE_BASH_VAL="translated"' | source
test "$PIPE_BASH_VAL" = "translated"; or exit 1
