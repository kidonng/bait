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
    if set --query IFS; and not string match --quiet --regex '^\s+$' -- "$IFS"
        set --local cur $argv
        for d in (string split "" -- "$IFS")
            test -n "$d"; and set cur (string split -- "$d" $cur)
        end
        set --local is_whitespace_ifs 1
        if string match --quiet --regex '\S' -- "$IFS"
            if not string match --quiet --regex '\s' -- "$IFS"
                set is_whitespace_ifs 0
            end
        end
        for w in $cur
            if test $is_whitespace_ifs -eq 0
                printf '%s\n' "$w"
            else
                test -n "$w"; and printf '%s\n' "$w"
            end
        end
        return 0
    end
    string match --regex --all '\S+' -- $argv
end
