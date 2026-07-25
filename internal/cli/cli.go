// Package cli implements the ducklab CLI client.
// It imports engineclt and daemon only.
package cli

import (
	"fmt"
	"os"

	"github.com/jrullan/ducklab/internal/daemon"
	"github.com/jrullan/ducklab/internal/engineclt"
)

// Version is the CLI version.
var Version = "0.1.0"

// Run runs the CLI.
func Run(args []string) int {
	if len(args) == 0 {
		return statusSummary()
	}

	// Parse global flags
	var repo string
	var noAutostart bool
	var i int
	for i = 0; i < len(args); i++ {
		switch args[i] {
		case "--repo":
			if i+1 < len(args) {
				repo = args[i+1]
				i++
			}
		case "--output":
			if i+1 < len(args) {
				i++ // consume but ignore for now
			}
		case "--no-autostart":
			noAutostart = true
		case "--version":
			fmt.Printf("ducklab %s (%s, go1.24+, %s/%s)\n", Version, "dev", "linux", "amd64")
			return 0
		default:
			break
		}
		break
	}

	remaining := args[i:]
	if len(remaining) == 0 {
		return statusSummary()
	}

	noun := remaining[0]
	var verb string
	if len(remaining) > 1 {
		verb = remaining[1]
	}

	// Discover or auto-start engine
	client, err := discoverEngine(noAutostart)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 9
	}
	_ = client

	switch noun {
	case "engine":
		return engineCmd(verb, remaining[2:])
	case "project":
		return projectCmd(verb, remaining[2:], repo)
	case "duckling":
		return ducklingCmd(verb, remaining[2:])
	case "run":
		return runCmd(verb, remaining[2:], repo)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", noun)
		return 2
	}
}

func discoverEngine(noAutostart bool) (*engineclt.Client, error) {
	info, err := daemon.ReadEngineJSON()
	if err == nil && daemon.IsEngineRunning(info) {
		return engineclt.New(info), nil
	}
	if noAutostart {
		return nil, fmt.Errorf("engine not running and auto-start disabled")
	}
	// Auto-start: spawn engine
	// For v0.1, this is a placeholder
	return nil, fmt.Errorf("engine not running; start with: ducklab-engine")
}

func statusSummary() int {
	info, err := daemon.ReadEngineJSON()
	if err != nil {
		fmt.Println("ducklab: no engine running")
		fmt.Println("hint: start with 'ducklab engine start' or 'ducklab-engine'")
		return 0
	}
	if daemon.IsEngineRunning(info) {
		fmt.Printf("ducklab: engine running (pid %d, port %d)\n", info.PID, info.Port)
	} else {
		fmt.Println("ducklab: engine not running (stale engine.json)")
	}
	fmt.Println("hint: see 'ducklab --help'")
	return 0
}

func engineCmd(verb string, args []string) int {
	switch verb {
	case "status":
		info, err := daemon.ReadEngineJSON()
		if err != nil {
			fmt.Println("engine not running")
			return 0
		}
		if daemon.IsEngineRunning(info) {
			fmt.Printf("engine running: pid %d, port %d, version %s\n", info.PID, info.Port, info.Version)
		} else {
			fmt.Println("engine not running (stale engine.json)")
		}
		return 0
	case "start":
		fmt.Println("engine auto-start not yet implemented; run ducklab-engine directly")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown engine command: %s\n", verb)
		return 2
	}
}

func projectCmd(verb string, args []string, repo string) int {
	switch verb {
	case "init":
		fmt.Println("project init: use the engine API directly for now")
		return 0
	case "list":
		fmt.Println("project list: use the engine API directly for now")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown project command: %s\n", verb)
		return 2
	}
}

func ducklingCmd(verb string, args []string) int {
	switch verb {
	case "list":
		fmt.Println("duckling list: use the engine API directly for now")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown duckling command: %s\n", verb)
		return 2
	}
}

func runCmd(verb string, args []string, repo string) int {
	switch verb {
	case "list":
		fmt.Println("run list: use the engine API directly for now")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown run command: %s\n", verb)
		return 2
	}
}
