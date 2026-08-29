source $SOURCE_FISH

# Strip all directories containing fish from PATH
while command --query fish
    set --local fish_loc (dirname (command --search fish))
    set --local new_path
    for p in $PATH
        if test "$p" != "$fish_loc"
            set --append new_path $p
        end
    end
    set --export PATH $new_path
end

if command --query fish
    echo "fish should not be in PATH for this test" >&2
    exit 99
end

# 1. Native fish script should still succeed via status fish-path
set script_path (mktemp)
echo 'set --global NATIVE_OK "yes"' >$script_path
source $script_path
test "$NATIVE_OK" = yes; or exit 1
rm -f $script_path

# 2. Bash script translation should also work via status fish-path
set bash_path (mktemp)
echo 'BASH_OK="yes"' >$bash_path
source $bash_path
test "$BASH_OK" = yes; or exit 2
rm -f $bash_path
