package protocol

import (
	"context"
	"encoding/json"
)

// Server is the set of LSP methods implemented by ktpls.
// Methods are added here as capabilities land; Dispatch routes to them.
type Server interface {
	Initialize(context.Context, *InitializeParams) (*InitializeResult, error)
	Initialized(context.Context, *InitializedParams) error
	Shutdown(context.Context) error
	Exit(context.Context) error

	DidOpen(context.Context, *DidOpenTextDocumentParams) error
	DidChange(context.Context, *DidChangeTextDocumentParams) error
	DidSave(context.Context, *DidSaveTextDocumentParams) error
	DidClose(context.Context, *DidCloseTextDocumentParams) error
	DidChangeWatchedFiles(context.Context, *DidChangeWatchedFilesParams) error

	Definition(context.Context, *DefinitionParams) ([]Location, error)
	Implementation(context.Context, *ImplementationParams) ([]Location, error)
	References(context.Context, *ReferenceParams) ([]Location, error)
	CodeAction(context.Context, *CodeActionParams) ([]CodeAction, error)
	Hover(context.Context, *HoverParams) (*Hover, error)
	Completion(context.Context, *CompletionParams) (*CompletionList, error)
	DocumentSymbol(context.Context, *DocumentSymbolParams) ([]DocumentSymbol, error)
	WorkspaceSymbol(context.Context, *WorkspaceSymbolParams) ([]SymbolInformation, error)
}

// Dispatch decodes params for method and calls the matching Server method.
// It reports handled=false for methods the server does not implement.
func Dispatch(ctx context.Context, s Server, method string, params json.RawMessage) (result any, handled bool, err error) {
	switch method {
	case "initialize":
		var p InitializeParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, true, err
		}
		res, err := s.Initialize(ctx, &p)
		return res, true, err
	case "initialized":
		var p InitializedParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, true, err
		}
		return nil, true, s.Initialized(ctx, &p)
	case "shutdown":
		return nil, true, s.Shutdown(ctx)
	case "exit":
		return nil, true, s.Exit(ctx)

	case "textDocument/didOpen":
		var p DidOpenTextDocumentParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, true, err
		}
		return nil, true, s.DidOpen(ctx, &p)
	case "textDocument/didChange":
		var p DidChangeTextDocumentParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, true, err
		}
		return nil, true, s.DidChange(ctx, &p)
	case "textDocument/didSave":
		var p DidSaveTextDocumentParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, true, err
		}
		return nil, true, s.DidSave(ctx, &p)
	case "textDocument/didClose":
		var p DidCloseTextDocumentParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, true, err
		}
		return nil, true, s.DidClose(ctx, &p)

	case "workspace/didChangeWatchedFiles":
		var p DidChangeWatchedFilesParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, true, err
		}
		return nil, true, s.DidChangeWatchedFiles(ctx, &p)

	case "textDocument/definition":
		var p DefinitionParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, true, err
		}
		res, err := s.Definition(ctx, &p)
		return res, true, err
	case "textDocument/implementation":
		var p ImplementationParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, true, err
		}
		res, err := s.Implementation(ctx, &p)
		return res, true, err
	case "textDocument/references":
		var p ReferenceParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, true, err
		}
		res, err := s.References(ctx, &p)
		return res, true, err
	case "textDocument/codeAction":
		var p CodeActionParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, true, err
		}
		res, err := s.CodeAction(ctx, &p)
		return res, true, err
	case "textDocument/hover":
		var p HoverParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, true, err
		}
		res, err := s.Hover(ctx, &p)
		return res, true, err
	case "textDocument/completion":
		var p CompletionParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, true, err
		}
		res, err := s.Completion(ctx, &p)
		return res, true, err
	case "textDocument/documentSymbol":
		var p DocumentSymbolParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, true, err
		}
		res, err := s.DocumentSymbol(ctx, &p)
		return res, true, err
	case "workspace/symbol":
		var p WorkspaceSymbolParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, true, err
		}
		res, err := s.WorkspaceSymbol(ctx, &p)
		return res, true, err
	}
	return nil, false, nil
}

func unmarshalParams(params json.RawMessage, v any) error {
	if len(params) == 0 || string(params) == "null" {
		return nil
	}
	if err := json.Unmarshal(params, v); err != nil {
		return Errorf(CodeInvalidParams, "%v", err)
	}
	return nil
}
