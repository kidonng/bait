package e2e

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStarshipInstall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	src, err := downloadScript(ctx, "https://starship.rs/install.sh")
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}

	fishScript, err := translateScript(src)
	if err != nil {
		t.Fatalf("translate failed: %v", err)
	}

	binDir := t.TempDir()
	sandboxDir, stdout, stderr, err := runIsolatedFish(t, ctx, fishScript,
		"BIN_DIR="+binDir,
		"FORCE=1",
	)
	if err != nil {
		t.Fatalf("fish run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	starshipBin := filepath.Join(binDir, "starship")
	cmd := exec.CommandContext(ctx, starshipBin, "--version")
	cmd.Env = []string{"HOME=" + sandboxDir}
	verOut, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to run installed starship (%s): %v\noutput:\n%s\ninstaller stdout:\n%s\ninstaller stderr:\n%s",
			starshipBin, err, string(verOut), stdout, stderr)
	}

	verStr := strings.TrimSpace(string(verOut))
	if !strings.Contains(verStr, "starship ") {
		t.Fatalf("unexpected starship version output: %q", verStr)
	}

	t.Logf("Starship successfully installed and verified in sandbox: %s", verStr)
}
