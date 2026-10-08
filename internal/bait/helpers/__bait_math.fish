# __bait_math prints the value of a bash arithmetic expression. Unset or
# non-numeric operands count as zero, as in bash arithmetic, and error
# output is suppressed because bash arithmetic never prints any.
function __bait_math
    set --local value (math --scale=0 "$argv[1]" 2>/dev/null)
    if test -z "$value"
        echo 0
    else
        echo $value
    end
end
