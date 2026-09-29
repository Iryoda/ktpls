package kotlin

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	ts "github.com/tree-sitter/go-tree-sitter"
)

var zzm = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*\b`)
var zzs = regexp.MustCompile(`"[^"]*"`)

func TestZZSyn(t *testing.T) {
	files, bad, regions := 0, 0, 0
	missing := map[string]int{}
	shapes := map[string]int{}
	for _, root := range strings.Split(os.Getenv("ROOTS"), ":") {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && (strings.HasPrefix(d.Name(), ".") || d.Name() == "build" || d.Name() == "bin") {
				return filepath.SkipDir
			}
			if !strings.HasSuffix(p, ".kt") {
				return nil
			}
			src, _ := os.ReadFile(p)
			tree := Parse(src)
			defer tree.Close()
			files++
			if !tree.RootNode().HasError() {
				return nil
			}
			bad++
			var walk func(n *ts.Node)
			walk = func(n *ts.Node) {
				if n.IsMissing() {
					missing["MISSING "+n.Kind()]++
					return
				}
				if n.IsError() {
					regions++
					ls := strings.LastIndex(string(src[:n.StartByte()]), "\n") + 1
					le := strings.IndexByte(string(src[n.StartByte():]), '\n')
					if le < 0 {
						le = len(src) - int(n.StartByte())
					}
					line := strings.TrimSpace(string(src[ls : int(n.StartByte())+le]))
					shape := zzm.ReplaceAllString(zzs.ReplaceAllString(line, `"s"`), "x")
					if len(shape) > 70 {
						shape = shape[:70]
					}
					shapes[shape]++
					return
				}
				for i := uint(0); i < n.ChildCount(); i++ {
					walk(n.Child(i))
				}
			}
			walk(tree.RootNode())
			return nil
		})
	}
	t.Logf("files=%d withErrors=%d (%.2f%%) errorRegions=%d", files, bad, 100*float64(bad)/float64(files), regions)
	for k, v := range missing {
		t.Logf("  %4d %s", v, k)
	}
	var ks []string
	for k := range shapes {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(a, b int) bool { return shapes[ks[a]] > shapes[ks[b]] })
	for _, k := range ks[:min(15, len(ks))] {
		t.Logf("  %4d  %s", shapes[k], k)
	}
}
