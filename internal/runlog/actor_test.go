package runlog

import "testing"

// B-530: an agent credential that names no agent is recorded "agent"; a
// replay must never turn that back into the person.
func TestActorPhraseNeverMakesAnAgentThePerson(t *testing.T) {
	for actor, want := range map[string]string{
		"":             "the person",
		"human":        "the person",
		"mcp:opencode": "the MCP operator mcp:opencode",
		"agent":        "an agent",
		"agent:ci":     "the agent ci",
	} {
		if got := ActorPhrase(actor); got != want {
			t.Errorf("ActorPhrase(%q) = %q, want %q", actor, got, want)
		}
	}
}
