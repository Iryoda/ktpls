# ktpls

**ktpls** is a language server for
[Kotlin](https://kotlinlang.org). It gives any editor that speaks the
[Language Server Protocol](https://microsoft.github.io/language-server-protocol/)
IDE features such as go to definition, hover, completion, find
references, rename, code actions and compiler diagnostics.

It's written in Go and built like
[gopls](https://github.com/golang/tools/tree/master/gopls), the Go
language server. It starts in a fraction of a second and stays light on
memory, even on large codebases.

> **Status: experimental.** ktpls is in daily use and tested on codebases
> of thousands of files, but it's young. Expect rough edges, and please
> [open an issue](../../issues) when something resolves to the wrong place.
> Neovim is the only editor tested so far.

## How it works

ktpls answers in two layers:

1. **A fast syntactic layer, in Go.** Every `.kt`/`.kts` file in the
   workspace is parsed with [tree-sitter](https://tree-sitter.github.io/)
   and indexed. Definition, hover, completion, references, rename and code
   actions are answered from that index by scope- and import-aware name
   resolution, plus a light type inference for receivers, lambdas and
   collections. Answers take microseconds and work as soon as the workspace
   loads (well under a second for thousands of files), with no build needed.
2. **The Kotlin compiler, in an optional JVM process.** The
   [analyzer](analyzer) runs the Kotlin compiler's front end (the Kotlin
   Analysis API, K2) over the project's Gradle model. It's a sibling
   process, the way jdtls is for Java. It supplies the compiler's own
   errors and warnings as you type, and answers what syntax alone can't:
   library declarations, their docs, and the types of library calls. If it
   isn't available, ktpls falls back to running the Gradle build on save.

So you get instant navigation from the first second, and compiler-accurate
diagnostics once the JVM side has warmed up.

### Compared with other Kotlin language servers

- [JetBrains Kotlin LSP](https://github.com/Kotlin/kotlin-lsp) is the
  official server, built on IntelliJ. It's the most accurate, and the
  heaviest. ktpls is a lighter alternative that uses the same compiler
  front end only for diagnostics and library lookups.
- [fwcd/kotlin-language-server](https://github.com/fwcd/kotlin-language-server)
  is the long-standing community server. It's JVM-based throughout.

## Features

**Navigation**

- **Go to definition** across the workspace: locals, members (inherited,
  companion, extension), imports and aliases, named arguments, and
  `apply`/`run`/`with` receivers. With the analyzer, library declarations
  open in their sources jar.
- **Go to type definition** (`List<Account>` goes to `Account`),
  **declaration** and **implementation**. Implementation finds the types
  implementing an interface or class, and the overrides of a method.
- **Find references**, including uses through import aliases.
- **Document outline** and **workspace symbol search**.

**Editing**

- **Hover**: signature, declaring container and KDoc as markdown. With the
  analyzer, library declarations show their docs and the call's types.
- **Completion**: members after `.` (inherited, extensions, companion and
  enum entries), locals, enclosing-class members, named arguments
  (`name =`), keywords, and workspace declarations not yet imported
  (accepting one adds the `import`).
- **Signature help**: overloads, the current parameter (by position or
  `name =`) and KDoc.
- **Rename** across the workspace. A method's whole override family is
  renamed, import aliases are kept, and named arguments follow a renamed
  parameter.
- **Type inference** for `it` and lambda parameters (`xs.forEach { it. }`,
  `x?.let { … }`, `for (a in xs)`, `map[key]`, …), so all of the above
  works inside lambdas.

**Code actions**

- Add parameter names to a call (`f(a, b)` → `f(x = a, y = b)`).
- Import an unresolved name.
- Go to test / go to tested class.
- Convert to expression body / block body.
- Specify the type explicitly.
- Add or remove braces.
- Convert string concatenation to a template.
- Implement members of interfaces and abstract classes.
- Add the remaining `when` branches (enums, sealed types).
- Create a function from its usage.

**Diagnostics**

- **Compiler errors and warnings** as you type, from the analyzer (or from
  the Gradle build on save).
- **Syntax errors** while typing, before the compiler has caught up.
- **Message keys**: warns when a string passed where a message-bundle key
  is expected isn't in `messages.properties`, like IntelliJ's property-key
  check. Hover on a key shows its translations.

**Workspace**

- Files changed on disk (git checkout, external edits) are picked up
  automatically.
- Build outputs are skipped, and `.gitignore` files are honored, nested
  ones and `!` negations included.

## Requirements

| For | You need |
|---|---|
| ktpls itself | Go ≥ 1.27 and a C compiler (gcc or clang): tree-sitter is a C library, so builds use CGo |
| Compiler diagnostics and library lookups (optional) | JDK 21+ and a Gradle project |

Without the analyzer, everything in *Navigation*, *Editing* and *Code
actions* still works. It covers the workspace's own sources, but not the
Kotlin stdlib or library jars.

## Installation

### Neovim with Mason

The plugin in [`editors/nvim`](editors/nvim) ships the server config and a
local Mason registry that builds ktpls and the analyzer from this checkout:

```lua
-- lazy.nvim
{ dir = "/path/to/ktpls/editors/nvim" },

require("mason").setup({
  registries = { "github:mason-org/mason-registry", "lua:ktpls.mason" },
})
```

Then run `:MasonInstall ktpls` and call `vim.lsp.enable("ktpls")`. See
[editors/nvim](editors/nvim) for details.

### From source

```sh
git clone https://github.com/Iryoda/ktpls
cd ktpls
make install                      # ktpls -> $GOBIN (default ~/go/bin)

# Optional: the analyzer. ktpls looks for analyzer.jar next to its binary.
(cd analyzer && ./gradlew shadowJar)
cp analyzer/build/libs/analyzer.jar "$(go env GOPATH)/bin/"
```

The first build of each takes a while: the Kotlin grammar is large, and
the analyzer downloads the Kotlin compiler.

## Editor setup

ktpls speaks LSP over stdin/stdout: `ktpls serve` (or just `ktpls`).
`-logfile <path>` writes logs to a file, and `-v` adds debug logs.

### Neovim ≥ 0.11

With the plugin on the runtimepath, `vim.lsp.enable("ktpls")` is enough.
By hand:

```lua
vim.lsp.config("ktpls", {
  cmd = { "ktpls", "serve" },
  filetypes = { "kotlin" },
  root_markers = { "settings.gradle.kts", "settings.gradle", "build.gradle.kts", "build.gradle", ".git" },
})
vim.lsp.enable("ktpls")
```

Completion works with the built-in `vim.lsp.completion`, nvim-cmp and
blink.cmp. Progress shows in `vim.lsp.status()` and fidget.nvim. Use
`:checkhealth vim.lsp` to check that it's attached.

<details>
<summary>Neovim 0.10</summary>

```lua
vim.api.nvim_create_autocmd("FileType", {
  pattern = "kotlin",
  callback = function(args)
    vim.lsp.start({
      name = "ktpls",
      cmd = { "ktpls", "serve" },
      root_dir = vim.fs.root(args.buf, { "settings.gradle.kts", "settings.gradle", ".git" }),
    })
  end,
})
```

</details>

### Other editors

Any LSP client should work. Point it at `ktpls serve` for the `kotlin`
language, with the project's Gradle settings file or `.git` as the root.
Contributions of tested configurations are welcome.

## Configuration

Settings go in `initializationOptions` (`init_options` in Neovim). Every
option has a default:

```lua
init_options = {
  diagnostics = "analyzer",          -- "analyzer", "gradle" (build on save) or "off"
  analyzer = {
    javaHome = "/path/to/jdk",       -- default: compile.env.JAVA_HOME, $JAVA_HOME, java on PATH
    maxMemory = "2g",
    delay = 150,                     -- ms of typing pause before a check
    downloadSources = true,          -- fetch libraries' sources jars for docs
  },
  compile = {                        -- used by diagnostics = "gradle"
    tasks = { "compileKotlin", "compileTestKotlin" },
    env = { JAVA_HOME = "/path/to/jdk" },
  },
  messages = {
    enabled = true,
    keyParameters = { "ApiError(code)" },   -- extra parameters that take message keys
  },
}
```

The full reference is in [editors/nvim/README.md](editors/nvim/README.md).
Caches (the Gradle model, extracted sources, last diagnostics) live in
`~/.cache/ktpls`.

## Limitations

- Without the analyzer, resolution is syntactic: it's accurate for common
  code, and gives several candidates when a name is ambiguous. It doesn't
  see the stdlib or library jars.
- The analyzer needs a Gradle project. Maven projects get the syntactic
  layer only.
- The tree-sitter grammar mis-parses a few rare shapes, such as a class
  body with a member on the same line (`class C { fun a() {} }`). Files
  still load, but the declarations inside may be missed.

## Development

```sh
make build                        # -> bin/ktpls
make test                         # go test -race ./...
make vet                          # gofmt + go vet
go run ./tools/tsdump File.kt     # print a file's tree-sitter syntax tree
```

The layout follows gopls:

| Package | Role (gopls equivalent) |
|---|---|
| `cmd/ktpls`, `internal/cmd` | CLI and `serve` (`gopls/internal/cmd`) |
| `internal/protocol` | JSON-RPC framing, LSP types, dispatch, UTF-8/UTF-16 position mapping (`gopls/internal/protocol`) |
| `internal/server` | One file per LSP feature, thin adapters (`gopls/internal/server`) |
| `internal/cache` | Session, editor overlays, parsed files, workspace walk (`gopls/internal/cache`) |
| `internal/kotlin` | Kotlin language logic: index, resolution, inference, features (`gopls/internal/golang`) |
| `internal/fuzzy` | Fuzzy matching for completion (`gopls/internal/fuzzy`) |
| `internal/analyzer` | Client for the analyzer process, Gradle project model |
| `internal/build` | Runs Gradle builds and parses the compiler's messages |
| `internal/messages` | Message bundles (`.properties`) |
| `internal/util/textutil` | Shared text helpers (`gopls/internal/util`) |
| `analyzer/` | The JVM analyzer (Kotlin Analysis API). See its [README](analyzer/README.md) |
| `editors/nvim` | Neovim plugin and Mason registry |

## License and credits

ktpls is licensed under the [Apache License 2.0](LICENSE). Redistributions
must keep the [NOTICE](NOTICE) file, which credits the projects ktpls builds
on:

- [gopls](https://github.com/golang/tools/tree/master/gopls), the model
  for ktpls's architecture;
- [fwcd/tree-sitter-kotlin](https://github.com/fwcd/tree-sitter-kotlin),
  the Kotlin grammar;
- [tree-sitter](https://github.com/tree-sitter/tree-sitter) and
  [go-tree-sitter](https://github.com/tree-sitter/go-tree-sitter);
- the [Kotlin Analysis API](https://github.com/JetBrains/kotlin), behind
  the analyzer;
- [JetBrains Kotlin LSP](https://github.com/Kotlin/kotlin-lsp), the
  reference for capabilities.

The licenses of third-party code compiled into ktpls are in
[THIRD_PARTY_LICENSES](THIRD_PARTY_LICENSES).
