# Specification: Native Brace Command Blocks & Fish 4.0+ Baseline

## Summary

Upgrade `bait`'s target Fish runtime baseline from Fish 3.4.0+ to **Fish 4.0.0+**, and replace all synthetic `begin ... end` command blocks with native Fish 4.0 `{ ... }` syntax across all translation paths (anonymous command blocks, subshells, inline multi-command conditions, combiner chain leaf statements, and test grouping).

## Motivation

Fish 4.0 (introduced with the Rust core rewrite) natively supports the `{ [COMMANDS ...] }` syntax as an exact, first-class alternative to `begin ... end`. It supports variable scoping (`set -l`), redirections, pipes, backgrounding, and combiners.

According to `AGENTS.md` Principle 1 (*Leverage Modern Fish Native Compatibility (Passthrough First)*):
> Never rewrite what Fish already natively accepts. Unchanged constructs pass through byte-for-byte (modulo printer whitespace normalization).

Translating Bash `{ ... }` into `begin ... end` is no longer necessary in Fish 4.0+. Retaining `{ ... }` aligns the output much closer to the original Bash script and removes synthetic translation overhead.

## Requirements & Scope

### 1. Target Version Contract
- Minimum required Fish version is updated to **Fish 4.0.0+**.
- Updated in `AGENTS.md` and `COMPATIBILITY.md`.

### 2. Block Translation (`*syntax.Block`)
- Bash anonymous block `{ ... }`:
  ```bash
  {
      cmd1
      cmd2
  } > out.txt
  ```
  Translates to:
  ```fish
  {
      cmd1
      cmd2
  } > out.txt
  ```
- Negated block:
  ```fish
  ! {
      cmd
  }
  ```
- Redirections and trailing backgrounding/combiners attach to `}` directly: `} > out.txt`, `} &`.

### 3. Subshell Translation (`*syntax.Subshell`)
- Standalone subshells `( ... )`:
  ```bash
  (cd /tmp && ls)
  ```
  Translates to:
  ```fish
  {
      cd /tmp && ls
  }
  ```
  with diagnostic warning updated:
  `subshell isolation is lost: (...) translated to a { ... } block`

### 4. Multi-Command Conditions (`condText` in `control.go`)
- Multi-statement condition in `if`/`while`:
  ```bash
  if cmd1; cmd2; then ... fi
  ```
  Translates to:
  ```fish
  if { cmd1; cmd2; }
      ...
  end
  ```

### 5. Combiner Chain Leaves (`chainLeaf` in `emit.go`)
- Multi-assignment sequences or multiline inlined statements inside `&&` / `||` chains:
  ```bash
  cmd1 && a=1 b=2 && cmd2
  ```
  Translates to:
  ```fish
  cmd1 && { set -g a 1; set -g b 2; } && cmd2
  ```

### 6. Parenthesized Test Expressions (`*syntax.ParenTest` in `test_clause.go`)
- Grouped test conditions:
  ```bash
  [[ ( -n "$a" || -n "$b" ) && "$c" == "d" ]]
  ```
  Translates to:
  ```fish
  { test -n "$a" || test -n "$b"; } && test "$c" = "d"
  ```

### 7. Process Substitution (`subst.go`)
- Multiline command process substitution `<(...)`:
  ```bash
  cat <(
      echo a
      echo b
  )
  ```
  Translates to:
  ```fish
  cat ({
      echo a
      echo b
  } | psub)
  ```

### 8. Runtime Helper Updates (`helpers/source.fish`)
- Update `__bait_first_line` pipeline in `source.fish` from `begin; ...; end` to `{ ... }`.

## Design Details

### Emitter Changes (`internal/bait/emit.go`)
1. Introduce `printBraceClose(tail string, trailing []syntax.Comment)` analogous to `printEnd`:
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
2. Update `e.group`:
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
3. Update `Subshell` diagnostic warning:
   `e.warn(cmd.Lparen, "subshell isolation is lost: (...) translated to a { ... } block")`
4. Update `chainLeaf`:
   Replace `"begin; " + strings.Join(parts, "; ") + "; end"` with `"{ " + strings.Join(parts, "; ") + "; }"`.

### Control Flow (`internal/bait/control.go`)
- In `condText`:
  Replace `"begin " + strings.Join(parts, "; ") + "; end"` with `"{ " + strings.Join(parts, "; ") + "; }"`.

### Test Expressions (`internal/bait/test_clause.go`)
- In `ParenTest`:
  Replace `"begin " + e.renderTestExpr(x.X) + "; end"` with `"{ " + e.renderTestExpr(x.X) + "; }"`.

### Process Substitution (`internal/bait/subst.go`)
- In `procSubst`:
  Replace `"(begin\n" + ... + "\n" + endPad + "end | psub)"` with `"(\n" + ...` or `"({\n" + ... + "\n" + endPad + "} | psub)"`.

### Documentation (`AGENTS.md` & `COMPATIBILITY.md`)
- Update target runtime description to Fish 4.0.0+.
- Update syntax tables and examples referencing `begin ... end` for blocks.

## Test Strategy

1. **Unit & Translation Tests (`internal/bait`)**:
   - Update expected output in `translate_test.go`, `helpers_test.go`, `expr_test.go` to assert `{ ... }` blocks.
   - Verify comment preservation on opening `{` and closing `}`.
   - Verify redirections (`{ ... } > file`), pipes, and backgrounding (`{ ... } &`).
2. **Equivalence Tests**:
   - Differential equivalence testing (`go test ./internal/bait ./cmd/bait`) executes Bash scripts and translated Fish scripts under the live Fish runtime (`fish 4.8.1` in devTools), verifying stdout, stderr, and exit codes match.
3. **E2E Tests (`e2e`)**:
   - Ensure all real-world E2E scripts pass with `go test ./e2e`.
4. **Code Quality Checks**:
   - Run `nix run .#fmt` and `nix run .#test`.
