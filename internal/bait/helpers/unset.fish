function unset --no-scope-shadowing --description "Erase variables or functions"
    set --local mode var
    set --local stop_opts 0
    set --local ret 0

    # bash names that fish reserves, or that the translator mangles away
    # from: unsetting the bash name erases the mangled variable, and is a
    # no-op (like bash unsetting an undefined variable) when the script
    # never touched it.
    set --local reserved _ _unused status _status version _version history _history hostname _hostname pipestatus _pipestatus status_generation _status_generation

    # fish's own read-only arrays: bash's unset succeeds, but fish refuses
    # to erase them, so the erase is a silent no-op.
    set --local readonly pipestatus dirstack

    for arg in $argv
        if test $stop_opts -eq 0; and string match --quiet --regex "^-" -- "$arg"
            if test "$arg" = --
                set stop_opts 1
                continue
            else if test "$arg" = -f
                set mode func
                continue
            else if test "$arg" = -v -o "$arg" = -n
                set mode var
                continue
            end
        end

        if test "$mode" = func
            functions --erase -- $arg
            test $status -ne 0; and set ret 1
            continue
        end

        # Check if arg is an array element: name[index]
        set --local m (string match --regex "^([a-zA-Z_][a-zA-Z0-9_]*)\\[([^\\]]+)\\]\$" -- "$arg")
        if test (count $m) -eq 3
            set --local arr_name (__bait_reserved_name $m[2] $reserved)
            if test -z "$arr_name"
                continue
            end
            if contains -- $arr_name $readonly
                continue
            end
            set --local idx_expr $m[3]
            set --local calc_idx (math --scale=0 "$idx_expr" 2>/dev/null)
            if test -n "$calc_idx"
                if test $calc_idx -ge 0
                    set idx_expr (math "$calc_idx + 1")
                else
                    set idx_expr $calc_idx
                end
            end
            # bash unsets an undefined name without complaint.
            if set --query $arr_name"[$idx_expr]"
                set --erase $arr_name"[$idx_expr]"
                test $status -ne 0; and set ret 1
            end
            continue
        end

        set --local name (__bait_reserved_name $arg $reserved)
        if test -z "$name"
            continue
        end
        if contains -- $name $readonly
            continue
        end
        if set --query -- $name
            set --erase -- $name
            test $status -ne 0; and set ret 1
        end
    end
    return $ret
end

# __bait_reserved_name maps a bash variable name to the fish name holding
# it: fish's own variables for PIPESTATUS and DIRSTACK, the mangled name
# for names fish reserves (only when the script created it), and the name
# itself otherwise.
function __bait_reserved_name --no-scope-shadowing
    set --local name $argv[1]
    set --local reserved $argv[2..-1]
    if test "$name" = PIPESTATUS
        echo pipestatus
        return 0
    else if test "$name" = DIRSTACK
        echo dirstack
        return 0
    end
    set --local i 1
    while test $i -lt (count $reserved)
        if test "$name" = $reserved[$i]
            set --local mangled $reserved[(math $i + 1)]
            if set --query $mangled
                echo $mangled
            end
            return 0
        end
        set i (math $i + 2)
    end
    echo $name
end
