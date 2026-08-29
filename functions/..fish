# ..fish - Forward dot command to source
function . --no-scope-shadowing --wraps source --description "Evaluate contents of file, forwarding to source"
    source $argv
end
