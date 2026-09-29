// Package cache holds the server's view of the workspace: every known
// Kotlin file, preferring the contents of open editor buffers (overlays)
// over what is on disk, and the symbol index built from them.
package cache

import (
	"fmt"
	"iter"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Iryoda/kt-vibe-lsp/internal/kotlin"
	"github.com/Iryoda/kt-vibe-lsp/internal/protocol"
)

// A Session is the state for one client connection.
//
// Files are keyed by cleaned file path rather than URI, since clients and
// the disk walk may percent-encode the same path differently.
type Session struct {
	root string // workspace root directory; "" in single-file mode
	enc  protocol.PositionEncodingKind
	log  *slog.Logger

	mu    sync.RWMutex
	files map[string]*File
	index *kotlin.Index
}

// NewSession returns a session for the workspace rooted at root.
func NewSession(root string, enc protocol.PositionEncodingKind, log *slog.Logger) *Session {
	return &Session{root: root, enc: enc, log: log, files: make(map[string]*File), index: kotlin.NewIndex()}
}

// Encoding returns the negotiated position encoding.
func (s *Session) Encoding() protocol.PositionEncodingKind { return s.enc }

// Root returns the workspace root directory.
func (s *Session) Root() string { return s.root }

// Open records an editor buffer opened with the given content.
func (s *Session) Open(path string, version int32, content []byte) {
	s.put(newFile(path, version, true, content, s.enc))
}

// Change records new content for an open editor buffer.
func (s *Session) Change(path string, version int32, content []byte) {
	s.put(newFile(path, version, true, content, s.enc))
}

// Close records that an editor buffer was closed: the file reverts to its
// on-disk contents, or is forgotten if it isn't a workspace file on disk.
func (s *Session) Close(path string) {
	if s.inWorkspace(path) {
		if content, err := os.ReadFile(path); err == nil {
			s.put(newFile(path, 0, false, content, s.enc))
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.files[path]; ok {
		f.close()
		delete(s.files, path)
		s.index.Remove(path)
	}
}

func (s *Session) put(f *File) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putLocked(f)
}

func (s *Session) putLocked(f *File) {
	if old, ok := s.files[f.Path]; ok {
		old.close()
	}
	s.files[f.Path] = f
	s.index.Update(f.Summary)
}

// Read calls fn with a consistent, read-only snapshot of the session.
// The snapshot and everything obtained from it must not be used after fn
// returns.
func (s *Session) Read(fn func(*Snapshot)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fn(&Snapshot{files: s.files, index: s.index, enc: s.enc})
}

// A Snapshot is a read-only view of the session, valid only during a call
// to Session.Read.
type Snapshot struct {
	files map[string]*File
	index *kotlin.Index
	enc   protocol.PositionEncodingKind
}

// File returns the file at path, or nil if it is unknown.
func (sn *Snapshot) File(path string) *File { return sn.files[path] }

// Files returns all known files, in no particular order.
func (sn *Snapshot) Files() iter.Seq[*File] { return maps.Values(sn.files) }

// Len returns the number of known files.
func (sn *Snapshot) Len() int { return len(sn.files) }

// Index returns the workspace symbol index.
func (sn *Snapshot) Index() *kotlin.Index { return sn.index }

func (s *Session) inWorkspace(path string) bool {
	if s.root == "" {
		return false
	}
	rel, err := filepath.Rel(s.root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// String describes the session for logging.
func (s *Session) String() string { return fmt.Sprintf("session(root=%q, encoding=%s)", s.root, s.enc) }
