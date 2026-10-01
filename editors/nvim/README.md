# Neovim plugin

This directory is a small Neovim plugin (Neovim ≥ 0.11) that provides:

- `lsp/ktpls.lua`, the server configuration. Once this directory is
  on the runtimepath, `vim.lsp.enable("ktpls")` is all you need.
- `lua/ktpls/mason`, a [Mason](https://github.com/mason-org/mason.nvim)
  registry that clones the server from
  [GitHub](https://github.com/Iryoda/ktpls) and builds it. Building needs
  Go and a C compiler.

With lazy.nvim (the plugin lives in a subdirectory of the repository):

```lua
{
  "Iryoda/ktpls",
  init = function(plugin) vim.opt.rtp:append(plugin.dir .. "/editors/nvim") end,
},
```

Mason, keeping the default registry:

```lua
require("mason").setup({
  registries = { "github:mason-org/mason-registry", "lua:ktpls.mason" },
})
```

Then run `:MasonInstall ktpls`, and call `vim.lsp.enable("ktpls")`.
Mason builds the commit the plugin's checkout tracks on GitHub, so after
the plugin manager pulls new commits the Mason UI shows an update (press
`U`).

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
documentation, read from the library's `-sources.jar` in the Gradle cache;
go to definition opens the declaration in those sources (extracted to
`~/.cache/ktpls/sources`). Go to (and peek) definition, type definition
and declaration also ask the analyzer when ktpls can't tell a value's
type from the source, as for a lambda parameter of a library call, so
lspsaga's `peek_definition` and `peek_type_definition` land on the right
declaration. Completion offers what the compiler sees on the receiver's
type, besides the workspace's declarations: library members and
extensions (`xs.map`, `result.getOrNull`, `it.uppercase` in a lambda) and
what default imports bring in scope (`listOf`). After startup, ktpls has
Gradle download the
sources jars still missing, like IntelliJ does (`downloadSources = false`
turns that off).

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
      downloadSources = true,     -- fetch libraries' sources jars for docs
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

## Message keys

Like IntelliJ's check of property keys, ktpls warns about a string passed
where a message key is expected that isn't in the project's message
bundles (`src/main/resources/messages.properties` and its locales, or the
bundles named by `spring.messages.basename`), suggesting the closest key.
Hover on a key shows its message in each locale, and go to definition
opens it in the bundles.

Which parameters take keys is learned from the code: a parameter most of
whose string arguments are keys (at least 3, 80% or more), like an
exception's `code`, takes keys; so do parameters annotated `@PropertyKey`.
This needs no analyzer and works as you type.

```lua
vim.lsp.config("ktpls", {
  init_options = {
    messages = {
      enabled = true,
      keyParameters = { "ApiError(code)" },  -- more, as "Function(param)"
    },
  },
})
```

## Configuration properties

Placeholders of Spring properties in strings, `"\${services.billing.host}"`
(or `${'$'}{key}` in a raw string), are checked against the project's
configuration: `application.yml`, `application.yaml`,
`application.properties` and `bootstrap.*` in `src/*/resources`, with
their profile variants (`application-production.yml`). Keys match as
Spring's relaxed binding does (`servicesMapping` is `services-mapping`);
test code also sees the test resources. A key missing from all of them
gets a warning with the closest key, and one naming a section (properties
nested under it) gets a warning too. Hover shows the value in each
profile, and go to definition opens each one.

Not checked: keys with a default (`\${port:8080}`), environment-style
names (`\${DB_HOST}`), names from other sources such as a secret manager
(`\${sm@api-key}`), and properties set at run time (`random.*`, JVM system
properties such as `user.home`, `local.server.port`). When
`spring.config.import` brings in a config server, Vault or the like, the
warning becomes information, since the key may come from there.

```lua
vim.lsp.config("ktpls", {
  init_options = {
    properties = {
      enabled = true,
      ignore = { "vault." },  -- key prefixes defined elsewhere
    },
  },
})
```
