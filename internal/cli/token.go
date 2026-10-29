package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/jrullan/ducklab/internal/daemon"
	"github.com/jrullan/ducklab/internal/engineclt"
)

// tokenCmd manages per-agent tokens (B-550). Issuing, changing and revoking
// are the person's: the engine refuses them to an agent's credential, so
// these work from the person's own terminal or the desktop.
func tokenCmd(verb string, args []string) int {
	info, err := daemon.ReadEngineJSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, "engine not running")
		return 9
	}
	client := cliClient(info)
	if os.Getenv(engineclt.AgentTokenEnv) != "" && verb != "whoami" {
		fmt.Fprintf(os.Stderr, "note: %s is set in this shell, so this runs as that agent and the engine will refuse it; "+
			"managing tokens needs your own credential — unset it first\n", engineclt.AgentTokenEnv)
	}

	switch verb {
	case "", "list":
		list, err := client.AgentTokenList()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		shared := "on — every agent that reads engine.json has full access to every project"
		if !list.LegacyEnabled {
			shared = "off"
		}
		fmt.Printf("shared agent token: %s\n", shared)
		if len(list.Items) == 0 {
			fmt.Println("no agent tokens; issue one with: ducklab token create <name> --grant <project>=read-only|full")
			return 0
		}
		fmt.Printf("%-12s %-22s %-12s %-10s %s\n", "ID", "NAME", "HINT", "LAST USED", "GRANTS")
		for _, t := range list.Items {
			used := "never"
			if t.LastUsedAt != "" {
				used = t.LastUsedAt[:10]
			}
			grants := grantsLabel(t.Grants)
			if t.RevokedAt != "" {
				grants = "REVOKED " + t.RevokedAt[:10]
			}
			fmt.Printf("%-12s %-22s %-12s %-10s %s\n", t.ID, t.Name, t.Hint+"…", used, grants)
		}
		return 0

	case "create":
		name, grants, err := parseTokenCreate(args)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			fmt.Fprintln(os.Stderr, tokenUsage)
			return 2
		}
		tok, secret, err := client.AgentTokenCreate(name, grants)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("issued %s %q — records as %s\n", tok.ID, tok.Name, tok.Actor)
		fmt.Printf("access: %s\n\n", grantsLabel(tok.Grants))
		fmt.Printf("  DUCKLAB_TOKEN=%s\n\n", secret)
		fmt.Println("This is the only time the token is shown. Put it in the agent's MCP server environment")
		fmt.Println("(DUCKLAB_TOKEN) or its shell; the engine keeps only a hash.")
		return 0

	case "grant":
		if len(args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: ducklab token grant <token-id|name> <project> read-only|full|none")
			return 2
		}
		tok, code := findToken(client, args[0])
		if tok == nil {
			return code
		}
		level := strings.ToLower(args[2])
		var grants []engineclt.AgentGrant
		for _, g := range tok.Grants {
			if g.Project != args[1] {
				grants = append(grants, g)
			}
		}
		switch level {
		case "none":
		case "read-only", "read", "full":
			grants = append(grants, engineclt.AgentGrant{Project: args[1], Level: level})
		default:
			fmt.Fprintf(os.Stderr, "unknown level %q: use read-only, full or none\n", args[2])
			return 2
		}
		updated, err := client.AgentTokenSetGrants(tok.ID, grants)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("%s %q: %s\n", updated.ID, updated.Name, grantsLabel(updated.Grants))
		return 0

	case "revoke":
		if len(args) != 1 {
			fmt.Fprintln(os.Stderr, "usage: ducklab token revoke <token-id|name>")
			return 2
		}
		tok, code := findToken(client, args[0])
		if tok == nil {
			return code
		}
		if err := client.AgentTokenRevoke(tok.ID); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("revoked %s %q; an agent presenting it is refused from now on\n", tok.ID, tok.Name)
		return 0

	case "legacy":
		if len(args) == 0 {
			list, err := client.AgentTokenList()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
			fmt.Println(map[bool]string{true: "on", false: "off"}[list.LegacyEnabled])
			return 0
		}
		var on bool
		switch args[0] {
		case "on":
			on = true
		case "off":
		default:
			fmt.Fprintln(os.Stderr, "usage: ducklab token legacy [on|off]")
			return 2
		}
		if err := client.AgentTokenLegacySet(on); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if on {
			fmt.Println("shared agent token on: any agent that reads engine.json has full access to every project")
		} else {
			fmt.Println("shared agent token off: only agents holding their own token (DUCKLAB_TOKEN) reach the engine")
		}
		return 0

	case "whoami":
		self, err := client.AgentTokenSelf()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		switch str(self["kind"]) {
		case "person":
			fmt.Println("the person's credential")
		case "shared":
			fmt.Println("the shared agent token (engine.json): every project, recorded as an agent")
		default:
			var grants []engineclt.AgentGrant
			if raw, ok := self["grants"].([]interface{}); ok {
				for _, g := range raw {
					if m, ok := g.(map[string]interface{}); ok {
						grants = append(grants, engineclt.AgentGrant{Project: str(m["project"]), Level: str(m["level"])})
					}
				}
			}
			fmt.Printf("agent token %s %q (%s): %s\n", self["id"], self["name"], self["actor"], grantsLabel(grants))
		}
		return 0
	}
	fmt.Fprintln(os.Stderr, tokenUsage)
	return 2
}

const tokenUsage = `usage: ducklab token list
       ducklab token create <name> [--grant <project>=read-only|full]...
       ducklab token grant <token-id|name> <project> read-only|full|none
       ducklab token revoke <token-id|name>
       ducklab token legacy [on|off]
       ducklab token whoami`

func parseTokenCreate(args []string) (string, []engineclt.AgentGrant, error) {
	var name string
	var grants []engineclt.AgentGrant
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--grant" && i+1 < len(args):
			i++
			a = "--grant=" + args[i]
			fallthrough
		case strings.HasPrefix(a, "--grant="):
			project, level, ok := strings.Cut(strings.TrimPrefix(a, "--grant="), "=")
			if !ok || project == "" || level == "" {
				return "", nil, fmt.Errorf("--grant takes <project>=read-only|full, got %q", strings.TrimPrefix(a, "--grant="))
			}
			grants = append(grants, engineclt.AgentGrant{Project: project, Level: level})
		case strings.HasPrefix(a, "-"):
			return "", nil, fmt.Errorf("unknown flag %s", a)
		case name == "":
			name = a
		default:
			return "", nil, fmt.Errorf("one name only; quote a name with spaces (\"OpenCode ti36\")")
		}
	}
	if name == "" {
		return "", nil, fmt.Errorf("a token needs a name")
	}
	return name, grants, nil
}

func findToken(client *engineclt.Client, ref string) (*engineclt.AgentToken, int) {
	list, err := client.AgentTokenList()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil, 1
	}
	for i, t := range list.Items {
		if t.ID == ref || (t.RevokedAt == "" && strings.EqualFold(t.Name, ref)) {
			return &list.Items[i], 0
		}
	}
	fmt.Fprintf(os.Stderr, "no agent token %q; `ducklab token list` shows them\n", ref)
	return nil, 1
}

func grantsLabel(grants []engineclt.AgentGrant) string {
	if len(grants) == 0 {
		return "no projects"
	}
	parts := make([]string, len(grants))
	for i, g := range grants {
		parts[i] = g.Project + "=" + g.Level
	}
	return strings.Join(parts, ", ")
}
