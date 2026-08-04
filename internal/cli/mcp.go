package cli

import (
	"fmt"
	"os"

	"github.com/jrullan/ducklab/internal/daemon"
	"github.com/jrullan/ducklab/internal/engineclt"
	"github.com/jrullan/ducklab/internal/mcp"
)

// mcpCmd serves the operator surface over stdio.
//
// `ducklab mcp serve` is what an MCP client configures as the command: it
// connects the model on stdin/stdout to the engine on loopback. Logs go to
// stderr — stdout belongs to the protocol.
func mcpCmd(verb string) int {
	switch verb {
	case "serve":
		info, err := daemon.ReadEngineJSON()
		if err != nil || !daemon.IsEngineRunning(info) {
			fmt.Fprintln(os.Stderr, "engine not running; start it with `ducklab engine start`")
			return 9
		}
		if err := mcp.NewServer(engineclt.New(info)).Serve(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "mcp: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(os.Stderr, "usage: ducklab mcp serve\n")
		return 2
	}
}
