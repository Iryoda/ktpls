package protocol

// This file holds the hand-written subset of LSP 3.17 types used by the
// server. Field names and JSON tags follow the specification; only fields
// the server reads or writes are included.

// DocumentURI is a file:// URI identifying a text document.
type DocumentURI string

// Position is a zero-based line and character offset. The unit of
// Character is set by the negotiated PositionEncodingKind.
type Position struct {
	Line      uint32 `json:"line"`
	Character uint32 `json:"character"`
}

// Range is a half-open [Start, End) span in a document.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a range within a document.
type Location struct {
	URI   DocumentURI `json:"uri"`
	Range Range       `json:"range"`
}

type TextDocumentIdentifier struct {
	URI DocumentURI `json:"uri"`
}

type VersionedTextDocumentIdentifier struct {
	URI     DocumentURI `json:"uri"`
	Version int32       `json:"version"`
}

type TextDocumentItem struct {
	URI        DocumentURI `json:"uri"`
	LanguageID string      `json:"languageId"`
	Version    int32       `json:"version"`
	Text       string      `json:"text"`
}

type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// --- Lifecycle ---

type PositionEncodingKind string

const (
	PositionEncodingUTF8  PositionEncodingKind = "utf-8"
	PositionEncodingUTF16 PositionEncodingKind = "utf-16"
)

type InitializeParams struct {
	ProcessID        *int32             `json:"processId"`
	ClientInfo       *ClientInfo        `json:"clientInfo,omitempty"`
	RootPath         *string            `json:"rootPath,omitempty"`
	RootURI          *DocumentURI       `json:"rootUri"`
	Capabilities     ClientCapabilities `json:"capabilities"`
	WorkspaceFolders []WorkspaceFolder  `json:"workspaceFolders,omitempty"`
}

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type WorkspaceFolder struct {
	URI  DocumentURI `json:"uri"`
	Name string      `json:"name"`
}

type ClientCapabilities struct {
	General *GeneralClientCapabilities `json:"general,omitempty"`
}

type GeneralClientCapabilities struct {
	PositionEncodings []PositionEncodingKind `json:"positionEncodings,omitempty"`
}

type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
	ServerInfo   *ServerInfo        `json:"serverInfo,omitempty"`
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type ServerCapabilities struct {
	PositionEncoding   PositionEncodingKind     `json:"positionEncoding,omitempty"`
	TextDocumentSync   *TextDocumentSyncOptions `json:"textDocumentSync,omitempty"`
	DefinitionProvider bool                     `json:"definitionProvider,omitempty"`
}

type TextDocumentSyncKind int

const (
	TextDocumentSyncNone        TextDocumentSyncKind = 0
	TextDocumentSyncFull        TextDocumentSyncKind = 1
	TextDocumentSyncIncremental TextDocumentSyncKind = 2
)

type TextDocumentSyncOptions struct {
	OpenClose bool                 `json:"openClose"`
	Change    TextDocumentSyncKind `json:"change"`
	Save      *SaveOptions         `json:"save,omitempty"`
}

type SaveOptions struct {
	IncludeText bool `json:"includeText"`
}

type InitializedParams struct{}

// --- Document synchronization ---

type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

// TextDocumentContentChangeEvent replaces Range with Text, or the whole
// document when Range is nil.
type TextDocumentContentChangeEvent struct {
	Range *Range `json:"range,omitempty"`
	Text  string `json:"text"`
}

type DidSaveTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Text         *string                `json:"text,omitempty"`
}

type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// --- Language features ---

type DefinitionParams struct {
	TextDocumentPositionParams
}

// --- Window ---

type MessageType int

const (
	MessageError   MessageType = 1
	MessageWarning MessageType = 2
	MessageInfo    MessageType = 3
	MessageLog     MessageType = 4
)

type LogMessageParams struct {
	Type    MessageType `json:"type"`
	Message string      `json:"message"`
}
