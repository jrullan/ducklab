package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/daemon"
	"github.com/jrullan/ducklab/internal/engineclt"
)

// projectCmd implements `ducklab project ...` (03 §3.2).
//
// These printed "use the engine API directly for now" until v0.4, which meant
// there was no supported way to register a project from the CLI at all — the
// documented entry point to the whole tool was a stub.
func projectCmd(verb string, args []string, repo string) int {
	client, err := connect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 9
	}

	switch verb {
	case "init":
		return projectInit(client, args, repo)
	case "list":
		return projectListCmd(client)
	case "", "show":
		return projectShow(client, repo)
	case "describe":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab project describe <text>")
			return 2
		}
		return projectSetKeys(client, repo, map[string]string{"describe": strings.Join(args, " ")})
	case "set":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: ducklab project set <key> <value>\nkeys: %s\n",
				strings.Join(config.Keys(), ", "))
			return 2
		}
		return projectSetKeys(client, repo, map[string]string{args[0]: strings.Join(args[1:], " ")})
	case "status":
		return projectStatusCmd(client, repo)
	default:
		fmt.Fprintf(os.Stderr, "unknown project command: %s\n", verb)
		fmt.Fprintln(os.Stderr, "usage: ducklab project init|list|show|describe|set|status")
		return 2
	}
}

func connect() (*engineclt.Client, error) {
	info, err := daemon.ReadEngineJSON()
	if err != nil || !daemon.IsEngineRunning(info) {
		return nil, fmt.Errorf("engine not running; start with: ducklab-engine")
	}
	c := engineclt.New(info)
	c.Version = Version
	return c, nil
}

func projectInit(client *engineclt.Client, args []string, repo string) int {
	name, describe, gitInit := "", "", false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 < len(args) {
				name = args[i+1]
				i++
			}
		case "--describe":
			if i+1 < len(args) {
				describe = args[i+1]
				i++
			}
		case "--git-init", "--yes":
			gitInit = true
		default:
			fmt.Fprintf(os.Stderr, "error: unknown argument %q\n", args[i])
			return 2
		}
	}
	path := repo
	if path == "" {
		path = "."
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	if name == "" {
		name = filepath.Base(abs)
	}

	p, err := client.ProjectInit(abs, name, describe, gitInit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	// init is idempotent (03 §3.2): on an existing project it prints and exits
	// 0 rather than complaining.
	fmt.Printf("%s  %s\n", str(p["id"]), str(p["path"]))
	if gate := str(p["gate"]); gate != "" {
		fmt.Printf("  gate: %s\n", gate)
	}
	return 0
}

func projectListCmd(client *engineclt.Client) int {
	projects, err := client.ProjectList()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if len(projects) == 0 {
		fmt.Println("no projects registered")
		fmt.Println("hint: run 'ducklab project init' inside a repo")
		return 0
	}
	fmt.Printf("%-24s %s\n", "ID", "PATH")
	for _, p := range projects {
		missing := ""
		if b, _ := p["missing"].(bool); b {
			missing = "  (missing)"
		}
		fmt.Printf("%-24s %s%s\n", str(p["id"]), str(p["path"]), missing)
	}
	return 0
}

func projectShow(client *engineclt.Client, repo string) int {
	id, code := resolveProjectID(client, repo)
	if code != 0 {
		return code
	}
	p, err := client.ProjectGet(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("%s\n", str(p["id"]))
	fmt.Printf("  path:     %s\n", str(p["path"]))
	fmt.Printf("  name:     %s\n", str(p["name"]))
	if cfg, ok := p["config"].(map[string]interface{}); ok {
		if d := str(cfg["describe"]); d != "" {
			fmt.Printf("  describe: %s\n", d)
		}
	}
	if g := str(p["gate"]); g != "" {
		fmt.Printf("  gate:     %s\n", g)
	}
	if a := str(p["autonomy"]); a != "" {
		fmt.Printf("  autonomy: %s\n", a)
	}
	return 0
}

func projectSetKeys(client *engineclt.Client, repo string, keys map[string]string) int {
	id, code := resolveProjectID(client, repo)
	if code != 0 {
		return code
	}
	if _, err := client.ProjectUpdate(id, keys); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	for _, k := range sortedStringKeys(keys) {
		fmt.Printf("%s = %s\n", k, keys[k])
	}
	return 0
}

func projectStatusCmd(client *engineclt.Client, repo string) int {
	id, code := resolveProjectID(client, repo)
	if code != 0 {
		return code
	}
	st, err := client.ProjectStatus(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("%s\n", id)

	// Stages in lifecycle order, not map order: the cycle is a sequence and
	// showing plan before intake would misrepresent it.
	if sp, ok := st["stage_progress"].(map[string]interface{}); ok && len(sp) > 0 {
		fmt.Println("  stages:")
		for _, stage := range []string{"intake", "spec", "plan"} {
			if v := str(sp[stage]); v != "" {
				fmt.Printf("    %-8s %s\n", stage, v)
			}
		}
	}
	if tc, ok := st["task_counts"].(map[string]interface{}); ok && len(tc) > 0 {
		fmt.Println("  tasks:")
		for _, k := range sortedAnyKeys(tc) {
			fmt.Printf("    %-8s %v\n", k, tc[k])
		}
	}
	fmt.Printf("  active runs: %v\n", num(st["active_runs"]))
	fmt.Printf("  spent today: $%.4f\n", num(st["budget_spent_today"]))
	return 0
}

func sortedStringKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedAnyKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
