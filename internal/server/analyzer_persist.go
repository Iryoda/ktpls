package server

import (
	"bytes"
	"encoding/json"
	"os"
	"time"

	"github.com/Iryoda/ktpls/internal/analyzer"
	"github.com/Iryoda/ktpls/internal/cache"
)

// The diagnostics of the files as on disk are kept across runs, so that a
// start shows the last run's diagnostics of unchanged files at once,
// while the analyzer starts and catches up.

const persistName = "diagnostics.json"

type persistedFile struct {
	Size        int64                 `json:"size"`
	ModTime     int64                 `json:"mtime"`
	Diagnostics []analyzer.Diagnostic `json:"diagnostics"`
}

// recordDisk remembers the diagnostics of a file as on disk, for the next
// run.
func (s *Server) recordDisk(path string, ds []analyzer.Diagnostic) {
	s.az.mu.Lock()
	defer s.az.mu.Unlock()
	if s.az.disk == nil {
		s.az.disk = map[string][]analyzer.Diagnostic{}
	}
	if len(ds) == 0 {
		delete(s.az.disk, path)
	} else {
		s.az.disk[path] = ds
	}
}

// saveDiagnostics writes the diagnostics of the files as on disk to the
// project's cache.
func (s *Server) saveDiagnostics() {
	out := map[string]persistedFile{}
	s.az.mu.Lock()
	for p, ds := range s.az.disk {
		out[p] = persistedFile{Diagnostics: ds}
	}
	s.az.mu.Unlock()
	for p, f := range out {
		info, err := os.Stat(p)
		if err != nil {
			delete(out, p)
			continue
		}
		f.Size, f.ModTime = info.Size(), info.ModTime().UnixNano()
		out[p] = f
	}
	data, err := json.Marshal(out)
	if err != nil {
		return
	}
	path, err := analyzer.CacheFile(s.root, persistName)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err == nil {
		os.Rename(tmp, path)
	}
}

// restoreDiagnostics publishes the last run's diagnostics of the files
// unchanged since.
func (s *Server) restoreDiagnostics() {
	start := time.Now()
	path, err := analyzer.CacheFile(s.root, persistName)
	if err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var files map[string]persistedFile
	if json.Unmarshal(data, &files) != nil {
		return
	}
	n := 0
	for p, f := range files {
		info, err := os.Stat(p)
		if err != nil || info.Size() != f.Size || info.ModTime().UnixNano() != f.ModTime {
			continue
		}
		content, err := os.ReadFile(p)
		if err != nil || s.editedSince(p, content) {
			continue
		}
		s.recordDisk(p, f.Diagnostics)
		s.setAnalyzerDiagnostics(p, s.toProtocol(content, f.Diagnostics))
		n++
	}
	s.log.Info("analyzer: restored the last run's diagnostics", "files", n, "elapsed", time.Since(start).Round(time.Millisecond))
}

// editedSince reports whether path is open with other content than disk.
func (s *Server) editedSince(path string, disk []byte) bool {
	edited := false
	s.session.Read(func(sn *cache.Snapshot) {
		if f := sn.File(path); f != nil && f.Overlay {
			edited = !bytes.Equal(f.Content, disk)
		}
	})
	return edited
}

// clearAnalyzerDiagnostics withdraws all the analyzer's diagnostics.
func (s *Server) clearAnalyzerDiagnostics() {
	s.az.mu.Lock()
	var paths []string
	for p := range s.az.diags {
		paths = append(paths, p)
	}
	s.az.diags, s.az.disk = nil, nil
	s.az.mu.Unlock()
	for _, p := range paths {
		s.publishDiagnostics(p)
	}
}
