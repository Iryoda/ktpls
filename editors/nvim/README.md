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

By default ktpls runs its **analyzer**: a sibling JVM process with the
Kotlin compiler's front end (the Kotlin Analysis API). As you type, the
buffer's compiler errors and warnings come back in a fraction of a second
(source `kotlin`), without saving; after a save, files affected by the
change are re-checked in the background. The analyzer jar is built by the
Mason install (the first build downloads its dependencies and can take a
while). It uses up to 2 GB of memory and stops with ktpls.

The analyzer also answers hovers ktpls can't resolve from the source
alone: library declarations such as `save`, `map` or `getOrThrow` show
their signature, the types of the call under the cursor, and their
documentation, read from the library's `-sources.jar` in the Gradle cache.

Starting is quick after the first run: the Gradle project model is cached
until a build file changes, the JVM keeps a class data sharing archive,
and the last run's diagnostics of unchanged files show at once while the
analyzer catches up (open files are checked first). The caches live in
`~/.cache/ktpls`.

If the analyzer can't run (no jar, no Java, no Gradle model), ktpls falls
back to Gradle builds on save and says so once.

```lua
vim.lsp.config("ktpls", {
  init_options = {
    diagnostics = "analyzer",   -- or "gradle" (build on save), or "off"
    analyzer = {
      javaHome = "/path/to/jdk",  -- default: compile.env.JAVA_HOME, $JAVA_HOME, java on PATH
      maxMemory = "2g",
      delay = 150,                -- ms of pause in typing before a check
    },
  },
})
```

### Gradle builds

With `diagnostics = "gradle"`, in a Gradle project (a `gradlew`, or `gradle` on `PATH` with a build
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
