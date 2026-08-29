# 🎣 BAIT: Another Incomplete Transpiler

`bait` is a somewhat complete Bash-to-Fish shell script translator.

This project is similar to the existing [babelfish](https://github.com/bouk/babelfish) project, which is also based on [mvdan/sh](https://github.com/mvdan/sh). However, bait translates bash scripts much better, in terms of supported features and output quality.

Bait passes through constructs as much as possible when modern fish shell features can be leveraged. Common bash snippets could come out barely changed after translation.

## Status

> [!WARNING]
> Running complex scripts translated by bait could result in undefined behavior.

By definition™️, bait is never complete. Some bash features do not translate easily into fish.

However, it can already translate [nvm](https://github.com/nvm-sh/nvm) and many prominent installers, allowing them to run natively in fish.

## Install

- [Download latest GitHub release](https://github.com/kidonng/bait/releases/latest)
- Run directly with [Nix](https://nixos.org/): `nix run github:kidonng/bait`

## Usage

```sh
# Translate from stdin
echo 'VAR=hello; echo "$VAR"' | bait

# Translate to stdout
bait install.sh > install.fish

# Suppress translation warnings on stderr
bait --quiet install.sh > install.fish

# Source your favorite script
bait < script.sh | source
curl script.sh | bait | source
```

Bind a shortcut to paste bash snippet as fish:

```sh
bind ctrl-b 'commandline --insert (fish_clipboard_paste | bait)
```

## Documentation

- **[Compatibility Guide](./COMPATIBILITY.md)**: Full reference for supported syntax, control flow, scoping rules, parameter expansions, integer arithmetic, runtime differences, and unsupported constructs.
- **[Architecture & Developer Guidance](./AGENTS.md)**: Core design principles, uniform scoping philosophy, repository layout, and contributor workflows.
