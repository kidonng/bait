package e2e

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPixiInstall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	src, err := downloadScript(ctx, "https://pixi.sh/install.sh")
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}

	fishScript, err := translateScript(src)
	if err != nil {
		t.Fatalf("translate failed: %v", err)
	}

	sandboxDir, stdout, stderr, err := runIsolatedFish(t, ctx, fishScript)
	if err != nil {
		t.Fatalf("fish run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	pixiBin := filepath.Join(sandboxDir, ".pixi", "bin", "pixi")
	cmd := exec.CommandContext(ctx, pixiBin, "--version")
	cmd.Env = []string{"HOME=" + sandboxDir}
	verOut, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to run installed pixi (%s): %v\noutput:\n%s\ninstaller stdout:\n%s\ninstaller stderr:\n%s",
			pixiBin, err, string(verOut), stdout, stderr)
	}

	verStr := strings.TrimSpace(string(verOut))
	if !strings.Contains(verStr, "pixi ") {
		t.Fatalf("unexpected pixi version output: %q", verStr)
	}

	t.Logf("Pixi successfully installed and verified in sandbox: %s", verStr)
}
