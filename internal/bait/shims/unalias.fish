function unalias --no-scope-shadowing
    for arg in $argv
        if test "$arg" = -a
            # fish stores aliases as functions; the alias builtin marks
            # them with an "alias ..." description.
            for name in (functions --names)
                if functions $name | string match --quiet --regex -- '--description .alias '
                    functions --erase -- $name
                end
            end
            continue
        end
        test -n "$arg"; and functions --erase -- $arg 2>/dev/null
    end
    return 0
end
