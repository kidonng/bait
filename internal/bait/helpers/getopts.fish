function getopts --no-scope-shadowing
    set --local __bait_optstring $argv[1]
    set --local __bait_varname $argv[2]
    set --local __bait_args
    if test (count $argv) -gt 2
        set __bait_args $argv[3..-1]
    else
        set __bait_args
    end

    if not set --query OPTIND; or test "$OPTIND" -lt 1
        set --global OPTIND 1
    end
    if not set --query OPTARG
        set --global OPTARG ""
    end
    if not set --query __bait_optpos; or test "$__bait_optpos" -lt 2
        set --global __bait_optpos 2
    end

    if test "$OPTIND" -gt (count $__bait_args)
        return 1
    end
    set --local __bait_current $__bait_args[$OPTIND]
    if test (string length -- "$__bait_current") -lt 2; or test (string sub --start=1 --length=1 -- "$__bait_current") != -
        return 1
    end
    if test "$__bait_current" = --
        set --global OPTIND (math $OPTIND + 1)
        return 1
    end

    set --local __bait_opt (string sub --start=$__bait_optpos --length=1 -- "$__bait_current")
    if test -z "$__bait_opt"
        set --global OPTIND (math $OPTIND + 1)
        set --global __bait_optpos 2
        getopts $__bait_optstring $__bait_varname $__bait_args
        return $status
    end

    set --global __bait_optpos (math $__bait_optpos + 1)
    if test "$__bait_optpos" -gt (string length -- "$__bait_current")
        set --global OPTIND (math $OPTIND + 1)
        set --global __bait_optpos 2
    end

    set --local __bait_colon_mode 0
    set --local __bait_clean_opts $__bait_optstring
    if string match --quiet ":*" -- "$__bait_optstring"
        set __bait_colon_mode 1
        set __bait_clean_opts (string sub --start=2 -- "$__bait_optstring")
    end

    set --local __bait_escaped_opt (string escape --style=regex -- "$__bait_opt")
    set --local __bait_match (string match --regex --index -- "$__bait_escaped_opt:?" "$__bait_clean_opts")
    if test -z "$__bait_match"
        set OPTARG "$__bait_opt"
        set $__bait_varname "?"
        if test $__bait_colon_mode -eq 0
            echo "getopts: illegal option -- $__bait_opt" >&2
        end
        return 0
    end

    set --local __bait_idx_parts (string split " " -- $__bait_match[1])
    set --local __bait_opt_spec (string sub --start=$__bait_idx_parts[1] --length=$__bait_idx_parts[2] -- "$__bait_clean_opts")
    if string match --quiet "*:" -- "$__bait_opt_spec"
        if test "$__bait_optpos" -gt 2; and test "$__bait_optpos" -le (string length -- "$__bait_current")
            set OPTARG (string sub --start=$__bait_optpos -- "$__bait_current")
            set --global OPTIND (math $OPTIND + 1)
            set --global __bait_optpos 2
        else if test "$OPTIND" -le (count $__bait_args)
            set OPTARG $__bait_args[$OPTIND]
            set --global OPTIND (math $OPTIND + 1)
            set --global __bait_optpos 2
        else
            set OPTARG "$__bait_opt"
            if test $__bait_colon_mode -eq 1
                set $__bait_varname ":"
            else
                set $__bait_varname "?"
                echo "getopts: option requires an argument -- $__bait_opt" >&2
            end
            return 0
        end
    end

    set $__bait_varname "$__bait_opt"
    return 0
end
