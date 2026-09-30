package cache

import (
	"bufio"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
	ignore := &gitignore{}
	ignore.load(s.root, "")
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
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || skipDirs[name] || ignore.match(rel, true) {
				return filepath.SkipDir
			}
			ignore.load(path, rel) // its .gitignore applies below it
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

// A gitignore holds the rules of the .gitignore files seen so far in a
// walk, outermost first. Each file's rules apply to its directory and
// below; within the applicable rules, the last match wins, so `!pattern`
// re-includes. (As in git, a file can't be re-included once its
// directory is excluded: the walk never enters that directory.)
type gitignore struct {
	rules []gitRule
}

type gitRule struct {
	base     string // directory of the .gitignore, relative to the root ("" for the root)
	re       *regexp.Regexp
	negate   bool
	dirOnly  bool
	basename bool // no slash in the pattern: it matches the last path element
}

// load reads dir/.gitignore, whose directory is rel (slash-separated,
// relative to the root).
func (g *gitignore) load(dir, rel string) {
	path := filepath.Join(dir, ".gitignore")
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	g.rules = append(g.rules, parsedGitignore(path, stampOf(info), rel)...)
}

// gitignoreCache holds compiled .gitignore files across workspace scans,
// keyed by path and invalidated by stamp.
var gitignoreCache sync.Map // path -> cachedGitignore

type cachedGitignore struct {
	stamp stamp
	rules []gitRule
}

func parsedGitignore(path string, st stamp, rel string) []gitRule {
	if c, ok := gitignoreCache.Load(path); ok && c.(cachedGitignore).stamp == st {
		return c.(cachedGitignore).rules
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var rules []gitRule
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if r, ok := parseGitRule(sc.Text()); ok {
			r.base = rel
			rules = append(rules, r)
		}
	}
	if sc.Err() != nil {
		return rules // read in part: not cached, so it is read again
	}
	gitignoreCache.Store(path, cachedGitignore{st, rules})
	return rules
}

// parseGitRule compiles one .gitignore line.
func parseGitRule(line string) (gitRule, bool) {
	line = strings.TrimRight(line, " \t\r")
	if line == "" || strings.HasPrefix(line, "#") {
		return gitRule{}, false
	}
	var r gitRule
	if strings.HasPrefix(line, "!") {
		r.negate, line = true, line[1:]
	} else if strings.HasPrefix(line, "\\") {
		line = line[1:] // escaped leading ! or #
	}
	if before, ok := strings.CutSuffix(line, "/"); ok {
		r.dirOnly, line = true, before
	}
	// A slash anywhere but the end anchors the pattern to the
	// .gitignore's directory; otherwise it matches at any depth.
	anchored := strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")
	if line == "" {
		return gitRule{}, false
	}
	r.basename = !anchored && !strings.Contains(line, "**")
	var b strings.Builder
	b.WriteString("^")
	if !anchored && !r.basename {
		b.WriteString("(?:.*/)?")
	}
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case strings.HasPrefix(line[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(line[i:], "/**") && i+3 == len(line):
			b.WriteString("/.*")
			i += 2
		case strings.HasPrefix(line[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '[':
			if j := strings.IndexByte(line[i:], ']'); j > 0 {
				b.WriteString(line[i : i+j+1])
				i += j
			} else {
				b.WriteString("\\[")
			}
		case c == '\\' && i+1 < len(line):
			i++
			b.WriteString(regexp.QuoteMeta(line[i : i+1]))
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return gitRule{}, false
	}
	r.re = re
	return r, true
}

// match reports whether rel (slash-separated, relative to the root) is
// ignored.
func (g *gitignore) match(rel string, isDir bool) bool {
	ignored := false
	for _, r := range g.rules {
		sub := rel
		if r.base != "" {
			if !strings.HasPrefix(rel, r.base+"/") {
				continue
			}
			sub = rel[len(r.base)+1:]
		}
		if r.dirOnly && !isDir {
			continue
		}
		if r.basename {
			sub = sub[strings.LastIndexByte(sub, '/')+1:]
		}
		if r.re.MatchString(sub) {
			ignored = !r.negate
		}
	}
	return ignored
}
