# 1. Verify nvm --version
set ver (nvm --version)
if test $status -ne 0 -o -z "$ver"
    echo "FAILED: nvm --version failed: status=$status, ver=$ver" >&2
    exit 1
end
echo "nvm version: $ver"

# 2. Verify nvm current
set cur (nvm current)
if test $status -ne 0 -o "$cur" != "none" -a "$cur" != "system"
    echo "FAILED: nvm current expected none or system, got: $cur" >&2
    exit 1
end

# 3. Verify nvm alias
nvm alias >/dev/null
if test $status -ne 0
    echo "FAILED: nvm alias failed: status=$status" >&2
    exit 1
end

# 4. Verify nvm ls
nvm ls >/dev/null
# nvm ls returns status 3 when no versions are installed locally, which is expected.
if test $status -ne 0 -a $status -ne 3
    echo "FAILED: nvm ls unexpected status: $status" >&2
    exit 1
end

# 5. Verify nvm install and execution
nvm install --lts
if test $status -ne 0
    echo "FAILED: nvm install failed: status=$status" >&2
    exit 1
end

nvm use --lts
if test $status -ne 0
    echo "FAILED: nvm use failed: status=$status" >&2
    exit 1
end

set node_ver (node -v)
if test $status -ne 0 -o -z "$node_ver"
    echo "FAILED: installed node execution failed: status=$status, node_ver=$node_ver" >&2
    exit 1
end
echo "installed node version: $node_ver"

# 6. Verify .nvmrc resolution via paired high-FD redirection
echo "lts/*" > .nvmrc
nvm use
if test $status -ne 0
    echo "FAILED: nvm use with .nvmrc failed: status=$status" >&2
    exit 1
end
