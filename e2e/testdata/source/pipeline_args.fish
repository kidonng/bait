source $SOURCE_FISH

echo 'ARG_RESULT="${1}-${2}"' | source - first second
test "$ARG_RESULT" = first-second; or exit 1
