package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Iryoda/ktpls/internal/analyzer"
	"github.com/Iryoda/ktpls/internal/protocol"
)

// The last run's diagnostics of unchanged files show at once, and go if
// the analyzer then can't run.
func TestRestoredDiagnostics(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	same := filepath.Join(root, "src/A.kt")
	changed := filepath.Join(root, "src/B.kt")
	writeFile(t, same, "package a\n\nval x: Int = \"no\"\n")
	writeFile(t, changed, "package a\n\nval y: Int = \"no\"\n")
	writeFile(t, filepath.Join(root, "build.gradle.kts"), "")
	script := filepath.Join(root, "fake-gradle.sh")
	writeFile(t, script, "#!/bin/sh\nsleep 1\n") // a build reporting nothing, after a while

	diag := []analyzer.Diagnostic{{Severity: "error", Start: 24, End: 28, Message: "Initializer type mismatch"}}
	stat := func(p string) persistedFile {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return persistedFile{Size: info.Size(), ModTime: info.ModTime().UnixNano(), Diagnostics: diag}
	}
	files := map[string]persistedFile{same: stat(same), changed: stat(changed)}
	later := time.Now().Add(time.Minute)
	os.Chtimes(changed, later, later) // edited since
	data, _ := json.Marshal(files)
	cache, err := analyzer.CacheFile(root, persistName)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, cache, string(data))

	c := newTestClient(t)
	resp := c.call("initialize", map[string]any{
		"processId": nil, "rootUri": protocol.URIFromPath(root), "capabilities": map[string]any{},
		"initializationOptions": map[string]any{
			"compile":  map[string]any{"command": []string{"/bin/sh", script}},
			"analyzer": map[string]any{"jar": filepath.Join(root, "missing.jar")},
		},
	})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	c.notify("initialized", map[string]any{})

	d := c.nextDiagnostics()
	if d.URI != protocol.URIFromPath(same) || len(d.Diagnostics) != 1 || d.Diagnostics[0].Range.Start.Line != 2 {
		t.Fatalf("first diagnostics: %+v, want the restored error in A.kt", d)
	}
	// The jar is missing: diagnostics fall back to Gradle, and the
	// restored ones go.
	if d := c.nextDiagnosticsFor(protocol.URIFromPath(same)); len(d.Diagnostics) != 0 {
		t.Errorf("after the fallback: %+v, want none", d.Diagnostics)
	}
}
