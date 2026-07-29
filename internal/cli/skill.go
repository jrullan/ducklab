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

	default:
		fmt.Fprintf(os.Stderr, "unknown skill command: %s\n", verb)
		fmt.Fprintln(os.Stderr, "usage: ducklab skill list|show|validate")
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
