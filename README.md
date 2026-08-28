# 🎣 BAIT: Another Incomplete Transpiler

`bait` is a fast, pragmatic Bash-to-Fish shell script translator.

This project is similar to the existing [babelfish](https://github.com/bouk/babelfish) project, which is also based on [mvdan/sh](https://github.com/mvdan/sh). However, bait translates bash scripts much better, in terms of supported features and output quality.

Bait passes through constructs as much as possible when modern fish shell features can be leveraged. Common bash snippets could come out barely changed after translation.

## Quickstart

Run `bait` directly using [Nix](https://nixos.org/):

```bash
# Translate from stdin
echo 'VAR=hello; echo "$VAR"' | nix run github:kidonng/bait

# Translate a script to stdout
nix run github:kidonng/bait -- install.sh > install.fish

# Suppress translation warnings on stderr
nix run github:kidonng/bait -- --quiet install.sh > install.fish
```

Inside the cloned repository:

```bash
# Run local build
nix run . -- script.sh

# Run the full test suite (unit tests, differential equivalence, and e2e installer sandboxes)
nix run .#test

# Run tests for a specific package
nix run .#test -- ./internal/bait
```

## Status

By definition™️, bait is never complete. Some bash features do not translate easily into fish.

However, it can already translate the most popular installer scripts and enable them to run natively in fish. _It is not recommended to actually do so._

## Documentation

- **[Compatibility Guide](./COMPATIBILITY.md)**: Full reference for supported syntax, control flow, scoping rules, parameter expansions, integer arithmetic, on-demand runtime helpers (`getopts`, `__bait_words`, `__bait_exec`), documented runtime differences, and tested real-world scripts.
- **[Architecture & Developer Guidance](./AGENTS.md)**: Core design principles, uniform scoping philosophy, repository layout, and contributor workflows.
