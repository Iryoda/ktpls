-- Mason package spec for ktpls, built with `go build` from the local
-- checkout (a C compiler is needed: tree-sitter is a C library).

-- The repository root: this file is editors/nvim/lua/ktpls/mason/package.lua.
local this = debug.getinfo(1, "S").source:gsub("^@", "")
local root = vim.fs.normalize(vim.fn.fnamemodify(this, ":p:h:h:h:h:h:h"))

-- The version is the checkout's commit, so Mason offers an update after
-- pulling (or committing) new changes.
local version = vim.trim(vim.fn.system({ "git", "-C", root, "describe", "--always", "--dirty" }))
if vim.v.shell_error ~= 0 or version == "" then
    version = "local"
end

return {
    name = "ktpls",
    description = "Kotlin language server written in Go: go to definition, hover and completion across the workspace, parsed with tree-sitter.",
    homepage = "file://" .. root,
    licenses = {},
    languages = { "Kotlin" },
    categories = { "LSP" },
    source = {
        id = "pkg:generic/iryoda/ktpls@" .. version,
        build = {
            run = ("CGO_ENABLED=1 go build -C %s -o \"$PWD/ktpls\" ./cmd/ktpls"):format(vim.fn.shellescape(root)),
        },
    },
    bin = {
        ["ktpls"] = "ktpls",
    },
}
