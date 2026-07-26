// Package cli implements the ducklab CLI client.
// It imports engineclt and daemon only.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

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
	case "report":
		return reportCmd(append([]string{verb}, cmdArgs...), repo)
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

// reportCmd prints the solo-baseline comparison for the current project.
func reportCmd(args []string, repo string) int {
	by, since := "mode", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--by":
			if i+1 < len(args) {
				by = args[i+1]
				i++
			}
		case "--since":
			if i+1 < len(args) {
				since = args[i+1]
				i++
			}
		}
	}
	info, err := daemon.ReadEngineJSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, "engine not running")
		return 9
	}
	client := engineclt.New(info)
	projectID, code := resolveProjectID(client, repo)
	if code != 0 {
		return code
	}
	rep, err := client.Report(projectID, by, since)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Print(renderReport(rep))
	return 0
}

// resolveProjectID maps --repo to the engine's project id.
func resolveProjectID(client *engineclt.Client, repo string) (string, int) {
	if repo == "" {
		repo = "."
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return "", 2
	}
	projects, err := client.ProjectList()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return "", 1
	}
	for _, p := range projects {
		if p["path"] == abs {
			if id, ok := p["id"].(string); ok {
				return id, 0
			}
		}
	}
	fmt.Fprintf(os.Stderr, "error: no ducklab project at %s\n", abs)
	return "", 2
}

func runCmd(verb string, args []string, repo string) int {
	if verb == "" {
		// ducklab run <task-id>
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab run <task-id> [--mode solo|pair|tournament] [--ducklings a,b] [--rounds n] [--dry-run] [--yes] [--no-wait]")
			return 2
		}
		taskID := args[0]
		mode := "solo"
		dryRun := false
		yes := false
		noWait := false
		var ducklings []string
		rounds := 0
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--mode":
				if i+1 < len(args) {
					mode = args[i+1]
					i++
				}
			case "--ducklings":
				if i+1 < len(args) {
					ducklings = strings.Split(args[i+1], ",")
					i++
				}
			case "--rounds":
				if i+1 < len(args) {
					fmt.Sscanf(args[i+1], "%d", &rounds)
					i++
				}
			case "--dry-run":
				dryRun = true
			case "--yes":
				yes = true
			case "--no-wait":
				noWait = true
			case "--wait":
				noWait = false
			}
		}
		return runStart(taskID, mode, dryRun, yes, noWait, repo, ducklings, rounds)
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
	case "watch":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab run watch <run-id>")
			return 2
		}
		info, err := daemon.ReadEngineJSON()
		if err != nil {
			fmt.Fprintln(os.Stderr, "engine not running")
			return 9
		}
		// Attaching to a run started elsewhere is the same code path as
		// waiting on one you started: the engine, not the CLI, owns the run.
		return followRun(engineclt.New(info), args[0])
	case "resume":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab run resume <run-id>")
			return 2
		}
		info, err := daemon.ReadEngineJSON()
		if err != nil {
			fmt.Fprintln(os.Stderr, "engine not running")
			return 9
		}
		client := engineclt.New(info)
		run, err := client.RunResume(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("run %s resumed (status: %s)\n", run["id"], run["status"])
		return followRun(client, args[0])
	default:
		fmt.Fprintf(os.Stderr, "unknown run command: %s\n", verb)
		return 2
	}
}

func runStart(taskID, mode string, dryRun, yes, noWait bool, repo string, ducklings []string, rounds int) int {
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
	if len(ducklings) > 0 {
		req["ducklings"] = ducklings
	}
	if rounds > 0 {
		req["rounds"] = rounds
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
		return 0
	}
	runID, _ := run["id"].(string)
	if noWait {
		return 0
	}
	return followRun(client, runID)
}

// followRun renders a run's events until it reaches a terminal or paused
// state, and maps that outcome to an exit code.
//
// Ctrl-C DETACHES: the run lives in the engine, not in this process, so
// interrupting the CLI must never abort the work. Aborting is an explicit
// `ducklab run abort`.
func followRun(client *engineclt.Client, runID string) int {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	return followRunWith(context.Background(), sigCh, client, runID)
}

// followRunWith is followRun with the interrupt source injected, so the
// detach path can be tested without sending real signals to the test binary.
func followRunWith(parent context.Context, sigCh <-chan os.Signal, client *engineclt.Client, runID string) int {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	var mu sync.Mutex
	detached := false
	go func() {
		select {
		case <-sigCh:
			mu.Lock()
			detached = true
			mu.Unlock()
			cancel()
		case <-ctx.Done():
		}
	}()
	isDetached := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return detached
	}

	verdict := ""
	status := ""
	err := client.StreamRunEvents(ctx, runID, 0, func(e engineclt.SSEEvent) bool {
		switch e.Type {
		case "turn_start":
			fmt.Printf("  → turn %v (%v)\n", e.Data["turn"], e.Data["role"])
		case "tool_call":
			fmt.Printf("    · %v\n", e.Data["tool"])
		case "policy_violation":
			fmt.Printf("    ! policy: %v\n", e.Data["detail"])
		case "gate":
			fmt.Printf("  gate %v: exit %v\n", e.Data["gate"], e.Data["exit"])
		case "verdict":
			verdict, _ = e.Data["verdict"].(string)
			fmt.Printf("  verdict: %s\n", verdict)
		case "human_needed":
			status = "paused"
			fmt.Printf("  ⏸ waiting for you (%v) — ducklab run accept %s\n", e.Data["kind"], runID)
			return false
		case "checkpoint":
			if e.Data["status"] == "paused" {
				status = "paused"
				fmt.Printf("  ⏸ paused (%v) — ducklab run resume %s\n", e.Data["reason"], runID)
				return false
			}
		case "run_end":
			status = "done"
			if v, ok := e.Data["verdict"].(string); ok {
				verdict = v
			}
			return false
		}
		return true
	})

	if isDetached() {
		fmt.Printf("detached; run continues — ducklab run watch %s\n", runID)
		return 0
	}
	if err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	switch {
	case status == "paused":
		return 7
	case verdict == "FAILED":
		return 5
	case verdict == "BUDGET_EXCEEDED":
		return 6
	case verdict == "ABORTED":
		return 7
	}
	return 0
}

// renderReport formats the engine's report JSON.
//
// The CLI renders rather than importing internal/report: it may import only
// engineclt, daemon and xplat (01 §4.1), and that restriction is what keeps it
// a client instead of growing a second implementation.
func renderReport(rep map[string]interface{}) string {
	var b strings.Builder
	by, _ := rep["by"].(string)
	if by == "" {
		by = "mode"
	}
	fmt.Fprintf(&b, "%-12s %5s %7s %11s %7s %11s %8s\n",
		by, "runs", "passed", "unverified", "failed", "avg_tokens", "avg_usd")

	rows, _ := rep["rows"].([]interface{})
	for _, raw := range rows {
		r, _ := raw.(map[string]interface{})
		runs := num(r["runs"])
		avgTokens, avgCost := 0.0, 0.0
		if runs > 0 {
			avgTokens = (num(r["tokens_in"]) + num(r["tokens_out"])) / runs
			avgCost = num(r["cost_usd"]) / runs
		}
		marker := ""
		if est, _ := r["estimated"].(bool); est {
			marker = "~"
		}
		fmt.Fprintf(&b, "%-12s %5.0f %7.0f %11.0f %7.0f %10.0f%s %8.4f\n",
			str(r["key"]), runs, num(r["passed"]), num(r["unverified"]), num(r["failed"]),
			avgTokens, marker, avgCost)
	}

	if by != "mode" {
		return b.String()
	}

	b.WriteString("\n")
	var baseRow map[string]interface{}
	for _, raw := range rows {
		if r, _ := raw.(map[string]interface{}); str(r["key"]) == "solo" {
			baseRow = r
		}
	}
	if baseRow == nil {
		b.WriteString("no solo runs yet — without the baseline there is nothing to compare against.\n")
		b.WriteString("run the same task with --mode solo to establish it.\n")
		return b.String()
	}
	baseRuns := num(baseRow["runs"])
	basePass := 0.0
	if baseRuns > 0 {
		basePass = num(baseRow["passed"]) / baseRuns * 100
	}
	fmt.Fprintf(&b, "solo baseline: %.1f%% passed (n=%.0f)\n", basePass, baseRuns)

	deltas, _ := rep["deltas"].([]interface{})
	for _, raw := range deltas {
		d, _ := raw.(map[string]interface{})
		fmt.Fprintf(&b, "%-14s %.1f%% passed  (%+.1f pts, n=%.0f)\n",
			str(d["key"])+":", num(d["pass_rate"]), num(d["points_vs_baseline"]), num(d["n"]))
	}

	if res, _ := rep["resolutions"].([]interface{}); len(res) > 0 {
		b.WriteString("\nresolutions: ")
		var parts []string
		for _, raw := range res {
			r, _ := raw.(map[string]interface{})
			parts = append(parts, fmt.Sprintf("%s=%.0f", str(r["kind"]), num(r["count"])))
		}
		b.WriteString(strings.Join(parts, " ") + "\n")
	}
	return b.String()
}

func num(v interface{}) float64 {
	f, _ := v.(float64)
	return f
}

func str(v interface{}) string {
	s, _ := v.(string)
	return s
}
