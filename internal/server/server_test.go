package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Iryoda/ktpls/internal/cache"
	"github.com/Iryoda/ktpls/internal/protocol"
)

// testClient drives a Server over in-memory pipes, like an editor would.
// Like a real client, it reads continuously: responses are routed to the
// waiting call by ID and notifications are discarded, so the server never
// blocks writing.
type testClient struct {
	t      *testing.T
	srv    *Server
	w      io.Writer
	nextID int

	mu      sync.Mutex
	pending map[string]chan *protocol.Message
}

func newTestClient(t *testing.T) *testClient {
	t.Helper()
	clientToServer, serverIn := io.Pipe()
	serverOut, serverToClient := io.Pipe()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	conn := protocol.NewConn(clientToServer, serverToClient, log)
	srv := New(conn, log)
	c := &testClient{t: t, srv: srv, w: serverIn, pending: map[string]chan *protocol.Message{}}
	go conn.Run(context.Background(), srv.Handle)
	go c.readLoop(bufio.NewReader(serverOut))
	t.Cleanup(func() {
		serverIn.Close()
		serverOut.Close()
	})
	return c
}

func (c *testClient) readLoop(r *bufio.Reader) {
	for {
		body, err := protocol.ReadMessage(r)
		if err != nil {
			return
		}
		var msg protocol.Message
		if err := json.Unmarshal(body, &msg); err != nil || msg.Method != "" {
			continue // notification (e.g. window/logMessage)
		}
		c.mu.Lock()
		ch := c.pending[string(msg.ID)]
		delete(c.pending, string(msg.ID))
		c.mu.Unlock()
		if ch != nil {
			ch <- &msg
		}
	}
}

func (c *testClient) send(v any) {
	c.t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := protocol.WriteMessage(c.w, body); err != nil {
		c.t.Fatal(err)
	}
}

func (c *testClient) notify(method string, params any) {
	c.t.Helper()
	c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// call sends a request and waits for its response.
func (c *testClient) call(method string, params any) *protocol.Message {
	c.t.Helper()
	c.nextID++
	id := c.nextID
	ch := make(chan *protocol.Message, 1)
	c.mu.Lock()
	c.pending[jsonString(id)] = ch
	c.mu.Unlock()
	c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	select {
	case msg := <-ch:
		return msg
	case <-time.After(10 * time.Second):
		c.t.Fatalf("%s: no response", method)
		return nil
	}
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycle(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "src/A.kt"), "package a\n\nfun a() = 1\n")
	writeFile(t, filepath.Join(root, "build/Gen.kt"), "package gen\n") // skipped
	writeFile(t, filepath.Join(root, ".gradle/X.kt"), "package x\n")   // skipped
	writeFile(t, filepath.Join(root, "build.gradle.kts"), "plugins {}\n")

	c := newTestClient(t)

	// Requests before initialize are rejected.
	if resp := c.call("textDocument/hover", map[string]any{}); resp.Error == nil || resp.Error.Code != protocol.CodeServerNotInitialized {
		t.Fatalf("pre-initialize request: got %+v, want ServerNotInitialized", resp.Error)
	}

	resp := c.call("initialize", map[string]any{
		"processId":    nil,
		"rootUri":      protocol.URIFromPath(root),
		"capabilities": map[string]any{"general": map[string]any{"positionEncodings": []string{"utf-8", "utf-16"}}},
	})
	if resp.Error != nil {
		t.Fatalf("initialize: %v", resp.Error)
	}
	var result protocol.InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if got := result.Capabilities.PositionEncoding; got != protocol.PositionEncodingUTF8 {
		t.Errorf("positionEncoding = %q, want utf-8", got)
	}
	if sync := result.Capabilities.TextDocumentSync; sync == nil || sync.Change != protocol.TextDocumentSyncFull || !sync.OpenClose {
		t.Errorf("textDocumentSync = %+v", sync)
	}

	c.notify("initialized", map[string]any{})

	select {
	case <-c.srv.loaded:
	case <-time.After(10 * time.Second):
		t.Fatal("workspace load did not finish")
	}
	paths := snapshotPaths(c.srv.session)
	want := []string{filepath.Join(root, "build.gradle.kts"), filepath.Join(root, "src/A.kt")}
	if !equalSets(paths, want) {
		t.Errorf("loaded files = %v, want %v", paths, want)
	}

	// Open a new, unsaved file; then change it; then close it.
	newPath := filepath.Join(root, "src/B.kt")
	uri := protocol.URIFromPath(newPath)
	c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "languageId": "kotlin", "version": 1, "text": "package b\n"},
	})
	c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": 2},
		"contentChanges": []map[string]any{{"text": "package b\n\nclass B\n"}},
	})
	waitFor(t, func() bool {
		var ok bool
		c.srv.session.Read(func(sn *cache.Snapshot) {
			f := sn.File(newPath)
			ok = f != nil && f.Version == 2 && f.Overlay && string(f.Content) == "package b\n\nclass B\n" && !f.SyntaxErrors
		})
		return ok
	})
	c.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": uri}})
	waitFor(t, func() bool {
		var gone bool
		c.srv.session.Read(func(sn *cache.Snapshot) { gone = sn.File(newPath) == nil }) // never saved: forgotten
		return gone
	})
}

// TestShutdownExit checks the shutdown request and exit notification.
func TestShutdownExit(t *testing.T) {
	c := newTestClient(t)
	if resp := c.call("initialize", map[string]any{"processId": nil, "rootUri": nil, "capabilities": map[string]any{}}); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if resp := c.call("textDocument/unknownThing", map[string]any{}); resp.Error == nil || resp.Error.Code != protocol.CodeMethodNotFound {
		t.Errorf("unknown method: got %+v, want MethodNotFound", resp.Error)
	}
	if resp := c.call("shutdown", nil); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if resp := c.call("initialize", map[string]any{}); resp.Error == nil {
		t.Error("request after shutdown: expected error")
	}
	c.notify("exit", nil)
	select {
	case <-c.srv.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("server did not exit")
	}
	if !c.srv.ShutdownReceived() {
		t.Error("ShutdownReceived = false")
	}
}

func TestApplyChanges(t *testing.T) {
	r := func(l1, c1, l2, c2 uint32) *protocol.Range {
		return &protocol.Range{Start: protocol.Position{Line: l1, Character: c1}, End: protocol.Position{Line: l2, Character: c2}}
	}
	got, err := applyChanges([]byte("val x = 1\nval y = 2\n"), protocol.PositionEncodingUTF16, []protocol.TextDocumentContentChangeEvent{
		{Range: r(0, 4, 0, 5), Text: "abc"},    // rename x -> abc
		{Range: r(1, 8, 1, 9), Text: "40 + 2"}, // then edit line 2 of the *new* text
		{Range: r(2, 0, 2, 0), Text: "// 😀\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "val abc = 1\nval y = 40 + 2\n// 😀\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, err := applyChanges(nil, protocol.PositionEncodingUTF16, []protocol.TextDocumentContentChangeEvent{{Range: r(0, 0, 0, 0), Text: "x"}}); err == nil {
		t.Error("ranged change to unknown document: expected error")
	}
}

func snapshotPaths(s *cache.Session) []string {
	var paths []string
	s.Read(func(sn *cache.Snapshot) {
		for f := range sn.Files() {
			paths = append(paths, f.Path)
		}
	})
	return paths
}

func equalSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		if !m[x] {
			return false
		}
	}
	return true
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestDefinitionRequest(t *testing.T) {
	root := t.TempDir()
	lib := filepath.Join(root, "src/lib/Greeter.kt")
	writeFile(t, lib, "package lib\n\nclass Greeter {\n    fun greet(name: String) = \"hi $name\"\n}\n")
	usePath := filepath.Join(root, "src/app/Main.kt")

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
	// An unsaved buffer that uses the library class.
	src := "package app\n\nimport lib.Greeter\n\nfun main() {\n    Greeter().greet(\"x\")\n}\n"
	c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": protocol.URIFromPath(usePath), "languageId": "kotlin", "version": 1, "text": src},
	})
	resp := c.call("textDocument/definition", map[string]any{
		"textDocument": map[string]any{"uri": protocol.URIFromPath(usePath)},
		"position":     map[string]any{"line": 5, "character": 16}, // on "greet"
	})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	var locs []protocol.Location
	if err := json.Unmarshal(resp.Result, &locs); err != nil {
		t.Fatal(err)
	}
	want := protocol.Location{URI: protocol.URIFromPath(lib), Range: protocol.Range{
		Start: protocol.Position{Line: 3, Character: 8}, End: protocol.Position{Line: 3, Character: 13},
	}}
	if len(locs) != 1 || locs[0] != want {
		t.Errorf("definition = %+v, want [%+v]", locs, want)
	}
}
