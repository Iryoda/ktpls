package server

import (
	"context"
	"slices"
	"time"

	"github.com/Iryoda/ktpls/internal/cache"
	"github.com/Iryoda/ktpls/internal/protocol"
)

func (s *Server) Initialize(ctx context.Context, params *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	s.log.Debug("initialize", "params", debugJSON(params))

	enc := protocol.PositionEncodingUTF16
	if g := params.Capabilities.General; g != nil && slices.Contains(g.PositionEncodings, protocol.PositionEncodingUTF8) {
		enc = protocol.PositionEncodingUTF8
	}

	root := workspaceRoot(params)
	session := cache.NewSession(root, enc, s.log)
	s.log.Info("initialize", "session", session.String())

	s.mu.Lock()
	s.session = session
	s.state = stateInitialized
	s.mu.Unlock()

	return &protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			PositionEncoding: enc,
			TextDocumentSync: &protocol.TextDocumentSyncOptions{
				OpenClose: true,
				Change:    protocol.TextDocumentSyncFull,
				Save:      &protocol.SaveOptions{IncludeText: false},
			},
			DefinitionProvider: true,
			HoverProvider:      true,
			CompletionProvider: &protocol.CompletionOptions{TriggerCharacters: []string{"."}},

			ImplementationProvider:  true,
			ReferencesProvider:      true,
			CodeActionProvider:      true,
			RenameProvider:          &protocol.RenameOptions{PrepareProvider: true},
			DocumentSymbolProvider:  true,
			WorkspaceSymbolProvider: true,
		},
		ServerInfo: &protocol.ServerInfo{Name: "ktpls", Version: Version},
	}, nil
}

// workspaceRoot picks the workspace root directory from the initialize
// params, preferring rootUri, then the first workspace folder, then the
// deprecated rootPath. It returns "" if the client opened no folder.
func workspaceRoot(params *protocol.InitializeParams) string {
	var uris []protocol.DocumentURI
	if params.RootURI != nil {
		uris = append(uris, *params.RootURI)
	}
	for _, f := range params.WorkspaceFolders {
		uris = append(uris, f.URI)
	}
	for _, u := range uris {
		if path, err := u.Path(); err == nil {
			return path
		}
	}
	if params.RootPath != nil {
		return *params.RootPath
	}
	return ""
}

func (s *Server) Initialized(ctx context.Context, params *protocol.InitializedParams) error {
	go s.loadWorkspace()
	return nil
}

// loadWorkspace parses every Kotlin file in the workspace in the
// background, so that cross-file features work before files are opened.
func (s *Server) loadWorkspace() {
	defer func() {
		s.mu.Lock()
		s.lastScan = time.Now()
		s.mu.Unlock()
		close(s.loaded)
	}()
	start := time.Now()
	n, err := s.session.LoadWorkspace(s.ctx)
	if err != nil {
		s.log.Error("loading workspace", "err", err)
		s.logMessage(protocol.MessageError, "ktpls: loading workspace: %v", err)
		return
	}
	withErrors := 0
	s.session.Read(func(sn *cache.Snapshot) {
		for f := range sn.Files() {
			if f.SyntaxErrors {
				withErrors++
			}
		}
	})
	elapsed := time.Since(start).Round(time.Millisecond)
	s.log.Info("workspace loaded", "files", n, "withSyntaxErrors", withErrors, "elapsed", elapsed)
	s.logMessage(protocol.MessageInfo, "ktpls: loaded %d Kotlin files in %v (%d with syntax errors)", n, elapsed, withErrors)
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = stateShutdown
	s.shutdownReceived = true
	return nil
}

func (s *Server) Exit(ctx context.Context) error {
	s.cancel()
	select {
	case <-s.exited:
	default:
		close(s.exited)
	}
	return nil
}
