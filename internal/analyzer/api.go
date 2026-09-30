package analyzer

import (
	"context"
	"strconv"
)

// A Diagnostic is a compiler diagnostic in a file. Start and End are
// offsets into the analyzed text, in UTF-16 code units (Java's string
// indices).
type Diagnostic struct {
	Severity string `json:"severity"` // "error", "warning" or "info"
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Message  string `json:"message"`
	Factory  string `json:"factory"` // e.g. UNRESOLVED_REFERENCE
}

// FileDiagnostics are the diagnostics of one file.
type FileDiagnostics struct {
	Path        string       `json:"path"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// InitResult describes a new session.
type InitResult struct {
	Files  int   `json:"files"`
	Millis int64 `json:"millis"`
}

// Init builds a session from the project model at modelPath, with the JDK
// at jdkHome.
func (c *Client) Init(ctx context.Context, modelPath, jdkHome string) (*InitResult, error) {
	var r InitResult
	err := c.Call(ctx, "init", map[string]string{"model": modelPath, "jdkHome": jdkHome}, &r)
	return &r, err
}

// Check returns the diagnostics of text as the new content of the file at
// path, against the rest of the project as of the current session.
func (c *Client) Check(ctx context.Context, path, text string) ([]Diagnostic, error) {
	var r FileDiagnostics
	err := c.Call(ctx, "check", map[string]string{"path": path, "text": text}, &r)
	return r.Diagnostics, err
}

// Rebuild replaces the session with a new one reading the files as they
// are on disk now.
func (c *Client) Rebuild(ctx context.Context) (*InitResult, error) {
	var r InitResult
	err := c.Call(ctx, "rebuild", nil, &r)
	return &r, err
}

// Diagnose returns the diagnostics of files as they are in the session;
// no paths means every file.
func (c *Client) Diagnose(ctx context.Context, paths []string) ([]FileDiagnostics, error) {
	var r struct {
		Files []FileDiagnostics `json:"files"`
	}
	err := c.Call(ctx, "diagnose", map[string][]string{"paths": paths}, &r)
	return r.Files, err
}

// HoverInfo describes the reference under the cursor as the compiler
// resolves it. Offsets are in UTF-16 code units.
type HoverInfo struct {
	Start       int     `json:"start"`
	End         int     `json:"end"`
	Signature   string  `json:"signature"`   // the declaration
	Call        string  `json:"call"`        // with this call's types, if they differ
	Container   string  `json:"container"`   // class or package
	Doc         string  `json:"doc"`         // the raw doc comment
	DocLanguage string  `json:"docLanguage"` // "kotlin" or "java"
	Source      *Source `json:"source"`
}

// A Source is where a declaration is: a project file (Jar empty; Path
// empty for the file asked about, in the text sent), or an entry of a
// library's sources jar. Offset is in UTF-16 code units.
type Source struct {
	Path   string `json:"path"`
	Jar    string `json:"jar"`
	Entry  string `json:"entry"`
	Offset int    `json:"offset"`
}

// Hover resolves the reference at offset (UTF-16) in text, the content
// of the file at path; nil if there is none.
func (c *Client) Hover(ctx context.Context, path, text string, offset int) (*HoverInfo, error) {
	var r *HoverInfo
	err := c.Call(ctx, "hover", map[string]string{"path": path, "text": text, "offset": strconv.Itoa(offset)}, &r)
	return r, err
}
