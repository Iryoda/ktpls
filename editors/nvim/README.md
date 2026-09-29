# Neovim plugin

This directory is a small Neovim plugin (Neovim ≥ 0.11) that provides:

- `lsp/ktpls.lua`, the server configuration. Once this directory is
  on the runtimepath, `vim.lsp.enable("ktpls")` is all you need.
- `lua/ktpls/mason`, a [Mason](https://github.com/mason-org/mason.nvim)
  registry that builds the server from this checkout. Building needs Go and
  a C compiler.

With lazy.nvim:

```lua
{ dir = "/path/to/ktpls/editors/nvim" },
```

Mason, keeping the default registry:

```lua
require("mason").setup({
  registries = { "github:mason-org/mason-registry", "lua:ktpls.mason" },
})
```

Then run `:MasonInstall ktpls`, and call `vim.lsp.enable("ktpls")`.
The package version is the checkout's `git describe` output, so after
pulling new commits the Mason UI shows an update (press `U`).

Without Mason: run `make install` in the repository root, and make sure
`~/go/bin` is on your `PATH`.
