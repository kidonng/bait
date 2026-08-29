package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

	// Base environment with bait prepended to PATH
	origPath := os.Getenv("PATH")
	envWithBait := []string{"PATH=" + baitDir + ":" + origPath}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	t.Run("NativeFishScript", func(t *testing.T) {
		fishScript := fmt.Sprintf(`
source %s

set script_path (mktemp)
echo 'set --global GREETING "hello from fish"' > $script_path
echo 'function fish_add; math $argv[1] + $argv[2]; end' >> $script_path

source $script_path
test "$GREETING" = "hello from fish"; or exit 1
test (fish_add 3 4) -eq 7; or exit 2
rm -f $script_path
`, sourceFish)

		_, stdout, stderr, err := runIsolatedFish(t, ctx, []byte(fishScript), envWithBait...)
		if err != nil {
			t.Fatalf("native fish script execution failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("BashScriptTranslation", func(t *testing.T) {
		fishScript := fmt.Sprintf(`
source %s

set script_path (mktemp)
echo 'GREETING="hello from bash"' > $script_path
echo 'ARG_CONCAT="${1}_${2}"' >> $script_path
echo 'COUNT=$#' >> $script_path
echo 'my_bash_calc() { local a=$1; local b=$2; echo $((a * b)); }' >> $script_path

source $script_path foo bar
test "$GREETING" = "hello from bash"; or begin; echo "greeting mismatch: $GREETING"; exit 1; end
test "$ARG_CONCAT" = "foo_bar"; or begin; echo "arg concat mismatch: $ARG_CONCAT"; exit 2; end
test "$COUNT" = "2"; or begin; echo "count mismatch: $COUNT"; exit 3; end
test (my_bash_calc 6 7) -eq 42; or begin; echo "calc mismatch"; exit 4; end
rm -f $script_path
`, sourceFish)

		_, stdout, stderr, err := runIsolatedFish(t, ctx, []byte(fishScript), envWithBait...)
		if err != nil {
			t.Fatalf("bash script translation failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("BashScriptReturnCode", func(t *testing.T) {
		fishScript := fmt.Sprintf(`
source %s

set script_path (mktemp)
echo 'FOO=bar' > $script_path
echo 'return 42' >> $script_path
echo 'FOO=baz' >> $script_path

source $script_path
set res $status
test $res -eq 42; or begin; echo "expected status 42, got $res"; exit 1; end
test "$FOO" = "bar"; or begin; echo "expected FOO=bar, got $FOO"; exit 2; end
rm -f $script_path
`, sourceFish)

		_, stdout, stderr, err := runIsolatedFish(t, ctx, []byte(fishScript), envWithBait...)
		if err != nil {
			t.Fatalf("bash return code propagation failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("PipelineFishScript", func(t *testing.T) {
		fishScript := fmt.Sprintf(`
source %s

echo 'set --global PIPE_FISH_VAL "ok"' | source
test "$PIPE_FISH_VAL" = "ok"; or exit 1
`, sourceFish)

		_, stdout, stderr, err := runIsolatedFish(t, ctx, []byte(fishScript), envWithBait...)
		if err != nil {
			t.Fatalf("pipeline fish script failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("PipelineBashScript", func(t *testing.T) {
		fishScript := fmt.Sprintf(`
source %s

echo 'PIPE_BASH_VAL="translated"' | source
test "$PIPE_BASH_VAL" = "translated"; or exit 1
`, sourceFish)

		_, stdout, stderr, err := runIsolatedFish(t, ctx, []byte(fishScript), envWithBait...)
		if err != nil {
			t.Fatalf("pipeline bash script failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("PipelineWithArguments", func(t *testing.T) {
		fishScript := fmt.Sprintf(`
source %s

echo 'ARG_RESULT="${1}-${2}"' | source - first second
test "$ARG_RESULT" = "first-second"; or exit 1
`, sourceFish)

		_, stdout, stderr, err := runIsolatedFish(t, ctx, []byte(fishScript), envWithBait...)
		if err != nil {
			t.Fatalf("pipeline with args failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("DoubleDashFileArgument", func(t *testing.T) {
		fishScript := fmt.Sprintf(`
source %s

set script_path (mktemp)
echo 'VAR="from double dash"' > $script_path

source -- $script_path
test "$VAR" = "from double dash"; or exit 1
rm -f $script_path
`, sourceFish)

		_, stdout, stderr, err := runIsolatedFish(t, ctx, []byte(fishScript), envWithBait...)
		if err != nil {
			t.Fatalf("double dash file argument failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("HelpOption", func(t *testing.T) {
		fishScript := fmt.Sprintf(`
source %s
source -h
`, sourceFish)

		_, stdout, stderr, err := runIsolatedFish(t, ctx, []byte(fishScript), envWithBait...)
		if err != nil {
			t.Fatalf("source -h failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
		if !strings.Contains(stdout, "source - evaluate contents of file") && !strings.Contains(stdout, "source [FILE") {
			t.Fatalf("expected fish source help in stdout, got: %s", stdout)
		}
	})

	t.Run("NonExistentFile", func(t *testing.T) {
		fishScript := fmt.Sprintf(`
source %s
source /tmp/nonexistent_file_for_bait_test_12345
`, sourceFish)

		_, _, stderr, err := runIsolatedFish(t, ctx, []byte(fishScript), envWithBait...)
		if err == nil {
			t.Fatalf("expected error sourcing non-existent file, got success")
		}
		if !strings.Contains(stderr, "No such file or directory") {
			t.Fatalf("expected 'No such file or directory' in stderr, got: %s", stderr)
		}
	})

	t.Run("MissingBaitBinary", func(t *testing.T) {
		// Environment WITHOUT bait in PATH
		fishScript := fmt.Sprintf(`
source %s

set script_path (mktemp)
echo 'VAR="should fail without bait"' > $script_path

source $script_path
set res $status
rm -f $script_path
exit $res
`, sourceFish)

		// Provide a clean PATH with only system tools, excluding baitDir
		cleanEnv := []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin:" + origPath}
		// Ensure baitDir is filtered out from PATH
		var filteredPath []string
		for _, p := range strings.Split(origPath, ":") {
			if p != baitDir {
				filteredPath = append(filteredPath, p)
			}
		}
		cleanEnv = []string{"PATH=" + strings.Join(filteredPath, ":")}

		_, stdout, stderr, err := runIsolatedFish(t, ctx, []byte(fishScript), cleanEnv...)
		if err == nil {
			t.Fatalf("expected error when bait is missing, got success")
		}
		if !strings.Contains(stderr, "'bait' is required to translate bash scripts") {
			t.Fatalf("expected missing bait warning in stderr, got: %s\nstdout: %s", stderr, stdout)
		}
	})
	t.Run("AutoloadViaFishFunctionPath", func(t *testing.T) {
		functionsDir := filepath.Dir(sourceFish)
		fishScript := fmt.Sprintf(`
set --unexport --prepend fish_function_path %s

set script_path (mktemp)
echo 'AUTOLOAD_VAR="autoloaded_ok"' > $script_path

source $script_path
test "$AUTOLOAD_VAR" = "autoloaded_ok"; or exit 1
rm -f $script_path
`, functionsDir)

		_, stdout, stderr, err := runIsolatedFish(t, ctx, []byte(fishScript), envWithBait...)
		if err != nil {
			t.Fatalf("autoload via fish_function_path failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("DotForwardingToSource", func(t *testing.T) {
		fishScript := fmt.Sprintf(`
source %s

set script_path (mktemp)
echo 'DOT_VAR="${1}_${2}"' > $script_path

. $script_path hello dot
test "$DOT_VAR" = "hello_dot"; or exit 1
rm -f $script_path
`, sourceFish)

		_, stdout, stderr, err := runIsolatedFish(t, ctx, []byte(fishScript), envWithBait...)
		if err != nil {
			t.Fatalf("dot forwarding to source failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("AutoloadViaFishFunctionPathDot", func(t *testing.T) {
		functionsDir := filepath.Dir(sourceFish)
		fishScript := fmt.Sprintf(`
set --unexport --prepend fish_function_path %s

set script_path (mktemp)
echo 'AUTOLOAD_DOT_VAR="dot_ok"' > $script_path

. $script_path
test "$AUTOLOAD_DOT_VAR" = "dot_ok"; or exit 1
rm -f $script_path
`, functionsDir)

		_, stdout, stderr, err := runIsolatedFish(t, ctx, []byte(fishScript), envWithBait...)
		if err != nil {
			t.Fatalf("autoload dot via fish_function_path failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})

	t.Run("NoFishInPath", func(t *testing.T) {
		fishScript := fmt.Sprintf(`
source %s

# Strip all directories containing fish from PATH
while command --query fish
    set --local fish_loc (dirname (command --search fish))
    set --local new_path
    for p in $PATH
        if test "$p" != "$fish_loc"
            set --append new_path $p
        end
    end
    set --export PATH $new_path
end

if command --query fish
    echo "fish should not be in PATH for this test" >&2
    exit 99
end

# 1. Native fish script should still succeed via status fish-path
set script_path (mktemp)
echo 'set --global NATIVE_OK "yes"' > $script_path
source $script_path
test "$NATIVE_OK" = "yes"; or exit 1
rm -f $script_path

# 2. Bash script translation should also work via status fish-path
set bash_path (mktemp)
echo 'BASH_OK="yes"' > $bash_path
source $bash_path
test "$BASH_OK" = "yes"; or exit 2
rm -f $bash_path
`, sourceFish)

		_, stdout, stderr, err := runIsolatedFish(t, ctx, []byte(fishScript), envWithBait...)
		if err != nil {
			t.Fatalf("source failed without fish in PATH: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	})
}
