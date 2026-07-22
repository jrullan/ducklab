// Command ducklab is a light, cross-platform terminal harness for developing
// with multiple local LLMs — the runtime of the Rubber Duck system.
package main

import (
	"os"

	"github.com/jrullan/ducklab/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
