package cache

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// maxFileSize bounds the size of workspace files we parse, to skip
// generated or vendored monsters.
const maxFileSize = 2 << 20

// skipDirs are directory names never descended into: build outputs, which
// may hold copies of the sources (Eclipse/Buildship, used by VS Code's Java
// tooling, copies .kt files into bin/), and dependency trees.
var skipDirs = map[string]bool{
	"build":        true, // Gradle
	"out":          true, // IntelliJ
	"bin":          true, // Eclipse/Buildship
	"target":       true, // Maven
	"node_modules": true,
}

// IsKotlinFile reports whether path names a Kotlin source or script file.
func IsKotlinFile(path string) bool {
	ext := filepath.Ext(path)
	return ext == ".kt" || ext == ".kts"
}

// LoadWorkspace walks the workspace root and indexes every Kotlin file not
// already known (open buffers win over disk), parsing in parallel. It
// returns the number of files loaded.
func (s *Session) LoadWorkspace(ctx context.Context) (int, error) {
	if s.root == "" {
		return 0, nil
	}
	paths, err := s.walk(ctx)
	if err != nil {
		return 0, err
	}

	work := make(chan string)
	results := make(chan *File, 64)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for path := range work {
				content, err := os.ReadFile(path)
				if err != nil {
					s.log.Debug("reading file", "path", path, "err", err)
					continue
				}
				results <- newFile(path, 0, false, content, s.enc)
			}
		})
	}
	go func() {
		defer close(work)
		for _, p := range paths {
			select {
			case work <- p:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	// Insert in batches to hold the write lock briefly.
	loaded := 0
	batch := make([]*File, 0, 64)
	flush := func() {
		s.mu.Lock()
		for _, f := range batch {
			if _, ok := s.files[f.Path]; ok {
				f.close() // opened in the editor meanwhile
				continue
			}
			s.putLocked(f)
			loaded++
		}
		s.mu.Unlock()
		batch = batch[:0]
	}
	for f := range results {
		if batch = append(batch, f); len(batch) == cap(batch) {
			flush()
		}
	}
	flush()
	return loaded, ctx.Err()
}

// walk returns the Kotlin files under the root that aren't known yet.
func (s *Session) walk(ctx context.Context) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			s.log.Debug("walk error", "path", path, "err", err)
			return nil // keep going
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			name := d.Name()
			if path != s.root && (strings.HasPrefix(name, ".") || skipDirs[name]) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !IsKotlinFile(path) {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > maxFileSize {
			return nil
		}
		if !s.known(path) {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

func (s *Session) known(path string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.files[path]
	return ok
}
