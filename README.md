# ktpls

**ktpls** is a Kotlin language server written in Go, structured after
[gopls](https://github.com/golang/tools/tree/master/gopls). It parses Kotlin
with [tree-sitter](https://tree-sitter.github.io/) and resolves symbols
across the `.kt`/`.kts` files of your workspace. There is no JVM and no
Gradle import.

See [PLAN.md](PLAN.md) for the roadmap. Status: **M3 (completion)**.
Supported:

- **Go to definition** (`gd`) across the workspace, covering locals,
  members (inherited, companion, extension), imports and aliases, named
  arguments, and `apply`/`run`/`with` receivers.
- **Hover** (`K`): the declaration's signature, where it's declared, and
  its KDoc rendered as markdown.
- **Completion**: members after `.` (including inherited members,
  extensions, and companion members or enum entries through a type name),
  locals, enclosing-class members, named arguments (`name =`), keywords,
  and workspace declarations that aren't imported yet (accepting one adds
  the `import`).
- **Go to implementation** (`gi`): from an interface or class (its
  declaration or any use), the types implementing it; from a method, its
  overrides.
- **Find references** (`gr`), including uses through import aliases.
- **Rename** across the workspace. Renaming a method renames the whole
  override family (the interface declaration, every override, all uses);
  uses through an import alias keep the alias; named arguments follow a
  renamed parameter.
- **Signature help** while typing arguments: overloads, the current
  parameter (by position, or by name for `name = `), and KDoc.
- **Go to type definition**: the declaration of a value's type
  (`List<Account>` goes to `Account`).
- **Code actions**:
  - add parameter names to a call's arguments (`f(a, b)` → `f(x = a, y = b)`);
  - import an unresolved name declared in another workspace package;
  - go to test / go to tested class;
  - convert to expression body / block body;
  - specify type explicitly (adds the imports it needs);
  - add / remove braces of `if`, `for`, `while` and `when` branches;
  - convert string concatenation to a template;
  - implement members of interfaces and abstract classes (generic
    supertypes substituted, imports added);
  - add the remaining `when` branches for enums and sealed types;
  - create a function from its usage (top-level, private member, or a
    member of the receiver's class, in its own file), with parameter and
    return types inferred from the call.
- **Document outline** and **workspace symbol search**.
- **Compiler diagnostics**: the Kotlin compiler's own errors and warnings
  (type mismatches, nullability, unresolved references, …), through the
  project's Gradle build, in the background on startup and on save (about
  1–5 s on an incremental build). See [editors/nvim](editors/nvim) for
  options, such as the JDK the build needs.
- **Syntax errors** as warnings while you type, before the next build.
  Only errors introduced since the file was opened are reported, so parser
  gaps on valid code don't show up as noise.
- **Type inference** for `it` and lambda parameters (`xs.forEach { it. }`,
  `x?.let { it }`, `for (a in xs)`, `xs.first()`, `map[key]`, and
  workspace functions taking lambdas), so definition, hover and completion
  work inside lambdas too.
- Files changed on disk (git checkout, edits made outside the editor) are
  picked up automatically. The workspace walk skips build outputs and
  honors `.gitignore` files (nested ones and `!` negations included).

Resolution is syntax- and scope-based (there is no type checker), and
limited to the workspace's own `.kt`/`.kts` files. It does not reach the
Kotlin stdlib or library jars.

## Building

tree-sitter is a C library, so building needs **CGo and a C compiler**
(gcc or clang). The Kotlin grammar is large, so the first build takes a
while.

```sh
make build     # -> bin/ktpls
make test      # go test -race ./...
make install   # go install (to $GOBIN or ~/go/bin)
```

## Neovim

The quickest route is the bundled plugin in [`editors/nvim`](editors/nvim),
which provides the server config and a local Mason registry, so
`:MasonInstall ktpls` builds it from this checkout. See its README. To
configure the server by hand instead:

Neovim ≥ 0.11:

```lua
vim.lsp.config('ktpls', {
  cmd = { 'ktpls', 'serve', '-logfile', '/tmp/ktpls.log' },
  filetypes = { 'kotlin' },
  root_markers = { 'settings.gradle.kts', 'settings.gradle', 'build.gradle.kts', 'build.gradle', '.git' },
})
vim.lsp.enable('ktpls')

-- Optional: Neovim's built-in completion, triggered as you type.
vim.api.nvim_create_autocmd('LspAttach', {
  callback = function(args)
    local client = vim.lsp.get_client_by_id(args.data.client_id)
    if client and client.name == 'ktpls' then
      vim.lsp.completion.enable(true, client.id, args.buf, { autotrigger = true })
    end
  end,
})
```

nvim-cmp and blink.cmp work too, through their LSP sources.

Neovim 0.10:

```lua
vim.api.nvim_create_autocmd('FileType', {
  pattern = 'kotlin',
  callback = function(args)
    vim.lsp.start({
      name = 'ktpls',
      cmd = { 'ktpls', 'serve', '-logfile', '/tmp/ktpls.log' },
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
| `internal/fuzzy` | Fuzzy matching for completion (`gopls/internal/fuzzy`) |
| `internal/build` | Runs the Gradle build and parses the Kotlin compiler's messages |
| `internal/util/textutil` | Shared text helpers: lines, indentation, identifiers, UTF-16 (`gopls/internal/util`) |
| `tools/tsdump` | Dev tool: print a file's syntax tree (`go run ./tools/tsdump File.kt`) |

## License and credits

ktpls is licensed under the [Apache License 2.0](LICENSE). You may use,
modify and redistribute it freely, including commercially. Redistributions
and derivative works must keep the [NOTICE](NOTICE) file, which credits the
projects ktpls builds on:

- [gopls](https://github.com/golang/tools/tree/master/gopls), the model
  for ktpls's architecture;
- [fwcd/tree-sitter-kotlin](https://github.com/fwcd/tree-sitter-kotlin),
  the Kotlin grammar;
- [tree-sitter](https://github.com/tree-sitter/tree-sitter) and
  [go-tree-sitter](https://github.com/tree-sitter/go-tree-sitter);
- [JetBrains Kotlin LSP](https://github.com/Kotlin/kotlin-lsp), the
  reference for capabilities; and others listed in NOTICE.

The licenses of third-party code compiled into ktpls are in
[THIRD_PARTY_LICENSES](THIRD_PARTY_LICENSES).
