package cache

import (
	"fmt"
	"os"
	"runtime"
	"sync"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/Iryoda/ktpls/internal/kotlin"
	"github.com/Iryoda/ktpls/internal/protocol"
)

// A File is one version of a Kotlin source file known to the session.
// Files are immutable; a change produces a new File, and the previous one
// is closed.
//
// Open editor buffers (overlays) keep their content and syntax tree, since
// requests are made against them. Files read from disk keep only their
// extracted Summary: keeping a tree for every workspace file costs hundreds
// of megabytes on large repositories.
type File struct {
	Path    string
	URI     protocol.DocumentURI
	Version int32 // editor version; 0 for files read from disk
	Overlay bool  // content comes from an open editor buffer

	Content []byte           // overlays only
	Tree    *ts.Tree         // overlays only
	Mapper  *protocol.Mapper // overlays only

	Summary      *kotlin.FileSummary
	SyntaxErrors bool

	stamp stamp // disk files: the version read
}

func newFile(path string, version int32, overlay bool, content []byte, enc protocol.PositionEncodingKind) *File {
	return newFileFrom(nil, path, version, overlay, content, enc)
}

// newFileFrom is newFile reusing the syntax tree of prev (the previous
// version of an open buffer, or nil) to reparse incrementally.
func newFileFrom(prev *File, path string, version int32, overlay bool, content []byte, enc protocol.PositionEncodingKind) *File {
	var tree *ts.Tree
	if prev != nil && prev.Tree != nil {
		tree = kotlin.Reparse(prev.Tree, prev.Content, content)
	} else {
		tree = kotlin.Parse(content)
	}
	m := protocol.NewMapper(content, enc)
	f := &File{
		Path:         path,
		URI:          protocol.URIFromPath(path),
		Version:      version,
		Overlay:      overlay,
		Summary:      kotlin.Extract(path, content, tree, m),
		SyntaxErrors: tree.RootNode().HasError(),
	}
	if overlay {
		f.Content, f.Tree, f.Mapper = content, tree, m
	} else {
		tree.Close()
	}
	return f
}

// close releases the file's syntax tree.
func (f *File) close() {
	if f.Tree != nil {
		f.Tree.Close()
		f.Tree = nil
	}
}

// Parse returns the parsed contents of the file at path: the open buffer
// if there is one, else the file on disk, parsed now. The caller must call
// release when done with the result.
func (sn *Snapshot) Parse(path string) (pf *kotlin.ParsedFile, release func(), err error) {
	if f := sn.files[path]; f != nil && f.Tree != nil {
		return &kotlin.ParsedFile{
			Path: f.Path, URI: f.URI, Content: f.Content, Tree: f.Tree, Mapper: f.Mapper, Summary: f.Summary,
		}, func() {}, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}
	tree := kotlin.Parse(content)
	m := protocol.NewMapper(content, sn.enc)
	pf = &kotlin.ParsedFile{
		Path: path, URI: protocol.URIFromPath(path), Content: content, Tree: tree, Mapper: m,
		Summary: kotlin.Extract(path, content, tree, m),
	}
	return pf, tree.Close, nil
}

// FilesContaining calls fn, concurrently, with every known file whose
// content contains name as a whole word, parsed. Open buffers are used as
// they are; disk files are read and parsed for the duration of the call.
func (sn *Snapshot) FilesContaining(name string, fn func(*kotlin.ParsedFile)) {
	work := make(chan *File)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for f := range work {
				if f.Tree != nil {
					if kotlin.ContainsWord(f.Content, name) {
						fn(&kotlin.ParsedFile{Path: f.Path, URI: f.URI, Content: f.Content, Tree: f.Tree, Mapper: f.Mapper, Summary: f.Summary})
					}
					continue
				}
				content, err := os.ReadFile(f.Path)
				if err != nil || !kotlin.ContainsWord(content, name) {
					continue
				}
				tree := kotlin.Parse(content)
				m := protocol.NewMapper(content, sn.enc)
				fn(&kotlin.ParsedFile{
					Path: f.Path, URI: f.URI, Content: content, Tree: tree, Mapper: m,
					Summary: kotlin.Extract(f.Path, content, tree, m),
				})
				tree.Close()
			}
		})
	}
	for _, f := range sn.files {
		work <- f
	}
	close(work)
	wg.Wait()
}
