# Coherence Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate test-overfit special cases and make each translation rule derive from a bash→fish semantic invariant, fixing every execution-verified divergence in `.superpowers/specs/2026-10-09-coherence-audit.md`.

**Architecture:** Three systemic refactors carry most of the fixes: (1) every statement is rendered through one translating pipeline so rewrites cannot be context-dependent; (2) one parameter/variable resolver maps bash names to fish references (native var / mangled var / substitution) and is consulted by words, operators, arithmetic and bindings alike; (3) one dataflow-driven list-variable policy decides expansion quoting at every site. Remaining fixes are construct-local, each pinned by a differential fixture.

**Tech Stack:** Go 1.26, `mvdan.cc/sh/v3` AST, embedded Fish helpers, differential tests executing bash (nix) vs fish (nix, 4.9.x).

**Spec:** `.superpowers/specs/2026-10-09-coherence-audit.md`

## Global Constraints

- Fish baseline is the pinned nix fish (currently 4.9.3); docs must state the real minimum for `{ … }` blocks (4.1.0) — verified via release notes.
- `BASH_VERSION` keeps a single documented emulation constant (user decision): one constant, named, documented, no per-site literals.
- Unsupported constructs: emit verbatim **plus** a warning with line:column. No silent drops, no silently invalid output where a translation exists.
- Every task lands with a differential fixture in `internal/bait/equivalence_test.go` (bash vs fish: stdout, stderr, exit) unless the construct cannot execute in the harness; then a unit test asserting translated text.
- No new dependencies. Run `nix run .#test` green before each commit; `nix run .#e2e` before the final commit.

## Review Focus

Inputs the spec implies but no current test exercises; each gets a fixture in its owning task.

- Empty/unset arithmetic operands (`a=""; ((a<1))`, `((x++))` with x=0) — status and stderr must match bash.
- PATH-suffixed variables (`MY_PATH=a:b:c`) in `for`, assignment RHS, `case` subject — list-ness must not leak.
- Double-quoted expansions followed by identifier characters or `[` (`"${V}_suffix"`, `"$foo[0]"`).
- Redirect-only statements in every position (top level, if-body, `&&` leaf, command substitution) — file effects must match bash.
- Case patterns with quoted/escaped wildcards and bracket classes — literal vs glob semantics must match bash.

---

### Task 1: Redirect-only statements and `$(<file)`

**Files:**
- Modify: `internal/bait/emit.go` (`stmt`, `simple`, `chainLeaf`, `condText` interplay)
- Modify: `internal/bait/subst.go` (`cmdSubstText`)
- Test: `internal/bait/equivalence_test.go`, `internal/bait/translate_test.go`

**Interfaces:**
- Produces: `func normalizeRedirectOnlyStmt(s *syntax.Stmt) bool` — rewrites `s.Cmd == nil && len(s.Redirs) > 0` into `Cmd = CallExpr{Args: [true]}`; returns true when rewritten. Called from `normalize`'s walk so ALL contexts see the same node.

- [ ] Step 1: Add failing fixtures: `> out.txt` then `cat out.txt`; `if true; then > a.txt; fi; test -f a.txt && echo yes`; `true && > b.txt; cat b.txt`; `y=$(< f.txt); echo "y=$y"`; heredoc-only `cat <<EOF …`.
- [ ] Step 2: Run `nix run .#test`; expect new fixtures FAIL (missing files, empty y, dangling `&&`).
- [ ] Step 3: Implement the normalize rewrite; for command substitutions whose body is a single redirect-only statement with a `<` redirect, emit `(cat <target>)`.
- [ ] Step 4: `nix run .#test` green.
- [ ] Step 5: Commit.

### Task 2: Quoted expansion boundaries and literal-bracket escaping

**Files:**
- Modify: `internal/bait/params.go` (`spliceParts`, `needsFishBracing`)
- Modify: `internal/bait/vars.go` (`escapeLiteralDollars` walk)
- Test: `internal/bait/params_test.go`, `internal/bait/equivalence_test.go`

**Interfaces:**
- Produces: quoted-path splitting — when a `ParamExp` replacement is directly followed (inside `DblQuoted`) by a `Lit` whose first rune is a name character, split the `DblQuoted` into two adjacent quoted words (`"$V""_suffix"`); `{$var}` is NOT usable inside double quotes (verified: braces stay literal).
- Produces: extend the literal-dollar pass to escape `[` in a `Lit` that immediately follows any expansion replacement (unquoted: `$V\[0\]`, quoted: segment split), so fish cannot read it as subscripting.

- [ ] Step 1: Failing fixtures: `V=hello; echo "${V}_suffix"`; `set -- a b; echo "P$@S"` (with args); `foo=hello; echo $foo[1]`; `echo "src=$BASH_SOURCE[0]"`.
- [ ] Step 2: Verify failures under `nix run .#test`.
- [ ] Step 3: Implement splitting + bracket escaping; keep the existing unquoted `{$V}` path for scalar-safe cases.
- [ ] Step 4: Suite green.
- [ ] Step 5: Commit.

### Task 3: One statement-rendering pipeline (conditions, comments, chains)

**Files:**
- Modify: `internal/bait/emit.go` (`simple` → `emitStmt`, `chainLeaf`, `inlineStmtText`)
- Modify: `internal/bait/control.go` (`condText`, `ifClause`, `whileClause`)
- Test: `internal/bait/translate_test.go`, `internal/bait/equivalence_test.go`

**Interfaces:**
- Produces: `func (e *emitter) stmtText(st *syntax.Stmt) string` (rename/wrap `inlineStmtText`) as THE single translating renderer for any statement, used by top level (`stmt`), chain leaves, and condition statements; `condText` for multi-statement conditions emits a multi-line `{ … }` block with translated statements and hoists leading comments above the header line.
- Consumes: Task 1 normalization.

- [ ] Step 1: Failing fixtures: `if a=1; b=2; then echo ok; fi`; condition with leading comment; `true && set -- a b; echo "$1"`; `set -- x && echo ok`; `while shift; do …; done`.
- [ ] Step 2: Implement; delete now-dead special cases in `chainLeaf`/`simple` that duplicate `stmtText` handling.
- [ ] Step 3: Suite green (expect several existing translate tests updated to the unified output only where output legitimately changes; run full suite).
- [ ] Step 4: Commit.

### Task 4: Function-definition operators

**Files:**
- Modify: `internal/bait/emit.go` (`funcDecl`, delete the `FuncDecl` branch in `binary`)
- Test: `internal/bait/equivalence_test.go`

- [ ] Step 1: Failing fixture: `f() { false; } || echo never; f; echo "st=$?"` (bash: st=1, no `never`); plus `f() { echo a; } | cat`.
- [ ] Step 2: Implement: emit definition, then `true <op> Y`; remove dead branch.
- [ ] Step 3: Suite green; commit.

### Task 5: `$-` predicate restriction

**Files:**
- Modify: `internal/bait/control.go` (`matchFlagRule` → `dollarDashPredicate`, `evalInteractiveCase`), `internal/bait/test_clause.go` (`evalInteractiveTest`)
- Test: `internal/bait/expr_test.go`, `internal/bait/equivalence_test.go`

**Interfaces:**
- Produces: `func dollarDashPredicate(pat *syntax.Word, isRegex bool) (pred string, ok bool)`: true only for case/`==` patterns exactly `*i*` (case-sensitive, no other tokens), and for `=~` the single token `i`; negation handled by callers.
- `evalInteractiveCase` claims an item only when **every** pattern of the item is recognized as the same predicate; otherwise the generic path runs **with a warning**.

- [ ] Step 1: Failing fixtures: `bash -c 'case $- in *I*) …'` equivalence is not directly runnable (interactivity); use unit tests on translated text + a runnable `[[ $- == *i* ]]` fixture under both shells non-interactively.
- [ ] Step 2: Implement; warn for any `$-` pattern not exactly contains-i; unify the emulation string (see Task 11).
- [ ] Step 3: Suite green; commit.

### Task 6: Arithmetic status, operators, operand coercion

**Files:**
- Modify: `internal/bait/arith.go`
- Test: `internal/bait/expr_test.go`, `internal/bait/equivalence_test.go`

**Interfaces:**
- Produces: status-preserving emit for assignment/inc/dec forms: `set x (math …)` followed by `test <coerced x> -ne 0` (for `x++`/`x--` use `math "$x - 1"` / `"$x + 1"` as the old value); comparison operands coerced as `(math --scale=0 "<operand>" 2>/dev/null; or echo 0)`; `**`/`**=` map to `pow(a, b)`; unmapped operators keep the existing warn+verbatim.
- Consumes: nothing new.

- [ ] Step 1: Failing fixtures: `i=0; if ((i++)); then echo then; else echo else; fi` (bash: else); `i=3; while ((i--)); do echo "t:$i"; done` terminates; `((x=0)); echo "st=$?"` → 1; `echo $((2**3))` → 8; `a=""; ((a<1)) && echo yes` → yes.
- [ ] Step 2: Implement; keep output single-command where possible for chains.
- [ ] Step 3: Suite green; commit.

### Task 7: Warning coverage for silent paths

**Files:**
- Modify: `internal/bait/params.go` (`paramReplacements` fall-through), `internal/bait/emit.go` (brace sequences), `internal/bait/redir.go` (`bashOnlyBuiltins`)
- Test: `internal/bait/params_test.go`, `internal/bait/translate_test.go`

- [ ] Step 1: Failing tests: `${!x}`, `${#arr[0]}`, `${!pre*}` produce a warning naming the construct; `{1..3}` translates to `(seq 1 3)` (numeric, with step) or warns when non-numeric (`{a..c}`); `mapfile`/`readarray`/`times`/`logout` warn.
- [ ] Step 2: Implement.
- [ ] Step 3: Suite green; commit.

### Task 8: Parameter resolver for operators, arithmetic, and bindings

**Files:**
- Modify: `internal/bait/vars.go` (new `resolveParamTarget`), `internal/bait/params.go` (`operatorExpansion`, `paramReplacements`), `internal/bait/arith.go` (`operandValue`, `arithmText`), `internal/bait/assign.go` (binding side)
- Test: `internal/bait/params_test.go`, `internal/bait/equivalence_test.go`

**Interfaces:**
- Produces: `type paramTarget struct { name string; subst []syntax.WordPart }` and `func (e *emitter) resolveParamTarget(name string) (paramTarget, bool)` — one mapping consulted everywhere; substitution-backed targets used under operators/arithmetic produce a warning instead of an unbound fish name; binding sites for mapped names (`HOSTNAME=…`, `RANDOM=…`) warn (no faithful fish write exists) instead of silently ignoring.

- [ ] Step 1: Failing fixtures: `${#FUNCNAME[@]}`, `${0#*/}`, `$((RANDOM % 2))`, `((UID == 0))`, `HOSTNAME=myhost; echo "$HOSTNAME"`, `RANDOM=1`.
- [ ] Step 2: Implement resolver; wire all sites.
- [ ] Step 3: Suite green; commit.

### Task 9: List-variable policy (dataflow, all sites)

**Files:**
- Modify: `internal/bait/vars.go` (policy), `internal/bait/control.go` (`forClause`, `caseClause`, `caseClauseAsIf`), `internal/bait/assign.go` (`assignValue`, `declClause`), `internal/bait/redir.go` (redirect targets), `internal/bait/params.go` (argument injection priority)
- Test: `internal/bait/assign_test.go`, `internal/bait/equivalence_test.go`

**Interfaces:**
- Produces: `func (e *emitter) listPolicy(name string) listKind` with kinds: `scalar`, `fishListEnv` (colon-join on quote), `bashArray` (element 0 for bare expansion, list for `[@]`); applied by: for-items (scalar → `(__bait_words $name)` when multiWordScalars, else quoted scalar), case/switch subjects, assignment RHS (`set x (string join : $PATH)` for fishListEnv; `$name[1]` for bare arrays), redirect targets (split via `__bait_words`), argument injection (multiWordScalars outranks the PATH-name exemption).
- Consumes: Task 8 resolver.

- [ ] Step 1: Failing fixtures: `for i in $MY_PATH` (env a:b:c) → 1 iteration; `x=$PATH; echo "$x"` → colon-joined; `arr=(a b); case $arr in *) …`; `f="a b"; echo hi > $f` (bash fails with ambiguous redirect; warn + emit).
- [ ] Step 2: Implement.
- [ ] Step 3: Suite green; commit.

### Task 10: AST-based pattern conversion

**Files:**
- Modify: `internal/bait/control.go` (`tokenizePattern`/`renderCasePattern` escaping, `caseClauseAsIf` regex source), `internal/bait/test_clause.go` (`==`/`!=` decision), `internal/bait/params.go` (`globToRegex` input)
- Test: `internal/bait/expr_test.go`, `internal/bait/equivalence_test.go`

**Interfaces:**
- Produces: `globToRegex` input built from AST parts (quoted literals escaped, expansions kept dynamic, wildcards meaningful only when unquoted); `caseClauseAsIf` regex built from the same conversion; quoted/escaped wildcards in switch patterns emitted with `\` escapes; `==`/`!=`: pattern semantics whenever the RHS contains any unquoted part (literal or expansion); fully quoted RHS stays `test =`.

- [ ] Step 1: Failing fixtures: `v=abc; case $v in "a*") echo literal;; *) echo other;; esac`; `case "$v" in [0-9]) …;; "[a-z]") echo lit;; esac` with `v=[a-z]`; `x=abc; y='a*'; [[ $x == $y ]] && echo m`.
- [ ] Step 2: Implement.
- [ ] Step 3: Suite green; commit.

### Task 11: Shared definitions — `$-` emulation, helper registry, synthesized tagging

**Files:**
- Modify: `internal/bait/params.go` (`statusDashPart` single source), `internal/bait/control.go` (use it), `internal/bait/helpers.go` (delete `hasHelper`), `internal/bait/params.go`+`internal/bait/emit.go` (single helper-dispatch list), `internal/bait/assign.go`+`internal/bait/emit.go` (explicit synthesized-set map on the emitter)
- Test: `internal/bait/helpers_test.go`, `internal/bait/translate_test.go`

**Interfaces:**
- Produces: `e.synthSets map[*syntax.CallExpr]bool` replacing `Args[0].Pos().Col()==0` / 3-arg shape sniffing; `helperDispatch(c *syntax.CallExpr)` consulted once during normalize.

- [ ] Step 1: Refactor under green suite (behavior-neutral); add a guard test that a user-written `set name value` still becomes `set argv name value`.
- [ ] Step 2: Suite green; commit.

### Task 12: Case-subject rendering parity

**Files:**
- Modify: `internal/bait/control.go` (`caseClause` subject), `caseClauseAsIf` comments
- Test: `internal/bait/translate_test.go`

- [ ] Step 1: Failing tests: `case $(if true; then echo a; fi) in a) …`; comments inside bracket-class case items are preserved.
- [ ] Step 2: Implement via `renderWordSmart` + Task 9 policy + comment emission mirroring the switch path.
- [ ] Step 3: Suite green; commit.

### Task 13: Heredoc byte fidelity

**Files:**
- Modify: `internal/bait/redir.go` (`emitHdoc`)
- Test: `internal/bait/helpers_test.go`, `internal/bait/equivalence_test.go`

- [ ] Step 1: Failing fixtures: quoted heredoc with backslashes and a trailing `\`; unquoted heredoc with literal `$`; body with trailing blank lines; two heredocs on one command (verbatim path).
- [ ] Step 2: Implement escaping (`\` then `'`), literal `$` escaping, newline preservation, multi-heredoc verbatim.
- [ ] Step 3: Suite green; commit.

### Task 14: Arrays — declarations, sparse literals, assignment RHS

**Files:**
- Modify: `internal/bait/assign.go` (`declClause`, `arrayElemArgs`, `assignValue`), `internal/bait/params.go` (bare `$name` element-0 policy ties to Task 9)
- Test: `internal/bait/assign_test.go`, `internal/bait/equivalence_test.go`

- [ ] Step 1: Failing fixtures: `local arr=(a b)`; `declare -a arr=(a b)`; `arr=([0]=a [2]=c); echo "${arr[1]}|${arr[2]}"`; `m=([k]=v)` (warn).
- [ ] Step 2: Implement mirroring `assignStmt`'s cases; warn where fish cannot express (associative/sparse).
- [ ] Step 3: Suite green; commit.

### Task 15: shift semantics

**Files:**
- Modify: `internal/bait/assign.go` (`shiftCmd`)
- Test: `internal/bait/assign_test.go`, `internal/bait/equivalence_test.go`

- [ ] Step 1: Failing fixtures: `f(){ shift; echo "rc=$?"; }; f` (rc=1); `f a; shift 2; echo "rc=$?"` (bash: rc=1, positionals unchanged); `shift -1` (rc=1).
- [ ] Step 2: Implement count check before erasing.
- [ ] Step 3: Suite green; commit.

### Task 16: `read` — options, fd, scope

**Files:**
- Modify: `internal/bait/redir.go` (`normalizeReadCmd`)
- Test: `internal/bait/helpers_test.go`, `internal/bait/equivalence_test.go`

- [ ] Step 1: Failing tests/fixtures: `read -pr var` keeps prompt/var; `read -u 3 x` warns (fish `-u` = `--unexport`); `read -t 1 x` warns; `f(){ read x; }; f < f.txt; echo "$x"` global.
- [ ] Step 2: Implement option-cluster parsing (argument-taking options consume the rest), warn for fd/timeout, emit `read --global` in functions for undeclared targets.
- [ ] Step 3: Suite green; commit.

### Task 17: Helper fidelity sweep

**Files:**
- Modify: `internal/bait/helpers/unset.fish`, `unalias.fish`, `__bait_exec.fish`, `__bait_words.fish`, `getopts.fish`, `hash.fish`, `helpers.go` (`Helper(".")` dependency), `__bait_ostype.fish`
- Test: `internal/bait/helpers_test.go`, `internal/bait/equivalence_test.go`

- [ ] Step 1: Failing tests: unset of every `fishReservedVars` key + absent name status 0 + native read-only no-op; `unalias -a`; `IFS=,; cmd="echo hi"; $cmd`; `IFS=", "` empty fields; getopts terminating call sets name to `?` and clears OPTARG; `hash -t` warns; `bait helper .` output includes `source`.
- [ ] Step 2: Implement per helper; keep one table by generating the fish mapping from the Go table (or assert equality in a test that compares both).
- [ ] Step 3: Suite green; commit.

### Task 18: Subshell fd pairing

**Files:**
- Modify: `internal/bait/redir.go` (`redirect`, `isPairedHighFDRedir`)
- Test: `internal/bait/translate_test.go`

- [ ] Step 1: Failing test: `x=$(cmd 1>&4)` warns/emits verbatim unless paired with `4>&1`.
- [ ] Step 2: Implement pairing gate.
- [ ] Step 3: Suite green; commit.

### Task 19: Equivalence harness — compare stderr

**Files:**
- Modify: `internal/bait/equivalence_test.go`
- Test: self

- [ ] Step 1: Capture stdout and stderr separately for both shells; compare both plus exit status.
- [ ] Step 2: Add fixtures for every construct fixed in Tasks 1–18 (one per divergence).
- [ ] Step 3: Suite green; commit.

### Task 20: Test and documentation contract repair

**Files:**
- Modify: `internal/bait/helpers_test.go`, `params_test.go`, `translate_test.go` (remove pins of broken behavior; assert semantics)
- Modify: `COMPATIBILITY.md`, `AGENTS.md`, `README.md`, `.superpowers/plans/2026-09-05-native-brace-command-blocks.md`
- Test: docs diff review

- [ ] Step 1: Replace `read -u` pin with warning assertion; replace `$-` marker assertions with semantic ones; make `BASH_VERSION` test assert the documented emulation constant; deduplicate repeated pins; drop helper-order assertions for set equality.
- [ ] Step 2: Docs: Fish 4.1.0 minimum, README Linux/arm64 asset mapping, AGENTS differential wording (stdout+stderr+exit, sequential), NO_COLOR, stale spec link, nameref/`read --global` claims made true by Tasks 14/16, BASH_VERSION documented as emulation constant.
- [ ] Step 3: `nix run .#test` and `nix run .#e2e` green; commit.
