-- Mason package spec for ktpls, cloned from GitHub and built with
-- `go build` (a C compiler is needed: tree-sitter is a C library).

-- The plugin's checkout: this file is editors/nvim/lua/ktpls/mason/package.lua.
local this = debug.getinfo(1, "S").source:gsub("^@", "")
local root = vim.fs.normalize(vim.fn.fnamemodify(this, ":p:h:h:h:h:h:h"))

local function git(...)
    local out = vim.fn.system({ "git", "-C", root, ... })
    if vim.v.shell_error == 0 and vim.trim(out) ~= "" then
        return vim.trim(out)
    end
end

-- The version is the commit the plugin's checkout tracks on GitHub (its
-- upstream, so local unpushed commits don't ask for a commit GitHub doesn't
-- have), so Mason offers an update after the plugin manager pulls.
local version = git("rev-parse", "--verify", "--quiet", "@{upstream}") or git("rev-parse", "HEAD") or "main"

return {
    name = "ktpls",
    description = "Kotlin language server written in Go: go to definition, hover and completion across the workspace, parsed with tree-sitter.",
    homepage = "https://github.com/Iryoda/ktpls",
    licenses = { "Apache-2.0" },
    languages = { "Kotlin" },
    categories = { "LSP" },
    source = {
        id = "pkg:github/Iryoda/ktpls@" .. version,
        build = {
            -- ktpls itself, then the analyzer jar (optional: without it ktpls
            -- falls back to Gradle builds for compiler diagnostics). The first
            -- analyzer build downloads its dependencies and can take a while.
            run = table.concat({
                "CGO_ENABLED=1 go build -o ktpls ./cmd/ktpls",
                "(cd analyzer && ./gradlew -q shadowJar --no-configuration-cache && cp build/libs/analyzer.jar ../analyzer.jar) || echo 'ktpls: analyzer.jar not built; using Gradle builds for diagnostics'",
            }, "\n"),
        },
    },
    bin = {
        ["ktpls"] = "ktpls",
    },
}
