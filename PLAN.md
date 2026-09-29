# ktpls — Kotlin Language Server in Go

Named **ktpls** after gopls: module `github.com/Iryoda/ktpls`, binary, Mason package and Neovim LSP config (the project started as `kt-vibe-lsp`). Entry point: `cmd/ktpls`.

## Context

Build a Kotlin LSP server in Go (`github.com/Iryoda/ktpls`) for use with Neovim. The repo is greenfield (bare `go.mod` + empty `main.go`). Structure mirrors **gopls** (golang/tools/gopls): CLI → jsonrpc2 transport → protocol dispatch → thin per-feature server layer → session/overlay cache → language-logic package. JetBrains' `Kotlin/kotlin-lsp` is the capabilities reference only (it wraps IntelliJ/K2 — JVM-locked, nothing reusable from Go).

**Decisions confirmed with the user:**
- Capability priority: **1) go to definition, 2) hover with KDoc, 3) completion**; go-to-implementation later.
- v1 resolves symbols in **workspace `.kt`/`.kts` files only** — no stdlib/jar/classpath.
- Parser: **tree-sitter** via official CGo bindings `github.com/tree-sitter/go-tree-sitter` + grammar `github.com/fwcd/tree-sitter-kotlin/bindings/go` pinned to `main` commit `1852ea17b7f6` (the v0.3.2 tag predates the Go binding). Chosen over `tree-sitter-grammars/tree-sitter-kotlin` v1.1.0 after a corpus comparison — see *Grammar notes*. Server is editor-agnostic (stdio JSON-RPC); Neovim is just the first client.

Tree-sitter gives syntax only (no `go/types` equivalent for Kotlin exists in Go), so resolution is name/scope-based over a workspace symbol index — accurate for common cases, multi-location fallback when ambiguous.

## Status

- **M0 — done.** stdio JSON-RPC, lifecycle, document sync, workspace load; verified in Neovim 0.12.
- **M1 — done.** `textDocument/definition`. On a 2,671-file production Kotlin service: workspace load 0.26–0.44 s, ~144 MB RSS; definition averages ~25 µs in-process, 0.06 ms round trip from Neovim. For names that exist in the workspace: 76% resolve to exactly one location (types 99%, named arguments 95%, member access 71%), 3.6% to more than 5.
- **M2 — done.** `textDocument/hover`: signature rebuilt from the syntax tree (annotations and bodies dropped, defaults elided to `= ...`, long parameter lists wrapped), declaring container/package, KDoc rendered to markdown (`@param`/`@property`/`@return`/`@throws`/`@see` sections, `[links]` as code). Signatures and docs are precomputed at extraction, since disk files keep no tree. Same corpus: ~24 µs per hover, 0.07 ms round trip from Neovim; RSS ~115 MB.
- **M3 — done.** `textDocument/completion`: dot-member completion (typed receivers, inherited members, extensions, companion/enum entries via a type name, `also`/`apply` returning the receiver; nothing for unknown receivers), locals nearest-first, enclosing-class members, same-package and imported symbols, named arguments `name =`, keywords, and unimported workspace symbols with an auto-import edit. Ranked by tier then fuzzy score. Pulled dot-completion forward from M4 since M1's receiver typing made it cheap. Measured by simulated typing (identifier truncated to its first characters, rest of line removed) on the same corpus: the intended name is offered 96% of the time after 2 characters (83% in the top 5), 93% right after a dot; ~0.9 ms per request in-process, 0.15 ms round trip from Neovim.
- **M4 (part 1) — done.** `textDocument/implementation` (transitive subtypes; same-name members for overrides), `textDocument/references` (same resolver as definition, compared by declaration location; searches import aliases; candidate files prefiltered by whole-word match and processed in parallel: ~10 ms avg, 142 ms worst on the corpus), `textDocument/documentSymbol`, `workspace/symbol`, background rescan of files changed on disk (stat-based, ~23 ms idle, at most every 3 s while active; also on `workspace/didChangeWatchedFiles`), root `.gitignore` subset.
- **Lambda/collection type inference — done.** Types keep their generic arguments (`List<Account>`); `it` and untyped lambda parameters are inferred from `let/also/takeIf/takeUnless` (receiver), collection operations (`forEach/map/filter/first/...` over `List/Set/Iterable/Sequence/Array/Flow`, `*Indexed` variants), element-returning calls (`first()`, `find {}`, `xs[i]`, `map[key]`), collection-preserving chains, `for` loops, and workspace functions with function-typed parameters. Per-request memoization of typing and name resolution keeps chains linear. Corpus: member access resolves exactly 74.5% (was 71%), >5 candidates 9.8% (was 14.4%); completion right after a dot finds the name 94.5% (was 93.1%). Hover shows `it: Type` and inferred lambda parameter types.
- **M1.5 — done.** Syntax diagnostics (Warning, source `ktpls`), debounced 250 ms, reported only if new since the buffer was opened (matched by error text, so they survive line shifts). ERROR regions are underlined to the end of their first line only. On the corpus: 0 visible errors across 2,671 valid files (the grammar's one-line-body gap fails through a hidden token and reports nothing).
- **Code action "add parameter names" — done.** On the corpus it applies to 7,892 of 7,900 member calls to workspace functions (the rest start with a vararg or have too many arguments).
- **Rename — done.** `textDocument/prepareRename` + `textDocument/rename` on top of references: override families (up through supertypes, down through implementations), import aliases kept, named arguments follow parameters; refuses library symbols, keywords, `it`, ambiguous targets and invalid names.
- **Signature help — done.** The call is found in the text (innermost unclosed `(`, skipping strings, stopping at lambdas), resolved like definition; active parameter by comma count or by `name =`; overloads listed, the first fitting one active; varargs absorb trailing arguments.
- **Type definition — done.** The type of a value (declared or inferred, `it` included); library types yield the workspace types among their arguments.
- **"Add import" code action — done.** Offered when the name under the cursor doesn't resolve to anything visible from the file (a local, a member of the receiver or an enclosing class, or a same-package/imported top-level declaration) and a top-level declaration of that name exists in another workspace package; after a dot, only extensions.
- Remaining ideas: nested `.gitignore` files and negations; incremental text sync.

## Key design decisions

- **Hand-roll JSON-RPC + LSP types.** `go.lsp.dev/*` is unmaintained. Framing (`Content-Length: N\r\n\r\n{json}`) is trivial; we need only ~30 protocol structs. Exactly two deps: the two tree-sitter modules.
- **Position encoding:** gopls-style `Mapper` (byte offset ↔ line/character). Negotiate `positionEncodings`: offer `utf-8` (Neovim ≥0.10 takes it, trivial path) and `utf-16` (LSP-mandatory, must be correct).
- **Sync:** full-text sync (`change: 1`) in v1 — tree-sitter full reparses are ms-fast; incremental sync + `Tree.Edit` is an M4 optimization (that's where silent tree-corruption bugs live).
- **Concurrency:** one stdin-read/dispatch goroutine; notifications mutate session state under a `sync.RWMutex`; requests read under RLock. No gopls-grade immutable snapshots at this scale.
- **tree-sitter memory:** official bindings need explicit `Close()` on parsers/trees. One parser per parse call (cheap); every cached tree closed when replaced or its file closes; tests run with `-race`.
- **Trees only for open buffers (from M1).** Measured in M0: keeping every tree costs ~650 MB for a 5,270-file repo. Disk files are parsed once, symbols extracted (with precomputed LSP ranges, signature and KDoc), then tree and content are dropped. Local-scope resolution only needs the file under the cursor, which is always open. Initial load parses in parallel (M0 sequential load: 3.7 s for that repo).
- **Dot-member completion:** deferred to M4 — M3 ships honest scope/package/keyword/workspace completion; on a `.` trigger, return an empty list rather than wrong results.

## Package layout

```
main.go                          # calls internal/cmd.Main()
internal/cmd/cmd.go              # flags: serve (default), -logfile, -version
internal/protocol/
  jsonrpc.go, conn.go            # framing, request/response correlation, write mutex
  tsprotocol.go                  # hand-written LSP type subset (json tags, no codegen)
  server.go                      # Server interface + serverDispatch(method) switch
  mapper.go                      # byte offset ↔ utf-8/utf-16 position (most load-bearing file)
  uri.go                         # file:// URI ↔ path
internal/server/
  server.go, general.go          # struct; initialize/initialized/shutdown/exit, capability negotiation
  text_synchronization.go        # didOpen/didChange/didSave/didClose
  definition.go, hover.go, completion.go, symbols.go, implementation.go   # thin adapters
internal/cache/
  session.go, overlay.go         # root, overlay buffers (URI → content+version), RWMutex
  parse.go                       # ParsedFile{URI, Version, Content, Tree, Mapper}; tree lifecycle
  workspace.go                   # walk root for .kt/.kts; skip .git/.gradle/.idea/build/out
internal/kotlin/
  syntax.go                      # NodeAt(offset), ancestor iteration, node-text helpers
  symbols.go                     # Symbol{Kind, Name, FQName, ContainerFQ, URI, SelectionRange, FullRange}
  index.go                       # byFQName, byShortName, perFile, membersOf, supertypesOf maps
  imports.go, scope.go           # package/import parsing; lexical scope walk-up
  definition.go, hover.go, kdoc.go, completion.go
internal/fuzzy/matcher.go        # ~120-line subsequence scorer (prefix/camelCase/run bonuses)
testdata/                        # marker-test fixture workspaces
Makefile                         # build/test/install with CGO_ENABLED=1
```

## Milestones

### M0 — Skeleton (exit: Neovim attaches, files parse)
- jsonrpc2 loop on stdio (case-insensitive headers; never log to stdout — logs go to `-logfile`/stderr via `slog`).
- Lifecycle: reject pre-`initialize` requests (code −32002); `shutdown`/`exit`. `initialize` records rootUri, negotiates encoding, returns `textDocumentSync: {openClose, change: Full, save}`.
- Overlay store from didOpen/didChange/didClose; parse each version with tree-sitter; close replaced trees.
- `initialized` kicks off background workspace walk + parse.
- **Tests:** framing round-trip; mapper matrix (ASCII, multibyte, emoji/surrogates, CRLF — non-negotiable); in-process `io.Pipe` smoke test (initialize→didOpen→shutdown). Manual: `vim.lsp.start{cmd={'bin/kt-vibe-lsp','-logfile','/tmp/ktlsp.log'}, root_dir=vim.fs.root(0,{'settings.gradle.kts','.git'})}`, verify `:checkhealth vim.lsp`.
- CGo notes in README: needs a C compiler; kotlin grammar is large (first build ~30–60 s); pin grammar release compatible with go-tree-sitter's tree-sitter ABI (mismatch fails at runtime).

### M1 — Go to definition (`textDocument/definition`)
- **Extraction** (tree walk per file; **must descend into `ERROR` nodes**, since recovery keeps declarations inside them): `class_declaration` (interface/enum/data/annotation classes are all `class_declaration` — discriminate by keyword child), `object_declaration`, `companion_object`, `function_declaration`, `property_declaration`, `val`/`var` primary-constructor params (they're properties), `secondary_constructor`, `type_alias`, `enum_entry`. Build FQN from `package_header` + ancestor containers. Node names: see *Grammar notes*.
- **Cache model change:** disk files keep only extracted symbols + package/imports (see *Key design decisions*); overlays keep tree + content.
- **Index:** `byFQName`, `byShortName`, `perFile` (per-file swap on change = cheap incremental reindex), `membersOf[classFQ]`, `supertypesOf[classFQ]` (heritage names resolved with the same import/package algorithm — feeds M4 implementation).
- **Resolution order** for identifier at cursor: ① lexical scope walking ancestors (params, lambda params, preceding local `val`/`var`, `for`/`catch`/`when` bindings) → ② enclosing class/object members incl. companion + one-level supertypes (visited-set loop) → ③ explicit imports (last segment or `as` alias) → ④ same-package top-level → ⑤ wildcard imports → ⑥ fallback: all `byShortName` matches, returned as multiple `Location`s (Neovim shows a picker). `Foo()` calls resolve to class `Foo`; inside `user_type` prefer type kinds; works inside string-template `${...}`.
- **Verify grammar node names empirically first** (throwaway test dumping s-expressions of a sample file) — don't trust docs.
- **Tests:** marker harness — fixtures with legal-Kotlin comments `/*@def(x)*/` and `/*@ref(x)*/`; harness regex-scans positions, builds a session over the fixture dir, asserts `Definition(ref) ⊇ def`. Cases: local, param, lambda shadowing, member, cross-file same package, explicit/aliased/wildcard import, ambiguous fallback (2 locations). Manual: `gd` cross-file in nvim.

### M1.5 — Syntax diagnostics (cheap win, makes the server feel alive)
- Walk tree for `ERROR`/`MISSING` nodes on change (debounced ~200 ms) → `textDocument/publishDiagnostics`.

### M2 — Hover (`textDocument/hover`)
- Reuse M1 resolver; if ambiguous show first + "_+N other candidates_".
- **Signature reconstruction** from syntax nodes (not raw text): re-serialize declaration header stopping before body; default values elided to `= ...`; properties never type-inferred.
- **KDoc:** nearest preceding `multiline_comment` starting `/**`, adjacent (≤1 blank line; check before `modifiers`/annotations too); strip gutters; `@param`/`@return` read fine as markdown.
- Response: `MarkupContent` markdown — fenced ```kotlin signature, `---`, doc body; `range` = identifier.
- **Tests:** `/*@hover(x)*/` markers + expected-markdown table. Cases: fn with KDoc, undocumented property, generic class, KDoc separated by >1 blank line (must NOT attach), annotated declaration. Manual: `K`.

### M3 — Completion (`textDocument/completion`)
- Prefix = identifier fragment at cursor (tree may have ERROR node — walk from nearest non-error ancestor).
- Tiers (encoded in `sortText`): ⓪ locals/params in scope → ① enclosing-class members → ② same-package + imported symbols → ③ keywords (`fun val var if when class object suspend …`) → ④ fuzzy-matched workspace symbols, capped ~100 with `isIncomplete: true`.
- `CompletionItem`: label, kind mapped from decl kind, `detail` = M2's signature reconstructor, plain-text insert (snippets later). Register triggerCharacter `.` but return empty on `.` until M4.
- If M3 finishes early, pull in **documentSymbol** and **workspaceSymbol** — both are direct index reads (~half day each).
- **Tests:** `/*@complete*/` marker table tests (labels present, tier ordering, cap + isIncomplete, empty-on-dot). Manual: `<C-x><C-o>` omnifunc or nvim-cmp/blink.cmp.

### M4 — Later capabilities
1. **documentSymbol / workspaceSymbol** (if not already pulled into M3).
2. **textDocument/implementation:** classes whose transitive `supertypesOf` set contains the target interface/class; for a member, same-name `override` functions in those classes.
3. ~~**Dot-member completion heuristic:**~~ (done in M3) receiver = identifier resolving to a binding with an explicit type annotation (or `this` / object name) → `membersOf[typeFQ]` + supertype members.
4. **references:** query all matching `simple_identifier`s, filter by running resolution at each and comparing to target.
5. Honor `.gitignore` in the workspace walk (today: a fixed list of build-output dirs — `build`, `out`, `bin`, `target`, `node_modules` — plus hidden dirs).
6. Incremental text sync + `Tree.Edit` reparse (with debug full-reparse s-expression comparison in tests).

## Risks
- Grammar gaps (single-line bodies; ~0.8% of real files have some ERROR) — extraction descends into ERROR nodes; handlers never crash on unexpected shapes (panics are recovered per request in `protocol.Conn`).
- UTF-16 column math is where young LSP servers break — M0 mapper test matrix is mandatory.
- tree-sitter ABI pinning between grammar and bindings — check compatibility when adding deps.
- Parsers aren't goroutine-safe — one per call site under the session lock.

## Verification
1. `go test -race ./...` — framing, mapper matrix, extraction, resolver, hover, completion marker tests over `testdata/`.
2. Per milestone in Neovim against a small multi-file sample Kotlin project: attach (M0), `gd` cross-file (M1), squiggles on broken syntax (M1.5), `K` signature+KDoc (M2), ranked completions (M3). `-logfile` + `:LspLog` for traces.
3. Index a real-world Kotlin repo (e.g. a Gradle sample app) to measure index time and shake out grammar edge cases.

## Grammar notes (verified with `go run ./tools/tsdump File.kt`; fwcd/tree-sitter-kotlin @ 1852ea17b7f6)

**Why this grammar.** On 10,846 real files (33 MB, local repos): fwcd `main` → 0.8% of files with errors, **0.01% of bytes inside ERROR**, 6.8 s total parse; tree-sitter-grammars v1.1.0 → 1.1%, 0.70%, 8.8 s. fwcd also puts annotations in `modifiers`, parses `$name` templates as identifiers, and distinguishes `type_identifier` from `simple_identifier`. Same grammar lineage as nvim-treesitter.

**Known limitation (both grammars, including Neovim's own parser):** a class/object body with a member on a *single line* — `class C { fun a() {} }` — fails. Usually recovery wraps the member in `ERROR` inside `class_body` (so extraction must descend into `ERROR`). Worse, a one-line `object X { fun f() {} }` silently misparses as `infix_expression(object_literal, simple_identifier X, lambda_literal{…})` with no ERROR — worth a targeted fallback in extraction. Multi-line bodies are fine.

Node shapes:
- No field names — find names by child kind.
- Identifiers: `simple_identifier` (values/functions), `type_identifier` (types). Dotted names (package, imports) are `identifier > simple_identifier*`.
- `package_header > identifier`. Imports: `import_list > import_header > identifier`, optional `import_alias > type_identifier`, optional `wildcard_import`.
- KDoc: `multiline_comment` starting with `/**`; line comments `line_comment`. Annotations live in the declaration's `modifiers > annotation`, so KDoc is the declaration's previous sibling.
- `class_declaration`: keyword child `class`/`interface`/`enum`(+`class`); `modifiers > class_modifier` (`data`, `sealed`, …); name `type_identifier`; `primary_constructor > class_parameter` (`binding_pattern_kind > val|var` ⇒ property; name `simple_identifier`); supertypes are direct children `delegation_specifier > user_type > type_identifier` (or `constructor_invocation`); body `class_body`, or `enum_class_body > enum_entry > simple_identifier`.
- `object_declaration > type_identifier, class_body`; `companion_object` (optional `type_identifier`) `> class_body`.
- `function_declaration`: optional `modifiers` (`member_modifier > override`, …), name `simple_identifier`, `function_value_parameters > parameter > simple_identifier, user_type` (default `= expr` is a sibling inside `function_value_parameters`), return `user_type` after `:`, `function_body` (`{ statements }` or `= expr`).
- `property_declaration`: `binding_pattern_kind > val|var`, `variable_declaration > simple_identifier` (or `multi_variable_declaration`).
- Blocks: `{ statements … }`; `for_statement > variable_declaration`; `control_structure_body`. Lambdas: `lambda_literal > lambda_parameters > variable_declaration`, body `statements`.
- Calls/navigation: `call_expression > simple_identifier, call_suffix > value_arguments`; `a.b` is `navigation_expression > …, navigation_suffix > simple_identifier`.
- String templates: `$name` → `interpolated_identifier`; `${expr}` → `interpolated_expression`.
- `type_alias > type_identifier = user_type`. `secondary_constructor`, `constructor_delegation_call`.
