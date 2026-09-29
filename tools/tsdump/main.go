package main

import (
	"fmt"
	"os"
	"strings"

	kotlin "github.com/fwcd/tree-sitter-kotlin/bindings/go"
	ts "github.com/tree-sitter/go-tree-sitter"
)

func dump(n *ts.Node, src []byte, depth int) {
	field := ""
	txt := ""
	if n.ChildCount() == 0 {
		txt = " " + strings.ReplaceAll(string(src[n.StartByte():n.EndByte()]), "\n", "\\n")
	}
	if n.IsNamed() || n.ChildCount() == 0 {
		fmt.Printf("%s%s%s%s\n", strings.Repeat("  ", depth), field, n.Kind(), txt)
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if f := n.FieldNameForChild(uint32(i)); f != "" {
			fmt.Printf("%s[%s]\n", strings.Repeat("  ", depth+1), f)
		}
		dump(c, src, depth+1)
	}
}

func main() {
	src, _ := os.ReadFile(os.Args[1])
	p := ts.NewParser()
	defer p.Close()
	p.SetLanguage(ts.NewLanguage(kotlin.Language()))
	t := p.Parse(src, nil)
	defer t.Close()
	dump(t.RootNode(), src, 0)
}
