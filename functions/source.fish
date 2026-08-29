# source.fish - Enhanced source function with transparent Bash script translation via bait.
#
# Load this file in your fish configuration (e.g. in config.fish or conf.d/):
#   source /path/to/bait/functions/source.fish
# Or add the functions directory to $fish_function_path:
#   set --unexport --prepend fish_function_path /path/to/bait/functions
#
# Behavior:
#   - If the script is recognized as valid fish syntax (fish --no-execute), native source is used.
#   - Otherwise, the script is translated on-the-fly with bait and evaluated in the caller's scope.

function source --no-scope-shadowing --description "Evaluate contents of file, translating with bait if necessary"
    # Pass through help flags to builtin source
    if test (count $argv) -ge 1
        switch $argv[1]
            case -h --help
                builtin source $argv
                return $status
        end
    end

    # Locate current fish binary without relying on PATH
    set --local __bait_fish (status fish-path)
    test -n "$__bait_fish"; or set __bait_fish fish

    # Normalize options: if the first argument is "--", consume it
    set --local __bait_args $argv
    if test (count $__bait_args) -ge 1 -a "$__bait_args[1]" = "--"
        if test (count $__bait_args) -ge 2
            set __bait_args $__bait_args[2..]
        else
            set __bait_args "-"
        end
    end

    # Determine input source: file vs stdin
    set --local __bait_from_stdin 0
    if test (count $__bait_args) -ge 1 -a "$__bait_args[1]" = "-"
        set __bait_from_stdin 1
        set __bait_args $__bait_args[2..]
    else if test (count $__bait_args) -eq 0
        if not isatty stdin
            set __bait_from_stdin 1
        else
            builtin source
            return $status
        end
    end

    if test $__bait_from_stdin -eq 1
        set --local __bait_input
        read --null __bait_input

        if printf "%s" "$__bait_input" | $__bait_fish --no-execute 2>/dev/null
            printf "%s" "$__bait_input" | builtin source - $__bait_args
            return $status
        else
            if not command --query bait
                echo "source: 'bait' is required to translate bash scripts, but it was not found in PATH" >&2
                return 127
            end

            printf "%s" "$__bait_input" | bait | builtin source - $__bait_args
            set --local __bait_ps $pipestatus
            if test $__bait_ps[1] -ne 0
                return $__bait_ps[1]
            end
            return $__bait_ps[2]
        end
    else
        # Handle file input
        set --local __bait_file "$__bait_args[1]"
        if not test -f "$__bait_file"
            # Non-existent file, directory, or special device: delegate to builtin source
            builtin source $argv
            return $status
        end

        if $__bait_fish --no-execute "$__bait_file" 2>/dev/null
            builtin source $argv
            return $status
        else
            if not command --query bait
                echo "source: 'bait' is required to translate bash scripts, but it was not found in PATH" >&2
                return 127
            end

            bait "$__bait_file" | builtin source - $__bait_args[2..]
            set --local __bait_ps $pipestatus
            if test $__bait_ps[1] -ne 0
                return $__bait_ps[1]
            end
            return $__bait_ps[2]
        end
    end
end

function . --no-scope-shadowing --wraps source --description "Evaluate contents of file, forwarding to source"
    source $argv
end
