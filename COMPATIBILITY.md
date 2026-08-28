# Compatibility Guide

`bait` translates Bash scripts into clean, idiomatic, and directly executable Fish shell scripts.

This guide inventories the compatibility mappings, runtime differences, and diagnostic warnings between Bash and the generated Fish code.

## Translation Policy

- **Native Passthrough**: What modern Fish natively supports (pipes, redirects, combiners, commands, per-command env `VAR=val cmd`, backgrounding, quotes, standard builtins) is passed through byte-for-byte with zero rewrite overhead.
- **Idiomatic Translation**: Constructs incompatible with Fish syntax are rewritten into native Fish keywords, builtins, and long-option flags (`set --function`, `set --append`, `string match --regex`, etc.).
- **On-Demand Helpers**: If a script relies on behaviors Fish lacks natively (POSIX `getopts`, unquoted field splitting, dynamic command strings), `bait` injects minimal, self-contained pure-Fish helpers at the top of the file. Plain scripts have zero runtime footprint.
- **Explicit Warnings**: Constructs with no faithful equivalent are emitted verbatim with a line/column diagnostic warning on stderr (suppressible with `--quiet`).

---

## 1. Control Flow & Structure

| Bash | Fish | Notes |
|---|---|---|
| `if/then/elif/else/fi` | `if` / `else if` / `else` / `end` | Clean Fish block structure |
| `while cond; do … done` | `while cond … end` | |
| `until cond; do … done` | `while not cond … end` | Negated condition loop |
| `for x in …; do … done` | `for x in … … end` | Bare `for x` iterates `$argv` |
| `case x in p) …;; esac` | `switch x` / `case 'p'` / `end` | Wildcards and multiple patterns supported |
| `f() { … }`, `function f { … }` | `function f … end` | Function definition |
| `{ … }` | `begin … end` | Anonymous command block |
| `( … )` | `begin … end` | Anonymous command block (emits warning: subshell isolation is lost) |
| `cmd &`, `end &` | `cmd &`, `end &` | Background execution |
| `<(cmd)` | `(cmd \| psub)` | Process substitution via Fish `psub` |
| `<< EOF`, `<<- EOF` | `printf '%s\n' '...' \| cmd` | Here-document pipeline |
| `<<< WORD` | `printf '%s\n' WORD \| cmd` | Here-string pipeline |
| `#!/bin/bash`, `#!/usr/bin/env bash`, `sh`, `ash`, `dash` | `#!/usr/bin/env fish` | Shebang rewritten; interpreter flags dropped |

---

## 2. Conditionals & Tests (`[ ... ]` and `[[ ... ]]`)

| Bash | Fish | Purpose |
|---|---|---|
| `[ ... ]` | `[ ... ]` | Native passthrough (Fish ships `[` as an alias for `test`) |
| `[[ -n "$x" ]]`, `[[ -f file ]]` | `test -n "$x"`, `test -f file` | Unary string and file tests |
| `[[ "$a" == "$b" ]]`, `[[ $a = b ]]` | `test "$a" = "$b"`, `test $a = b` | String equality |
| `[[ $a == glob* ]]`, `[[ $a != glob* ]]` | `string match -q -- 'glob*' $a`, `! string match -q -- 'glob*' $a` | Wildcard pattern matching |
| `[[ $str =~ regex ]]` | `string match -r -q -- 'regex' $str` | Regular expression matching |
| `[[ -v VAR ]]` | `set -q VAR` | Variable existence check |
| `[[ $- == *i* ]]`, `case $- in *i*)` | `status is-interactive` | Interactive shell check (simple 1- or 2-branch case forms; other patterns warn and fall back) |
| `[[ cond1 && cond2 ]]` | `cond1 && cond2` | Logical AND |
| `[[ cond1 \|\| cond2 ]]` | `cond1 \|\| cond2` | Logical OR |
| `[[ (c1 \|\| c2) && c3 ]]` | `begin c1 \|\| c2; end && c3` | Grouped condition |

---

## 3. Variables, Scoping & State Builtins

Fish uses explicit scoping flags (`--function`, `--global`). `bait` translates assignments predictably without magic heuristics:

| Bash | Fish | Scoping / Notes |
|---|---|---|
| `x=val`, `x=""`, `x=` | `set x val`, `set x ""` | Script top-level assignment |
| `x=val` (inside function) | `set --global x val` | In Bash, assignments in functions are global by default |
| `local x=val`, bare `local x` | `set --function x val`, `set --function x ""` | Scoped to the current function |
| `declare x=val`, `typeset x=val` | `set x val` (top-level) / `set --function x val` (in func) | Scoped declaration |
| Reassignment to local `x=new` | `set x new` | Updates existing function-local without `--global` |
| `export X=val` | `export X=val` | Passthrough (Fish ships a built-in `export` wrapper) |
| `arr=(a b c)` | `set arr a b c` | Native Fish list |
| `arr[2]=val` | `set arr[3] val` | Array index shifted +1 (Fish is 1-based) |
| `arr+=(val)` | `set --append arr val` | Appends element to list |
| `X="$X more words"` | `set X $X more words` | Self-referential string accumulation optimized into Fish list append |
| `set -- a b`, `set - a b`, `set a b` | `set argv a b` | Positional parameter assignment |
| `set --` | `set argv` | Clears positional parameters |
| `shift`, `shift N` | `set --erase argv[1]`, `set --erase argv[1..N]` | Shifts positional arguments |
| `unset x`, `unset -v x y` | `set --erase x`, `set --erase x y` | Erases variable |
| `unset -f func` | `functions --erase func` | Erases function definition |
| `unset 'arr[0]'` | `set --erase arr[1]` | Erases specific array element |
| `read -r line`, `read _` | `read line`, `read _unused` | Drops `-r` (default in Fish); rewrites `_` to `_unused` (Fish `$_` is read-only) |
| `set` (bare), `set -` | *(dropped with warning)* | Prints shell state / trace flags in Bash; dropped in Fish |
| `set -e`, `set -u`, `set +x`, `set -o ...` | *(dropped with warning)* | Fish has no shell option flags |
| `eval "..."` | `eval "..."` | Passthrough (emits warning: Fish `eval` executes Fish syntax; incompatible Bash syntax will fail at runtime) |

---

## 4. Parameter Expansions & Special Variables

### Special Variables

| Bash | Fish | Description |
|---|---|---|
| `$?` | `$status` | Exit status of last command |
| `$$`, `$BASHPID` | `$fish_pid` | Current process ID |
| `$!` | `$last_pid` | Process ID of last background job |
| `$#` | `$(count $argv)` | Number of positional arguments |
| `$0`, `$BASH_SOURCE[0]`, `$BASH_ARGV0` | `$(status filename)` | Current script path |
| `$1`…`$N` | `$argv[1]`…`$argv[N]` | Positional arguments |
| `"$@"`, `${arr[@]}` | `$argv`, `$arr` | Unquoted list (see [Documented Differences](#documented-differences)) |
| `$*`, `${arr[*]}` | `$argv`, `$arr` | Unquoted list |
| `$UID`, `$EUID` | `$(id -u)` | User ID |
| `$GROUPS` | `$(id -g)` | Primary group ID |
| `$HOSTNAME` | `$hostname` | Hostname |
| `$HOSTTYPE`, `$MACHTYPE` | `$(uname -m)` | Machine architecture |
| `$OSTYPE` | `$(uname -s \| string lower)` | Operating system (lowercase) |
| `$PIPESTATUS` | `$pipestatus` | Array of pipeline exit codes |
| `$DIRSTACK` | `$dirstack` | Directory stack array |
| `$RANDOM`, `$SRANDOM` | `$(random 0 32767)`, `$(random)` | Random numbers |
| `$EPOCHSECONDS` | `$(date +%s)` | Unix epoch timestamp |
| `$BASH`, `$BASH_COMMAND`, `$FUNCNAME` | `$(status fish-path)`, `$(status current-command)`, `$(status current-function)` | Execution introspection |
| `$IFS` | `$BAIT_IFS` | Internal IFS state for `__bait_words` field splitting |

*Note: Bash-internal completion variables (`COMP_*`), debug stack arrays (`BASH_ARGC`, `BASH_LINENO`), and prompt variables (`PS0`…`PS4`) are not mapped to Fish.*

### Parameter Operators

| Bash | Fish |
|---|---|
| `${var}` | `$var` (braces stripped) |
| `${#var}` / `${#arr[@]}` | `$(string length -- $var)` / `$(count $arr)` |
| `${v:-default}` | `$(test -n "$v" && printf '%s\n' "$v" \|\| printf '%s\n' default)` |
| `${v-default}` | `$(set --query v && printf '%s\n' "$v" \|\| printf '%s\n' default)` |
| `${v:+alternate}` | `$(test -n "$v" && printf '%s\n' alternate \|\| true)` |
| `${v+alternate}` | `$(set --query v && printf '%s\n' alternate \|\| true)` |
| `${f%.txt}`, `${f%%.*}` | `$(string replace --regex -- '\.txt$' '' $f)`, `$(string replace --regex -- '\..*$' '' $f)` |
| `${p#prefix}`, `${p##*/}` | `$(string replace --regex -- '^prefix' '' $p)`, `$(string replace --regex -- '^.*/' '' $p)` |
| `${s/pat/repl}`, `${s//pat/repl}` | `$(string replace --regex -- 'pat' 'repl' $s)`, `$(string replace --regex --all -- 'pat' 'repl' $s)` |
| `${s:offset:length}`, `${s:offset}` | `$(string sub --start=(offset+1) --length=length -- $s)`, `$(string sub --start=(offset+1) -- $s)` |
| `${arr[@]:1:2}`, `${arr[@]:1}` | `$arr[2..3]`, `$arr[2..-1]` (1-based slices) |

### Literal `$` Escaping

Literal unquoted trailing dollars (e.g. `echo 404$`) are escaped as `404\$` during AST normalization to prevent Fish variable syntax errors on bare `$` signs.

---

## 5. Integer Arithmetic

Bash arithmetic `$(( ... ))` and statements `(( ... ))` are integer-only. `bait` translates them to Fish `math --scale=0` (truncation toward zero, matching Bash negatives):

| Bash | Fish |
|---|---|
| `$((a + b * 2))` | `$(math --scale=0 "$a + $b * 2")` |
| `((i++))`, `((--i))` | `set i $(math --scale=0 "$i + 1")`, `set i $(math --scale=0 "$i - 1")` |
| `((n += 5))` | `set n $(math --scale=0 "$n + 5")` |
| `((x > 0))` | `test "$x" -gt 0` (comparisons map to `test` flags `-gt`, `-lt`, `-eq`, etc.) |
| `((count))` | `test "$count" -ne 0` (truthiness test) |

---

## 6. On-Demand Runtime Helpers

When scripts use POSIX constructs that Fish does not provide natively, `bait` injects lightweight, self-contained pure-Fish helper functions at the top of the file:

1. **`getopts` option parsing**:
   - Injected when scripts call `getopts :optstring var [args...]`.
   - Pure Fish function managing `$OPTIND` and `$OPTARG`, supporting short flags, argument binding, and quiet (`:`) mode.
2. **`__bait_words` field splitting**:
   - Injected when unquoted variables or command substitutions appear in word-splitting contexts (e.g. `for x in $var` $\to$ `for x in (__bait_words $var)`).
   - Splits on whitespace or `$BAIT_IFS` (set from `IFS`).
3. **`__bait_exec` dynamic commands**:
   - Injected for dynamic command string execution (`$cmd arg` $\to$ `__bait_exec $cmd arg`).
   - Evaporates empty prefixes (e.g. `sudo=""`) while strictly preserving positional boundaries.

---

## 7. Documented Differences

While `bait` strives for 100% behavioral equivalence, Fish semantics differ from Bash in several intentional ways:

1. **Subshell Isolation Loss**:
   - Subshells `( … )` become `begin … end` (both at statement level and nested inside command substitutions or pipelines). Fish has no subshell; variable and `cd` state persists after the block. Every occurrence is reported as a warning.
2. **List Expansions & Quoting**:
   - In Bash, `"$@"` expands to separate quoted words, while `"$*"` expands to a single joined string.
   - In Fish, quoting a list `"$argv"` joins all elements into a single space-separated string. Therefore, `"$@"` and `"${arr[@]}"` translate to unquoted `$argv` and `$arr` to preserve argument splitting.
3. **Command Substitution Splitting**:
   - Fish splits command substitutions `$(cmd)` on newlines only, not on spaces.
4. **Globbing Behavior**:
   - In Fish, unmatched globs abort the command (equivalent to Bash's `failglob`).
   - The `?` character is treated as a literal by default in modern Fish (`qmark-noglob`).

---

## 8. Unsupported Constructs (Warnings Emitted)

The following constructs have no faithful Fish equivalent and are emitted verbatim with a diagnostic warning:

- C-style `for ((i=0; i<n; i++))` loops and `select` loops
- Output process substitution `>(cmd)`
- Ternary `?:`, bitwise (`& | ^ ~ << >>`), and logical operators inside `$(( ... ))`
- Case fallthrough (`;&` and `;;&`)
- Variable attributes (`readonly`, namerefs `declare -n`)
- Unsupported parameter assignment/assertions (`${v:=def}`, `${v?=error}`)
- Case modification operators (`${v^^}`, `${v,,}`)
- Bash-only builtins without Fish equivalents (`shopt`, `let`, `hash`, `unalias`, `caller`, `compgen`, `compopt`, `enable`, `fc`)
- `set` shell option flags (`set -e`, `set -u`, `set -o ...`) and bare `set` / `set -` (dropped with warning)
- Dynamic array indexing (`arr[$i]`) and non-integer substring/slice offsets
- Export command flags (`export -f`, `export -n`, etc.)
- Embedded `$@` or `$*` inside words (e.g. `prefix$@suffix`)
- Standalone `$-` parameter expansion (emits diagnostic warning; recommend status subcommands)
- Subshell isolation loss (`(...)` translated to a `begin` block)
- `eval` statement passthrough (emits diagnostic warning; Fish `eval` executes Fish syntax, so dynamic execution of incompatible Bash syntax will fail at runtime)
---

## 9. Real-World Tested Installers

The translator is continuously validated by running original Bash installers alongside translated Fish outputs in isolated sandbox environments:

- **Pixi installer** (`https://pixi.sh/install.sh`, ~400 lines) — `read -r`, nested `case`, `for`, string manipulation.
- **Starship installer** (`https://starship.rs/install.sh`, ~400 lines) — nested `for`, field splitting, `IFS` scoping, binary accumulation.
- **uv installer** (`https://astral.sh/uv/install.sh`, ~2200 lines) — complex here-docs, archive pattern matching, subshells in conditionals, function call graphs.
- **Rustup installer** (`https://sh.rustup.rs`, ~1000 lines) — `getopts` argument loop, literal dollar escaping, TLS/ciphersuite detection, CLI flag parsing.
