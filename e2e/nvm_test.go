package e2e

import (
	"context"
	_ "embed"
	"strings"
	"testing"
	"time"
)

const nvmSetupScript = `
set --global --export NVM_DIR (pwd)/.nvm
mkdir -p "$NVM_DIR"
`

//go:embed testdata/nvm/verify.fish
var nvmVerifyScript []byte

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

	combinedScript := append([]byte(nvmSetupScript), fishScript...)
	combinedScript = append(combinedScript, '\n')
	combinedScript = append(combinedScript, nvmVerifyScript...)
	_, stdout, stderr, err := runIsolatedFish(t, ctx, combinedScript)
	if err != nil {
		t.Fatalf("nvm verification in sandbox failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if strings.Contains(stderr, "Unknown command") {
		t.Fatalf("nvm execution encountered unknown command:\n%s", stderr)
	}
	if strings.Contains(stderr, "unknown option") {
		t.Fatalf("nvm execution encountered unknown option:\n%s", stderr)
	}

	t.Logf("nvm.sh successfully verified in sandbox:\n%s", strings.TrimSpace(stdout))
}
