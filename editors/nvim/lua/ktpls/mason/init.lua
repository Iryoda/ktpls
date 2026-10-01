-- A Mason registry holding one package, ktpls, cloned from
-- github.com/Iryoda/ktpls and built. Add it next to the default registry:
--
--   require("mason").setup({
--     registries = { "github:mason-org/mason-registry", "lua:ktpls.mason" },
--   })
--
-- then run :MasonInstall ktpls.
return {
    ["ktpls"] = "ktpls.mason.package",
}
