package server

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Iryoda/ktpls/internal/kotlin"
	"github.com/Iryoda/ktpls/internal/protocol"
)

func TestPropertyPlaceholders(t *testing.T) {
	root := t.TempDir()
	yml := filepath.Join(root, "src/main/resources/application.yml")
	writeFile(t, yml, "services:\n  billing:\n    host: http://billing\n    port: 8080\n")
	writeFile(t, filepath.Join(root, "src/main/resources/application-production.yml"), "services:\n  billing:\n    host: https://billing.prod\n")
	path := filepath.Join(root, "src/main/kotlin/app/Billing.kt")
	src := `package app

@FeignClient(
    name = "billing",
    url = "\${services.billing.host}:" +
        "\${services.billing.prot}",
)
interface Billing

class Jobs(
    @Value("\${jobs.cron:0 0 * * *}") val cron: String,
    @Value("\${services.billing}") val section: String,
    @Value("\${PORT} \${random.uuid} \${sm@api-key}") val runtime: String,
)
`
	c := newTestClient(t)
	if resp := c.call("initialize", map[string]any{"processId": nil, "rootUri": protocol.URIFromPath(root), "capabilities": map[string]any{}}); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	c.notify("initialized", map[string]any{})
	select {
	case <-c.srv.loaded:
	case <-time.After(10 * time.Second):
		t.Fatal("workspace load did not finish")
	}
	uri := protocol.URIFromPath(path)
	c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "languageId": "kotlin", "version": 1, "text": src},
	})

	d := c.nextDiagnosticsFor(uri)
	var msgs []string
	for _, dg := range d.Diagnostics {
		msgs = append(msgs, dg.Message)
	}
	want := []string{
		`Unknown property "services.billing.prot": not in application.yml, application-production.yml (did you mean "services.billing.port"?)`,
		`Property "services.billing" is a section of application.yml, application-production.yml, not a value`,
	}
	if !slices.Equal(msgs, want) {
		t.Errorf("diagnostics =\n  %s\nwant\n  %s", strings.Join(msgs, "\n  "), strings.Join(want, "\n  "))
	}

	at := func(s string) map[string]any {
		off := strings.Index(src, s) + 2
		line := strings.Count(src[:off], "\n")
		return map[string]any{"line": line, "character": off - strings.LastIndex(src[:off], "\n") - 1}
	}
	resp := c.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at("billing.host")})
	var h protocol.Hover
	if err := json.Unmarshal(resp.Result, &h); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"services.billing.host", "**default** (application.yml): `http://billing`", "**production** (application-production.yml): `https://billing.prod`"} {
		if !strings.Contains(h.Contents.Value, s) {
			t.Errorf("hover lacks %q:\n%s", s, h.Contents.Value)
		}
	}
	resp = c.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at("jobs.cron")})
	if err := json.Unmarshal(resp.Result, &h); err != nil || !strings.Contains(h.Contents.Value, "**if unset**: `0 0 * * *`") {
		t.Errorf("hover on a defaulted key: %s %v", h.Contents.Value, err)
	}

	resp = c.call("textDocument/definition", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at("billing.port")})
	var locs []protocol.Location
	json.Unmarshal(resp.Result, &locs)
	if len(locs) != 0 {
		t.Errorf("definition of an unknown key = %+v", locs)
	}
	resp = c.call("textDocument/definition", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at("billing.host")})
	json.Unmarshal(resp.Result, &locs)
	if len(locs) != 2 || locs[0].URI != protocol.URIFromPath(yml) || locs[0].Range.Start.Line != 2 {
		t.Errorf("definition = %+v", locs)
	}
}

func TestPropertyChecked(t *testing.T) {
	s := &Server{}
	for key, want := range map[string]bool{
		"services.billing.host": true,
		"list[0].name":          true,
		"PORT":                  false, // an environment variable
		"random.uuid":           false,
		"sm@api-key":            false,
		"sm://api-key":          false,
		"#{1 + 1}":              false,
		"a b":                   false,
	} {
		if got := s.checked(kotlin.Placeholder{Key: key}); got != want {
			t.Errorf("checked(%q) = %v, want %v", key, got, want)
		}
	}
	if s.checked(kotlin.Placeholder{Key: "a.b", HasDefault: true}) {
		t.Error("a key with a default is checked")
	}
}
