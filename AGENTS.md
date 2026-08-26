# bait - Agent Guidance & Architecture

## Overview

`bait` is a high-fidelity Bash-to-Fish shell script translator written in Go. It translates Bash scripts (such as installers, build scripts, and CI workflows) into clean, idiomatic, and directly executable Fish shell scripts.

## Architectural Principles & Decisions

1. **Leverage Modern Fish Native Compatibility (Passthrough First)**
   - Modern Fish (v3.7+ and v4.x) natively supports or ships wrappers for many POSIX and Bash constructs, including:
     - Pipes (`|`, `|&`, `2>|`), redirections (`>`, `2>`, `&>`, `2>&1`, etc.), and combiners (`&&`, `||`, `!`).
     - Command substitutions (`$(...)`), backgrounding (`&`), brace expansions (`{a,b}`), and test brackets (`[ ... ]`).
     - Standard utility stubs and built-in functions (`export`, `alias`, `pushd`, `popd`, `dirs`, `trap`, `umask`, `ulimit`, `eval`, `exec`, `wait`).
   - **Decision**: Never rewrite what Fish already natively accepts. Unchanged constructs pass through byte-for-byte (modulo printer formatting) to maintain fidelity and readability.

2. **Minimal and Faithful Transformation**
   - Translate only what is structurally or lexically incompatible with Fish:
     - Control flow keywords (`if/then/elif/else/fi` -> `if/else if/else/end`, `while/until` -> `while/while not`, `for` -> `for/end`, `case` -> `switch/case/end`, `f() { ... }` -> `function f ... end`).
     - Variable assignments (`VAR=val` -> `set VAR val`, `local` -> `set --local`, function assignments -> `set --global`).
     - Parameter expansions (`$?` -> `$status`, `$0` -> `$(status filename)`, `$#` -> `$(count $argv)`, `$1` -> `$argv[1]`, `${v:-def}` -> `test/printf` chains, `${v/a/b}` -> `string replace`).
     - Integer arithmetic (`$((expr))` -> `$(math --scale=0 "expr")`, `((i++))` -> `set i $(math --scale=0 "$i + 1")`, comparisons -> `test`).
     - Shell builtin sanitization (e.g. dropping `set -e` flags, sanitizing `read _` to avoid Fish's read-only `$_`).

3. **Explicit Warning Contract over Silent Breakage**
   - When encountering constructs with no faithful Fish equivalent (e.g. C-style `for ((;;))` loops, `select` loops, unsupported parameter expansion operators, missing builtins like `shopt` or `hash`), `bait` emits the original construct verbatim and reports a diagnostic warning with source line/column coordinates.
   - Scripts are never silently corrupted or partially translated without clear diagnostics.

4. **Hermetic Development & Differential Testing**
   - **Reproducible Environment**: Nix flake (`flake.nix`) provides pinned versions of Go, GNU Bash, and Fish for consistent local and CI development.
   - **Dual-Shell Differential Testing**: The test suite validates translations by executing original scripts under real `bash` and translated outputs under real `fish`, asserting identical stdout, stderr, and exit codes.
   - **End-to-End Real-World Validation**: Continuously verified against complex real-world installers (e.g. Pixi's multi-platform installer script) in isolated container and virtual machine environments.

## Repository Layout

- `cmd/bait/`: CLI binary entry point (stdin/stdout streaming, file translation, `--quiet` flag).
- `internal/bait/`: Core translation engine:
  - `translate.go`: High-level entry points and AST parsing via `mvdan.sh/v3/syntax`.
  - `emit.go`: AST normalization, structural emission, and diagnostic warning collection.
  - `shebang.go`: Shebang line inspection and rewriting.
  - `translate_test.go`: Unit tests, warning assertions, and `TestBashFishEquivalence` differential test runner.
- `COMPATIBILITY.md`: Detailed inventory of supported constructs, differences, and verbatim fallbacks.
- `flake.nix` & `flake.lock`: Nix development shell configuration.
