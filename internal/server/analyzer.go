package server

import (
	"sync"
	"time"

	"github.com/Iryoda/ktpls/internal/analyzer"
	"github.com/Iryoda/ktpls/internal/protocol"
)

// Compiler diagnostics from the analyzer, a sibling JVM analyzing the
// project with the Kotlin compiler's front end. On save, the saved file is
// checked at once against the current session; the session is then
// rebuilt in the background so files depending on it catch up.

// rebuildDelay debounces session rebuilds after saves.
const rebuildDelay = 1500 * time.Millisecond

// maxAffected bounds the files re-diagnosed after a rebuild.
const maxAffected = 400

type analyzerState struct {
	mu        sync.Mutex
	client    *analyzer.Client
	closing   bool
	restarts  int
	timer     *time.Timer
	saved     map[string]bool                  // saved since the last rebuild
	live      map[string]*time.Timer           // pending live checks
	inflight  map[string]bool                  // live checks running; false: run again after
	lastEdit  time.Time                        // of any buffer, to hold rebuilds while typing
	diags     map[string][]protocol.Diagnostic // per file
	rebuildMu sync.Mutex                       // one rebuild at a time

	disk        map[string][]analyzer.Diagnostic // per file, as on disk: persisted
	restoreOnce sync.Once                        // the last run's diagnostics are shown on the first start
	modelCached bool                             // the session's model came from the cache
	gradle, env []string                         // how the model was read, to refresh it
}

const progressAnalyzer = "ktpls/analyzer"

func (s *Server) progressBegin(token, title, msg string) {
	if !s.progress {
		return
	}
	if _, err := s.client.Request(s.ctx, "window/workDoneProgress/create", &protocol.WorkDoneProgressCreateParams{Token: token}); err != nil {
		return
	}
	s.client.Notify("$/progress", &protocol.ProgressParams{Token: token, Value: &protocol.WorkDoneProgressBegin{Kind: "begin", Title: title, Message: msg}})
}

func (s *Server) progressEnd(token, msg string) {
	if s.progress {
		s.client.Notify("$/progress", &protocol.ProgressParams{Token: token, Value: &protocol.WorkDoneProgressEnd{Kind: "end", Message: msg}})
	}
}
