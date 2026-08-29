function hash
    if test (count $argv) -eq 0
        return 0
    end
    type --query $argv
end
