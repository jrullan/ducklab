package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/jrullan/ducklab/internal/daemon"
	"github.com/jrullan/ducklab/internal/engineclt"
)

// providerCmd is `ducklab provider` — the endpoints ducklings are reached
// through.
//
// A key is never passed here. `--key-env` names the environment variable the
// engine reads at call time, so a key never lands in config.toml, in a shell
// history, or in an API response (I10).
func providerCmd(verb string, args []string) int {
	info, err := daemon.ReadEngineJSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, "engine not running")
		return 9
	}
	client := engineclt.New(info)

	switch verb {
	case "", "list":
		items, err := client.ProviderList()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if len(items) == 0 {
			fmt.Println("no providers configured")
			return 0
		}
		fmt.Printf("%-16s %-10s %-40s %s\n", "ID", "KIND", "BASE URL", "KEY")
		for _, p := range items {
			key := "none needed"
			if env := str(p["api_key_env"]); env != "" {
				key = env
				if present, _ := p["key_present"].(bool); !present {
					// The commonest reason a hosted duckling fails, and
					// invisible unless something says it.
					key += " (NOT SET)"
				}
			}
			fmt.Printf("%-16s %-10s %-40s %s\n", p["id"], p["kind"], p["base_url"], key)
			if used := strs(p["in_use"]); len(used) > 0 {
				fmt.Printf("%-16s   used by: %s\n", "", strings.Join(used, ", "))
			}
		}
		return 0

	case "set":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab provider set <id> --url URL [--kind openai|anthropic] [--key-env VAR]")
			return 2
		}
		id := args[0]
		body := map[string]interface{}{}
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--url":
				if i+1 < len(args) {
					body["base_url"] = args[i+1]
					i++
				}
			case "--kind":
				if i+1 < len(args) {
					body["kind"] = args[i+1]
					i++
				}
			case "--key-env":
				if i+1 < len(args) {
					body["api_key_env"] = args[i+1]
					i++
				}
			default:
				fmt.Fprintf(os.Stderr, "unknown flag: %s\n", args[i])
				return 2
			}
		}
		if err := client.ProviderSet(id, body); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("provider %s saved\n", id)
		if env := str(body["api_key_env"]); env != "" && os.Getenv(env) == "" {
			// Said now rather than at the first run, when it would look like
			// the model is broken.
			fmt.Printf("note: %s is not set in this shell — the engine reads it from its own environment\n", env)
		}
		return 0

	case "remove":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab provider remove <id>")
			return 2
		}
		if err := client.ProviderRemove(args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("provider %s removed\n", args[0])
		return 0

	default:
		fmt.Fprintf(os.Stderr, "unknown provider command: %s\n", verb)
		fmt.Fprintln(os.Stderr, "usage: ducklab provider list|set|remove")
		return 2
	}
}

// ducklingSetCmd is `ducklab duckling set` and `remove`.
func ducklingSetCmd(client *engineclt.Client, args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ducklab duckling set <id> --provider P --model M [--roles a,b] [--context N] [--no-native-tools] [--cost-in X --cost-out Y] [--max-tokens N] [--temperature F] [--suppress-thinking] [--notes ...]")
		return 2
	}
	id := args[0]
	body := map[string]interface{}{}
	caps := map[string]interface{}{}
	cost := map[string]interface{}{}
	params := map[string]interface{}{}

	for i := 1; i < len(args); i++ {
		next := func() (string, bool) {
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch args[i] {
		case "--provider":
			if v, ok := next(); ok {
				body["provider"] = v
			}
		case "--model":
			if v, ok := next(); ok {
				body["model"] = v
			}
		case "--notes":
			if v, ok := next(); ok {
				body["notes"] = v
			}
		case "--roles":
			if v, ok := next(); ok {
				body["roles"] = strings.Split(v, ",")
			}
		case "--context":
			if v, ok := next(); ok {
				n, err := strconv.Atoi(v)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: --context wants a number, got %q\n", v)
					return 2
				}
				caps["context_tokens"] = n
			}
		case "--no-native-tools":
			// The text protocol, for a server that cannot do function calling.
			caps["native_tools"] = false
		case "--native-tools":
			caps["native_tools"] = true
		case "--cost-in":
			if v, ok := next(); ok {
				f, err := strconv.ParseFloat(v, 64)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: --cost-in wants a number, got %q\n", v)
					return 2
				}
				cost["input_per_mtok"] = f
			}
		case "--cost-out":
			if v, ok := next(); ok {
				f, err := strconv.ParseFloat(v, 64)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: --cost-out wants a number, got %q\n", v)
					return 2
				}
				cost["output_per_mtok"] = f
			}
		case "--max-tokens":
			// The cap on one reply. A reasoning model without one spends its
			// whole output budget thinking and returns nothing, which reads as
			// a transport fault rather than a setting.
			if v, ok := next(); ok {
				n, err := strconv.Atoi(v)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: --max-tokens wants a number, got %q\n", v)
					return 2
				}
				params["max_tokens"] = n
			}
		case "--temperature":
			if v, ok := next(); ok {
				f, err := strconv.ParseFloat(v, 64)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: --temperature wants a number, got %q\n", v)
					return 2
				}
				params["temperature"] = f
			}
		case "--suppress-thinking":
			params["disable_thinking"] = true
		case "--allow-thinking":
			params["disable_thinking"] = false
		default:
			fmt.Fprintf(os.Stderr, "unknown flag: %s\n", args[i])
			return 2
		}
	}
	if len(caps) > 0 {
		body["caps"] = caps
	}
	if len(cost) > 0 {
		body["cost"] = cost
	}
	if len(params) > 0 {
		body["params"] = params
	}

	if err := client.DucklingSet(id, body); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("duckling %s saved\n", id)
	fmt.Printf("check it with:  ducklab duckling test %s --prompt \"say OK\"\n", id)
	return 0
}
