package cache

import (
	"bufio"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
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

// A stamp identifies the on-disk version of a file.
type stamp struct {
	mod  time.Time
	size int64
}

func stampOf(info fs.FileInfo) stamp { return stamp{info.ModTime(), info.Size()} }

// LoadWorkspace indexes every Kotlin file in the workspace. It returns the
// number of files loaded.
func (s *Session) LoadWorkspace(ctx context.Context) (int, error) {
	return s.Rescan(ctx)
}

// Rescan brings the index up to date with the disk: it parses workspace
// files that are new or changed since they were read (by modification
// time and size) and forgets deleted ones. Open buffers are left alone:
// they are the source of truth while open. It returns the number of files
// added, changed or removed.
func (s *Session) Rescan(ctx context.Context) (int, error) {
	if s.root == "" {
		return 0, nil
	}
	s.scanMu.Lock() // one scan at a time
	defer s.scanMu.Unlock()

	onDisk, err := s.walk(ctx)
	if err != nil {
		return 0, err
	}
	var load []string
	var gone []string
	s.mu.RLock()
	for path, st := range onDisk {
		if f, ok := s.files[path]; !ok || (!f.Overlay && f.stamp != st) {
			load = append(load, path)
		}
	}
	for path, f := range s.files {
		if _, ok := onDisk[path]; !ok && !f.Overlay && s.inWorkspace(path) {
			gone = append(gone, path)
		}
	}
	s.mu.RUnlock()

	changed := s.parseAll(ctx, load)
	if len(gone) > 0 {
		s.mu.Lock()
		for _, path := range gone {
			if f, ok := s.files[path]; ok && !f.Overlay {
				f.close()
				delete(s.files, path)
				s.index.Remove(path)
				changed++
			}
		}
		s.mu.Unlock()
	}
	return changed, ctx.Err()
}

// parseAll reads and parses paths in parallel and stores the results,
// except where an open buffer took over meanwhile. It returns the number
// of files stored.
func (s *Session) parseAll(ctx context.Context, paths []string) int {
	work := make(chan string)
	results := make(chan *File, 64)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for path := range work {
				if f := s.readFile(path); f != nil {
					results <- f
				}
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
	stored := 0
	batch := make([]*File, 0, 64)
	flush := func() {
		s.mu.Lock()
		for _, f := range batch {
			if old, ok := s.files[f.Path]; ok && old.Overlay {
				f.close() // opened in the editor meanwhile
				continue
			}
			s.putLocked(f)
			stored++
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
	return stored
}

// readFile reads and parses a workspace file from disk, or returns nil.
func (s *Session) readFile(path string) *File {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		s.log.Debug("reading file", "path", path, "err", err)
		return nil
	}
	f := newFile(path, 0, false, content, s.enc)
	f.stamp = stampOf(info)
	return f
}

// walk returns the stamps of the Kotlin files under the root.
func (s *Session) walk(ctx context.Context) (map[string]stamp, error) {
	ignore := loadGitignore(s.root)
	files := map[string]stamp{}
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			s.log.Debug("walk error", "path", path, "err", err)
			return nil // keep going
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if path == s.root {
			return nil
		}
		rel, _ := filepath.Rel(s.root, path)
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || skipDirs[name] || ignore.match(rel, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !IsKotlinFile(path) || ignore.match(rel, false) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxFileSize {
			return nil
		}
		files[path] = stampOf(info)
		return nil
	})
	return files, err
}

// A gitignore holds the patterns of the workspace root's .gitignore. It
// supports the common subset: comments, name globs (`*.gen.kt`), rooted
// patterns (`/generated`), directory-only patterns (`tmp/`) and leading
// `**/`. Negations (`!pattern`) and nested .gitignore files are ignored.
type gitignore struct {
	patterns []gitPattern
}

type gitPattern struct {
	glob    string
	rooted  bool // contains a slash: matched against the whole relative path
	dirOnly bool
}

func loadGitignore(root string) *gitignore {
	g := &gitignore{}
	f, err := os.Open(filepath.Join(root, ".gitignore"))
	if err != nil {
		return g
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		p := gitPattern{}
		if strings.HasSuffix(line, "/") {
			p.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		line = strings.TrimPrefix(line, "**/")
		if strings.Contains(line, "/") {
			p.rooted = true
			line = strings.TrimPrefix(line, "/")
		}
		if line != "" {
			p.glob = line
			g.patterns = append(g.patterns, p)
		}
	}
	return g
}

// match reports whether the path rel (relative to the root, with the OS
// separator) is ignored.
func (g *gitignore) match(rel string, isDir bool) bool {
	rel = filepath.ToSlash(rel)
	base := rel[strings.LastIndexByte(rel, '/')+1:]
	for _, p := range g.patterns {
		if p.dirOnly && !isDir {
			continue
		}
		target := base
		if p.rooted {
			target = rel
		}
		if ok, _ := filepath.Match(p.glob, target); ok {
			return true
		}
	}
	return false
}
