package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jrullan/ducklab/internal/daemon"
	"github.com/jrullan/ducklab/internal/engineclt"
)

// benchCmd is `ducklab bench` (03 §3.10).
//
// It takes no project: every cell is a throwaway project the engine creates
// and forgets. That is the point — a bench measures the models, not whatever
// happens to be in the repo you are standing in.
func benchCmd(args []string, repo string) int {
	suite := "std"
	var ducklings, modes []string
	keep := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--suite":
			if i+1 < len(args) {
				suite = args[i+1]
				i++
			}
		case "--ducklings":
			if i+1 < len(args) {
				ducklings = strings.Split(args[i+1], ",")
				i++
			}
		case "--modes":
			if i+1 < len(args) {
				modes = strings.Split(args[i+1], ",")
				i++
			}
		case "--keep":
			keep = true
		default:
			fmt.Fprintf(os.Stderr, "unknown flag: %s\n", args[i])
			fmt.Fprintln(os.Stderr, "usage: ducklab bench [--suite std] [--ducklings a,b] [--modes solo,pair] [--keep]")
			return 2
		}
	}
	if len(ducklings) == 0 {
		fmt.Fprintln(os.Stderr, "error: bench needs --ducklings a,b")
		return 2
	}

	info, err := daemon.ReadEngineJSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, "engine not running")
		return 9
	}

	client := engineclt.New(info)
	fmt.Fprintf(os.Stderr, "running suite %s: %d duckling(s) x %d mode(s) x every task — this takes a while\n",
		suite, len(ducklings), max(1, len(modes)))

	// Follow the engine's progress while it works. Without this the terminal
	// is silent for hours and a slow model looks exactly like a wedged one.
	stop := followBench(client)
	rendered, path, err := client.Bench(suite, ducklings, modes, keep)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Print(rendered)
	if path != "" {
		fmt.Printf("\nwritten to %s\n", path)
	}
	return 0
}

// followBench prints a line per cell as the engine finishes it.
//
// Best effort: if the event stream cannot be opened the bench still runs, just
// quietly. Losing progress output is not a reason to lose the bench.
func followBench(client *engineclt.Client) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = client.StreamEvents(ctx, func(e engineclt.SSEEvent) bool {
			switch e.Type {
			case "bench_cell_start":
				fmt.Fprintf(os.Stderr, "  [%v/%v] %v %v/%v …\n",
					e.Data["n"], e.Data["total"], e.Data["task"], e.Data["duckling"], e.Data["mode"])
			case "bench_cell_end":
				outcome := fmt.Sprintf("%v", e.Data["verdict"])
				if errMsg, _ := e.Data["error"].(string); errMsg != "" {
					outcome = "could not run: " + errMsg
				}
				fmt.Fprintf(os.Stderr, "  [%v/%v] %v %v\n",
					e.Data["n"], e.Data["total"], e.Data["task"], outcome)
			}
			return true
		})
	}()
	return func() { cancel(); <-done }
}
