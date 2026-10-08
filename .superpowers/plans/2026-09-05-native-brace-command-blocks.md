# Native Brace Command Blocks & Fish 4.0+ Baseline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upgrade `bait` to target Fish 4.0.0+ runtime natively, replacing all synthetic `begin ... end` command blocks with native Fish 4.0 `{ ... }` syntax.

**Architecture:** Update the AST emitter to emit `{` and `}` for `syntax.Block` and `syntax.Subshell`, replace inline `begin ... end` in multi-statement conditions (`condText`), combiner leaves (`chainLeaf`), parenthesized test expressions (`ParenTest`), and multiline process substitutions with `{ ... }`. Update compatibility docs and test expectations.

**Tech Stack:** Go (1.23+), mvdan.cc/sh/v3 (POSIX/Bash AST parser), Fish Shell (4.0+).

**Spec:** `docs/superpowers/specs/2026-09-05-native-brace-command-blocks-design.md`

## Global Constraints

- Minimum target Fish version is Fish 4.0.0+.
- All block emissions (`{ ... }`) must preserve proper indenting, comments, redirections, and backgrounding without syntax errors under Fish 4.
- Subshell isolation loss warning is preserved with updated text: `subshell isolation is lost: (...) translated to a { ... } block`.
- Zero runtime helper overhead for basic blocks; clean passthrough of braces.

---

### Task 1: Update Block & Subshell Emission in Emitter

**Files:**
- Modify: `internal/bait/emit.go:173-183,307-310,708-720`
- Test: `internal/bait/translate_test.go`
- Test: `internal/bait/helpers_test.go`

**Interfaces:**
- Consumes: `e.tails(s)`, `e.body(stmts, last)`, `classifyComments(s)`
- Produces: `e.printBraceClose(tail string, trailing []syntax.Comment)`, `e.group(s *syntax.Stmt, stmts []*syntax.Stmt, last []syntax.Comment)` emitting `{` and `}`

- [ ] **Step 1: Update test expectations in `translate_test.go` and `helpers_test.go` for block and subshell translation**

Update the test cases in `internal/bait/translate_test.go` and `internal/bait/helpers_test.go` to expect `{ ... }` instead of `begin ... end`:

In `internal/bait/translate_test.go`:
```go
		{
			"{ echo a; echo b; } > out.txt\n",
			"{\n" +
				"    echo a\n" +
				"    echo b\n" +
				"} > out.txt\n",
		},
		{
			"{ sleep 1; } &\n",
			"{\n" +
				"    sleep 1\n" +
				"} &\n",
		},
```
And for subshell tests in `translate_test.go`:
```go
		{"basic subshell", "(cd /tmp && ls)\n", "{\n    cd /tmp && ls\n}\n"},
		{"multiple commands", "(cd /tmp; pwd)\n", "{\n    cd /tmp\n    pwd\n}\n"},
		{"subshell with redirection", "(cd /tmp && pwd) > /tmp/out\n", "{\n    cd /tmp && pwd\n} > /tmp/out\n"},
		{"subshell in chain", "(exit 0) && echo ok\n", "{\n    exit 0\n} && echo ok\n"},
		{"pipe into subshell", "echo hello | (cat)\n", "echo hello | {\n    cat\n}\n"},
		{"subshell inside command substitution", "x=$( (cd /tmp && pwd) )\n", "set x $({\n    cd /tmp && pwd\n})\n"},
```
In `internal/bait/helpers_test.go`:
```go
		{
			"subshell inside substitution becomes begin block",
			"x=$( (echo hi); )\n",
			"set x $({\n    echo hi\n})\n",
			1,
		},
		{
			"subshell inside double quotes inside test",
			"if [ \"$(echo a | (cat))\" = a ]; then echo match; fi\n",
			"if [ \"$(echo a | {\n    cat\n})\" = a ]\n    echo match\nend\n",
			1,
		},
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/bait -run 'TestTranslateStmt/block|TestSubshell|TestSubshellInside'`
Expected: FAIL with diff showing `begin ... end` received vs `{ ... }` expected.

- [ ] **Step 3: Implement `printBraceClose` and update `e.group` in `internal/bait/emit.go`**

In `internal/bait/emit.go`:
Add `printBraceClose`:
```go
func (e *emitter) printBraceClose(tail string, trailing []syntax.Comment) {
	if len(trailing) == 0 {
		e.printf("}%s", tail)
		return
	}
	e.printf("}%s%s", tail, trailingCommentSuffix(trailing[0]))
	for _, c := range trailing[1:] {
		e.comment(c)
	}
}
```
Update `e.group`:
```go
func (e *emitter) group(s *syntax.Stmt, stmts []*syntax.Stmt, last []syntax.Comment) {
	sc := classifyComments(s)
	e.leadingComments(sc.leading)
	tail := e.tails(s)
	if s.Negated {
		e.printf("! {")
	} else {
		e.printf("{")
	}
	e.body(stmts, last)
	e.printBraceClose(tail, sc.trailing)
}
```
Update subshell warning:
```go
	case *syntax.Subshell:
		e.warn(cmd.Lparen, "subshell isolation is lost: (...) translated to a { ... } block")
		e.group(s, cmd.Stmts, cmd.Last)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/bait -run 'TestTranslateStmt/block|TestSubshell|TestSubshellInside'`
Expected: PASS

- [ ] **Step 5: Commit changes**

```bash
git add internal/bait/emit.go internal/bait/translate_test.go internal/bait/helpers_test.go
git commit -m "feat(emitter): translate blocks and subshells to native fish { ... } syntax"
```

---

### Task 2: Update Multi-Statement Conditions & Combiner Leaves

**Files:**
- Modify: `internal/bait/control.go:37-42`
- Modify: `internal/bait/emit.go:616,659`
- Test: `internal/bait/control_test.go` (or `internal/bait/translate_test.go`)

**Interfaces:**
- Consumes: `e.render(st)`
- Produces: `e.condText(cond []*syntax.Stmt)` using `{ ... }`, `e.chainLeaf(st *syntax.Stmt)` using `{ ... }`

- [ ] **Step 1: Write/update tests for multi-statement conditions and combiner chain leaves**

Find test cases in `internal/bait/translate_test.go` or `internal/bait/control_test.go` where `begin ... end` was expected for multi-command conditions or chains:
For example in `internal/bait/translate_test.go`:
```go
		{
			"if echo 1; true; then echo ok; fi\n",
			"if { echo 1; true; }\n    echo ok\nend\n",
		},
```
And check if any test asserts `chainLeaf` with multiple assignments:
```go
		{
			"true && a=1 b=2 && echo ok\n",
			"true && { set -g a 1; set -g b 2; } && echo ok\n",
		},
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/bait -run 'TestTranslateStmt/if|TestCombiner'`
Expected: FAIL showing `begin ... end` received vs `{ ... }` expected.

- [ ] **Step 3: Update `control.go` and `chainLeaf` in `emit.go`**

In `internal/bait/control.go`:
```go
	parts := make([]string, len(cond))
	for i, st := range cond {
		parts[i] = e.render(st)
	}
	return "{ " + strings.Join(parts, "; ") + "; }"
```
In `internal/bait/emit.go` (in `chainLeaf`):
Around line 616:
```go
				return "{ " + strings.Join(parts, "; ") + "; }"
```
Around line 659:
```go
	if len(parts) == 1 {
		return parts[0]
	}
	return "{ " + strings.Join(parts, "; ") + "; }"
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/bait`
Expected: PASS (check which other tests might have asserted `begin ... end` and update if necessary).

- [ ] **Step 5: Commit changes**

```bash
git add internal/bait/control.go internal/bait/emit.go internal/bait/translate_test.go
git commit -m "feat(emitter): use { ... } in multi-command conditions and combiner leaves"
```

---

### Task 3: Update Test Expressions, Process Substitution & Runtime Helper

**Files:**
- Modify: `internal/bait/test_clause.go:95-97`
- Modify: `internal/bait/subst.go:206-208`
- Modify: `internal/bait/helpers/source.fish:44`
- Test: `internal/bait/expr_test.go`
- Test: `internal/bait/helpers_test.go`

**Interfaces:**
- Consumes: `e.renderTestExpr(x.X)`
- Produces: `ParenTest` rendering with `{ ... }`, `procSubst` rendering with `({ ... } | psub)`

- [ ] **Step 1: Update test expectations in `expr_test.go`**

In `internal/bait/expr_test.go`:
```go
		{
			"parenthesized expression in chain",
			"[[ ( -n \"$a\" || -n \"$b\" ) && \"$c\" == \"d\" ]]\n",
			"{ test -n \"$a\" || test -n \"$b\"; } && test \"$c\" = \"d\"\n",
		},
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./internal/bait -run 'TestRenderTestExpr'`
Expected: FAIL showing `begin ... end` received vs `{ ... }` expected.

- [ ] **Step 3: Update `test_clause.go`, `subst.go`, and `helpers/source.fish`**

In `internal/bait/test_clause.go`:
```go
	case *syntax.ParenTest:
		return "{ " + e.renderTestExpr(x.X) + "; }"
```
In `internal/bait/subst.go`:
```go
	endPad := strings.Repeat(indentUnit, e.depth)
	return "({\n" + strings.Join(lines, "\n") + "\n" + endPad + "} | psub)", true
```
In `internal/bait/helpers/source.fish`:
Line 44:
```fish
        set --local __bait_first_line (printf "%s" "$__bait_input" | { set -l l; read -l l; echo "$l"; })
```

- [ ] **Step 4: Run unit and equivalence tests**

Run: `go test ./internal/bait ./cmd/bait`
Expected: PASS

- [ ] **Step 5: Commit changes**

```bash
git add internal/bait/test_clause.go internal/bait/subst.go internal/bait/helpers/source.fish internal/bait/expr_test.go
git commit -m "feat(emitter): use { ... } in test parens, process substitution and runtime helpers"
```

---

### Task 4: Documentation Updates & Full Test Suite Verification

**Files:**
- Modify: `COMPATIBILITY.md`
- Modify: `AGENTS.md`

- [ ] **Step 1: Update `COMPATIBILITY.md`**

Update target version section and table entries:
- Target Version: **Fish 4.0.0+**.
- Subshells note: `( … )` becomes `{ … }` (warning emitted).
- Syntax mappings table:
  - `{ … }` -> `{ … }` (Anonymous command block)
  - `( … )` -> `{ … }` (Anonymous command block, warning emitted)
  - `[[ (c1 || c2) && c3 ]]` -> `{ c1 || c2; } && c3`

- [ ] **Step 2: Update `AGENTS.md`**

Update Principle 1:
- Change `Modern Fish (v3.4.0+)` to `Modern Fish (v4.0.0+)`.

- [ ] **Step 3: Run full test suite and linters**

Run:
- `go test ./...`
- `go test ./e2e`
- `nix run .#fmt` (or `go fmt ./...`)
- `git diff` to ensure no unexpected changes

- [ ] **Step 4: Commit documentation and final cleanups**

```bash
git add COMPATIBILITY.md AGENTS.md
git commit -m "docs: update target version to fish 4.0.0+ and document { ... } blocks"
```
