package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUvInstall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	src, err := downloadScript(ctx, "https://releases.astral.sh/installers/uv/latest/uv-installer.sh")
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}

	fishScript, err := translateScript(src)
	if err != nil {
		t.Fatalf("translate failed: %v", err)
	}

	sandboxDir := t.TempDir()
	installDir := filepath.Join(sandboxDir, "bin")
	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatalf("failed to create install dir: %v", err)
	}

	// runIsolatedFish runs in an isolated HOME, passing installer config env vars
	scriptPath := filepath.Join(sandboxDir, "installer.fish")
	if err := os.WriteFile(scriptPath, fishScript, 0755); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	cmd := exec.CommandContext(ctx, "fish", scriptPath)
	cmd.Env = []string{
		"HOME=" + sandboxDir,
		"PATH=" + os.Getenv("PATH"),
		"UV_INSTALL_DIR=" + installDir,
		"CARGO_DIST_FORCE_INSTALL=1",
		"INSTALLER_NO_MODIFY_PATH=1",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fish run failed: %v\noutput:\n%s", err, string(out))
	}

	uvBin := filepath.Join(installDir, "uv")
	verCmd := exec.CommandContext(ctx, uvBin, "--version")
	verCmd.Env = []string{"HOME=" + sandboxDir}
	verOut, err := verCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to run installed uv (%s): %v\noutput:\n%s\ninstaller output:\n%s",
			uvBin, err, string(verOut), string(out))
	}

	verStr := strings.TrimSpace(string(verOut))
	if !strings.Contains(verStr, "uv ") {
		t.Fatalf("unexpected uv version output: %q", verStr)
	}

	t.Logf("uv successfully installed and verified in sandbox: %s", verStr)
}
