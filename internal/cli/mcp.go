package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/jrullan/ducklab/internal/engineclt"
	"github.com/jrullan/ducklab/internal/mcp"
)

// mcpCmd serves the operator surface over stdio.
//
// `ducklab mcp serve` is what an MCP client configures as the command: it
// connects the model on stdin/stdout to the engine on loopback. Logs go to
// stderr — stdout belongs to the protocol.
func mcpCmd(verb string, noAutostart bool) int {
	return mcpCmdWith(verb, noAutostart, os.Stdin, os.Stdout, discoverEngine)
}

func mcpCmdWith(verb string, noAutostart bool, in io.Reader, out io.Writer, discover func(bool) (*engineclt.Client, error)) int {
	switch verb {
	case "serve":
		client, err := discover(noAutostart)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mcp: %v\n", err)
			return 9
		}
		if err := mcp.NewServer(client).Serve(in, out); err != nil {
			fmt.Fprintf(os.Stderr, "mcp: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(os.Stderr, "usage: ducklab mcp serve\n")
		return 2
	}
}
