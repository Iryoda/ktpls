package cache

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Iryoda/ktpls/internal/protocol"
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

func TestRescan(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		// Make each write visibly newer, whatever the filesystem's
		// timestamp granularity.
		later := time.Now().Add(time.Duration(len(content)) * time.Second)
		os.Chtimes(p, later, later)
	}
	names := func(s *Session) []string {
		var out []string
		s.Read(func(sn *Snapshot) {
			for f := range sn.Files() {
				for _, sym := range f.Summary.Symbols {
					out = append(out, sym.Name)
				}
			}
		})
		slices.Sort(out)
		return out
	}
	write("a/A.kt", "class Alpha\n")
	write("a/B.kt", "class Beta\n")
	write("gen/G.kt", "class Generated\n")
	write("x.gen.kt", "class GenFile\n")
	write(".gitignore", "# generated code\n/gen/\n*.gen.kt\n")

	s := newTestSession(t, root)
	if _, err := s.LoadWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := names(s); !slices.Equal(got, []string{"Alpha", "Beta"}) {
		t.Fatalf("initial load: %v", got)
	}

	// Nothing changed: nothing to do.
	if n, _ := s.Rescan(context.Background()); n != 0 {
		t.Errorf("idle rescan changed %d files", n)
	}

	// Edit, add and delete files on disk (e.g. git checkout).
	write("a/A.kt", "class AlphaRenamed\n")
	write("a/C.kt", "class Gamma\n")
	os.Remove(filepath.Join(root, "a/B.kt"))
	// An open buffer is the source of truth, even if its file changes.
	s.Open(filepath.Join(root, "a/C.kt"), 1, []byte("class GammaEdited\n"))
	write("a/C.kt", "class GammaOnDisk\n")

	if n, _ := s.Rescan(context.Background()); n != 2 {
		t.Errorf("rescan changed %d files, want 2 (A edited, B deleted)", n)
	}
	if got := names(s); !slices.Equal(got, []string{"AlphaRenamed", "GammaEdited"}) {
		t.Errorf("after rescan: %v", got)
	}
	// Closing the buffer picks up the disk version.
	s.Close(filepath.Join(root, "a/C.kt"))
	if got := names(s); !slices.Equal(got, []string{"AlphaRenamed", "GammaOnDisk"}) {
		t.Errorf("after close: %v", got)
	}
}

func TestGitignore(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	write(".gitignore", "tmp/\n/generated\n*.bak.kt\n**/cache\ndocs/api/*.kt\n*.gen.kt\n!keep.gen.kt\n# comment\n\\#literal.kt\n")
	write("module/.gitignore", "local/\n/Only.kt\n!/generated2\n")
	g := &gitignore{}
	g.load(root, "")
	g.load(filepath.Join(root, "module"), "module")
	for _, tt := range []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{"tmp", true, true},
		{"src/tmp", true, true},
		{"tmp", false, false}, // dir-only pattern
		{"generated", true, true},
		{"src/generated", true, false}, // rooted
		{"src/x.bak.kt", false, true},
		{"a/b/cache", true, true},
		{"docs/api/A.kt", false, true},
		{"docs/A.kt", false, false},
		{"docs/api/sub/A.kt", false, false}, // * doesn't cross directories
		{"src/Main.kt", false, false},
		{"src/X.gen.kt", false, true},
		{"src/keep.gen.kt", false, false}, // negation re-includes
		{"#literal.kt", false, true},
		{"module/local", true, true},    // nested .gitignore
		{"local", true, false},          // ...applies only below its directory
		{"module/Only.kt", false, true}, // rooted at the nested directory
		{"module/sub/Only.kt", false, false},
	} {
		if got := g.match(tt.rel, tt.isDir); got != tt.want {
			t.Errorf("match(%q, dir=%v) = %v, want %v", tt.rel, tt.isDir, got, tt.want)
		}
	}
}

func TestWalkNestedGitignore(t *testing.T) {
	root := t.TempDir()
	for rel, content := range map[string]string{
		".gitignore":      "*.gen.kt\n!Keep.gen.kt\n",
		"a/A.kt":          "class A\n",
		"a/X.gen.kt":      "class X\n",
		"a/Keep.gen.kt":   "class Keep\n",
		"m/.gitignore":    "fixtures/\n",
		"m/fixtures/F.kt": "class F\n",
		"fixtures/Top.kt": "class Top\n", // m's rule doesn't apply here
	} {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	s := newTestSession(t, root)
	files, err := s.walk(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for p := range files {
		rel, _ := filepath.Rel(root, p)
		got = append(got, filepath.ToSlash(rel))
	}
	slices.Sort(got)
	if want := []string{"a/A.kt", "a/Keep.gen.kt", "fixtures/Top.kt"}; !slices.Equal(got, want) {
		t.Errorf("walked %v, want %v", got, want)
	}
}
