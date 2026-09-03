package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/jrullan/ducklab/internal/daemon"
	"github.com/jrullan/ducklab/internal/engineclt"
)

func intentCmd(verb, repo string) int {
	if verb != "" && verb != "show" {
		fmt.Fprintln(os.Stderr, "usage: ducklab intent [show]")
		return 2
	}
	client, projectID, code := project(repo)
	if code != 0 {
		return code
	}
	got, err := client.ArtifactGet(projectID, "intent")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Print(str(got["markdown"]))
	return 0
}

// stageCmd runs intake, spec or plan and follows the council to its gate.
// A stage takes an optional subcommand acting on the proposal it left behind:
// `ducklab intake accept`. Without one it runs the stage.
func stageCmd(stage string, args []string, repo string) int {
	from, yes := "", false
	mode := ""
	rounds := 0
	adopt := false
	var refs []string
	sub := ""
	var revArgs []string
	for i := 0; i < len(args); i++ {
		// Everything after `revise` is the note, not more flags. A note is
		// prose and will contain words the parser would otherwise reject.
		if sub == "revise" {
			revArgs = append(revArgs, args[i])
			continue
		}
		switch a := args[i]; a {
		case "--from":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --from needs a file")
				return 2
			}
			from = args[i+1]
			i++
		case "--yes":
			yes = true
		case "--ref":
			// Repeatable: reference documents (files, or directories of
			// .md/.txt) loaded bounded into the stage's prompt as context —
			// the wiki an adopt should read lives where run tools cannot.
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --ref needs a file or directory")
				return 2
			}
			refs = append(refs, args[i+1])
			i++
		case "--adopt":
			// Intake only, and the engine enforces it: a survey of the tree
			// instead of an interview about an idea.
			adopt = true
		case "--mode":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --mode needs council or solo")
				return 2
			}
			mode = args[i+1]
			i++
		case "--rounds":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --rounds needs a number")
				return 2
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 1 {
				fmt.Fprintf(os.Stderr, "error: --rounds wants a number of 1 or more, got %q\n", args[i+1])
				return 2
			}
			rounds = n
			i++
		case "accept", "reject", "diff", "revise":
			sub = a
		default:
			// Never silently ignore an argument. This used to fall through,
			// so `ducklab intake accept` — which the command itself told you
			// to run — started a fresh multi-minute council instead, and the
			// proposal the user meant to accept was overwritten by its result.
			fmt.Fprintf(os.Stderr, "error: unknown argument %q\n", a)
			fmt.Fprintf(os.Stderr, "usage: ducklab %s [--from FILE] [--ref FILE|DIR]... [--adopt] [--mode council|solo] [--rounds N] [--yes]\n"+
				"       ducklab %s accept|reject|diff\n"+
				"       ducklab %s revise \"what to change\"\n", stage, stage, stage)
			return 2
		}
	}

	client, projectID, code := project(repo)
	if code != 0 {
		return code
	}

	if sub != "" {
		return proposalCmd(client, projectID, stage, sub, revArgs)
	}

	req := map[string]interface{}{}
	if from != "" {
		req["from"] = from
	}
	if len(refs) > 0 {
		req["refs"] = refs
	}
	if yes {
		req["autonomy"] = "auto"
	}
	if mode != "" {
		req["mode"] = mode
	}
	if rounds > 0 {
		req["rounds"] = rounds
	}
	if adopt {
		req["adopt"] = true
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
	if !ok || !proposalBelongsToRun(proposal, runID) {
		return code
	}
	fmt.Printf("\n%s\n", strings.TrimRight(str(proposal["diff"]), "\n"))
	if yes {
		return promoteArtifact(client, projectID, kind)
	}
	fmt.Printf("\naccept with:  ducklab %s accept\nrevise with:  ducklab %s revise \"what to change\"\nreject with:  ducklab %s reject\n", stage, stage, stage)
	return code
}

func proposalBelongsToRun(proposal map[string]interface{}, runID string) bool {
	return strings.TrimSpace(str(proposal["run_id"])) != "" && str(proposal["run_id"]) == runID
}

// proposalCmd acts on the proposal a stage already produced, without running a
// model. Accepting is a human gate (05 §1.1), not a stage.
func proposalCmd(client *engineclt.Client, projectID, stage, sub string, revArgs []string) int {
	kind := artifactFor(stage)
	got, err := client.ArtifactGet(projectID, kind)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	proposal, ok := got["proposal"].(map[string]interface{})
	if !ok {
		fmt.Fprintf(os.Stderr, "error: no pending %s proposal — run `ducklab %s` first\n", kind, stage)
		return 2
	}

	switch sub {
	case "diff":
		fmt.Printf("%s\n", strings.TrimRight(str(proposal["diff"]), "\n"))
		return 0
	case "reject":
		// The draft stays on disk. A rejected proposal is evidence about what
		// the ducklings did, and deleting it would destroy the only record.
		//
		// The run is closed, though: a proposal left undecided sits in the
		// inbox claiming to wait for an answer that was given by walking away.
		if runID := str(proposal["run_id"]); runID != "" {
			if err := client.RunReject(runID, "rejected at the stage gate"); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not close run %s: %v\n", runID, err)
			}
		}
		fmt.Printf("left in place: .ducklab/docs/%s.md.proposed\n", kind)
		fmt.Printf("re-run with:   ducklab %s\n", stage)
		fmt.Printf("or revise it:  ducklab %s revise \"what to change\"\n", stage)
		return 0

	case "revise":
		// The third answer, in the terminal as in the desktop. Accept and
		// reject are a verdict on a document that is usually almost right, and
		// re-running regenerates the parts that were fine.
		note := strings.TrimSpace(strings.Join(revArgs, " "))
		if note == "" {
			fmt.Fprintf(os.Stderr, "usage: ducklab %s revise \"what to change\"\n", stage)
			return 2
		}
		run, err := client.StageStart(projectID, stage, map[string]interface{}{
			"stage": stage, "revise": note, "stream": true,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		runID := str(run["id"])
		fmt.Printf("revising %s (run %s)\n", kind, runID)
		code := followRun(client, runID)
		fmt.Printf("\nread it:      ducklab %s diff\nthen:         ducklab %s accept\n", stage, stage)
		return code
	default:
		return promoteArtifact(client, projectID, kind)
	}
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
			// A blocked row that does not say what blocked it is a row that
			// sends you to the run log to find out.
			if why := str(t["blocked"]); why != "" {
				fmt.Printf("%-8s %s\n", "", why)
			}
		}
		return 0

	case "remove":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab task remove <id>")
			return 2
		}
		if _, err := client.TaskRemove(projectID, args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("task %s removed\n", args[0])
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
				if why := str(t["blocked"]); why != "" {
					fmt.Printf("  blocked:    %s\n", why)
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
		errs, proposed, err := client.TraceCheck(projectID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		// Which document was checked changes what the answer means.
		if len(proposed) > 0 {
			fmt.Printf("checking the proposed %s — this is what you are about to accept.\n\n",
				strings.Join(proposed, ", "))
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

	case "report":
		rendered, err := client.TraceReport(projectID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		// Markdown to stdout: `ducklab trace report > report.md` is the whole
		// export story.
		fmt.Print(rendered)
		return 0

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

// reviewCmd implements `ducklab review <task> [--mode council]` (03 §3).
func reviewCmd(args []string, repo string) int {
	taskID, mode := "", "solo"
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--mode":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --mode needs solo or council")
				return 2
			}
			mode = args[i+1]
			i++
		default:
			if strings.HasPrefix(a, "-") || taskID != "" {
				fmt.Fprintf(os.Stderr, "error: unknown argument %q\n", a)
				fmt.Fprintln(os.Stderr, "usage: ducklab review <task> [--mode solo|council]")
				return 2
			}
			taskID = a
		}
	}
	if taskID == "" {
		fmt.Fprintln(os.Stderr, "usage: ducklab review <task> [--mode solo|council]")
		return 2
	}

	client, projectID, code := project(repo)
	if code != 0 {
		return code
	}
	run, err := client.ReviewStart(projectID, taskID, mode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	runID := str(run["id"])
	fmt.Printf("review of %s started (run %s)\n", taskID, runID)
	return followRun(client, runID)
}

// releaseCmd implements `ducklab release plan|cut` (05 §9.1).
func releaseCmd(verb string, args []string, repo string) int {
	if verb == "" {
		fmt.Fprintln(os.Stderr, "usage: ducklab release plan [--bump major|minor|patch]")
		fmt.Fprintln(os.Stderr, "       ducklab release cut <version>")
		return 2
	}

	client, projectID, code := project(repo)
	if code != 0 {
		return code
	}

	switch verb {
	case "plan":
		bump := "minor"
		for i := 0; i < len(args); i++ {
			if args[i] == "--bump" && i+1 < len(args) {
				bump = args[i+1]
				i++
			}
		}
		run, err := client.ReleasePlan(projectID, bump)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		runID := str(run["id"])
		fmt.Printf("release plan started (run %s)\n", runID)
		code = followRun(client, runID)
		fmt.Println("\ncut it with:  ducklab release cut <version>")
		return code

	case "cut":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab release cut <version>")
			return 2
		}
		out, err := client.ReleaseCut(projectID, args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("released %s\n", str(out["version"]))
		fmt.Printf("  notes:  %s\n", str(out["notes"]))
		fmt.Printf("  tag:    %s\n", str(out["tag"]))
		if c := str(out["commit"]); c != "" {
			fmt.Printf("  commit: %s\n", c[:min(9, len(c))])
		}
		return 0

	default:
		fmt.Fprintf(os.Stderr, "unknown release command: %s\n", verb)
		fmt.Fprintln(os.Stderr, "usage: ducklab release plan [--bump major|minor|patch]")
		fmt.Fprintln(os.Stderr, "       ducklab release cut <version>")
		return 2
	}
}

// bugCmd implements `ducklab bug add|list|status` (05 §6).
func bugCmd(verb string, args []string, repo string) int {
	client, projectID, code := project(repo)
	if code != 0 {
		return code
	}

	switch verb {
	case "add":
		req := map[string]string{"severity": "normal"}
		var title []string
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--severity":
				if i+1 < len(args) {
					req["severity"] = args[i+1]
					i++
				}
			case "--body":
				if i+1 < len(args) {
					req["body"] = args[i+1]
					i++
				}
			case "--body-file":
				if i+1 < len(args) {
					data, err := os.ReadFile(args[i+1])
					if err != nil {
						fmt.Fprintf(os.Stderr, "error: %v\n", err)
						return 2
					}
					req["body"] = string(data)
					i++
				}
			case "--reporter":
				if i+1 < len(args) {
					req["reporter"] = args[i+1]
					i++
				}
			default:
				title = append(title, args[i])
			}
		}
		req["title"] = strings.Join(title, " ")
		b, err := client.BugAdd(projectID, req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("%s  %s\n", str(b["id"]), str(b["title"]))
		return 0

	case "", "list":
		openOnly := false
		for _, a := range args {
			if a == "--open" {
				openOnly = true
			}
		}
		bugs, err := client.BugList(projectID, openOnly)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if len(bugs) == 0 {
			fmt.Println("no bugs")
			return 0
		}
		fmt.Printf("%-8s %-10s %-12s %s\n", "ID", "SEVERITY", "STATUS", "TITLE")
		for _, b := range bugs {
			fmt.Printf("%-8s %-10s %-12s %s\n",
				str(b["id"]), str(b["severity"]), str(b["status"]), str(b["title"]))
		}
		return 0

	case "triage":
		run, err := client.BugTriage(projectID, "", nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		runID := str(run["id"])
		fmt.Printf("triage started (run %s)\n", runID)
		return followRun(client, runID)

	case "promote":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab bug promote <id>")
			return 2
		}
		out, err := client.BugPromote(projectID, args[0], "human")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 2
		}
		fmt.Printf("%s is now task %s (%s)\n", str(out["bug"]), str(out["task"]), str(out["status"]))
		fmt.Printf("\nrun it with: ducklab run %s --mode pair\n", str(out["task"]))
		return 0

	case "status":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: ducklab bug status <id> <status>")
			return 2
		}
		b, err := client.BugMove(projectID, args[0], args[1], "human")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 2
		}
		fmt.Printf("%s is now %s\n", str(b["id"]), str(b["status"]))
		return 0

	default:
		fmt.Fprintf(os.Stderr, "unknown bug command: %s\n", verb)
		fmt.Fprintln(os.Stderr, "usage: ducklab bug add <title> [--severity s] [--body-file f]")
		fmt.Fprintln(os.Stderr, "       ducklab bug list [--open]")
		fmt.Fprintln(os.Stderr, "       ducklab bug triage")
		fmt.Fprintln(os.Stderr, "       ducklab bug promote <id>")
		fmt.Fprintln(os.Stderr, "       ducklab bug status <id> <status>")
		return 2
	}
}
