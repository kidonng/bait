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
| Here-documents `<< EOF`, `<<- EOF` | Translated to `printf '%s\n' '...' \| cmd` (with variable expansions preserved where active) |
| Here-strings `<<< WORD` | Translated to `printf '%s\n' WORD \| cmd` |
| Combiners `&&` `\|\|` `!` | Native fish syntax |
| Command substitution `$(...)` | Also `(...)` output from bait itself uses `$()` |
| Process substitution `<(cmd)` | Translated to `(cmd \| psub)` or `(begin ...; end \| psub)` |
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
| `for x in …; do … done` | `for x in … … end`; bare `for x` iterates `$argv`; unquoted `for x in $var` uses `(__bait_words $var)` to match POSIX field splitting |
| `case x in p) …;; esac` | `switch x` / `case 'p'` / `end` |
| `f() { … }`, `function f { … }` | `function f … end` |
| `{ … }` groups, `( … )` subshells 🟡 | `begin … end` |
| structural command substitutions `$(if …)`, `$( (…); )` | emitted through the translator; nested structure becomes valid fish |
| combiners over compounds `cmd \| (subshell)` | structural sides become translated blocks (`cmd \| begin … end`) |
| `[[ ... ]]` conditional test expressions | Translated to `test`, `string match`, and `set -q` combinations |

### Conditional tests (`[[ ... ]]`, `[ ... ]`)

| bash | fish | Notes |
|---|---|---|
| `[ ... ]` | `[ ... ]` / `test ...` | fish ships `[` as a builtin alias of `test` (Tier 0 passthrough) |
| `[[ -n "$x" ]]`, `[[ -f file ]]` | `test -n "$x"`, `test -f file` | Unary file and string tests map to fish's `test` builtin |
| `[[ "$a" == "$b" ]]`, `[[ $a = b ]]` | `test "$a" = "$b"`, `test $a = b` | Literal string equality |
| `[[ $a == b* ]]`, `[[ $a != b* ]]` | `string match -q -- 'b*' $a`, `! string match -q -- 'b*' $a` | Wildcard/glob pattern matching translated to fish `string match` |
| `[[ $str =~ regex ]]` | `string match -r -q -- 'regex' $str` | Regular expression matching translated to fish `string match -r` |
| `[[ -v VAR ]]` | `set -q VAR` | Variable set test mapped to fish `set --query` |
| `[[ cond1 && cond2 ]]`, `[[ cond1 \|\| cond2 ]]` | `cond1 && cond2`, `cond1 \|\| cond2` | Logical combiners |
| `[[ ( cond1 \|\| cond2 ) && cond3 ]]` | `begin cond1 \|\| cond2; end && cond3` | Parenthesized condition groups |
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
| `local x=1`, bare `local x` | `set --function x 1` / `set --function x ""` (uses fish's `--function` scope flag so variables survive nested loop/conditional blocks without leaking globally) |
| `declare x=1`, `typeset x=1` | top-level: `set x 1`; inside function: `set --function x 1` |
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
| `$?` `$$` `$!` `$BASHPID` | `$status` `$fish_pid` `$last_pid` `$fish_pid` |
| `$#` | `$(count $argv)` |
| `$0` `${BASH_SOURCE[0]}` `$BASH_SOURCE` `$BASH_ARGV0` | `$(status filename)` |
| `$BASH` | `$(status fish-path)` |
| `$BASH_COMMAND` | `$(status current-command)` |
| `$FUNCNAME` `${FUNCNAME[0]}` | `$(status current-function)` |
| `$UID` `$EUID` | `$(id -u)` |
| `$GROUPS` `${GROUPS[0]}` | `$(id -g)` |
| `$HOSTNAME` | `$hostname` |
| `$HOSTTYPE` `$MACHTYPE` | `$(uname -m)` |
| `$OSTYPE` | `$(uname -s \| string lower)` |
| `$PIPESTATUS` `${PIPESTATUS[@]}` `${PIPESTATUS[N]}` | `$pipestatus` `$pipestatus` `$pipestatus[N+1]` |
| `$RANDOM` | `$(random 0 32767)` |
| `$SRANDOM` | `$(random)` |
| `$EPOCHSECONDS` | `$(date +%s)` |
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

### Unsupported / unmapped shell variables

Bash defines numerous shell-specific variables that do not map directly to Fish due to architectural differences. These are left verbatim or require refactoring:

| Variable | Status / Fish alternative | Reason |
|---|---|---|
| `$SECONDS` | Not mapped | Seconds since shell startup; Fish lacks a built-in background elapsed timer |
| `$PPID` | Not mapped | Parent PID; Fish provides `$fish_pid` for self, but parent PID requires external `ps -o ppid= -p $fish_pid` |
| `$LINENO` | Not mapped | Script line number; Fish's `(status line-number)` inside command substitutions reports the subshell's line (`1`), not caller position |
| `$DIRSTACK` | Not mapped | Directory stack; in Bash `${DIRSTACK[0]}` is `$PWD` (length $N+1$), whereas Fish `$dirstack` only holds pushed entries (length $N$) |
| `$BASH_REMATCH` | Not mapped | Array of regex capture groups from `[[ =~ ]]`; in Fish, regex capture is handled via `string match -r` |
| `$BASH_VERSION`, `$BASH_VERSINFO` | Not mapped | Identifies Bash interpreter version; in Fish, use `$version` or `$FISH_VERSION` |
| `$BASH_LINENO`, `$BASH_ARGC`, `$BASH_ARGV` | Not mapped | Extended execution call stack arrays from `shopt -s extdebug` |
| `$BASHOPTS`, `$SHELLOPTS`, `$BASH_ALIASES`, `$BASH_CMDS` | Not mapped | Bash-internal option lists and hash/alias tables |
| `COMP_*`, `COMPREPLY` | Not mapped | Bash programmable completion variables; Fish uses its own declarative `complete -c` subsystem |
| `HIST*`, `histchars`, `FCEDIT` | Not mapped | Interactive history and readline configuration |
| `PROMPT_*`, `PS0`…`PS4` | Not mapped | Interactive prompt variables; Fish uses `fish_prompt` and `fish_right_prompt` functions |
| `OPTARG`, `OPTIND` | Not mapped | Option parsing via `getopts`; Fish scripts use the `argparse` builtin |
| `COPROC` | Not mapped | Asynchronous coprocess file descriptor array; coprocesses are unsupported |

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

### Word splitting, IFS, and dynamic command dispatch

Fish deliberately does not perform implicit word splitting on unquoted variable expansions (`$var`). When bait encounters constructs that rely on POSIX field splitting or dynamic command strings, it injects self-contained runtime helper functions (`__bait_words`, `__bait_exec`) at the top of the translated file on demand (plain scripts without dynamic constructs have zero runtime footprint):

| bash | fish | Notes |
|---|---|---|
| `IFS=":"`, `IFS=" "` | `set BAIT_IFS ':'`, `set BAIT_IFS ' '` (local inside functions) | Modifying `IFS` maps onto `BAIT_IFS` |
| `for x in $var; do … done` (unquoted) | `for x in (__bait_words $var) … end` | Splits on whitespace or `$BAIT_IFS` matching POSIX field splitting |
| `$cmd` (dynamic command string), `$sudo tar …` | `__bait_exec $cmd`, `__bait_exec $sudo tar …` | Evaporates empty leading prefixes (`sudo=""`) and parses command flags while strictly preserving argument boundaries |

### Builtin rewrites (`set`, `shift`, `unset`)

| bash | fish |
|---|---|
| `set -- a b` and the flagless `set a b` form | `set argv a b` — bash positional parameters map onto fish's argv list |
| `set --` | `set argv` (clears positional parameters / argv) |
| `shift`, `shift 1` | `set --erase argv[1]` |
| `shift N` | `set --erase argv[1..N]` |
| `unset x`, `unset -v x y` | `set --erase x`, `set --erase x y` |
| `unset -f func` | `functions --erase func` |
| `unset 'arr[0]'` | `set --erase arr[1]` (constant indices shifted +1) |
| `set -e`, `set -u`, `set +x`, `set -o name`, … | dropped; a warning is reported — fish has no shell option flags |
| `bare set` | dropped; a warning is reported — bash dumps shell state, which has no fish meaning |
Values are emitted as written, so quoting and command substitutions are
preserved (`set -- "$(cmd)"` stays `set argv "$(cmd)"`).

### POSIX utilities and builtins supported by fish

Fish provides either built-in commands or shipped functions for several
common bash/POSIX utilities, which pass through verbatim and work
natively:

| Command | Mechanism in fish | Notes |
|---|---|---|
| `export VAR=val` | Shipped function (`functions/export.fish`) | Supports `export VAR=val` and bare `export` |
| `alias name='cmd'` | Shipped function (`functions/alias.fish`) | Defines a wrapper function |
| `pushd`, `popd`, `dirs` | Shipped functions (`functions/pushd.fish`, etc.) | Full directory stack support with `+N` / `-N` |
| `trap 'cmd' SIG...` / `EXIT` | Shipped function (`functions/trap.fish`) | Maps signal handlers and exit handlers |
| `umask` | Shipped function (`functions/umask.fish`) | Supports octal and `-S` symbolic modes |
| `ulimit` | Builtin (`builtin --names`) | Full resource limit manipulation |
| `eval`, `exec`, `type`, `time`, `wait`, `disown`, `jobs`, `bg`, `fg` | Builtins | Native fish builtins |

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
- bash-only builtins with no fish equivalent: `hash`,
  `let`, `getopts`, `shopt`, `unalias`, `caller`, `compgen`,
  `compopt`, `enable`, `fc` — emitted verbatim with a hint (fish ships
  `builtin`, `argparse`, `status` instead).

## Real-world scripts under test

- **Pixi installer** (`https://pixi.sh/install.sh`, ~400 lines) — exercising
  `read -r`, nested `case`, `for`, string manipulations, and installer flow.
- **Starship installer** (`https://starship.rs/install.sh`, ~400 lines) — exercising
  nested `for` loops with variable-defined targets, `_bait_words` field splitting,
  `IFS` local scoping, dynamic binary and flag accumulation.
- **uv installer** (`https://astral.sh/uv/install.sh`, ~2200 lines) — exercising
  here-documents (`<< EOF`, `<< EORECEIPT`), complex case patterns (`.tar.*`),
  subshells in conditional tests (`if ! (cmd | grep ...)`), and function-scoped
  local variable tracking across deep call graphs and loop bodies.

Every real-world script passes byte-for-byte or functional integration tests in
an isolated sandbox environment.
