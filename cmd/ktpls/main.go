// Command ktpls is a Kotlin language server.
package main

import (
	"os"

	"github.com/Iryoda/ktpls/internal/cmd"
)

func main() {
	os.Exit(cmd.Main(os.Args[1:]))
}
