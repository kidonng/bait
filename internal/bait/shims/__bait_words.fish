function __bait_words --no-scope-shadowing
    if test (count $argv) -eq 0
        return 0
    end
    if not set --query IFS
        string match --regex --all '\S+' -- $argv
        return 0
    end
    if test -z "$IFS"
        for a in $argv
            printf '%s\n' $a
        end
        return 0
    end
    set --local ws (string match --regex --all '\s' -- "$IFS")
    set --local nws (string match --regex --all '\S' -- "$IFS")
    if test (count $nws) -eq 0
        string match --regex --all '\S+' -- $argv
        return 0
    end
    # bash splits on runs of IFS whitespace and on each non-whitespace IFS
    # character, with the whitespace around such a character folded into
    # the same separator; a trailing separator adds no field.
    set --local ws_set (string join "" -- $ws)
    for a in $argv
        set --local fields
        set --local pieces $a
        for d in $nws
            set pieces (string split -- $d $pieces)
        end
        for piece in $pieces
            if test (count $ws) -gt 0
                set piece (string trim --chars=$ws_set -- "$piece")
            end
            if test -z "$piece"
                set --append fields ""
                continue
            end
            if test (count $ws) -gt 0
                set --append fields (string match --regex --all '\S+' -- "$piece")
            else
                set --append fields "$piece"
            end
        end
        if test (count $fields) -gt 0; and test -z "$fields[-1]"
            set --erase fields[-1]
        end
        for w in $fields
            printf '%s\n' "$w"
        end
    end
end
