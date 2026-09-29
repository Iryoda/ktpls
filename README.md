# kt-vibe-lsp

A Kotlin language server written in Go, structured after
[gopls](https://github.com/golang/tools/tree/master/gopls). It parses Kotlin
with [tree-sitter](https://tree-sitter.github.io/) and resolves symbols
across the `.kt`/`.kts` files of your workspace. There is no JVM and no
Gradle import.

See [PLAN.md](PLAN.md) for the roadmap. Status: **M2 (hover)**.
Supported:

- **Go to definition** (`gd`) across the workspace, covering locals,
  members (inherited, companion, extension), imports and aliases, named
  arguments, and `apply`/`run`/`with` receivers.
- **Hover** (`K`): the declaration's signature, where it's declared, and
  its KDoc rendered as markdown.

Completion is next.

Resolution is syntax- and scope-based (there is no type checker), and
limited to the workspace's own `.kt`/`.kts` files. It does not reach the
Kotlin stdlib or library jars.

## Building

tree-sitter is a C library, so building needs **CGo and a C compiler**
(gcc or clang). The Kotlin grammar is large, so the first build takes a
while.

```sh
make build     # -> bin/kt-vibe-lsp
make test      # go test -race ./...
make install   # go install (to $GOBIN or ~/go/bin)
```

## Neovim

Neovim ≥ 0.11:

```lua
vim.lsp.config('kt_vibe_lsp', {
  cmd = { 'kt-vibe-lsp', 'serve', '-logfile', '/tmp/kt-vibe-lsp.log' },
  filetypes = { 'kotlin' },
  root_markers = { 'settings.gradle.kts', 'settings.gradle', 'build.gradle.kts', 'build.gradle', '.git' },
})
vim.lsp.enable('kt_vibe_lsp')
```

Neovim 0.10:

```lua
vim.api.nvim_create_autocmd('FileType', {
  pattern = 'kotlin',
  callback = function(args)
    vim.lsp.start({
      name = 'kt-vibe-lsp',
      cmd = { 'kt-vibe-lsp', 'serve', '-logfile', '/tmp/kt-vibe-lsp.log' },
      root_dir = vim.fs.root(args.buf, { 'settings.gradle.kts', 'settings.gradle', '.git' }),
    })
  end,
})
```

To check that it's working, run `:checkhealth vim.lsp`. `:LspLog` shows the
"loaded N Kotlin files" message. Add `-v` to `cmd` for debug logs in the
logfile.

## Layout

| Package | Role (gopls equivalent) |
|---|---|
| `internal/cmd` | CLI and `serve` (`gopls/internal/cmd`) |
| `internal/protocol` | JSON-RPC framing, LSP types, dispatch, position mapping (`gopls/internal/protocol`) |
| `internal/server` | One file per LSP feature, thin adapters (`gopls/internal/server`) |
| `internal/cache` | Session, editor overlays, parsed files (`gopls/internal/cache`) |
| `internal/kotlin` | Kotlin language logic (`gopls/internal/golang`) |
| `tools/tsdump` | Dev tool: print a file's syntax tree (`go run ./tools/tsdump File.kt`) |
