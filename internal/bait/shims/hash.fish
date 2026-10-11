function hash
    set --local cmds
    for arg in $argv
        switch $arg
            case -r -d -l
                continue
            case --
                continue
            case '-*'
                continue
            case '*'
                set --append cmds $arg
        end
    end
    if test (count $cmds) -eq 0
        return 0
    end
    for cmd in $cmds
        if not type --query -- $cmd
            return 1
        end
    end
    return 0
end
