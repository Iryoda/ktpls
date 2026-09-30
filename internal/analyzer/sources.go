package analyzer

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SourceFile returns a local file with the content of a declaration's
// source: the project file itself, or the sources jar entry extracted
// (once) to ktpls's cache, for the editor to open.
func SourceFile(src *Source) (string, error) {
	if src.Jar == "" {
		return src.Path, nil
	}
	if strings.Contains(src.Entry, "..") {
		return "", fmt.Errorf("bad entry %q", src.Entry)
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(src.Jar))
	jarName := strings.TrimSuffix(filepath.Base(src.Jar), ".jar")
	dest := filepath.Join(base, "ktpls", "sources", jarName+"-"+hex.EncodeToString(sum[:4]), filepath.FromSlash(src.Entry))
	if _, err := os.Stat(dest); err == nil {
		return dest, nil
	}
	z, err := zip.OpenReader(src.Jar)
	if err != nil {
		return "", err
	}
	defer z.Close()
	f, err := z.Open(src.Entry)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".extract-*")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, f); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	tmp.Close()
	os.Chmod(tmp.Name(), 0o444) // library sources: read only
	return dest, os.Rename(tmp.Name(), dest)
}
