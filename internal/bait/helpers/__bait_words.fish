function __bait_words --no-scope-shadowing
    if test (count $argv) -eq 0
        return 0
    end
    if set --query IFS; and test -z "$IFS"
        for a in $argv
            printf '%s\n' $a
        end
        return 0
    end
    if set --query IFS; and test "$IFS" != (printf '\n \t' | string collect)
        set --local cur $argv
        for d in (string split "" -- "$IFS")
            test -n "$d"; and set cur (string split -- "$d" $cur)
        end
        for w in $cur
            test -n "$w"; and printf '%s\n' $w
        end
        return 0
    end
    string match --regex --all '\S+' -- $argv
end
