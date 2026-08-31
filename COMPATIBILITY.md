# Compatibility Guide

`bait` translates Bash scripts into clean, idiomatic, and directly executable Fish shell scripts.

## Overview

- **Target Version**: **Fish 3.4.0+**. Translated scripts strictly require Fish 3.4.0 or newer (specifically for POSIX-style `$(cmd)` command substitutions and `set --function` lexical scoping).
- **Passthrough First**: Constructs natively supported by modern Fish (pipes, redirects, combiners, commands, per-command env `VAR=val cmd`, backgrounding, quotes) pass through byte-for-byte.
- **Idiomatic Fish**: Incompatible syntax is translated into native Fish keywords, builtins, and modern `$(cmd)` command substitutions.
- **On-Demand Helpers**: Runtime helper functions are injected strictly on demand when scripts require missing POSIX semantics (suppressible via `--no-helpers`); clean scripts carry zero runtime overhead.
- **Explicit Diagnostics**: Constructs without a faithful Fish equivalent emit diagnostic warnings on stderr (suppressible via `--quiet`).

---

## 1. Runtime Differences

While `bait` strives for behavioral equivalence, Fish semantics differ from Bash in several intentional ways:

1. **Subshell Isolation Loss**:
   - Subshells `( … )` become `begin … end` (both at statement level and nested inside command substitutions or pipelines). Fish has no subshell isolation; variable and directory mutations persist after the block. Every occurrence emits a warning.
2. **List Expansions & Quoting**:
   - In Bash, `"$@"` expands to separate words, while `"$*"` expands to a single space-joined string.
   - In Fish, quoting a list `"$argv"` joins all elements into a single space-separated string. Therefore, `"$@"` and `"${arr[@]}"` translate to unquoted `$argv` and `$arr` to preserve argument splitting, while `"$*"` translates to `"$argv"` to preserve single joined string semantics.
3. **Command Substitution Splitting**:
   - Fish splits command substitutions `$(cmd)` on newlines only, whereas Bash splits on all whitespace characters (POSIX `$IFS`).
4. **Globbing Behavior**:
   - In Fish, unmatched globs abort the command (equivalent to Bash's `failglob`).
   - The `?` character is treated as a literal by default in modern Fish (`qmark-noglob`).
5. **Exit Status Lifetime (`$?` vs `$status`)**:
   - In Fish, `$status` is immediately updated by virtually every statement, built-in test (`test`, `string`), block boundary (`begin ... end`), and command substitution.
   - Unlike Bash where `$?` can survive certain non-mutating shell constructs, in Fish `$status` must be captured immediately into a variable (`set --local exit_code $status`) if it needs to be checked after intermediate statements.

---

## 2. Unsupported & Untranslated Constructs (Warnings Emitted)

The following constructs are currently untranslated by `bait` or lack direct Fish equivalents. They are emitted verbatim (or rewritten with best-effort fallbacks) alongside a line/column diagnostic warning:

### Currently Untranslated Syntax

- **Loops**: C-style `for ((i=0; i<n; i++))` loops and `select` loops (not yet translated to equivalent `while` loops)
- **Parameter Assertions**: `${v:=def}`, `${v=def}`, `${v?=error}`, `${v?error}` (not yet expanded to conditional checks; emitted verbatim with warning)
- **Extended Arithmetic Operators**: Ternary `?:`, bitwise (`& | ^ ~ << >>`), and logical operators inside `$(( ... ))`
- **Dynamic Array Indexing**: Variable array indexing (`arr[$i]`) and non-integer substring/slice offsets (require dynamic 1-based index shifting)
- **Case Modification**: Pattern-based (`${v^^pattern}`, `${v,,pattern}`) and single-character (`${v^}`, `${v,}`) transformations (full conversions `${v^^}` and `${v,,}` are supported)
- **Case Fallthrough**: `;&` and `;;&`
- **Process Substitution**: Output process substitution `>(cmd)`
- **Variable Attributes**: `readonly`, namerefs (`declare -n`), and attribute declaration flags (`declare -i`, `declare -a`, `declare -A`, `declare -r`, `typeset -i`, etc.; unsupported attributes are stripped or warn, and associative arrays `-A` have no Fish equivalent)
- **Export Flags**: `export -f`, `export -n`, etc.
- **Word Boundaries**: Embedded `$@` or `$*` inside larger words (e.g. `prefix$@suffix`)

### Fish Inherent Limitations

- **Shell Options**: `set` option flags (`set -e`, `set -u`, `set -o ...`) and bare `set` / `set -` (Fish has no shell option flags; dropped with warning)
- **Bash-only Builtins**: Builtins without Fish equivalents (`shopt`, `let`, `caller`, `compgen`, `compopt`, `enable`, `fc`)
- **High File Descriptor Redirections**: Redirections to file descriptors above 2 (`3>`, `4>&1`, etc.) on blocks, functions, or builtins (supported in Fish only for external commands)
- **Background Function Execution**: Functions cannot be started in the background in Fish (`func &`)
- **Interactive Shell Detection**: Standalone `$-` parameter expansion (warns and rewrites to `status is-interactive` fallback)

---

## 3. Translation Reference

### Control Flow & Structure

| Bash | Fish | Notes |
|---|---|---|
| `if/then/elif/else/fi` | `if` / `else if` / `else` / `end` | Clean Fish block structure |
| `while cond; do … done` | `while cond … end` | |
| `until cond; do … done` | `while not cond … end` | Negated condition loop |
| `for x in …; do … done` | `for x in … … end` | Bare `for x` iterates `$argv` |
| `case x in p) …;; esac` | `switch x` / `case 'p'` / `end` | Wildcards and multiple patterns supported; patterns containing bracket classes (`[...]`) translate to `if`/`else if` regex blocks |
| `f() { … }`, `function f { … }` | `function f … end` | Function definition |
| `{ … }` | `begin … end` | Anonymous command block |
| `( … )` | `begin … end` | Anonymous command block (emits warning: subshell isolation is lost) |
| `cmd &`, `end &` | `cmd &`, `end &` | Background execution (background function calls emit a warning) |
| `<(cmd)` | `(cmd \| psub)` | Process substitution via Fish `psub` |
| `<< EOF`, `<<- EOF` | `printf '%s\n' '...' \| cmd` | Here-document pipeline |
| `<<< WORD` | `printf '%s\n' WORD \| cmd` | Here-string pipeline |
| `>&2 cmd`, `>out cmd` | `cmd >& 2`, `cmd >out` | Leading redirections are normalized to statement tails to match Fish syntax |
| `#!/bin/bash`, `#!/usr/bin/env bash`, `sh`, `ash`, `dash` | `#!/usr/bin/env fish` | Shebang rewritten; interpreter flags dropped |
| `\cmd` | `cmd` | Bash alias-bypass syntax normalized to plain command name |

### Conditionals & Tests (`[ ... ]` and `[[ ... ]]`)

| Bash | Fish | Purpose |
|---|---|---|
| `[ ... ]` | `[ ... ]` / `set --query VAR` | Native passthrough; `[ -v VAR ]` and `test -v VAR` rewrite to `set --query VAR` |
| `[[ -n "$x" ]]`, `[[ -f file ]]` | `test -n "$x"`, `test -f file` | Unary string and file tests |
| `[[ "$a" == "$b" ]]`, `[[ $a = b ]]` | `test "$a" = "$b"`, `test $a = b` | String equality |
| `[[ $a == glob* ]]`, `[[ $a != glob* ]]` | `string match --quiet -- 'glob*' $a`, `! string match --quiet -- 'glob*' $a` | Wildcard pattern matching |
| `[[ $str =~ regex ]]` | `string match --regex --quiet -- 'regex' $str` | Regular expression matching |
| `[[ -v VAR ]]` | `set --query VAR` | Variable existence check |
| `[[ $- == *i* ]]`, `case $- in *i*)` | `status is-interactive` | Interactive shell check |
| `[[ cond1 && cond2 ]]` | `cond1 && cond2` | Logical AND |
| `[[ cond1 \|\| cond2 ]]` | `cond1 \|\| cond2` | Logical OR |
| `[[ (c1 \|\| c2) && c3 ]]` | `begin c1 \|\| c2; end && c3` | Grouped condition |

### Variables, Scoping & State Builtins

Fish uses explicit scoping flags (`--function`, `--global`). `bait` translates assignments predictably without magic heuristics:

| Bash | Fish | Scoping / Notes |
|---|---|---|
| `x=val`, `x=""`, `x=` | `set x val`, `set x ""` | Script top-level assignment |
| `x=val` (inside function) | `set --global x val` | In Bash, assignments in functions are global by default |
| `local x=val`, bare `local x` | `set --function x val`, `set --function x ""` | Scoped to the current function |
| `declare x=val`, `typeset x=val` | `set x val` (top-level) / `set --function x val` (in func) | Scoped declaration |
| `declare -g x=val`, `typeset -g x=val` | `set x val` (top-level) / `set --global x val` (in func) | Explicit global declaration |
| Reassignment to local `x=new` | `set x new` | Updates existing function-local without `--global` |
| `export X=val`, `export PATH="${NEWPATH}"` | `export X=val`, `export PATH="$NEWPATH"` | Preserves `export` syntax while normalizing assignments and expansions; unsupported flags (e.g. `export -f`) warn |
| `arr=(a b c)` | `set arr a b c` | Native Fish list |
| `arr[2]=val` | `set arr[3] val` | Array index shifted +1 (Fish is 1-based) |
| `arr+=(val)` | `set --append arr val` | Appends element to list |
| `X="$X more words"`, `X="$X $(cmd)"` | `set X "$X more words"`, `set X "$X $(cmd)"` | Scalar string concatenation |
| `FLAGS="--retry 3 -C -"` | `set FLAGS "--retry 3 -C -"` | Scalar string assignment (word splitting handled at unquoted call sites) |
| `set -- a b`, `set - a b`, `set a b` | `set argv a b` | Positional parameter assignment |
| `set --` | `set argv` | Clears positional parameters |
| `shift`, `shift N` | `set --erase argv[1]`, `set --erase argv[1..N]` | Shifts positional arguments |
| `unset x`, `unset -v x y`, `unset -f func`, `unset 'arr[0]'` | `unset x`, `unset -v x y`, `unset -f func`, `unset 'arr[0]'` | Supported via an on-demand runtime helper |
| `read -r line`, `read _`, `read status`, `read pipestatus` | `read line`, `read _unused`, `read _status`, `read _pipestatus` | Drops `-r` (default in Fish); automatically mangles variable names conflicting with Fish read-only variables (e.g. `_` $\to$ `_unused`, `status` $\to$ `_status`) |
| `set` (bare), `set -` | *(dropped with warning)* | Prints shell state / trace flags in Bash; dropped in Fish |
| `set -e`, `set -u`, `set +x`, `set -o ...` | *(dropped with warning)* | Fish has no shell option flags |
| `eval "..."` | `__bait_eval "..."` | Supported via an on-demand runtime helper translating Bash code on-the-fly (requires `bait` in PATH; falls back to passthrough with warning when `--no-helpers` is specified) |
| `hash cmd`, `hash cmd1 cmd2`, `hash -r` | `hash cmd`, `hash cmd1 cmd2`, `hash -r` | Supported via an on-demand runtime helper |
| `unalias foo`, `unalias "$1"`, `unalias -a` | `unalias foo`, `unalias "$argv[1]"`, `unalias -a` | Supported via an on-demand runtime helper |
| `source file.sh`, `. file.sh` | `source file.sh`, `. file.sh` | Supported via an on-demand runtime helper translating Bash scripts on-the-fly |

#### Lexical Scoping Decision Matrix

`bait` enforces uniform lexical scoping across all constructs without dynamic caller-callee heuristics:

| Bash Construct | Context | Fish Translation | Target Scope | Semantic Contract |
|---|---|---|---|---|
| `x=val` | Top-level | `set x val` | Universal / local to script | Standard script-level binding |
| `x=val` (undeclared) | Inside function | `set --global x val` | Global | Emulates Bash's dynamic global assignment |
| `local x=val` / `local x` | Inside function | `set --function x val` | Function | Strictly scoped to current function frame |
| `declare x=val` / `typeset x=val` | Inside function | `set --function x val` | Function | Function-local unless `-g` is supplied |
| `declare -g x=val` | Inside function | `set --global x val` | Global | Explicitly targets global scope |
| `x=val` (after `local x`) | Inside function | `set x val` | Function | Mutates existing function-local variable |
| `((x++))` / `((x = val))` | Inside function (undeclared) | `set --global x (math ...)` | Global | Arithmetic commands match unadorned assignment scoping |
| `((x++))` (after `local x`) | Inside function | `set x (math ...)` | Function | Reassigns existing function-local variable |
| `read x` | Inside function (undeclared) | `read --global x` | Global | Preserves global side-effect of `read` |
| `read x` (after `local x`) | Inside function | `read x` | Function | Modifies declared local variable |

#### Array Indexing & Slicing Transformation Guide

Bash uses 0-based arrays, while Fish lists are 1-based. `bait` systematically rewrites static indices and slices:

| Operation | Bash (0-based) | Fish (1-based) | Notes |
|---|---|---|---|
| First element | `${arr[0]}` | `$arr[1]` | Index shifted by +1 |
| Literal N-th element | `${arr[N]}` | `$arr[N+1]` | Literal offset shifted by +1 |
| Last element | `${arr[-1]}` | `$arr[-1]` | Negative index from end supported natively by Fish |
| Length-based slice | `${arr[@]:start:len}` | `$arr[(start+1)..(start+len)]` | Shifted start index + computed end index |
| Open slice to end | `${arr[@]:start}` | `$arr[(start+1)..-1]` | Slice starting from 1-based offset to end (`-1`) |
| Negative slice offset | `${arr[@]: -N}` | `$arr[-N..-1]` | Slice last `N` elements to end |
| Array element erasure | `unset "arr[N]"` | `set --erase arr[N+1]` | Done via `unset` helper with 0-to-1 conversion |


### Parameter Expansions & Special Variables

#### Special Variables

| Bash | Fish | Description |
|---|---|---|
| `$?` | `$status` | Exit status of last command |
| `$$`, `$BASHPID` | `$fish_pid` | Current process ID |
| `$!` | `$last_pid` | Process ID of last background job |
| `$#` | `$(count $argv)` | Number of positional arguments |
| `$0`, `$BASH_SOURCE[0]`, `$BASH_ARGV0` | `$(status filename)` | Current script path |
| `$1`…`$N` | `$argv[1]`…`$argv[N]` | Positional arguments |
| `"$@"`, `${arr[@]}` | `$argv`, `$arr` | Unquoted list (see [Runtime Differences](#1-runtime-differences)) |
| `"$*"` | `"$argv"` | Space-joined single string |
| `$*`, `${arr[*]}` | `$argv`, `$arr` | Unquoted list |
| `$UID`, `$EUID` | `$(id -u)`, `$EUID` | User ID (`$EUID` is native in modern Fish) |
| `$GROUPS` | `$(id -g)` | Primary group ID |
| `$HOSTNAME` | `$hostname` | Hostname |
| `$HOSTTYPE`, `$MACHTYPE` | `$(uname -m)` | Machine architecture |
| `$OSTYPE` | `$(__bait_ostype)` | Operating system identifier (emulating Bash `linux-gnu`, `darwin<version>`, etc. via helper) |
| `$PIPESTATUS` | `$pipestatus` | Array of pipeline exit codes |
| `$DIRSTACK` | `$dirstack` | Directory stack array |
| `$RANDOM`, `$SRANDOM` | `$(random)`, `$(random 0 4294967295)` | Random numbers (0–32767 and 32-bit unsigned 0–4294967295) |
| `$EPOCHSECONDS` | `$(date +%s)` | Unix epoch timestamp |
| `$BASH`, `$BASH_COMMAND`, `$FUNCNAME`, `$FUNCNAME[0]` | `$(status fish-path)`, `$(status current-command)`, `$(status current-function)` | Execution context introspection |
| `$BASH_VERSION` | `$(echo 5.2.0)` | Bash compatibility version string |
| `$-` (standalone) | `$(status is-interactive && echo i \|\| echo '')` | Interactive shell check (emits warning; uses `status is-interactive`) |

*Note: Bash-internal completion variables (`COMP_*`), debug stack arrays (`BASH_ARGC`, `BASH_LINENO`), and prompt variables (`PS0`…`PS4`) are not mapped to Fish.*

#### Parameter Operators

| Bash | Fish | Notes |
|---|---|---|
| `${var}` | `$var` | Braces stripped |
| `${#var}` / `${#arr[@]}` / `${#arr[*]}` | `$(string length -- "$var")` / `$(count $arr)` | String length / array count |
| `${v:-default}` | `$(test -n "$v" && printf '%s\n' "$v" \|\| printf '%s\n' default)` | Default value if unset or null |
| `${v-default}` | `$(set --query v && printf '%s\n' "$v" \|\| printf '%s\n' default)` | Default value if unset |
| `${v:+alternate}` | `$(test -n "$v" && printf '%s\n' alternate \|\| true)` | Alternate value if set and not null |
| `${v+alternate}` | `$(set --query v && printf '%s\n' alternate \|\| true)` | Alternate value if set |
| `${f%.txt}`, `${f%%.*}` | `$(string replace --regex -- '\.txt$' '' $f)`, `$(string replace --regex -- '\..*$' '' $f)` | Suffix removal (literal and dynamic patterns) |
| `${p#prefix}`, `${p##*/}` | `$(string replace --regex -- '^prefix' '' $p)`, `$(string replace --regex -- '^.*/' '' $p)` | Prefix removal (literal and dynamic patterns) |
| `${s/pat/repl}`, `${s//pat/repl}` | `$(string replace --regex -- 'pat' 'repl' $s)`, `$(string replace --regex --all -- 'pat' 'repl' $s)` | Pattern replacement (literal and dynamic patterns) |
| `${s:offset:length}`, `${s:offset}` | `$(string sub --start=(offset+1) --length=length -- "$s")`, `$(string sub --start=(offset+1) -- "$s")` | Substring extraction |
| `${arr[@]:1:2}` / `${arr[*]:1:2}`, `${arr[@]:1}` / `${arr[*]:1}` | `$arr[2..3]`, `$arr[2..-1]` | 1-based slice |
| `${v^^}` / `${v,,}` | `$(string upper -- "$v")` / `$(string lower -- "$v")` | Uppercase / lowercase string conversion |

#### Literal `$` Escaping

Unquoted literal trailing dollars (e.g. `echo 404$`) are escaped as `404\$` to prevent Fish syntax errors on bare `$` signs.

#### Escaped Backticks in Double Quotes

Escaped backticks (`\``) inside double quotes and unquoted here-documents are unescaped to `` ` `` to match Bash string output.

#### Fish Variable Bracing (`{$var}`)

When an unquoted variable expansion is immediately followed by an alphanumeric character or underscore (e.g. `-x${tar_compression_flag}f`), `bait` transforms it to `{$var}` (e.g. `-x{$tar_compression_flag}f`) to prevent Fish from treating subsequent characters as part of the variable name.

### Integer Arithmetic

Bash arithmetic `$(( ... ))` and statements `(( ... ))` are integer-only. `bait` translates them to Fish `math --scale=0` (truncation toward zero, matching Bash negatives):

| Bash | Fish |
|---|---|
| `$((a + b * 2))` | `$(math --scale=0 "$a + $b * 2")` |
| `((i++))`, `((--i))` | `set i $(math --scale=0 "$i + 1")`, `set i $(math --scale=0 "$i - 1")` |
| `((n += 5))` | `set n $(math --scale=0 "$n + 5")` |
| `((x > 0))` | `test "$x" -gt 0` (comparisons map to `test` flags `-gt`, `-lt`, `-eq`, etc.) |
| `((count))` | `test "$count" -ne 0` (truthiness test) |

### On-Demand Runtime Helpers

When scripts use POSIX constructs that Fish does not provide natively, `bait` injects helper functions at the top of the file (suppressible via `--no-helpers`):

1. **`getopts` option parsing**:
   - Injected when scripts call `getopts :optstring var [args...]`.
   - Supports standard short flags, argument binding, and quiet (`:`) mode, managing `$OPTIND` and `$OPTARG`.
2. **`__bait_words` field splitting**:
   - Injected when unquoted variables or command substitutions require field splitting matching POSIX `$IFS` in structural contexts (`for x in $var`) or at unquoted call sites (`cmd $FLAGS` $\to$ `cmd $(__bait_words $FLAGS)`).
   - Variables recognized as native Fish lists (such as environment variables ending in `PATH` and `LANGUAGE`) adhere to native list semantics without unnecessary word splitting.
3. **`__bait_exec` dynamic commands**:
   - Injected for dynamic command string execution (`$cmd arg` $\to$ `__bait_exec $cmd arg`).
   - Dispatches dynamically split command words while strictly preserving argument boundaries and command prefixes.
4. **`hash` command hashing**:
   - Injected when scripts call `hash`.
   - Handles `-r` and checks command availability.
5. **`unalias` alias erasure**:
   - Injected when scripts call `unalias`.
   - Erases alias functions and supports `-a`.
6. **`source` and `.` transparent script translation**:
   - Injected when scripts call `source` or `.`.
   - Translates Bash scripts on-the-fly via `bait` (requires `bait` in PATH) and evaluates them in the caller's scope. Can also be printed for interactive Fish configurations via `bait helper source` and `bait helper .`.
7. **`unset` variable and function erasure**:
   - Injected when scripts call `unset`.
   - Erases variables (including array elements with 0-based index translation) and function definitions, supporting `-v`, `-f`, `-n`, and `--` (with `-n` handled as standard variable erasure due to Fish's lack of namerefs).
8. **`__bait_ostype` operating system identifier**:
   - Injected when scripts expand `$OSTYPE`.
   - Resolves the operating system identifier matching Bash's target format (e.g. `linux-gnu`, `linux-musl`, `darwin<version>`, `freebsd<version>`), ensuring pattern matches like `linux-gnu*` and `darwin*` behave identically to Bash.
9. **`__bait_eval` dynamic evaluation**:
   - Injected when scripts call `eval` (`eval "..."` $\to$ `__bait_eval "..."`).
   - Evaluates dynamic commands in the caller's scope via `--no-scope-shadowing`, translating Bash constructs on-the-fly via `bait` (requires `bait` in PATH).
