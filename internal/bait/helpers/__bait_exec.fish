function __bait_exec
    if test (count $argv) -eq 0
        return 0
    end
    set --local words (string match --regex --all '\S+' -- "$argv[1]")
    if test (count $words) -gt 0
        if test "$words[1]" = "command"
            command $words[2..-1] $argv[2..-1]
        else if test "$words[1]" = "builtin"
            builtin $words[2..-1] $argv[2..-1]
        else
            $words $argv[2..-1]
        end
    else if test (count $argv) -gt 1
        __bait_exec $argv[2..-1]
    end
end
