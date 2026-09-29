-- Neovim (>= 0.11) configuration for ktpls, found automatically
-- when this plugin is on the runtimepath. Enable it with:
--
--   vim.lsp.enable("ktpls")
return {
    cmd = { "ktpls", "serve" },
    filetypes = { "kotlin" },
    root_markers = { "settings.gradle.kts", "settings.gradle", "build.gradle.kts", "build.gradle", "pom.xml", ".git" },
}
