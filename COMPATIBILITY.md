# Compatibility Guide

This document inventories how `bait` handles each bash construct: what is
translated to fish, what is passed through with a warning, and where the
translated script intentionally behaves differently at runtime.

Legend:

- ✅ Translated to fish.
- 🟡 Translated, with a documented runtime difference (see notes).
- ⚠️ Emitted verbatim; a warning with source line/column is printed to
  stderr (suppress with `--quiet`).

Everything not listed below follows the base policy: valid fish stays
byte-for-byte identical (modulo printer whitespace normalization), and
anything bait cannot translate is emitted unchanged plus a warning.

## Fully supported

### Lexical layer (tier 0 passthrough)

| Construct | Notes |
|---|---|
| Simple commands, quoting, escapes | Verbatim |
| Pipes `\|`, `\|&`, `2>\|` | |
| Redirections `< > >> 2> 2>> &> &>> N< N> 2>&1 >&2` | Normalized to spaced form (`> out.txt`) |
| Combiners `&&` `\|\|` `!` | Native fish syntax |
| Command substitution `$(...)` | Also `(...)` output from bait itself uses `$()` |
| Per-command environment `VAR=val cmd` | Supported natively since fish 3.1 |
| Backgrounding `cmd &` | Including on structural blocks (`end &`) |
| Brace expansion `{a,b,c}`, tilde `~`, globs `*` `**` | |
| `[ ... ]` tests | fish ships `[` as a builtin alias of `test` |
| Comments | Preserved, including inside blocks |

### Structure (keyword rewrites)

| bash | fish |
|---|---|
| `if/then/elif/else/fi` | `if` / `else if` / `else` / `end` |
| `while cond; do … done` | `while cond … end` |
| `until cond; do … done` | `while not cond … end` |
| `for x in …; do … done` | `for x in … … end`; bare `for x` iterates `$argv` |
| `case x in p) …;; esac` | `switch x` / `case p` / `end` |
| `f() { … }`, `function f { … }` | `function f … end` |
| `{ … }` groups, `( … )` subshells 🟡 | `begin … end` |
| structural command substitutions `$(if …)`, `$( (…); )` | emitted through the translator; nested structure becomes valid fish |
| combiners over compounds `cmd \| (subshell)` | structural sides become translated blocks (`cmd \| begin … end`) |

### Shebang

`#!/bin/bash`, `#!/usr/bin/env bash`, `sh`, `ash`, `dash` are rewritten to
`#!/usr/bin/env fish`. Interpreter flags are dropped. Non-shell
interpreters and existing fish shebangs are left untouched; scripts whose
content is not shell fail at the parse stage with an error.

### Variables and assignments

| bash | fish |
|---|---|
| `x=1`, `msg="a b"`, `x=` | `set x 1` / `set x ""` (empty string, never an empty list) |
| `a=1 b=2` (one line) | two `set` commands |
| `export X=v` | passed through verbatim (fish ships an `export` wrapper accepting `NAME=VALUE`) |
| `local x=1`, bare `local x` | `set --local x 1` / `set --local x ""` |
| `declare` / `typeset` (no flags) | top level: `set …`; inside a function: `set --local …` (mirrors bash scoping rules) |
| function-body bare assignment `X=val` | `set --global X val` 🟡 (see differences) |
| `colors=(red blue)` | `set colors red blue` |
| `arr[2]=x` | `set arr[3] x` (constant indices shifted +1) |
| `arr+=(x)` | `set --append arr x` |
| assignment in a combiner chain: `cmd \|\| x=fall`, `x=init && cmd` | `set` command emitted as the chain leaf: `cmd \|\| set x fall` |
| self-referential accumulation `X="$X more words"` | list append `set X $X more words` — bash word-splits the accumulated string at unquoted use sites; fish reaches the same observable behavior by accumulating a list (initial values containing spaces are not split) |

All generated builtin options use long form (`set --local`, `set --append`,
`string --regex`, …).

### Parameter expansion

| bash | fish |
|---|---|
| `${var}` | `$var` (braces stripped when no operator is present) |
| `$?` `$$` `$!` | `$status` `$fish_pid` `$last_pid` |
| `$#` | `$(count $argv)` |
| `$0` | `$(status filename)` |
| `$1`…`${N}` | `$argv[1]`…`$argv[N]` (both are 1-based; no off-by-one) |
| `"$@"`, `$*`, `"${arr[@]}"`, `${arr[*]}` 🟡 | `$arr` / `$argv` (quotes dropped, see differences) |
| `${#var}` / `${#arr[@]}` | `$(string length -- $var)` / `$(count $arr)` |
| `${v:-def}` `${v-def}` `${v:+alt}` | `$(test -n "$v" && printf %s\n "$v" \|\| printf %s\n def)` family |
| `${f%.txt}` `${f%%.*}` `${p##*/}` `${p#x}` | `string replace --regex` with anchored glob-to-regex translation (short forms match lazily) |
| `${s/pat/repl}` `${s//pat/repl}` | `string replace --regex [--all]` |
| `${s:o:l}` `${s:o}` | `string sub --start=o+1 --length=l` (bash offsets are 0-based) |
| `${arr[@]:1:2}`, `${arr[@]:2}` | `$arr[2..3]`, `$arr[3..-1]` |
| `${arr[i]%.txt}` (index + strip) | composed `string replace … -- $arr[i+1]` |
| `${arr[-1]}` | `$arr[-1]` (negative indices match; no shift) |

### Arithmetic

bash arithmetic is integer-only; bait always emits `math --scale=0`
(truncation toward zero, verified against bash including negatives).
Comparisons become `test` because fish `math` rejects logical operators.

| bash | fish |
|---|---|
| `$((expr))` | `$(math --scale=0 "expr")` with bare names rewritten to `$refs` |
| `((i++))` `((--i))` | `set i $(math --scale=0 "$i + 1")` |
| `((n += 2))` and other compound assigns | `set n $(math --scale=0 "$n + 2")` |
| `((t = a * b + 1))` | `set t $(math --scale=0 "$a * $b + 1")` |
| `((x > 0))` | `test "$x" -gt 0` (`-eq -ne -lt -le -ge -gt` mapped) |
| `((count))` | `test "$count" -ne 0` |

Supported operators: `+ - * / % ** == != < > <= >=`, unary minus,
parentheses, and the corresponding compound assignments.

### Misc fixes

| bash | fish | Why |
|---|---|---|
| `read -r line` | `read line` | fish's `read` has no `-r`; it would treat `-r` as a variable name and silently corrupt input |

### The `set` builtin

| bash | fish |
|---|---|
| `set -- a b` and the flagless `set a b` form | `set argv a b` — bash positional parameters map onto fish's argv list |
| `set --` | `set argv` (clears positional parameters / argv) |
| `set -e`, `set -u`, `set +x`, `set -o name`, … | dropped; a warning is reported — fish has no shell option flags |
| bare `set` | dropped; a warning is reported — bash dumps shell state, which has no fish meaning |

Values are emitted as written, so quoting and command substitutions are
preserved (`set -- "$(cmd)"` stays `set argv "$(cmd)"`).

## Translated with documented differences

- **Subshells** `( … )` become `begin … end` (both at statement level
  and nested inside command substitutions or pipelines). Fish has no
  subshell; variable and `cd` state persists after the block. Every
  occurrence is reported as a warning.
- **Function-body assignments** become `set --global` so that values
  survive the call exactly as they do in bash. Note that arithmetic
  statements (`((i += 1))`) deliberately emit plain `set` instead, which
  writes the innermost binding — pair counters with `local`.
- **`"$@"` / `"${arr[@]}"` lose their quotes** (`$argv` / `$arr`). This is
  required to preserve argument splitting; fish quoted variables would
  collapse the list into one word.
- **Unquoted `$*` / `${arr[*]}`** expand as separate arguments in fish,
  while bash joins them with spaces into one word.
- **Command substitutions split on newlines only.** Bash scripts relying
  on `$IFS` word splitting of unquoted expansions need explicit
  `string split`.
- **Unmatched globs abort the command** in fish (like `failglob`); bash
  passes the literal pattern through.
- **`?` is not a glob character** in modern fish (`qmark-noglob`).
- **Glob sort order**: fish sorts case-insensitively and naturally.

## Passed through verbatim with a warning

- C-style `for ((i=0; i<n; i++))` loops and `select` loops
- Ternary `?:`, bitwise (`& | ^ ~ << >>`), and logical (`&& \|\| !`)
  operators inside arithmetic
- Dynamic array subscripts (`arr[$i]`) — index shifting cannot be done
  statically
- Non-removal operators on indexed elements (`${arr[0]/a/b}`,
  `${arr[0]:2}`)
- `${v:?err}` and `${v:=def}` (exit side effects / assignments cannot be
  expressed as pure substitutions)
- Case fallthrough `;&` and `;;&` (fish `switch` has none)
- `readonly` and nameref declarations
- `export` flags such as `export -n` (not supported by fish's wrapper)
- Case-modification operators `${v^}` `${v^^}` `${v,}` `${v,,}`
- `$@` / `$*` embedded inside larger words
- bash-only builtins with no fish equivalent: `hash`, `trap`, `unset`,
  `shift`, `let`, `getopts`, `pushd`, `popd`, `dirs`, `shopt`, `ulimit`,
  `unalias` — emitted verbatim with a hint (silent passthrough would
  only fail at runtime)
- `coproc`, mksh/zsh-only constructs

## Not supported

Scripts that are not shell (Python, Perl, …) fail at the parse stage with
the underlying parser error rather than being passed through.

## Testing

- Snapshot suites pin every rule above (`TestPassthrough`, `TestTier1`,
  `TestTier2*`, `TestShebang`, `TestReadFlags`).
- Warning emission and positions are covered per unsupported construct.
- `TestBashFishEquivalence` executes fixtures under real bash and their
  translations under real fish, requiring identical stdout and exit
  status — this is how the `read -r`, quote-dropping, and scoping rules
  were discovered and pinned.
