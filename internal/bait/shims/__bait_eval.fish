function __bait_eval --no-scope-shadowing --description "Evaluate bash arguments, translating with bait on-the-fly"
    if test (count $argv) -eq 0
        return 0
    end

    set --local __bait_cmd (string join ' ' -- $argv | string collect)
    if test -z "$__bait_cmd"
        return 0
    end

    if not command --query bait
        echo "eval: 'bait' is required to translate bash commands, but it was not found in PATH" >&2
        return 127
    end

    set --local __bait_script (printf "%s\n" "$__bait_cmd" | bait | string collect)
    set --local __bait_ps $pipestatus
    if test $__bait_ps[2] -ne 0
        return $__bait_ps[2]
    end

    if test -n "$__bait_script"
        builtin eval "$__bait_script"
        return $status
    end
    return 0
end
