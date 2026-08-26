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

func TestRustupInstall(t *testing.T) {
	skipIfNoFish(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	src, err := downloadScript(ctx, "https://sh.rustup.rs")
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}

	fishScript, err := translateScript(src)
	if err != nil {
		t.Fatalf("translate failed: %v", err)
	}

	sandboxDir := t.TempDir()
	rustupHome := filepath.Join(sandboxDir, "rustup")
	cargoHome := filepath.Join(sandboxDir, "cargo")
	scriptPath := filepath.Join(sandboxDir, "rustup-init.fish")

	if err := os.WriteFile(scriptPath, fishScript, 0755); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	cmd := exec.CommandContext(ctx, "fish", scriptPath, "-y", "--no-modify-path", "--default-toolchain", "none")
	cmd.Dir = sandboxDir
	cmd.Env = []string{
		"HOME=" + sandboxDir,
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + os.TempDir(),
		"RUSTUP_HOME=" + rustupHome,
		"CARGO_HOME=" + cargoHome,
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if val, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+val)
		}
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fish run failed: %v\noutput:\n%s", err, string(out))
	}

	rustupBin := filepath.Join(cargoHome, "bin", "rustup")
	verCmd := exec.CommandContext(ctx, rustupBin, "--version")
	verCmd.Env = []string{
		"HOME=" + sandboxDir,
		"RUSTUP_HOME=" + rustupHome,
		"CARGO_HOME=" + cargoHome,
	}
	verOut, err := verCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to run installed rustup (%s): %v\noutput:\n%s\ninstaller output:\n%s",
			rustupBin, err, string(verOut), string(out))
	}

	verStr := strings.TrimSpace(string(verOut))
	if !strings.Contains(verStr, "rustup ") {
		t.Fatalf("unexpected rustup version output: %q", verStr)
	}

	t.Logf("rustup successfully installed and verified in sandbox: %s", verStr)
}
