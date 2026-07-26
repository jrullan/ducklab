// Package cli implements the ducklab CLI client.
// It imports engineclt and daemon only.
package cli

import (
	"fmt"
	"os"
	"path/filepath"

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

	remaining := os.Args[1:]
	if len(remaining) == 0 {
		return statusSummary()
	}

	noun := remaining[0]
	var verb string
	var cmdArgs []string
	if len(remaining) > 1 {
		// Check if the first arg is a known verb for this noun
		switch noun {
		case "engine":
			if remaining[1] == "status" || remaining[1] == "start" || remaining[1] == "stop" || remaining[1] == "restart" || remaining[1] == "log" {
				verb = remaining[1]
				cmdArgs = remaining[2:]
			} else {
				verb = remaining[1]
				cmdArgs = remaining[2:]
			}
		case "project":
			if remaining[1] == "init" || remaining[1] == "list" || remaining[1] == "show" || remaining[1] == "describe" || remaining[1] == "set" || remaining[1] == "status" {
				verb = remaining[1]
				cmdArgs = remaining[2:]
			} else {
				verb = remaining[1]
				cmdArgs = remaining[2:]
			}
		case "provider", "duckling", "roster":
			if remaining[1] == "list" || remaining[1] == "add" || remaining[1] == "probe" || remaining[1] == "test" || remaining[1] == "remove" || remaining[1] == "show" || remaining[1] == "set" || remaining[1] == "suggest" {
				verb = remaining[1]
				cmdArgs = remaining[2:]
			} else {
				verb = remaining[1]
				cmdArgs = remaining[2:]
			}
		case "run":
			if remaining[1] == "list" || remaining[1] == "show" || remaining[1] == "watch" || remaining[1] == "accept" || remaining[1] == "reject" || remaining[1] == "answer" || remaining[1] == "abort" || remaining[1] == "gc" || remaining[1] == "resume" {
				verb = remaining[1]
				cmdArgs = remaining[2:]
			} else {
				// First arg is a task ID
				verb = ""
				cmdArgs = remaining[1:]
			}
		default:
			verb = remaining[1]
			cmdArgs = remaining[2:]
		}
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
		return engineCmd(verb, cmdArgs)
	case "project":
		return projectCmd(verb, cmdArgs, repo)
	case "duckling":
		return ducklingCmd(verb, cmdArgs)
	case "run":
		return runCmd(verb, cmdArgs, repo)
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
		info, err := daemon.ReadEngineJSON()
		if err != nil {
			fmt.Fprintln(os.Stderr, "engine not running")
			return 9
		}
		client := engineclt.New(info)
		ducklings, err := client.DucklingList()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("%-20s %-15s %-30s %s\n", "ID", "PROVIDER", "MODEL", "ROLES")
		for _, d := range ducklings {
			fmt.Printf("%-20s %-15s %-30s %v\n", d["id"], d["provider"], d["model"], d["roles"])
		}
		return 0
	case "test":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab duckling test <id> [--prompt <s>]")
			return 2
		}
		id := args[0]
		prompt := "say OK"
		for i := 1; i < len(args); i++ {
			if args[i] == "--prompt" && i+1 < len(args) {
				prompt = args[i+1]
				i++
			}
		}
		info, err := daemon.ReadEngineJSON()
		if err != nil {
			fmt.Fprintln(os.Stderr, "engine not running")
			return 9
		}
		client := engineclt.New(info)
		result, err := client.DucklingTest(id, prompt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("%s\n", result["text"])
		fmt.Printf("tokens: %v in / %v out · $%v\n", result["tokens_in"], result["tokens_out"], result["cost_usd"])
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown duckling command: %s\n", verb)
		return 2
	}
}

func runCmd(verb string, args []string, repo string) int {
	if verb == "" {
		// ducklab run <task-id>
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab run <task-id> [--mode solo] [--dry-run] [--yes]")
			return 2
		}
		taskID := args[0]
		mode := "solo"
		dryRun := false
		yes := false
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--mode":
				if i+1 < len(args) {
					mode = args[i+1]
					i++
				}
			case "--dry-run":
				dryRun = true
			case "--yes":
				yes = true
			}
		}
		return runStart(taskID, mode, dryRun, yes, repo)
	}
	switch verb {
	case "list":
		info, err := daemon.ReadEngineJSON()
		if err != nil {
			fmt.Fprintln(os.Stderr, "engine not running")
			return 9
		}
		client := engineclt.New(info)
		runs, err := client.RunList("")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("%-30s %-10s %-10s %-10s %s\n", "ID", "STAGE", "MODE", "STATUS", "TASK")
		for _, r := range runs {
			fmt.Printf("%-30s %-10s %-10s %-10s %s\n", r["id"], r["stage"], r["mode"], r["status"], r["task_id"])
		}
		return 0
	case "show":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab run show <run-id>")
			return 2
		}
		info, err := daemon.ReadEngineJSON()
		if err != nil {
			fmt.Fprintln(os.Stderr, "engine not running")
			return 9
		}
		client := engineclt.New(info)
		run, err := client.RunGet(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("run %s: %s (task %s)\n", run["id"], run["status"], run["task_id"])
		fmt.Printf("  verdict: %s\n", run["verdict"])
		return 0
	case "accept":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab run accept <run-id> [--message <msg>]")
			return 2
		}
		msg := ""
		for i := 1; i < len(args); i++ {
			if args[i] == "--message" && i+1 < len(args) {
				msg = args[i+1]
				i++
			}
		}
		info, err := daemon.ReadEngineJSON()
		if err != nil {
			fmt.Fprintln(os.Stderr, "engine not running")
			return 9
		}
		client := engineclt.New(info)
		result, err := client.RunAccept(args[0], msg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("accepted: commit %s\n", result["commit_sha"])
		return 0
	case "reject":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab run reject <run-id> [--reason <text>]")
			return 2
		}
		reason := ""
		for i := 1; i < len(args); i++ {
			if args[i] == "--reason" && i+1 < len(args) {
				reason = args[i+1]
				i++
			}
		}
		info, err := daemon.ReadEngineJSON()
		if err != nil {
			fmt.Fprintln(os.Stderr, "engine not running")
			return 9
		}
		client := engineclt.New(info)
		if err := client.RunReject(args[0], reason); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println("rejected")
		return 0
	case "abort":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab run abort <run-id>")
			return 2
		}
		info, err := daemon.ReadEngineJSON()
		if err != nil {
			fmt.Fprintln(os.Stderr, "engine not running")
			return 9
		}
		client := engineclt.New(info)
		if err := client.RunAbort(args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println("aborted")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown run command: %s\n", verb)
		return 2
	}
}

func runStart(taskID, mode string, dryRun, yes bool, repo string) int {
	if repo == "" {
		repo = "."
	}
	absRepo, err := filepath.Abs(repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	info, err := daemon.ReadEngineJSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, "engine not running")
		return 9
	}
	client := engineclt.New(info)
	// Find project by path
	projects, err := client.ProjectList()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	var projectID string
	for _, p := range projects {
		if p["path"] == absRepo {
			projectID = p["id"].(string)
			break
		}
	}
	if projectID == "" {
		fmt.Fprintf(os.Stderr, "error: no ducklab project at %s\n", absRepo)
		return 2
	}
	req := map[string]interface{}{
		"task_id": taskID,
		"mode":    mode,
		"dry_run": dryRun,
	}
	if yes {
		req["autonomy"] = "yolo"
	}
	run, err := client.RunStart(projectID, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("run %s started (status: %s)\n", run["id"], run["status"])
	if dryRun {
		fmt.Println("(dry run — no model calls made)")
	}
	return 0
}
