// Command kt-vibe-lsp is a Kotlin language server.
package main

import (
	"os"

	"github.com/Iryoda/kt-vibe-lsp/internal/cmd"
)

func main() {
	os.Exit(cmd.Main(os.Args[1:]))
}
