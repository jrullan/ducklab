// Package cli implements the ducklab CLI client.
// It imports engineclt and daemon only.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jrullan/ducklab/internal/daemon"
	"github.com/jrullan/ducklab/internal/engineclt"
)

// Version is the CLI version. cmd/ducklab injects it from internal/build.
//
// The CLI may not import a domain package (AC-16), and that includes reaching
// for the version constant directly: the rule is about what the client can
// reach, not about how harmless this particular symbol is.
var Version = "dev"
var Provenance = "dev@unknown"

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
			fmt.Printf("ducklab %s (%s, go1.24+, %s/%s)\n", Version, Provenance, "linux", "amd64")
			return 0
		case "--help", "-h", "help":
			printHelp()
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
		case "task", "trace":
			verb = remaining[1]
			cmdArgs = remaining[2:]
		case "provider", "duckling", "roster", "budget":
			if remaining[1] == "list" || remaining[1] == "add" || remaining[1] == "probe" || remaining[1] == "test" || remaining[1] == "remove" || remaining[1] == "show" || remaining[1] == "set" || remaining[1] == "suggest" {
				verb = remaining[1]
				cmdArgs = remaining[2:]
			} else {
				verb = remaining[1]
				cmdArgs = remaining[2:]
			}
		case "mcp":
			verb = remaining[1]
			cmdArgs = remaining[2:]
		case "run":
			if runVerbs[remaining[1]] {
				verb = remaining[1]
				cmdArgs = remaining[2:]
			} else if taskIDRe.MatchString(remaining[1]) {
				// A task ID: ducklab run T-001
				verb = ""
				cmdArgs = remaining[1:]
			} else {
				// Anything else used to be treated as a task ID, so a
				// mistyped or newly added subcommand started a model run
				// instead of reporting itself. A typo should not cost tokens.
				fmt.Fprintf(os.Stderr, "unknown run command: %s\n", remaining[1])
				fmt.Fprintf(os.Stderr, "  a task ID looks like T-001; subcommands are: %s\n", runVerbList())
				return 2
			}
		default:
			verb = remaining[1]
			cmdArgs = remaining[2:]
		}
	}

	// Commands that must work in a locked room. The discovery gate below
	// used to run first, so `ducklab engine start` — the very command the
	// bare hint recommends, whose implementation was correct all along —
	// answered "engine not running", and `help` did not exist at all: a
	// first-run user's only escape was a second terminal nobody documented
	// (B-111).
	switch noun {
	case "help":
		printHelp()
		return 0
	case "engine":
		return engineCmd(verb, cmdArgs)
	}

	// MCP stdio serving manages its own engine connection and must not trigger
	// the ordinary CLI discovery path (which would hide its exit contract).
	if noun == "mcp" {
		return mcpCmd(verb)
	}

	if noun == "proof" {
		return proofCmd(verb, cmdArgs, repo)
	}

	// Discover or auto-start engine
	client, err := discoverEngine(noAutostart)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 9
	}
	_ = client

	switch noun {
	case "mcp":
		return mcpCmd(verb)
	case "project":
		return projectCmd(verb, cmdArgs, repo)
	case "duckling":
		return ducklingCmd(verb, cmdArgs)
	case "run":
		return runCmd(verb, cmdArgs, repo)
	case "bench":
		return benchCmd(remaining[1:], repo)
	case "report":
		return reportCmd(append([]string{verb}, cmdArgs...), repo)
	case "provider":
		return providerCmd(verb, cmdArgs)
	case "budget":
		return budgetCmd(verb, cmdArgs)
	case "roster":
		return rosterCmd(verb, cmdArgs, repo)
	case "intake", "spec", "plan":
		return stageCmd(noun, remaining[1:], repo)
	case "test":
		return testFirstCmd(remaining[1:], repo)
	case "review":
		return reviewCmd(remaining[1:], repo)
	case "release":
		return releaseCmd(verb, cmdArgs, repo)
	case "skill":
		return skillCmd(verb, cmdArgs, repo)
	case "bug":
		return bugCmd(verb, cmdArgs, repo)
	case "task":
		return taskCmd(verb, cmdArgs, repo)
	case "trace":
		return traceCmd(verb, cmdArgs, repo)
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
	// Auto-start, for real — this was a confessed placeholder from v0.1
	// until the first stranger install locked a fresh user out of every
	// command (B-111). Announced on stderr: an engine appearing out of
	// nowhere without a word is how mystery processes are born.
	path, err := exec.LookPath("ducklab-engine")
	if err != nil {
		return nil, fmt.Errorf("engine not running and ducklab-engine is not on PATH; run `make install` or start the desktop app")
	}
	if err := daemon.StartEngine(path); err != nil {
		return nil, fmt.Errorf("engine not running and auto-start failed: %w", err)
	}
	started, err := daemon.WaitReady(15 * time.Second)
	if err != nil {
		return nil, fmt.Errorf("engine auto-started but did not become ready: %w", err)
	}
	fmt.Fprintf(os.Stderr, "engine auto-started: pid %d, port %d\n", started.PID, started.Port)
	return engineclt.New(started), nil
}

// printHelp is the engineless map: every noun, one line each. Anything
// deeper asks the engine, and the engine may not exist yet — which is
// exactly when a person needs help the most.
func printHelp() {
	fmt.Print(`ducklab — multi-LLM development harness

usage: ducklab [--repo <path>] [--no-autostart] <command> [args]

  engine    start | stop | restart | status | log
  project   init | list | show | describe | set | status
  intake    draft requirements (--from <brief>, --ref <doc|dir>, --adopt)
  spec      draft the spec from approved requirements
  plan      draft milestones and tasks from the spec
  run       <task-id> | list | show | diff | accept | reject | answer
  test      write the failing test first (test-first flow)
  review    read an accepted task's commit
  proof     verify <receipt>
  release   plan | cut | list
  bug       file | list | show | triage | promote
  task      list | show | remove
  skill     list | show | new | run | validate
  provider  set | list | remove        duckling  set | list | probe | test
  roster    show | set | suggest       budget    show | set
  trace     check requirement→spec→task coverage
  mcp       serve (stdio MCP server exposing the loop)

The first command auto-starts the engine if none is running
(--no-autostart to refuse). Full walkthrough: README.md — a cycle end
to end. Every command prints its own usage when called wrong.
`)
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
		if info, err := daemon.ReadEngineJSON(); err == nil && daemon.IsEngineRunning(info) {
			fmt.Printf("engine already running: pid %d, port %d\n", info.PID, info.Port)
			return 0
		}
		path, err := exec.LookPath("ducklab-engine")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: ducklab-engine not on PATH")
			return 1
		}
		if err := daemon.StartEngine(path); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		info, err := daemon.WaitReady(15 * time.Second)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("engine started: pid %d, port %d, version %s\n", info.PID, info.Port, info.Version)
		return 0
	case "stop", "restart":
		force := len(args) > 0 && args[0] == "--force"
		info, err := daemon.ReadEngineJSON()
		running := err == nil && daemon.IsEngineRunning(info)
		if running {
			client := engineclt.New(info)
			// Running or queued work dies mid-call on a restart; paused runs
			// survive by design (I9) and do not block.
			if !force {
				if active, aErr := client.ActiveRuns(); aErr == nil && len(active) > 0 {
					fmt.Fprintf(os.Stderr, "error: %d run(s) still going (%s) — wait, abort them, or --force\n",
						len(active), strings.Join(active, ", "))
					return 1
				}
			}
			// The replacement engine inherits THIS shell's environment, and
			// the keys live nowhere else (I10). A restart from a shell without
			// them silently produces an engine whose hosted models all fail —
			// measured, by exactly that mistake.
			if verb == "restart" && !force {
				if envs, kErr := client.ProviderKeyEnvs(); kErr == nil {
					if missing := missingEnvs(envs); len(missing) > 0 {
						fmt.Fprintf(os.Stderr, "error: %s not set in this shell — the restarted engine "+
							"would lose the key(s). Export them first, or --force to proceed without\n",
							strings.Join(missing, ", "))
						return 1
					}
				}
			}
			// A forced restart with active runs must checkpoint them as an
			// attributed restart request, not as an unattributed engine_shutdown:
			// the record must say who asked, and each checkpoint carries a
			// recovery deadline so a restart that never completes un-parks its
			// runs instead of stranding them (B-046). Best-effort: an engine that
			// cannot take the request is still stopped below.
			if verb == "restart" {
				_ = client.RequestRestart("cli")
			}
			if sErr := client.Shutdown(); sErr == nil {
				if wErr := daemon.WaitGone(15 * time.Second); wErr != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", wErr)
					return 1
				}
			}
			fmt.Println("engine stopped")
		} else if verb == "stop" {
			fmt.Println("engine not running")
		}
		if verb == "stop" {
			return 0
		}
		return engineCmd("start", nil)
	default:
		fmt.Fprintf(os.Stderr, "unknown engine command: %s\n", verb)
		return 2
	}
}

func ducklingCmd(verb string, args []string) int {
	switch verb {
	case "set", "remove":
		info, err := daemon.ReadEngineJSON()
		if err != nil {
			fmt.Fprintln(os.Stderr, "engine not running")
			return 9
		}
		client := engineclt.New(info)
		if verb == "remove" {
			if len(args) < 1 {
				fmt.Fprintln(os.Stderr, "usage: ducklab duckling remove <id>")
				return 2
			}
			if err := client.DucklingRemove(args[0]); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
			fmt.Printf("duckling %s removed\n", args[0])
			return 0
		}
		return ducklingSetCmd(client, args)
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
	case "probe":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab duckling probe <id>")
			return 2
		}
		info, err := daemon.ReadEngineJSON()
		if err != nil {
			fmt.Fprintln(os.Stderr, "engine not running")
			return 9
		}
		caps, err := engineclt.New(info).DucklingProbe(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 8
		}
		dialect := "text protocol (dialect B)"
		if b, _ := caps["native_tools"].(bool); b {
			dialect = "native tool calling (dialect A)"
		}
		fmt.Printf("%s\n", args[0])
		fmt.Printf("  tools:   %s\n", dialect)
		fmt.Printf("  json:    %v\n", caps["json_mode"])
		fmt.Printf("  context: %.0f tokens\n", num(caps["context_tokens"]))
		if at := str(caps["probed_at"]); at != "" {
			fmt.Printf("  probed:  %s\n", at)
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

// rosterCmd shows or changes which duckling plays which role.
func rosterCmd(verb string, args []string, repo string) int {
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

	switch verb {
	case "", "show":
		view, err := client.RosterGet(projectID, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Print(renderRoster(view))
		return 0

	case "set":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: ducklab roster set <role> <duckling-id>")
			return 2
		}
		view, err := client.RosterSet(projectID, args[0], args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("%s -> %s\n\n", args[0], args[1])
		fmt.Print(renderRoster(view))
		return 0

	case "suggest":
		apply := false
		for _, a := range args {
			if a == "--apply" {
				apply = true
			}
		}
		if apply {
			view, err := client.RosterApply(projectID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
			fmt.Print(renderRoster(view))
			return 0
		}
		sugg, err := client.RosterSuggest(projectID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("%-13s %-16s %8s %7s  %s\n", "ROLE", "DUCKLING", "PASS", "RUNS", "EVIDENCE")
		for _, sg := range sugg {
			fmt.Printf("%-13s %-16s %7.0f%% %7.0f  %s\n",
				str(sg["role"]), str(sg["duckling"]),
				num(sg["pass_rate"]), num(sg["runs"]), str(sg["evidence"]))
		}
		fmt.Println("\nranked from recorded runs only — no model was consulted.")
		fmt.Println("apply with: ducklab roster suggest --apply")
		return 0

	default:
		fmt.Fprintf(os.Stderr, "unknown roster command: %s\n", verb)
		return 2
	}
}

func renderRoster(view map[string]interface{}) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-13s %-16s %s\n", "ROLE", "DUCKLING", "SOURCE")
	entries, _ := view["entries"].([]interface{})
	for _, raw := range entries {
		e, _ := raw.(map[string]interface{})
		fmt.Fprintf(&b, "%-13s %-16s %s\n", str(e["role"]), str(e["duckling"]), str(e["source"]))
	}
	if w := str(view["warning"]); w != "" {
		fmt.Fprintf(&b, "\nwarning: %s\n", w)
	}
	return b.String()
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
	// Printed as the engine rendered it. See handleReport.
	if rendered := str(rep["rendered"]); rendered != "" {
		fmt.Print(rendered)
		return 0
	}
	fmt.Fprintln(os.Stderr, "error: the engine returned no report")
	return 1
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
	// Walk up looking for the registered root, the way git finds .git. A user
	// deep in src/ has not left the project, and telling them so is a worse
	// answer than finding it.
	byPath := map[string]string{}
	for _, p := range projects {
		path, _ := p["path"].(string)
		if id, ok := p["id"].(string); ok && path != "" {
			byPath[path] = id
		}
	}
	for dir := abs; ; {
		if id, ok := byPath[dir]; ok {
			return id, 0
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	fmt.Fprintf(os.Stderr, "error: no ducklab project at %s or any parent\n", abs)
	return "", 2
}

// runVerbs are the subcommands of `ducklab run`. Anything else in that
// position must look like a task ID.
var runVerbs = map[string]bool{
	"list": true, "show": true, "watch": true, "diff": true, "accept": true,
	"reject": true, "answer": true, "abort": true, "gc": true, "resume": true,
}

// taskIDRe is the shape of an ID on the traceability spine (02 §3).
var taskIDRe = regexp.MustCompile(`^[A-Za-z]+-\d+$`)

func runVerbList() string {
	names := make([]string, 0, len(runVerbs))
	for v := range runVerbs {
		names = append(names, v)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func runCmd(verb string, args []string, repo string) int {
	if verb == "" {
		// ducklab run <task-id>
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab run <task-id> [--mode solo|pair|tournament|split] [--ducklings a,b] [--rounds n] [--max-tokens N] [--dry-run] [--yes] [--no-wait]")
			return 2
		}
		taskID := args[0]
		mode := "solo"
		dryRun := false
		yes := false
		noWait := false
		noStream := false
		var ducklings []string
		rounds := 0
		maxTokens := 0
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
			case "--max-tokens":
				// This run's ceiling only. The default lives in the engine
				// config; `ducklab budget` shows and changes it.
				if i+1 < len(args) {
					fmt.Sscanf(args[i+1], "%d", &maxTokens)
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
			case "--no-stream":
				// The documented opt-out (07 §7.2). It also makes streaming
				// falsifiable: without a control run there is no way to tell a
				// model's own formatting mistake from one the stream introduced.
				noStream = true
			}
		}
		return runStart(taskID, mode, dryRun, yes, noWait, noStream, repo, ducklings, rounds, maxTokens)
	}
	switch verb {
	case "list":
		// Scoped to the project you are standing in.
		//
		// It listed every run the engine knew, from every project, so `run
		// list` in one repo showed another's work. I was misled by it twice
		// while building this: once reading a stage's result off the wrong
		// project, once wiring a monitor to the first row. --all is there for
		// when the whole picture is what you want.
		all := false
		for _, a := range args {
			if a == "--all" {
				all = true
			}
		}
		client, projectID, code := project(repo)
		if code != 0 {
			return code
		}
		if all {
			projectID = ""
		}
		runs, err := client.RunList(projectID)
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
		// A run that says only FAILED sends you to events.jsonl. Some of these
		// messages are written to be acted on.
		if why := str(run["failure"]); why != "" {
			fmt.Printf("  failure: %s\n", why)
		}
		return 0
	case "diff":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab run diff <run-id> [--tests]")
			return 2
		}
		info, err := daemon.ReadEngineJSON()
		if err != nil {
			fmt.Fprintln(os.Stderr, "engine not running")
			return 9
		}
		diff, tests, warning, err := engineclt.New(info).RunDiff(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		// --tests narrows to the hunks the guard pulled out, which is what the
		// warning tells a reader to look at.
		if len(args) > 1 && args[1] == "--tests" {
			if tests == "" {
				fmt.Println("this run changed no test files")
				return 0
			}
			fmt.Print(tests)
			return 0
		}
		// Unflagged, the whole diff. Flagged, the test hunks come first,
		// because being read before the decision is the entire point.
		if tests != "" {
			fmt.Printf("⚠ %s\n\n", warning)
			fmt.Print(tests)
			fmt.Println()
		}
		fmt.Print(diff)
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
			fmt.Fprintln(os.Stderr, "usage: ducklab run reject <run-id> [--reason <text>] | --landed <commit-sha> [--reason <text>]")
			return 2
		}
		reason, landedSHA := "", ""
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--reason":
				if i+1 < len(args) {
					reason = args[i+1]
					i++
				}
			case "--landed":
				if i+1 < len(args) {
					landedSHA = args[i+1]
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
		if landedSHA != "" {
			if err := client.RunLand(args[0], landedSHA, reason); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
			fmt.Printf("landed: commit %s\n", landedSHA)
			return 0
		}
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
	case "answer":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab run answer <run-id> --answer <text> [--question <id>]")
			return 2
		}
		runID := args[0]
		var answer, question string
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--answer":
				if i+1 < len(args) {
					answer = args[i+1]
					i++
				}
			case "--question":
				if i+1 < len(args) {
					question = args[i+1]
					i++
				}
			}
		}
		if answer == "" {
			fmt.Fprintln(os.Stderr, "error: --answer is required")
			return 2
		}
		info, err := daemon.ReadEngineJSON()
		if err != nil {
			fmt.Fprintln(os.Stderr, "engine not running")
			return 9
		}
		client := engineclt.New(info)
		if err := client.RunAnswer(runID, question, answer); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("answered; run %s resumed\n", runID)
		return followRun(client, runID)
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

// asStrings reads a JSON array of strings out of an event payload.
func asStrings(v interface{}) []string {
	items, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func runStart(taskID, mode string, dryRun, yes, noWait, noStream bool, repo string, ducklings []string, rounds, maxTokens int) int {
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
	if noStream {
		req["no_stream"] = true
	}
	if len(ducklings) > 0 {
		req["ducklings"] = ducklings
	}
	if rounds > 0 {
		req["rounds"] = rounds
	}
	// Only the limit named: the engine fills every other one from the defaults,
	// and a zero would be a ceiling of zero.
	if maxTokens > 0 {
		req["budget"] = map[string]interface{}{"max_tokens": maxTokens}
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
		case "round_gate":
			fmt.Printf("  round %v: %v\n", e.Data["round"], e.Data["result"])
		case "verdict":
			verdict, _ = e.Data["verdict"].(string)
			fmt.Printf("  verdict: %s\n", verdict)
		case "skill_problems":
			fmt.Println("  ⚠ a skill this run wrote will not load:")
			for _, p := range asStrings(e.Data["problems"]) {
				fmt.Printf("      %s\n", p)
			}
		case "tests_modified":
			// Printed before the gate line, because a green verdict on a run
			// that rewrote its own tests is the one that most needs reading
			// (05 §5.3). Not a blocker; the accept command is unchanged.
			fmt.Printf("  ⚠ %v\n", e.Data["message"])
			for _, f := range asStrings(e.Data["files"]) {
				fmt.Printf("      %s\n", f)
			}
			fmt.Printf("      ducklab run diff %s --tests\n", runID)
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
func num(v interface{}) float64 {
	f, _ := v.(float64)
	return f
}

func str(v interface{}) string {
	s, _ := v.(string)
	return s
}

// missingEnvs returns the names not present in this process's environment.
func missingEnvs(names []string) []string {
	var missing []string
	for _, n := range names {
		if os.Getenv(n) == "" {
			missing = append(missing, n)
		}
	}
	return missing
}
