function unset --no-scope-shadowing --description "Erase variables or functions"
    set --local mode var
    set --local stop_opts 0
    set --local ret 0

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
        else
            # Normalize special variables if needed
            if test "$arg" = PIPESTATUS
                set arg pipestatus
            else if test "$arg" = DIRSTACK
                set arg dirstack
            else if test "$arg" = status; and set -q _status
                set arg _status
            else if test "$arg" = _; and set -q _unused
                set arg _unused
            end

            # Check if arg is array element: name[index]
            set --local m (string match --regex "^([a-zA-Z_][a-zA-Z0-9_]*)\\[([^\\]]+)\\]\$" -- "$arg")
            if test (count $m) -eq 3
                set --local arr_name $m[2]
                set --local idx_expr $m[3]
                if test "$arr_name" = PIPESTATUS
                    set arr_name pipestatus
                else if test "$arr_name" = DIRSTACK
                    set arr_name dirstack
                else if test "$arr_name" = status; and set -q _status
                    set arr_name _status
                else if test "$arr_name" = _; and set -q _unused
                    set arr_name _unused
                end

                set --local calc_idx (math --scale=0 "$idx_expr" 2>/dev/null)
                if test -n "$calc_idx"
                    if test $calc_idx -ge 0
                        set idx_expr (math "$calc_idx + 1")
                    else
                        set idx_expr $calc_idx
                    end
                end
                set --erase $arr_name"[$idx_expr]"
                test $status -ne 0; and set ret 1
            else
                set --erase -- $arg
                test $status -ne 0; and set ret 1
            end
        end
    end
    return $ret
end
