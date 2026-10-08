# __bait_cmp evaluates a bash arithmetic comparison. Both operands are
# arithmetic expressions whose unset or non-numeric values count as zero.
function __bait_cmp
    set --local op $argv[1]
    set --local lhs (__bait_math "$argv[2]")
    set --local rhs (__bait_math "$argv[3]")
    test $lhs $op $rhs
end
