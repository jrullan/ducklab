package main

import (
	"os"

	"github.com/jrullan/ducklab/internal/build"
	"github.com/jrullan/ducklab/internal/cli"
)

func main() {
	cli.Version = build.Version
	cli.Provenance = build.Provenance()
	os.Exit(cli.Run(os.Args[1:]))
}
