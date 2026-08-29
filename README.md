# 🎣 Bait: Almost Idiomatic Transpiler

<table>
<tr>
<td width="50%" valign="top">

Bait is a [Bash](https://www.gnu.org/software/bash/) to [fish](https://fishshell.com/) transpiler.

This project is based on [mvdan/sh](https://github.com/mvdan/sh), similar to the existing [babelfish](https://github.com/bouk/babelfish) project. However, bait translates bash much better, both in quality and supported features.

Bait leverages modern fish features to produce clean, idiomatic output. Most common Bash snippets translate with only minimal changes.

Bait is experimental, but it can already translate [nvm](https://github.com/nvm-sh/nvm) and many prominent installers, allowing them to run natively in fish.

</td>
<td width="50%" valign="top">

https://github.com/user-attachments/assets/d23f56dc-42a6-425b-9de0-432306c8ec47

</td>
</tr>
</table>

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

# Suppress warnings on stderr (or BAIT_QUIET=1 env)
bait --quiet install.sh > install.fish

# Source your favorite script
bait < script.sh | source
curl script.sh | bait | source
```

### Advanced

Load helpers to directly `source` bash scripts:

```sh
bait helper source > ~/.config/fish/functions/source.fish
bait helper . > ~/.config/fish/functions/..fish
```

Load all helpers:

```fish
for helper in (bait helper --names)
    bait helper $helper > ~/.config/fish/functions/$helper.fish
end
```

Bind a shortcut to paste bash snippet into fish:

```fish
bind ctrl-b 'commandline --insert (fish_clipboard_paste | bait --no-helpers)'
```


## Docs

- [Compatibility Guide](COMPATIBILITY.md): reference for (un)supported features
- [Developer Guide](AGENTS.md): architectural principles & developer workflows
