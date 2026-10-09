package runlog

import "strings"

// ActorPhrase says who did a recorded act, from the actor an event carries.
//
// Empty and "human" are the person; "mcp:<client>" is an MCP operator — a
// model acting through the loop, never the person. A consultant_switched
// replay said "The person switched" for an operator's switch (B-513 review),
// turning an operator's act into human intent in the next model's prompt and
// in the record a reader trusts. Every renderer of an actor-carrying event
// says it through this one function.
func ActorPhrase(actor string) string {
	a := strings.TrimSpace(actor)
	switch {
	case a == "" || a == "human":
		return "the person"
	case strings.HasPrefix(a, "mcp:"):
		return "the MCP operator " + a
	default:
		return a
	}
}
