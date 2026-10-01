package springconfig

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestParseYAML(t *testing.T) {
	const src = `# comment
server:
  port: 8080 # trailing comment
  servlet:
    context-path: "/api"
services-mapping:
  billing:
    host: ${BILLING_HOST:http://localhost}
    port: '9000'
spring.application.name: shop
feature:
  flags:
    - alpha
    - beta
  servers:
    - name: one
      url: http://one
    - name: two
query: |
  select *
    from t
  where x: 1
empty:
cors:
- http://a
"[weird.key]": 1
after: true
---
spring:
  config:
    activate:
      on-profile: production
server:
  port: 80
`
	got := map[string]Property{}
	for _, p := range ParseYAML([]byte(src)) {
		got[p.Key+"@"+p.Profile] = p
	}
	for key, want := range map[string]string{
		"server.port@":                   "8080",
		"server.servlet.context-path@":   "/api",
		"services-mapping.billing.host@": "${BILLING_HOST:http://localhost}",
		"services-mapping.billing.port@": "9000",
		"spring.application.name@":       "shop",
		"feature.flags[0]@":              "alpha",
		"feature.flags[1]@":              "beta",
		"feature.servers[0].name@":       "one",
		"feature.servers[0].url@":        "http://one",
		"feature.servers[1].name@":       "two",
		"query@":                         "(multi-line text)",
		"empty@":                         "",
		"cors[0]@":                       "http://a",
		"[weird.key]@":                   "1",
		"after@":                         "true",
		"server.port@production":         "80",
		"spring.config.activate.on-profile@production": "production",
	} {
		p, ok := got[key]
		if !ok {
			t.Errorf("missing %s", key)
			continue
		}
		if p.Value != want {
			t.Errorf("%s = %q, want %q", key, p.Value, want)
		}
	}
	for _, key := range []string{"server@", "server.servlet@", "feature@", "feature.servers@", "where x@", "from t@"} {
		if _, ok := got[key]; ok {
			t.Errorf("%s: a section or block text, not a property", key)
		}
	}
	if p := got["server.port@"]; p.Line != 2 {
		t.Errorf("server.port line = %d, want 2", p.Line)
	}
}

func TestCanonical(t *testing.T) {
	for in, want := range map[string]string{
		"services-mapping.dojo.host": "servicesmapping.dojo.host",
		"servicesMapping.dojo.host":  "servicesmapping.dojo.host",
		"services_mapping.Dojo.HOST": "servicesmapping.dojo.host",
		"map[Some-Key].x":            "map[Some-Key].x",
	} {
		if got := Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoad(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/main/resources/application.yml", "spring:\n  config:\n    import: optional:configserver:http://cfg, classpath:extra.yml\nshop:\n  serviceUrl: http://shop\n")
	write("src/main/resources/application-production.yml", "shop:\n  service-url: https://shop\n  only-prod: x\n")
	write("src/main/resources/application.properties", "billing.timeout=5s\n")
	write("src/test/resources/application.yml", "test-only: y\n")
	write("build/resources/main/application.yml", "ignored: z\n")

	c := Load(root)
	es, _ := c.Lookup("shop.service-url", false)
	if len(es) != 2 || es[0].Profile != "" || es[0].Value != "http://shop" || es[1].Profile != "production" {
		t.Errorf("shop.service-url = %+v", es)
	}
	if es, _ := c.Lookup("billing.timeout", false); len(es) != 1 || es[0].Value != "5s" {
		t.Errorf("billing.timeout = %+v", es)
	}
	if es, _ := c.Lookup("test-only", false); len(es) != 0 {
		t.Errorf("test-only visible from main code: %+v", es)
	}
	if es, _ := c.Lookup("test-only", true); len(es) != 1 {
		t.Errorf("test-only not visible from test code: %+v", es)
	}
	if es, section := c.Lookup("shop", false); len(es) != 0 || !section {
		t.Errorf("shop = %+v, section %v; want a section", es, section)
	}
	if es, _ := c.Lookup("ignored", true); len(es) != 0 {
		t.Errorf("build output read: %+v", es)
	}
	if !slices.Equal(c.Imports, []string{"configserver:http://cfg"}) {
		t.Errorf("imports = %v", c.Imports)
	}
	if c.Stale(root) {
		t.Error("stale right after loading")
	}
	write("src/main/resources/application-staging.yml", "a: 1\n")
	if !c.Stale(root) {
		t.Error("not stale after a profile file was added")
	}
}
