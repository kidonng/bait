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

//go:embed testdata/source/native.fish
var sourceNativeScript []byte

//go:embed testdata/source/bash_translation.fish
var sourceBashTranslationScript []byte

//go:embed testdata/source/bash_return_code.fish
var sourceBashReturnCodeScript []byte

//go:embed testdata/source/pipeline_fish.fish
var sourcePipelineFishScript []byte

//go:embed testdata/source/pipeline_bash.fish
var sourcePipelineBashScript []byte

//go:embed testdata/source/pipeline_args.fish
var sourcePipelineArgsScript []byte

//go:embed testdata/source/double_dash.fish
var sourceDoubleDashScript []byte

//go:embed testdata/source/help.fish
var sourceHelpScript []byte

//go:embed testdata/source/nonexistent.fish
var sourceNonexistentScript []byte

//go:embed testdata/source/missing_bait.fish
var sourceMissingBaitScript []byte

//go:embed testdata/source/autoload_function_path.fish
var sourceAutoloadFunctionPathScript []byte

//go:embed testdata/source/dot.fish
var sourceDotScript []byte

//go:embed testdata/source/autoload_dot.fish
var sourceAutoloadDotScript []byte

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
	absPath, err := filepath.Abs("../functions/source.fish")
	if err != nil {
		t.Fatalf("failed to get absolute path of source.fish: %v", err)
	}
	if _, err := os.Stat(absPath); err != nil {
		t.Fatalf("functions/source.fish does not exist at %s: %v", absPath, err)
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

	t.Run("NativeFishScript", func(t *testing.T) {
		_, stdout, stderr, err := runIsolatedFish(t, ctx, sourceNativeScript, envWithBait...)
		if err != nil {
			t.Fatalf("native fish script execution failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("BashScriptTranslation", func(t *testing.T) {
		_, stdout, stderr, err := runIsolatedFish(t, ctx, sourceBashTranslationScript, envWithBait...)
		if err != nil {
			t.Fatalf("bash script translation failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("BashScriptReturnCode", func(t *testing.T) {
		_, stdout, stderr, err := runIsolatedFish(t, ctx, sourceBashReturnCodeScript, envWithBait...)
		if err != nil {
			t.Fatalf("bash return code propagation failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("PipelineFishScript", func(t *testing.T) {
		_, stdout, stderr, err := runIsolatedFish(t, ctx, sourcePipelineFishScript, envWithBait...)
		if err != nil {
			t.Fatalf("pipeline fish script failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("PipelineBashScript", func(t *testing.T) {
		_, stdout, stderr, err := runIsolatedFish(t, ctx, sourcePipelineBashScript, envWithBait...)
		if err != nil {
			t.Fatalf("pipeline bash script failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("PipelineWithArguments", func(t *testing.T) {
		_, stdout, stderr, err := runIsolatedFish(t, ctx, sourcePipelineArgsScript, envWithBait...)
		if err != nil {
			t.Fatalf("pipeline with args failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("DoubleDashFileArgument", func(t *testing.T) {
		_, stdout, stderr, err := runIsolatedFish(t, ctx, sourceDoubleDashScript, envWithBait...)
		if err != nil {
			t.Fatalf("double dash file argument failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("HelpOption", func(t *testing.T) {
		_, stdout, stderr, err := runIsolatedFish(t, ctx, sourceHelpScript, envWithBait...)
		if err != nil {
			t.Fatalf("source -h failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
		if !strings.Contains(stdout, "source - evaluate contents of file") && !strings.Contains(stdout, "source [FILE") {
			t.Fatalf("expected fish source help in stdout, got: %s", stdout)
		}
	})

	t.Run("NonExistentFile", func(t *testing.T) {
		_, _, stderr, err := runIsolatedFish(t, ctx, sourceNonexistentScript, envWithBait...)
		if err == nil {
			t.Fatalf("expected error sourcing non-existent file, got success")
		}
		if !strings.Contains(stderr, "No such file or directory") {
			t.Fatalf("expected 'No such file or directory' in stderr, got: %s", stderr)
		}
	})

	t.Run("MissingBaitBinary", func(t *testing.T) {
		// Provide a clean PATH with only system tools, excluding baitDir
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

		_, stdout, stderr, err := runIsolatedFish(t, ctx, sourceMissingBaitScript, cleanEnv...)
		if err == nil {
			t.Fatalf("expected error when bait is missing, got success")
		}
		if !strings.Contains(stderr, "'bait' is required to translate bash scripts") {
			t.Fatalf("expected missing bait warning in stderr, got: %s\nstdout: %s", stderr, stdout)
		}
	})

	t.Run("AutoloadViaFishFunctionPath", func(t *testing.T) {
		_, stdout, stderr, err := runIsolatedFish(t, ctx, sourceAutoloadFunctionPathScript, envWithBait...)
		if err != nil {
			t.Fatalf("autoload via fish_function_path failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("DotForwardingToSource", func(t *testing.T) {
		_, stdout, stderr, err := runIsolatedFish(t, ctx, sourceDotScript, envWithBait...)
		if err != nil {
			t.Fatalf("dot forwarding to source failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("AutoloadViaFishFunctionPathDot", func(t *testing.T) {
		_, stdout, stderr, err := runIsolatedFish(t, ctx, sourceAutoloadDotScript, envWithBait...)
		if err != nil {
			t.Fatalf("autoload dot via fish_function_path failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("NoFishInPath", func(t *testing.T) {
		_, stdout, stderr, err := runIsolatedFish(t, ctx, sourceNoFishInPathScript, envWithBait...)
		if err != nil {
			t.Fatalf("source failed without fish in PATH: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})
}
