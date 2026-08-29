source $SOURCE_FISH

echo 'set --global PIPE_FISH_VAL "ok"' | source
test "$PIPE_FISH_VAL" = "ok"; or exit 1
