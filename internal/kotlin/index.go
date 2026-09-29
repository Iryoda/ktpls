package kotlin

import "slices"

// An Index holds the symbols of every workspace file, for lookup by fully
// qualified name, simple name, and container. It is not safe for
// concurrent use; the cache guards it with the session lock.
type Index struct {
	files   map[string]*FileSummary
	byFQ    map[string][]*Symbol // overloads share an FQName
	byName  map[string][]*Symbol
	members map[string][]*Symbol // container FQName -> members
}

// NewIndex returns an empty index.
func NewIndex() *Index {
	return &Index{
		files:   make(map[string]*FileSummary),
		byFQ:    make(map[string][]*Symbol),
		byName:  make(map[string][]*Symbol),
		members: make(map[string][]*Symbol),
	}
}

// Update replaces the symbols of sum.Path with those of sum.
func (ix *Index) Update(sum *FileSummary) {
	ix.Remove(sum.Path)
	ix.files[sum.Path] = sum
	for _, s := range sum.Symbols {
		ix.byFQ[s.FQName] = append(ix.byFQ[s.FQName], s)
		ix.byName[s.Name] = append(ix.byName[s.Name], s)
		if s.Container != "" {
			ix.members[s.Container] = append(ix.members[s.Container], s)
		}
	}
}

// Remove forgets the symbols of the file at path.
func (ix *Index) Remove(path string) {
	old, ok := ix.files[path]
	if !ok {
		return
	}
	delete(ix.files, path)
	for _, s := range old.Symbols {
		removeFrom(ix.byFQ, s.FQName, s)
		removeFrom(ix.byName, s.Name, s)
		if s.Container != "" {
			removeFrom(ix.members, s.Container, s)
		}
	}
}

func removeFrom(m map[string][]*Symbol, key string, s *Symbol) {
	list := slices.DeleteFunc(m[key], func(x *Symbol) bool { return x == s })
	if len(list) == 0 {
		delete(m, key)
	} else {
		m[key] = list
	}
}

// File returns the summary of the file at path, or nil.
func (ix *Index) File(path string) *FileSummary { return ix.files[path] }

// ByFQName returns the symbols with the given fully qualified name.
func (ix *Index) ByFQName(fq string) []*Symbol { return ix.byFQ[fq] }

// ByName returns the symbols with the given simple name.
func (ix *Index) ByName(name string) []*Symbol { return ix.byName[name] }

// Members returns the direct members of the container with the given FQName.
func (ix *Index) Members(container string) []*Symbol { return ix.members[container] }

// NumFiles returns the number of indexed files.
func (ix *Index) NumFiles() int { return len(ix.files) }
