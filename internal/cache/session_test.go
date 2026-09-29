package cache

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/Iryoda/kt-vibe-lsp/internal/protocol"
)

func newTestSession(t *testing.T, root string) *Session {
	t.Helper()
	return NewSession(root, protocol.PositionEncodingUTF16, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestOverlayRevertsToDisk(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "A.kt")
	if err := os.WriteFile(path, []byte("class OnDisk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newTestSession(t, root)
	if n, err := s.LoadWorkspace(context.Background()); err != nil || n != 1 {
		t.Fatalf("LoadWorkspace = %d, %v", n, err)
	}

	s.Open(path, 1, []byte("class Edited {"))
	// check verifies the file state and that the index holds exactly the
	// class declared by the current version.
	check := func(wantClass string, wantOverlay, wantErrors bool) {
		t.Helper()
		s.Read(func(sn *Snapshot) {
			f := sn.File(path)
			if f == nil {
				t.Fatal("file missing")
			}
			if f.Overlay != wantOverlay || f.SyntaxErrors != wantErrors || (f.Tree != nil) != wantOverlay {
				t.Errorf("got overlay=%v errors=%v tree=%v", f.Overlay, f.SyntaxErrors, f.Tree != nil)
			}
			for _, name := range []string{"OnDisk", "Edited"} {
				if got, want := len(sn.Index().ByName(name)), btoi(name == wantClass); got != want {
					t.Errorf("index has %d symbols named %s, want %d", got, name, want)
				}
			}
		})
	}
	check("Edited", true, true)

	// A later workspace load must not clobber the open buffer.
	if n, _ := s.LoadWorkspace(context.Background()); n != 0 {
		t.Errorf("reload loaded %d files, want 0", n)
	}
	check("Edited", true, true)

	s.Close(path)
	check("OnDisk", false, false)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestCloseOutsideWorkspaceForgets(t *testing.T) {
	s := newTestSession(t, t.TempDir())
	other := filepath.Join(t.TempDir(), "Scratch.kt")
	s.Open(other, 1, []byte("fun main() {}"))
	s.Close(other)
	s.Read(func(sn *Snapshot) {
		if sn.Len() != 0 {
			t.Errorf("files = %d, want 0", sn.Len())
		}
	})
}

func TestInWorkspace(t *testing.T) {
	s := newTestSession(t, "/home/u/proj")
	for path, want := range map[string]bool{
		"/home/u/proj/a/B.kt":   true,
		"/home/u/proj":          true,
		"/home/u/project/B.kt":  false,
		"/home/u/B.kt":          false,
		"/home/u/proj/..x/B.kt": true, // a directory named "..x" is inside
	} {
		if got := s.inWorkspace(path); got != want {
			t.Errorf("inWorkspace(%q) = %v, want %v", path, got, want)
		}
	}
}
