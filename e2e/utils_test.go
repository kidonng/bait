package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kidonng/bait/internal/bait"
)

func skipIfNoFish(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("fish"); err != nil {
		t.Skip("fish not found in PATH")
	}
}

func downloadScript(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "curl/8.0.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download script: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status: %s", resp.Status)
	}

	return io.ReadAll(resp.Body)
}

func translateScript(src []byte) ([]byte, error) {
	fishScript, _, err := bait.Translate(src)
	if err != nil {
		return nil, fmt.Errorf("bait translation failed: %w", err)
	}
	return fishScript, nil
}

// runIsolatedFish executes the fish script inside an isolated temporary directory
// with a sandboxed HOME and XDG environment, returning the temporary directory,
// stdout, stderr, and any execution error.
func runIsolatedFish(t *testing.T, ctx context.Context, script []byte, extraEnv ...string) (string, string, string, error) {
	t.Helper()
	skipIfNoFish(t)

	tmpDir := t.TempDir()

	env := []string{
		"HOME=" + tmpDir,
		"XDG_CACHE_HOME=" + filepath.Join(tmpDir, ".cache"),
		"XDG_CONFIG_HOME=" + filepath.Join(tmpDir, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(tmpDir, ".local", "share"),
	}

	// Forward essential system and network variables.
	for _, key := range []string{"PATH", "TMPDIR", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if val, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+val)
		}
	}
	env = append(env, extraEnv...)

	cmd := exec.CommandContext(ctx, "fish", "-c", string(script))
	cmd.Dir = tmpDir
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return tmpDir, stdout.String(), stderr.String(), err
}
