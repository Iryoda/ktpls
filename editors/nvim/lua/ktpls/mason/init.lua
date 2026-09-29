-- A Mason registry holding one package, ktpls, built from the local
-- checkout this file belongs to. Add it next to the default registry:
--
--   require("mason").setup({
--     registries = { "github:mason-org/mason-registry", "lua:ktpls.mason" },
--   })
--
-- then run :MasonInstall ktpls.
return {
    ["ktpls"] = "ktpls.mason.package",
}
