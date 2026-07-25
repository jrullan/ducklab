package main

import (
	"fmt"
	"os"

	"github.com/jrullan/ducklab/internal/cli"
)

var version = "0.1.0"

func main() {
	cli.Version = version
	os.Exit(cli.Run(os.Args[1:]))
}

func init() {
	fmt.Fprintln(os.Stderr, "ducklab CLI")
}
