package cli

import (
	"fmt"
	"os"
	"strconv"

	"github.com/jrullan/ducklab/internal/daemon"
	"github.com/jrullan/ducklab/internal/engineclt"
)

// budgetCmd shows and changes the budget every run starts with.
//
// It was invisible and immutable: the ceiling came from the engine's config and
// no client could read it, so a run that hit it failed with a number nobody had
// chosen and nobody could raise.
func budgetCmd(verb string, args []string) int {
	info, err := daemon.ReadEngineJSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, "engine not running")
		return 9
	}
	client := engineclt.New(info)

	switch verb {
	case "", "show":
		b, err := client.BudgetDefaults()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		printBudget(b)
		// Not obvious, and it is what makes the ceiling easy to hit: every model
		// call re-sends the whole conversation, so the same context is counted
		// again each round.
		fmt.Println("\ntokens counts prompt and completion together, and every round")
		fmt.Println("re-sends the conversation — a long task spends most of it on input.")
		return 0

	case "set":
		body := map[string]interface{}{}
		for i := 0; i+1 < len(args); i += 2 {
			key := args[i]
			switch key {
			case "max_usd":
				f, err := strconv.ParseFloat(args[i+1], 64)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %s wants a number, got %q\n", key, args[i+1])
					return 2
				}
				body[key] = f
			case "max_tokens", "max_turns", "max_wallclock_s":
				n, err := strconv.Atoi(args[i+1])
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %s wants a number, got %q\n", key, args[i+1])
					return 2
				}
				body[key] = n
			default:
				fmt.Fprintf(os.Stderr, "unknown budget key: %s "+
					"(max_tokens, max_usd, max_turns, max_wallclock_s)\n", key)
				return 2
			}
		}
		if len(body) == 0 {
			fmt.Fprintln(os.Stderr, "usage: ducklab budget set max_tokens 1500000 [max_usd 5 ...]")
			return 2
		}
		// The engine wants every limit, and an unnamed one must keep its current
		// value rather than become a ceiling of zero.
		current, err := client.BudgetDefaults()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		for _, k := range []string{"max_usd", "max_tokens", "max_turns", "max_wallclock_s"} {
			if _, given := body[k]; !given {
				body[k] = current[k]
			}
		}
		saved, err := client.BudgetDefaultsSet(body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		printBudget(saved)
		return 0

	default:
		fmt.Fprintln(os.Stderr, "usage: ducklab budget show|set")
		return 2
	}
}

func printBudget(b map[string]interface{}) {
	for _, k := range []string{"max_tokens", "max_usd", "max_turns", "max_wallclock_s"} {
		fmt.Printf("  %-16s %v\n", k, b[k])
	}
}
