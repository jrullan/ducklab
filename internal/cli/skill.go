package cli

import (
	"fmt"
	"os"
	"strings"
)

// skillCmd is `ducklab skill` (03 §3.9).
func skillCmd(verb string, args []string, repo string) int {
	client, projectID, code := project(repo)
	if code != 0 {
		return code
	}

	switch verb {
	case "", "list":
		scope := ""
		for i := 0; i < len(args); i++ {
			if args[i] == "--scope" && i+1 < len(args) {
				scope = args[i+1]
				i++
			}
		}
		items, err := client.SkillList(projectID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		shown := 0
		for _, sk := range items {
			name := str(sk["name"])
			if name == "" {
				// A directory that failed to parse has no name, only a
				// problem. Reported, because a skill someone just wrote must
				// not look like a skill that was never there.
				fmt.Printf("  (unreadable) %s\n", strings.Join(strs(sk["problems"]), "; "))
				shown++
				continue
			}
			if scope != "" && scope != "all" && str(sk["scope"]) != scope {
				continue
			}
			kind := "doc"
			if b, _ := sk["runnable"].(bool); b {
				kind = "run"
			}
			if b, _ := sk["pending"].(bool); b {
				// A duckling cannot use this until the run that wrote it is
				// accepted (05 §7.1), and a person wondering why needs to see
				// the reason here rather than in a run log.
				kind += " pending"
			}
			fmt.Printf("  %-24s %-8s %-5s %s\n", name, str(sk["scope"]), kind, str(sk["description"]))
			if problems := strs(sk["problems"]); len(problems) > 0 {
				// Beside the skill rather than only under `skill validate`:
				// this is where someone is already looking.
				fmt.Printf("  %-24s %s\n", "", "⚠ "+strings.Join(problems, "; "))
			}
			shown++
		}
		if shown == 0 {
			fmt.Println("no skills")
		}
		return 0

	case "show":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab skill show <name>")
			return 2
		}
		sk, err := client.SkillGet(projectID, args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("%s (%s, version %v)\n", str(sk["name"]), str(sk["scope"]), sk["version"])
		fmt.Printf("  %s\n", str(sk["description"]))
		if entry := str(sk["entry"]); entry != "" {
			fmt.Printf("  entry: %s\n", entry)
		} else {
			fmt.Println("  documentation only — nothing to run")
		}
		if body := str(sk["body"]); body != "" {
			fmt.Printf("\n%s\n", body)
		}
		if problems := strs(sk["problems"]); len(problems) > 0 {
			fmt.Fprintf(os.Stderr, "\n⚠ %s\n", strings.Join(problems, "\n⚠ "))
			return 1
		}
		return 0

	case "validate":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab skill validate <name>")
			return 2
		}
		sk, err := client.SkillGet(projectID, args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		problems := strs(sk["problems"])
		if len(problems) == 0 {
			fmt.Printf("%s is valid\n", args[0])
			return 0
		}
		// Every problem at once. One per round turns fixing a skill into a
		// guessing game.
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "  %s\n", p)
		}
		return 1

	case "new":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab skill new <name> [--runnable]")
			return 2
		}
		runnable := false
		for _, a := range args[1:] {
			if a == "--runnable" {
				runnable = true
			}
		}
		dir, err := client.SkillNew(projectID, args[0], runnable)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("created %s\n", dir)
		// The scaffold deliberately fails validate: every field it leaves as
		// TODO is one a human has to answer, and saying so now beats letting
		// them find out at the first skill_run.
		fmt.Println("fill in the TODOs, then:  ducklab skill validate " + args[0])
		return 0

	case "run":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab skill run <name> [--arg k=v]...")
			return 2
		}
		params := map[string]interface{}{}
		for i := 1; i < len(args); i++ {
			if args[i] != "--arg" || i+1 >= len(args) {
				continue
			}
			k, v, ok := strings.Cut(args[i+1], "=")
			if !ok {
				fmt.Fprintf(os.Stderr, "error: --arg wants k=v, got %q\n", args[i+1])
				return 2
			}
			params[k] = v
			i++
		}
		out, failed, err := client.SkillRun(projectID, args[0], params)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Print(out)
		if !strings.HasSuffix(out, "\n") {
			fmt.Println()
		}
		if failed {
			return 1
		}
		return 0

	default:
		fmt.Fprintf(os.Stderr, "unknown skill command: %s\n", verb)
		fmt.Fprintln(os.Stderr, "usage: ducklab skill list|show|new|run|validate")
		return 2
	}
}

// strs reads a JSON array of strings out of a response field.
func strs(v interface{}) []string {
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
