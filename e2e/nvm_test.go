package e2e

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNVM(t *testing.T) {
	skipIfNoFish(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	src, err := downloadScript(ctx, "https://github.com/nvm-sh/nvm/raw/refs/heads/master/nvm.sh")
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}

	fishScript, err := translateScript(src)
	if err != nil {
		t.Fatalf("translate failed: %v", err)
	}

	setup := `
set -gx NVM_DIR (pwd)/.nvm
mkdir -p "$NVM_DIR"
`
	testRunner := `
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
`

	combinedScript := append([]byte(setup), fishScript...)
	combinedScript = append(combinedScript, []byte("\n"+testRunner)...)
	_, stdout, stderr, err := runIsolatedFish(t, ctx, combinedScript)
	if err != nil {
		t.Fatalf("nvm verification in sandbox failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if strings.Contains(stderr, "Unknown command") {
		t.Fatalf("nvm execution encountered unknown command:\n%s", stderr)
	}

	t.Logf("nvm.sh successfully verified in sandbox:\n%s", strings.TrimSpace(stdout))
}
