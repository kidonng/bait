# Coherence & Overfit Audit — 2026-10-09

Status: review complete; remediation pending approval.
Baseline: `nix run .#test` green (internal/bait 2.3s, cmd/bait 0.35s).
Method: 7 parallel slice reviews (params/vars, emit/subst, control/test/arith, assign/redir,
helpers, tests, docs/CLI) + maintainer verification probes with `result/bin/bait` vs bash
(3.2 local / 5.3 nix) and fish 4.9.3. Nearly every finding below was reproduced by execution.
Finding IDs: `PV`=params/vars, `ES`=emit/subst, `CT`=control/test, `AR`=assign/redir,
`H`=helpers, `T`=tests, `D`=docs/CLI, `M`=maintainer probes.

## Root causes

### RC1 — One concept, many render paths (statement translation is not shared)
`stmt()` dispatches to `simple`, `chainLeaf`, `condText`, `inlineStmtText`, `emitSimpleFish`;
each re-implements a subset of statement rewrites (set builtin, shift, assignments, `test -v`,
`$cmd` exec, reserved-name mangling). A statement's translation depends on where it appears.

- ES2/AR10: `set` builtin rewrite wired only into `simple()`; `true && set -- a b` sets a
  variable `a=b` in fish; `true && set -e` / `if set -e` emit verbatim with no warning (rc=2).
- ES3/CT5: multi-statement conditions `"; "`-join raw printer output: `if a=1; b=2; then`
  → `if { a=1; b=2; }` — fish rejects (`Unsupported use of '='`); comments inside the join
  swallow the closing brace.
- CT6: leading comments of condition statements are replayed onto the header line
  (`if # check`) → fish parse error.
- AR4: `cmdSubst` bodies containing assignment-only statements or `shift` bypass the
  translator (`echo $(y=after; echo inner)` → fish rejects `y=after`; `v=$(shift; true)` →
  `Unknown command: shift`), while the sibling chain path translates them.
- ES1: function-definition `||`/`|` operators are parsed into `fd.Body`; `funcDecl`
  special-cases only `&&`. `f() { false; } || echo b` runs `echo b` on every call. The
  `AndStmt` branch in `binary()` is dead code duplicating the same concept.
- ES6: command-prefix assignments skip mangling (`status=1 echo hi` emitted verbatim;
  fish: "Tried to modify the special variable 'status'"), while bare `status=1` → `set _status 1`.

### RC2 — Text/shape heuristics instead of AST + dataflow
- CT1/ES4: `matchFlagRule` strips wildcards, joins literals, `EqualFold`s: `case $- in i*)`,
  `*i)`, `i)`, `*I*`, `[[ $- == i ]]`, `[[ $- =~ i* ]]` all become `status is-interactive`.
  `!ok` fallback drops dynamic parts entirely: `case $- in *$(uname)i*)` → `if status is-interactive`.
- CT2: `evalInteractiveCase` claims a case item when ONE pattern matches, ignoring the rest:
  `case $- in *|*i*)` → `if status is-interactive` (bash: unconditional match).
- CT3: `caseClauseAsIf` regex-converts `e.render(p)` (quoted printed text): quoted bracket
  classes and `*"$suffix"` become wrong regexes; the switch path renders the same pattern correctly.
- CT9: quoted/escaped wildcards in case patterns (`"a*"`, `a\*`) are emitted unescaped inside
  single quotes; fish still globs them → bash `other` vs translation `literal`.
- AR13: `read` option clusters parsed by scanning for 'r' — `read -pr var` loses `var`
  (bash: `-p r` prompt); `-u`/`-t` collide with fish `--unexport`/`--tokenize`.
- M4: `[[ $x == $y ]]` decides literal-vs-pattern by scanning Lit chars for `*?[`; bash uses
  the pattern value of unquoted expansions → bash matches, translation `test x = y` doesn't.
- M3: `{1..3}` passes through unchanged (fish has no brace sequences) with no warning.
- AR12/T5: `fishBuiltins` is a hand-kept allowlist keyed to bash provenance; `dirs` warns while
  the sibling fish function `vared` must not (contradictory test pins); injected helpers
  (`unset`) and fish functions (`realpath`) unwarned; `string` missing.
- ES7: synthesized `set` nodes self-identify via `Args[0].Pos().Col()==0` + exact 3-arg shape
  (4 call sites inline the magic check).
- M2: `**`/`**=` mapped to `"**"`, which fish `math` rejects (only `pow(a,b)` works) →
  silent runtime error. `$((2**3))` emits `math --scale=0 "2 ** 3"`.

### RC3 — Duplicated sources of truth
- `$-` emulation text spelled 3× (control.go:230 literal, params.go statusDashPart, varPlain/varQuoted).
- Helper dispatch lists duplicated (params.go normalize vs emit.go simple); `hasHelper` dead.
- `unset` runtime rename table (helpers/unset.fish) is an incomplete copy of Go `fishReservedVars`:
  `unset pipestatus` errors on read-only native name; `version=5; unset version` doesn't erase
  the mangled `$_version`; fish-side guards key on incidental existence.
- `Helper(".")` returns the dot wrapper without its `source` dependency, while the emitter
  deliberately closes that dependency.
- Case translation has two paths (switch vs if/regex) with different quoting and comment
  behavior for the same patterns.

### RC4 — Explicit warning contract incomplete (silent verbatim fallbacks)
- PV7/M: `${!v}`, `${#arr[0]}`, `${!pre*}` emitted verbatim with NO warning; fish parse errors
  (`${ is not a valid variable in fish`) while bait exits 0.
- ES5/M6: statements whose `Cmd == nil` (redirect-only `> out.txt`, `< in`, `2>err`) are dropped
  silently at top level and in bodies (`if true; then > a.txt; fi` loses the file creation);
  in chains they leave a dangling `true && `; heredoc-only variants emit a dangling pipe.
- M5: `y=$(< f.txt)` (bash read-file substitution) → `set y ""` silently.
- AR4: raw-bash command-substitution bodies (above).
- AR11: `mapfile`/`readarray`/`times` emitted verbatim with no warning.
- CT8: case subject uses the raw printer: `case $(if …) …` → invalid fish; `case $arr in` with
  a dataflow-known list → `switch $arr` → runtime `switch: Expected at most one argument`.
- M3/M2: `{1..3}`, `**` (above).

### RC5 — Bash semantics not modeled (fidelity gaps)
- CT7/M8: `((…))` assignment/inc/dec forms drop exit status (`set` always exits 0):
  `if ((x++))` inverted; `while ((i--))` never terminates (infinite loop, verified).
- PV1: `${!arr[@]}` classified by `soleArrayAll` as plain list expansion (no `Excl` check) →
  `for i in $arr` iterates values instead of indices, silently.
- PV2: `bashContextVars`/special mappings resolved only at plain-word sites: `${#FUNCNAME[@]}`,
  `${0#*/}`, `$((RANDOM % 2))`, `((UID == 0))` emit unbound fish vars / runtime errors, no warnings.
- PV6: binding side missing for mapped names: `HOSTNAME=myhost` invisible to `$hostname` reads;
  `RANDOM=seed` ignored.
- PV3: `${s/a/\\}`/`${s/a/\n}`/`${s/a/&}` mis-encode for fish `string replace` escape rules.
- PV5/CT8/AR2/AR6(M): fish list-valued env vars (`*PATH`, `LANGUAGE`) treated inconsistently:
  case quotes textually (right), for-loop emits unquoted (wrong: 3 iterations vs bash 1),
  assignments copy lists (`x=$PATH` → 18-element list), redirect targets never word-split.
- PV5: `multiWordScalars` evidence suppressed by the `isFishListEnvVar` name exemption.
- D1/M1: inside double quotes, expansions absorb following literals (`"${V}_suffix"` →
  `"$V_suffix"`; `"P$@S"` → `"P$argvsS"`), silently empty. `{$var}` bracing is gated on
  `!inDblQuotes`; and `{$var}` does NOT delimit inside fish double quotes (verified) — quoted
  segments must be split instead.
- D2/M: literal `[` after an expansion is parsed by fish as indexing (`$BASH_SOURCE[0]` →
  `$(status filename)[0]` → fish aborts).
- AR1/AR9: array literals in declare/local collapse to `set x ""`; sparse/associative literal
  indices ignored (`arr=([0]=a [2]=c)` lands values on wrong elements) with no warning.
- AR3/AR5/AR6: heredoc bodies lose backslash escapes, literal `$`, and trailing newlines;
  multiple heredocs produce hybrid output contradicting the warning text.
- AR7: subshell high-FD rewrite ignores pairing: `x=$(cmd 1>&4)` → `1>& 2` silently.
- AR8: `shift` ignores failure status/out-of-range (`shift` on empty argv: fish 0 vs bash 1;
  `shift 2` with 1 positional erases instead of failing).
- AR14 T1: `read -u`/`-t` pinned as pass-through although fish's `-u` is `--unexport`.
- H1–H9: helper fidelity — unset status/table, `unalias -a` no-op despite docs, `__bait_exec`
  whitespace-only splitting vs IFS-aware `__bait_words`, empty-field IFS rules, `__bait_ostype`
  darwin value differs from pinned bash, `getopts` stale `opt`/`OPTARG`, `hash -t`/bare hash, etc.
- D3: `declare -n` nameref silently stripped (`set ref x`) with no warning despite docs.
- D4: `read x` in a function is fish-local while bash is global; COMPATIBILITY promises
  `read --global`.
- PV8/T2: `BASH_VERSION` fabricated as `$(echo 5.2.0)` (hardcoded literal pinned by a unit test).

### RC6 — Tests & docs encode the overfit
- T6: equivalence harness runs bash then fish sequentially and compares stdout+exit only —
  stderr is discarded; AGENTS.md claims stderr parity and concurrency. No fixtures for
  `read -u`, `$-`, arrays in case, mangled reserved names, `BASH_VERSION`, heredoc bytes.
- T1: `read -u` mismatch pinned as expected output (test freezes the bug).
- T4: `$-` tests assert marker strings (`status is-interactive` appears) instead of flag
  semantics; warnings returned by Translate discarded.
- T5: `dirs` warns / `vared` must not — contradictory classifications pinned from both sides.
- T7: repeated pins for one behavior; helper order assertions duplicate a code list.
- D6: Fish minimum should be 4.1.0 (`{ … }` compound commands), not 4.0.0.
- D5: README install URL maps `uname -m` directly; Linux/arm64 assets are `arm64`, not `aarch64`.
- D7: AGENTS.md differential-testing wording; D8: dead spec link after `.superpowers` move;
  D9: NO_COLOR no longer documented after cobra rewrite.

## Remediation program (proposed)

- **P0 — Silent-breakage blockers** (each gets a differential fixture before/after):
  redirect-only statements & `$(<f)`; quoted name absorption & literal brackets; multi-statement
  conditions + condition comments; function-definition operators; `$-` overreach incl. item-pattern
  logic; `((…))` status; `**`; case-subject rendering/arity; warning coverage for silent
  verbatim paths (`${!…}`, `${#arr[0]}`, `{1..3}`, mapfile, `$(( ))` bitwise is already warned).
- **P1 — Systemic coherence** (the overfit-elimination proper):
  one statement-rendering pipeline (single rewrite chain per statement, shared by all contexts);
  one parameter/variable resolver (native var | mangled var | substitution) used by plain words,
  operators, arithmetic, and bindings; one list-valued-variable policy (dataflow: knownLists,
  multiWordScalars, fish list env vars) applied uniformly at every expansion site (for/case/assign/
  argument/redirect); AST-based pattern→match conversion (case/`[[ == ]]`) with quoting-aware
  literal escaping; shared `$-` emulation; shared helper registry (delete dead/duplicated lists);
  explicit synthesized-node tagging replacing `Col()==0`.
- **P2 — Fidelity & helpers**: arrays (declare/local literals, sparse indices), arithmetic operator
  coverage, shift status, heredoc bytes (backslashes, `$`, newlines, multiple heredocs), fd pairing,
  `read` scope/options, helper semantics sweep + differential fixtures, `HOSTNAME`/`RANDOM` binding.
- **P3 — Test/doc contract repair**: compare stderr in equivalence harness; add fixtures for every
  P0/P1 class; replace pins that freeze broken behavior; docs fixes (4.1.0, README asset,
  AGENTS wording, NO_COLOR, spec link, BASH_VERSION policy, nameref/read claims).

## Decisions needed

1. Scope for the current session (P0 / P0+P1 / full program).
2. `BASH_VERSION` policy: keep as documented emulation constant vs warn+verbatim (strict contract).
3. Render-pipeline unification depth: full unification vs per-path patches.
