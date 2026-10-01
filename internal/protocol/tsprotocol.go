package protocol

import "encoding/json"

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

	InitializationOptions json.RawMessage `json:"initializationOptions,omitempty"`
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
	Window  *WindowClientCapabilities  `json:"window,omitempty"`
}

type WindowClientCapabilities struct {
	WorkDoneProgress bool `json:"workDoneProgress,omitempty"`
}

// --- Work done progress ---

type WorkDoneProgressCreateParams struct {
	Token string `json:"token"`
}

type ProgressParams struct {
	Token string `json:"token"`
	Value any    `json:"value"`
}

type WorkDoneProgressBegin struct {
	Kind        string `json:"kind"` // "begin"
	Title       string `json:"title"`
	Message     string `json:"message,omitempty"`
	Cancellable bool   `json:"cancellable"`
}

type WorkDoneProgressEnd struct {
	Kind    string `json:"kind"` // "end"
	Message string `json:"message,omitempty"`
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
	HoverProvider      bool                     `json:"hoverProvider,omitempty"`
	CompletionProvider *CompletionOptions       `json:"completionProvider,omitempty"`

	ImplementationProvider  bool                   `json:"implementationProvider,omitempty"`
	TypeDefinitionProvider  bool                   `json:"typeDefinitionProvider,omitempty"`
	DeclarationProvider     bool                   `json:"declarationProvider,omitempty"`
	ReferencesProvider      bool                   `json:"referencesProvider,omitempty"`
	CodeActionProvider      bool                   `json:"codeActionProvider,omitempty"`
	ExecuteCommandProvider  *ExecuteCommandOptions `json:"executeCommandProvider,omitempty"`
	RenameProvider          *RenameOptions         `json:"renameProvider,omitempty"`
	SignatureHelpProvider   *SignatureHelpOptions  `json:"signatureHelpProvider,omitempty"`
	DocumentSymbolProvider  bool                   `json:"documentSymbolProvider,omitempty"`
	WorkspaceSymbolProvider bool                   `json:"workspaceSymbolProvider,omitempty"`
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

type TypeDefinitionParams struct {
	TextDocumentPositionParams
}

type DeclarationParams struct {
	TextDocumentPositionParams
}

type ImplementationParams struct {
	TextDocumentPositionParams
}

type ReferenceParams struct {
	TextDocumentPositionParams
	Context ReferenceContext `json:"context"`
}

type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

type CodeActionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Context      CodeActionContext      `json:"context"`
}

type CodeActionContext struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
	Only        []string     `json:"only,omitempty"`
}

type CodeAction struct {
	Title   string         `json:"title"`
	Kind    string         `json:"kind,omitempty"`
	Edit    *WorkspaceEdit `json:"edit,omitempty"`
	Command *Command       `json:"command,omitempty"`
}

type Command struct {
	Title     string `json:"title"`
	Command   string `json:"command"`
	Arguments []any  `json:"arguments,omitempty"`
}

type ExecuteCommandOptions struct {
	Commands []string `json:"commands"`
}

type ExecuteCommandParams struct {
	Command   string            `json:"command"`
	Arguments []json.RawMessage `json:"arguments,omitempty"`
}

type ShowDocumentParams struct {
	URI       DocumentURI `json:"uri"`
	TakeFocus bool        `json:"takeFocus"`
	Selection *Range      `json:"selection,omitempty"`
}

// Navigation code actions.
const Source = "source"

type WorkspaceEdit struct {
	Changes map[DocumentURI][]TextEdit `json:"changes"`
}

// Code action kinds.
const (
	QuickFix        = "quickfix"
	RefactorRewrite = "refactor.rewrite"
)

type SignatureHelpOptions struct {
	TriggerCharacters   []string `json:"triggerCharacters,omitempty"`
	RetriggerCharacters []string `json:"retriggerCharacters,omitempty"`
}

type SignatureHelpParams struct {
	TextDocumentPositionParams
}

type SignatureHelp struct {
	Signatures      []SignatureInformation `json:"signatures"`
	ActiveSignature uint32                 `json:"activeSignature"`
	ActiveParameter uint32                 `json:"activeParameter"`
}

type SignatureInformation struct {
	Label           string                 `json:"label"`
	Documentation   *MarkupContent         `json:"documentation,omitempty"`
	Parameters      []ParameterInformation `json:"parameters"`
	ActiveParameter *uint32                `json:"activeParameter,omitempty"`
}

// ParameterInformation labels a parameter by its [start, end) offsets
// (in UTF-16 code units) within the signature label.
type ParameterInformation struct {
	Label [2]uint32 `json:"label"`
}

type RenameOptions struct {
	PrepareProvider bool `json:"prepareProvider"`
}

type PrepareRenameParams struct {
	TextDocumentPositionParams
}

type PrepareRenameResult struct {
	Range       Range  `json:"range"`
	Placeholder string `json:"placeholder"`
}

type RenameParams struct {
	TextDocumentPositionParams
	NewName string `json:"newName"`
}

type HoverParams struct {
	TextDocumentPositionParams
}

type MarkupKind string

const (
	PlainText MarkupKind = "plaintext"
	Markdown  MarkupKind = "markdown"
)

type MarkupContent struct {
	Kind  MarkupKind `json:"kind"`
	Value string     `json:"value"`
}

type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

type CompletionOptions struct {
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
	ResolveProvider   bool     `json:"resolveProvider"`
}

type CompletionTriggerKind int

const (
	CompletionInvoked                         CompletionTriggerKind = 1
	CompletionTriggerCharacter                CompletionTriggerKind = 2
	CompletionTriggerForIncompleteCompletions CompletionTriggerKind = 3
)

type CompletionContext struct {
	TriggerKind      CompletionTriggerKind `json:"triggerKind"`
	TriggerCharacter string                `json:"triggerCharacter,omitempty"`
}

type CompletionParams struct {
	TextDocumentPositionParams
	Context *CompletionContext `json:"context,omitempty"`
}

type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

type CompletionItemKind int

const (
	CompletionKindText          CompletionItemKind = 1
	CompletionKindMethod        CompletionItemKind = 2
	CompletionKindFunction      CompletionItemKind = 3
	CompletionKindConstructor   CompletionItemKind = 4
	CompletionKindField         CompletionItemKind = 5
	CompletionKindVariable      CompletionItemKind = 6
	CompletionKindClass         CompletionItemKind = 7
	CompletionKindInterface     CompletionItemKind = 8
	CompletionKindModule        CompletionItemKind = 9
	CompletionKindProperty      CompletionItemKind = 10
	CompletionKindEnum          CompletionItemKind = 13
	CompletionKindKeyword       CompletionItemKind = 14
	CompletionKindEnumMember    CompletionItemKind = 20
	CompletionKindConstant      CompletionItemKind = 21
	CompletionKindStruct        CompletionItemKind = 22
	CompletionKindTypeParameter CompletionItemKind = 25
)

type CompletionItemLabelDetails struct {
	Detail      string `json:"detail,omitempty"`
	Description string `json:"description,omitempty"`
}

type CompletionItem struct {
	Label               string                      `json:"label"`
	LabelDetails        *CompletionItemLabelDetails `json:"labelDetails,omitempty"`
	Kind                CompletionItemKind          `json:"kind,omitempty"`
	Detail              string                      `json:"detail,omitempty"`
	Documentation       *MarkupContent              `json:"documentation,omitempty"`
	SortText            string                      `json:"sortText,omitempty"`
	FilterText          string                      `json:"filterText,omitempty"`
	TextEdit            *TextEdit                   `json:"textEdit,omitempty"`
	AdditionalTextEdits []TextEdit                  `json:"additionalTextEdits,omitempty"`
	// Data is kept by the client and sent back with completionItem/resolve.
	Data json.RawMessage `json:"data,omitempty"`
}

type SymbolKind int

const (
	SymbolKindFile          SymbolKind = 1
	SymbolKindModule        SymbolKind = 2
	SymbolKindNamespace     SymbolKind = 3
	SymbolKindPackage       SymbolKind = 4
	SymbolKindClass         SymbolKind = 5
	SymbolKindMethod        SymbolKind = 6
	SymbolKindProperty      SymbolKind = 7
	SymbolKindField         SymbolKind = 8
	SymbolKindConstructor   SymbolKind = 9
	SymbolKindEnum          SymbolKind = 10
	SymbolKindInterface     SymbolKind = 11
	SymbolKindFunction      SymbolKind = 12
	SymbolKindVariable      SymbolKind = 13
	SymbolKindConstant      SymbolKind = 14
	SymbolKindObject        SymbolKind = 19
	SymbolKindEnumMember    SymbolKind = 22
	SymbolKindTypeParameter SymbolKind = 26
)

type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           SymbolKind       `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

type SymbolInformation struct {
	Name          string     `json:"name"`
	Kind          SymbolKind `json:"kind"`
	Location      Location   `json:"location"`
	ContainerName string     `json:"containerName,omitempty"`
}

type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

// --- Diagnostics ---

type DiagnosticSeverity int

const (
	SeverityError       DiagnosticSeverity = 1
	SeverityWarning     DiagnosticSeverity = 2
	SeverityInformation DiagnosticSeverity = 3
	SeverityHint        DiagnosticSeverity = 4
)

type Diagnostic struct {
	Range    Range              `json:"range"`
	Severity DiagnosticSeverity `json:"severity,omitempty"`
	Source   string             `json:"source,omitempty"`
	Message  string             `json:"message"`
}

type PublishDiagnosticsParams struct {
	URI         DocumentURI  `json:"uri"`
	Version     *int32       `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// --- Workspace ---

type DidChangeWatchedFilesParams struct {
	Changes []FileEvent `json:"changes"`
}

type FileEvent struct {
	URI  DocumentURI `json:"uri"`
	Type int         `json:"type"` // 1 created, 2 changed, 3 deleted
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
