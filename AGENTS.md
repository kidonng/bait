# bait - Agent Guidance & Architecture

## Overview

`bait` is a high-fidelity Bash-to-Fish shell script translator written in Go. It translates Bash scripts (such as installers, build scripts, and CI workflows) into clean, idiomatic, and directly executable Fish shell scripts.

## Architectural Principles

1. **Leverage Modern Fish Native Compatibility (Passthrough First)**
   - Modern Fish (v3.7+ and v4.x) natively supports or provides wrappers for many POSIX/Bash constructs (pipes, redirections, combiners, command substitutions, brace expansions, per-command env `VAR=val cmd`, backgrounding, and builtins like `alias`, `pushd`, `trap`, `umask`, `eval`, `wait`).
   - **Rule**: Never rewrite what Fish already natively accepts. Unchanged constructs pass through byte-for-byte (modulo printer whitespace normalization).

2. **Minimal & Principled Transformation**
   - Translate only constructs structurally or lexically incompatible with Fish:
     - **Control Flow**: Convert keywords to Fish block syntax (`if/else if/else/end`, `while/end`, `for/end`, `switch/case/end`, `function ... end`).
     - **Uniform Scoping**:
       - Function-body assignments without `local` map to `set --global`.
       - Explicit `local` (or `declare` / `typeset` within functions) maps to `set --function`; top-level `declare` or `typeset` maps to plain `set`.
       - Reassignment to declared locals emits plain `set` (retaining function scope).
       - **Strict Rule**: No function name heuristics (e.g. `main` is treated identically to any other function). Do not simulate caller-callee dynamic scoping.
     - **Parameter Expansions & Builtins**: Map POSIX/Bash expansions (`$?`, `$0`, `$#`, `$@`, `${v:-def}`, `${v/a/b}`) and state commands (`set`, `shift`, `unset`, `read`) to Fish builtins using long options (`set --function`, `set --global`, `set --append`, `string replace -- ...`).
     - **On-Demand Runtime Helpers**: When scripts require semantics Fish lacks natively, inject minimal, self-contained helpers at file top:
       - `getopts`: Pure-Fish option parser tracking `$OPTIND` and `$OPTARG`.
       - `__bait_words`: Unquoted variable expansion and command substitution field splitting matching POSIX `$IFS`.
       - `__bait_exec`: Dynamic command string execution with flag splitting.

3. **Explicit Warning Contract over Silent Breakage**
   - When encountering constructs with no faithful Fish equivalent (C-style `for ((;;))` loops, `select`, ternary `?:`, namerefs, unsupported builtins like `shopt`), emit the construct verbatim and print a diagnostic warning to stderr with line/column coordinates.
   - Never silently truncate or generate invalid scripts.

4. **Hermetic Development & Differential Testing**
   - All tools and shell versions are hermetically pinned via `flake.nix`.
   - **Differential Equivalence (`internal/bait`)**: `TestBashFishEquivalence` executes snippets concurrently under GNU Bash and Fish, asserting identical stdout, stderr, and exit status.
   - **Sandbox E2E Suite (`e2e`)**: Real-world installers tested in isolated environments:
     - Pixi installer (`https://pixi.sh/install.sh`)
     - Starship installer (`https://starship.rs/install.sh`)
     - uv installer (`https://astral.sh/uv/install.sh`)
     - Rustup installer (`https://sh.rustup.rs`)

## Developer Workflows

- **Run CLI**: `nix run . -- [options] [script]` or `echo '...' | nix run .`
- **Run All Tests**: `nix run .#test` or `bait-test` inside `nix develop`
- **Run Specific Tests**:
  - `nix run .#test -- ./internal/bait` (unit & differential tests)
  - `nix run .#test -- ./e2e` (sandbox integration tests)
  - `nix run .#test -- -run TestRustup ./e2e` (single target)

## Repository Layout

- `cmd/bait/`: CLI binary entry point (streaming stdin/stdout, file translation, `--quiet` flag).
- `internal/bait/`: Core translation engine:
  - `translate.go`: High-level entry points and AST parsing via `mvdan.cc/sh/v3/syntax`.
  - `emit.go`: AST normalization, structural emission, pure-Fish runtime helpers, and diagnostic warning collection.
  - `shebang.go`: Shebang line inspection and rewriting.
  - `translate_test.go`: Unit tests, warning assertions, and `TestBashFishEquivalence` differential test runner.
- `e2e/`: End-to-end sandbox integration tests verifying translated real-world installers against live Fish runtimes.
- `COMPATIBILITY.md`: User-facing compatibility inventory and differences.
- `flake.nix` & `flake.lock`: Nix build packages, apps (`bait`, `test`), and development shell (`bait-test`).
