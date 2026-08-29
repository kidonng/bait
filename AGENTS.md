# Developer Guide

## Overview

`bait` is a high-fidelity Bash-to-Fish shell script translator written in Go. It translates Bash scripts (such as installers, build scripts, and CI workflows) into clean, idiomatic, and directly executable Fish shell scripts.

## Architectural Principles

1. **Leverage Modern Fish Native Compatibility (Passthrough First)**
   - Modern Fish (v3.7+ and v4.x) natively supports or provides wrappers for many POSIX/Bash constructs (pipes, redirections, combiners, command substitutions, brace expansions, per-command environment variables, backgrounding, and builtins).
   - **Rule**: Never rewrite what Fish already natively accepts. Unchanged constructs pass through byte-for-byte (modulo printer whitespace normalization).

2. **Minimal & Principled Transformation**
   - Translate only constructs structurally or lexically incompatible with Fish:
     - **Control Flow**: Transform Bash compound statements into Fish block syntax (`if/else if/else/end`, `while/end`, `for/end`, `switch/case/end`, `function ... end`).
     - **Uniform Lexical Scoping**: Map variable scopes predictably via lexical analysis (unadorned assignments to undeclared variables in functions target global scope, explicit declarations target function scope, and local reassignments within the same function lexical scope target local scope). Never use function-name heuristics (e.g. `main` is treated identically to any other function) and never simulate dynamic caller-callee scoping.
     - **Explicit Long-Option Builtins**: Prefer readable, explicit Fish builtin flags (such as `--function`, `--global`, `--append`) over ambiguous or implicit state flags.
     - **Symmetric Variable Collision Avoidance**: Systematically mangle variable names that collide with Fish read-only variables across all binding, declaration, and expansion sites. Never use one-sided or ad-hoc renames.
     - **Dataflow-Driven Word Splitting over Call-Site Name Heuristics**: Inject call-site word splitting on-demand via assignment dataflow analysis instead of guessing call-site splitting via variable name heuristics, while adhering to Fish's native list semantics (such as list-valued environment variables).
     - **Decoupled Context Introspection**: Unify execution context introspection (script paths, function frames, execution IDs) behind a dedicated translation layer decoupled from generic array index arithmetic.
     - **Zero-Footprint Runtime Helpers**: Inject pure-Fish helper functions strictly on-demand when modern Fish lacks native semantics; clean scripts must carry zero runtime overhead.

3. **Explicit Warning Contract over Silent Breakage**
   - When encountering constructs with no faithful Fish equivalent (e.g. C-style loops, namerefs, unsupported builtins), emit the construct verbatim and print a diagnostic warning to stderr with source coordinates.
   - Never silently truncate or generate invalid scripts.

4. **Hermetic Development & Differential Testing**
   - All tools and shell versions are hermetically pinned via `flake.nix`.
   - **Differential Equivalence (`internal/bait`)**: Validate translations by executing original scripts under GNU Bash and translated outputs under Fish concurrently, asserting identical stdout, stderr, and exit status.
   - **Sandbox E2E Suite (`e2e`)**: Continuously verify against complex real-world installer scripts in isolated environments.

## Maintenance Rules

- **High-Level Decisions Only**: Keep this document focused strictly on architectural invariants, principles, and workflows. Never treat it as a changelog or feature dump.
- **No Implementation Enumerations**: Do not list specific variable names, parameter expansions, helper functions, AST passes, or regex patterns here.
- **Single Source of Truth**: Syntax mapping and compatibility belong in `COMPATIBILITY.md`; concrete translation logic and helpers belong in code and tests.

## Developer Workflows

- **Run CLI**: `nix run . -- [options] [script]` or `echo '...' | nix run .`
- **Run All Tests**: `nix run .#test` or `bait-test` inside `nix develop`
- **Run Checks**: `nix flake check`
- **Run Specific Tests**:
  - `nix run .#test -- ./internal/bait` (unit & differential tests)
  - `nix run .#test -- ./e2e` (sandbox integration tests)
  - `nix run .#test -- -run TestRustup ./e2e` (single target)
- **Format Code**: `nix run .#fmt`, `nix fmt`, or `bait-fmt` inside `nix develop`

## Repository Layout

- `cmd/bait/`: CLI binary entry point (streaming stdin/stdout, file translation, `--quiet` flag).
- `internal/bait/`: Core translation engine (AST parsing, normalization, pure-Fish emission, diagnostics).
- `e2e/`: End-to-end sandbox integration tests verifying translated real-world installers against live Fish runtimes.
- `COMPATIBILITY.md`: Exhaustive user-facing compatibility inventory, syntax mappings, and runtime differences.
- `flake.nix` & `flake.lock`: Hermetic Nix environment, packages, and test runners.
- `scripts/`: Development workflow and automation scripts (`test.fish`, `fmt.fish`).
