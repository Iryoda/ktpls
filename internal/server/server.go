// Package server implements the LSP server: it adapts protocol requests
// to the cache and to the Kotlin language features in package kotlin.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Iryoda/ktpls/internal/cache"
	"github.com/Iryoda/ktpls/internal/protocol"
)

// Version is the server version reported to clients.
const Version = "0.1.0-dev"

type state int

const (
	stateCreated state = iota
	stateInitialized
	stateShutdown
)

// Server implements protocol.Server.
type Server struct {
	client *protocol.Conn
	log    *slog.Logger

	// ctx scopes background work (workspace loading); cancelled on exit.
	ctx    context.Context
	cancel context.CancelFunc

	mu               sync.Mutex
	state            state
	shutdownReceived bool
	session          *cache.Session // set by Initialize

	exited chan struct{} // closed on exit
	loaded chan struct{} // closed when the initial workspace load finishes

	lastScan time.Time   // guarded by mu
	scanning atomic.Bool // a background rescan is running
}

// rescanInterval is the minimum time between background rescans of the
// workspace for files changed on disk (git checkout, external edits).
const rescanInterval = 3 * time.Second

var _ protocol.Server = (*Server)(nil)

// New returns a server that sends notifications to client.
func New(client *protocol.Conn, log *slog.Logger) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		client: client,
		log:    log,
		ctx:    ctx,
		cancel: cancel,
		exited: make(chan struct{}),
		loaded: make(chan struct{}),
	}
}

// Exited is closed when the client sends the exit notification.
func (s *Server) Exited() <-chan struct{} { return s.exited }

// ShutdownReceived reports whether shutdown preceded exit; the process
// exit code should be 0 if so and 1 otherwise.
func (s *Server) ShutdownReceived() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shutdownReceived
}

// Handle is the protocol.Handler for the connection. It enforces the LSP
// lifecycle, then dispatches to the Server methods.
func (s *Server) Handle(ctx context.Context, msg *protocol.Message) (any, error) {
	start := time.Now()
	defer func() {
		s.log.Debug("handled", "method", msg.Method, "elapsed", time.Since(start))
	}()

	s.mu.Lock()
	st := s.state
	s.mu.Unlock()

	switch {
	case msg.Method == "exit":
		// Always allowed.
	case st == stateCreated && msg.Method != "initialize":
		if msg.IsCall() {
			return nil, protocol.Errorf(protocol.CodeServerNotInitialized, "server not initialized")
		}
		return nil, nil // notifications before initialize are dropped
	case st != stateCreated && msg.Method == "initialize":
		return nil, protocol.Errorf(protocol.CodeInvalidRequest, "initialize called twice")
	case st == stateShutdown:
		if msg.IsCall() {
			return nil, protocol.Errorf(protocol.CodeInvalidRequest, "server is shut down")
		}
		return nil, nil
	}

	result, handled, err := protocol.Dispatch(ctx, s, msg.Method, msg.Params)
	s.maybeRescan(false)
	if !handled {
		if msg.IsCall() {
			return nil, protocol.Errorf(protocol.CodeMethodNotFound, "method not supported: %s", msg.Method)
		}
		return nil, nil // unknown notifications (e.g. $/cancelRequest) are ignored
	}
	return result, err
}

// maybeRescan starts a background rescan of the workspace if the initial
// load is done and none ran recently (or force is set). Activity drives
// it: an idle editor causes no disk traffic.
func (s *Server) maybeRescan(force bool) {
	select {
	case <-s.loaded:
	default:
		return
	}
	s.mu.Lock()
	due := force || time.Since(s.lastScan) >= rescanInterval
	if due {
		s.lastScan = time.Now()
	}
	s.mu.Unlock()
	if !due || !s.scanning.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.scanning.Store(false)
		start := time.Now()
		n, err := s.session.Rescan(s.ctx)
		if err != nil {
			s.log.Debug("rescan", "err", err)
		}
		if n > 0 {
			s.log.Info("rescan", "changed", n, "elapsed", time.Since(start).Round(time.Millisecond))
		}
	}()
}

// logMessage shows a message in the client's log (:LspLog in Neovim).
func (s *Server) logMessage(typ protocol.MessageType, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if err := s.client.Notify("window/logMessage", &protocol.LogMessageParams{Type: typ, Message: msg}); err != nil {
		s.log.Error("sending window/logMessage", "err", err)
	}
}

// debugJSON renders v for debug logs.
func debugJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
