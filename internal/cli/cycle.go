package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/jrullan/ducklab/internal/daemon"
	"github.com/jrullan/ducklab/internal/engineclt"
)

// stageCmd runs intake, spec or plan and follows the council to its gate.
func stageCmd(stage string, args []string, repo string) int {
	from, yes := "", false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--from":
			if i+1 < len(args) {
				from = args[i+1]
				i++
			}
		case "--yes":
			yes = true
		}
	}

	client, projectID, code := project(repo)
	if code != 0 {
		return code
	}

	req := map[string]interface{}{}
	if from != "" {
		req["from"] = from
	}
	if yes {
		req["autonomy"] = "auto"
	}

	run, err := client.StageStart(projectID, stage, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	runID, _ := run["id"].(string)
	fmt.Printf("%s started (run %s)\n", stage, runID)

	code = followRun(client, runID)

	// A stage's output is a proposal, so show what it wants to change and how
	// to accept it. Ending on "paused" with no next step would leave the user
	// holding a decision they cannot see.
	kind := artifactFor(stage)
	got, err := client.ArtifactGet(projectID, kind)
	if err != nil {
		return code
	}
	proposal, ok := got["proposal"].(map[string]interface{})
	if !ok {
		return code
	}
	fmt.Printf("\n%s\n", strings.TrimRight(str(proposal["diff"]), "\n"))
	if yes {
		return promoteArtifact(client, projectID, kind)
	}
	fmt.Printf("\naccept with:  ducklab %s accept\nreject with: leave it; the draft stays at .ducklab/docs/%s.md.proposed\n", stage, kind)
	return code
}

func promoteArtifact(client *engineclt.Client, projectID, kind string) int {
	got, err := client.ArtifactPromote(projectID, kind, "human")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("accepted; %s.md updated\n", kind)
	if errs, ok := got["trace_errors"].([]interface{}); ok && len(errs) > 0 {
		fmt.Printf("\n%d traceability gap(s) after this change:\n", len(errs))
		for _, raw := range errs {
			e, _ := raw.(map[string]interface{})
			fmt.Printf("  %s %s: %s\n", str(e["kind"]), str(e["id"]), str(e["detail"]))
		}
		fmt.Println("\nrun 'ducklab trace check' to see them again.")
	}
	return 0
}

func artifactFor(stage string) string {
	switch stage {
	case "intake":
		return "requirements"
	case "spec":
		return "spec"
	case "plan":
		return "plan"
	}
	return stage
}

// taskCmd lists and inspects the plan's tasks.
func taskCmd(verb string, args []string, repo string) int {
	client, projectID, code := project(repo)
	if code != 0 {
		return code
	}

	switch verb {
	case "", "list":
		tasks, err := client.TaskList(projectID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if len(tasks) == 0 {
			fmt.Println("no tasks yet — run 'ducklab plan' to create them")
			return 0
		}
		filter := ""
		for i := 0; i < len(args); i++ {
			if args[i] == "--status" && i+1 < len(args) {
				filter = args[i+1]
			}
		}
		fmt.Printf("%-8s %-12s %-10s %-14s %s\n", "ID", "STATUS", "MILESTONE", "IMPLEMENTS", "TITLE")
		for _, t := range tasks {
			if filter != "" && str(t["status"]) != filter {
				continue
			}
			fmt.Printf("%-8s %-12s %-10s %-14s %s\n",
				str(t["id"]), str(t["status"]), str(t["milestone"]),
				joinAny(t["implements"]), str(t["title"]))
		}
		return 0

	case "next":
		task, err := client.TaskNext(projectID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if len(task) == 0 || str(task["id"]) == "" {
			fmt.Println("nothing ready: every task is done, running, or waiting on a dependency")
			return 0
		}
		fmt.Printf("%s — %s\n", str(task["id"]), str(task["title"]))
		if imp := joinAny(task["implements"]); imp != "" {
			fmt.Printf("  implements %s\n", imp)
		}
		fmt.Printf("\nrun it with: ducklab run %s --mode pair\n", str(task["id"]))
		return 0

	case "show":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab task show <id>")
			return 2
		}
		tasks, err := client.TaskList(projectID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		want := strings.ToUpper(args[0])
		for _, t := range tasks {
			if strings.EqualFold(str(t["id"]), want) {
				fmt.Printf("%s — %s\n", str(t["id"]), str(t["title"]))
				fmt.Printf("  status:     %s\n", str(t["status"]))
				fmt.Printf("  milestone:  %s\n", str(t["milestone"]))
				fmt.Printf("  implements: %s\n", joinAny(t["implements"]))
				if dep := joinAny(t["depends_on"]); dep != "" {
					fmt.Printf("  depends on: %s\n", dep)
				}
				if body := str(t["body"]); body != "" {
					fmt.Printf("\n%s\n", body)
				}
				return 0
			}
		}
		fmt.Fprintf(os.Stderr, "error: no task %s in the plan\n", want)
		return 2

	default:
		fmt.Fprintf(os.Stderr, "unknown task command: %s\n", verb)
		return 2
	}
}

// traceCmd walks the spine.
func traceCmd(verb string, args []string, repo string) int {
	client, projectID, code := project(repo)
	if code != 0 {
		return code
	}

	switch verb {
	case "", "check":
		errs, err := client.TraceCheck(projectID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if len(errs) == 0 {
			fmt.Println("the cycle is linked end to end.")
			return 0
		}
		fmt.Printf("%d break(s) in the traceability spine:\n\n", len(errs))
		for _, e := range errs {
			fmt.Printf("  %-22s %-10s %s", str(e["kind"]), str(e["id"]), str(e["detail"]))
			if m := str(e["missing"]); m != "" {
				fmt.Printf(" (%s)", m)
			}
			fmt.Println()
		}
		// Non-zero so a script or CI can act on it.
		return 1

	case "show":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab trace show <id>")
			return 2
		}
		node, err := client.TraceShow(projectID, strings.ToUpper(args[0]))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 2
		}
		fmt.Printf("%s (%s) — %s\n", str(node["id"]), str(node["kind"]), str(node["title"]))
		if up := joinAny(node["up"]); up != "" {
			fmt.Printf("  implements:    %s\n", up)
		}
		if down := joinAny(node["down"]); down != "" {
			fmt.Printf("  implemented by: %s\n", down)
		}
		return 0

	default:
		fmt.Fprintf(os.Stderr, "unknown trace command: %s\n", verb)
		return 2
	}
}

// project resolves the engine client and this repo's project id.
func project(repo string) (*engineclt.Client, string, int) {
	info, err := daemon.ReadEngineJSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, "engine not running")
		return nil, "", 9
	}
	client := engineclt.New(info)
	id, code := resolveProjectID(client, repo)
	return client, id, code
}

func joinAny(v interface{}) string {
	list, ok := v.([]interface{})
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(list))
	for _, item := range list {
		parts = append(parts, fmt.Sprint(item))
	}
	return strings.Join(parts, ", ")
}
