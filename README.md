# 🎣 Bait: Almost Idiomatic Transpiler

Bait is a [Bash](https://www.gnu.org/software/bash/) to [fish](https://fishshell.com/) transpiler.

This project is based on [mvdan/sh](https://github.com/mvdan/sh), similar to the existing [babelfish](https://github.com/bouk/babelfish) project. However, bait translates bash much better, both in quality and supported features.

Bait leverages modern fish features to produce clean, idiomatic output. Most common Bash snippets translate with only minimal changes.

Bait is experimental, but it can already translate [nvm](https://github.com/nvm-sh/nvm) and many prominent installers, allowing them to run natively in fish.

## Install

- [GitHub](https://github.com/kidonng/bait/releases/latest)

    ```sh
    mkdir -p ~/.local/bin && curl --location "https://github.com/kidonng/bait/releases/latest/download/bait_$(uname -s)_$(uname -m).tar.gz" | tar -xz -C ~/.local/bin bait
    ```

- [Nix](https://nixos.org/)

    ```sh
    nix profile install github:kidonng/bait
    ```

## Usage

> [!WARNING]
> Bait translation may produce unexpected result. Check output before serious usage.

```sh
# Translate from stdin
echo 'VAR=hello; echo "$VAR"' | bait

# Translate to stdout
bait install.sh > install.fish

# Suppress warnings on stderr
bait --quiet install.sh > install.fish

# Source your favorite script
bait < script.sh | source
curl script.sh | bait | source
```

Bind a shortcut to paste bash snippet into fish:

```sh
bind ctrl-b 'commandline --insert (fish_clipboard_paste | bait)
```

### Plugin

Bait offers a fish plugin that enables bash scripts to be `source`d directly.

Install via a plugin manager like [plug.fish](https://github.com/kidonng/plug.fish) or [fisher](https://github.com/jorgebucaran/fisher):

```sh
fisher install kidonng/bait
```

Or [load manually](functions/source.fish):

```sh
curl https://raw.githubusercontent.com/kidonng/bait/refs/heads/main/functions/source.fish --output-dir ~/.config/fish/functions
```

## Docs

- [Compatibility Guide](COMPATIBILITY.md): reference for (un)supported features
- [Developer Guide](AGENTS.md): architectural principles & developer workflows
