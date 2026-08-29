package e2e

import (
	"context"
	_ "embed"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

//go:embed testdata/source/file_source.fish
var sourceFileScript []byte

//go:embed testdata/source/pipeline.fish
var sourcePipelineScript []byte

//go:embed testdata/source/autoload.fish
var sourceAutoloadScript []byte

//go:embed testdata/source/no_fish_in_path.fish
var sourceNoFishInPathScript []byte

func buildBaitBinary(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	baitBin := filepath.Join(tmpDir, "bait")

	cmd := exec.Command("go", "build", "-o", baitBin, "../cmd/bait")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build bait binary: %v\nOutput: %s", err, string(out))
	}
	return baitBin
}

func getSourceFishPath(t *testing.T) string {
	t.Helper()
	absPath, err := filepath.Abs("../internal/bait/helpers/source.fish")
	if err != nil {
		t.Fatalf("failed to get absolute path of source.fish: %v", err)
	}
	if _, err := os.Stat(absPath); err != nil {
		t.Fatalf("internal/bait/helpers/source.fish does not exist at %s: %v", absPath, err)
	}
	return absPath
}

func TestSourceFish(t *testing.T) {
	skipIfNoFish(t)

	baitBin := buildBaitBinary(t)
	baitDir := filepath.Dir(baitBin)
	sourceFish := getSourceFishPath(t)
	functionsDir := filepath.Dir(sourceFish)

	// Base environment with bait prepended to PATH and paths exposed
	origPath := os.Getenv("PATH")
	envWithBait := []string{
		"PATH=" + baitDir + ":" + origPath,
		"SOURCE_FISH=" + sourceFish,
		"FUNCTIONS_DIR=" + functionsDir,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	t.Run("FileSource", func(t *testing.T) {
		_, stdout, stderr, err := runIsolatedFish(t, ctx, sourceFileScript, envWithBait...)
		if err != nil {
			t.Fatalf("file source execution failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("Pipeline", func(t *testing.T) {
		_, stdout, stderr, err := runIsolatedFish(t, ctx, sourcePipelineScript, envWithBait...)
		if err != nil {
			t.Fatalf("pipeline execution failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("Autoload", func(t *testing.T) {
		_, stdout, stderr, err := runIsolatedFish(t, ctx, sourceAutoloadScript, envWithBait...)
		if err != nil {
			t.Fatalf("autoload via fish_function_path failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("HelpOption", func(t *testing.T) {
		script := []byte(`
source $SOURCE_FISH
source -h
`)
		_, stdout, stderr, err := runIsolatedFish(t, ctx, script, envWithBait...)
		if err != nil {
			t.Fatalf("source -h failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
		if !strings.Contains(stdout, "source - evaluate contents of file") && !strings.Contains(stdout, "source [FILE") {
			t.Fatalf("expected fish source help in stdout, got: %s", stdout)
		}
	})

	t.Run("NonExistentFile", func(t *testing.T) {
		script := []byte(`
source $SOURCE_FISH
source /tmp/nonexistent_file_for_bait_test_12345
`)
		_, _, stderr, err := runIsolatedFish(t, ctx, script, envWithBait...)
		if err == nil {
			t.Fatalf("expected error sourcing non-existent file, got success")
		}
		if !strings.Contains(stderr, "No such file or directory") {
			t.Fatalf("expected 'No such file or directory' in stderr, got: %s", stderr)
		}
	})

	t.Run("MissingBaitBinary", func(t *testing.T) {
		var filteredPath []string
		for _, p := range strings.Split(origPath, ":") {
			if p != baitDir {
				filteredPath = append(filteredPath, p)
			}
		}
		cleanEnv := []string{
			"PATH=" + strings.Join(filteredPath, ":"),
			"SOURCE_FISH=" + sourceFish,
			"FUNCTIONS_DIR=" + functionsDir,
		}

		script := []byte(`
source $SOURCE_FISH

set script_path (mktemp)
echo 'VAR="should fail without bait"' >$script_path

source $script_path
set res $status
rm -f $script_path
exit $res
`)
		_, stdout, stderr, err := runIsolatedFish(t, ctx, script, cleanEnv...)
		if err == nil {
			t.Fatalf("expected error when bait is missing, got success")
		}
		if !strings.Contains(stderr, "'bait' is required to translate bash scripts") {
			t.Fatalf("expected missing bait warning in stderr, got: %s\nstdout: %s", stderr, stdout)
		}
	})

	t.Run("NoFishInPath", func(t *testing.T) {
		_, stdout, stderr, err := runIsolatedFish(t, ctx, sourceNoFishInPathScript, envWithBait...)
		if err != nil {
			t.Fatalf("source failed without fish in PATH: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})
}
