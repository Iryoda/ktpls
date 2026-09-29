package protocol

import (
	"fmt"
	"net/url"
	"path/filepath"
)

// URIFromPath returns the file:// URI for an absolute file path.
func URIFromPath(path string) DocumentURI {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return DocumentURI(u.String())
}

// Path returns the file path for a file:// URI.
func (u DocumentURI) Path() (string, error) {
	parsed, err := url.Parse(string(u))
	if err != nil {
		return "", fmt.Errorf("invalid URI %q: %w", u, err)
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf("unsupported URI scheme %q in %q", parsed.Scheme, u)
	}
	return filepath.Clean(filepath.FromSlash(parsed.Path)), nil
}
