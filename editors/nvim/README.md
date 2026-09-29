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

## Compiler diagnostics

In a Gradle project (a `gradlew`, or `gradle` on `PATH` with a build
file), ktpls runs `compileKotlin compileTestKotlin` in the background
after the workspace loads and after each save, and shows the Kotlin
compiler's errors and warnings (source `kotlinc`). Progress shows up in
`vim.lsp.status()` (and plugins such as fidget.nvim).

Options, all optional:

```lua
vim.lsp.config("ktpls", {
  init_options = {
    compile = {
      enabled = true,                              -- false turns it off
      tasks = { "compileKotlin", "compileTestKotlin" },
      command = { "./gradlew" },                   -- default: detected
      env = { JAVA_HOME = "/path/to/jdk" },        -- the JDK the build needs
    },
  },
})
```
