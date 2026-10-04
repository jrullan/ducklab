package mcp

import (
	"strings"
	"testing"
)

// B-493: an operator resuming a paused run can tell it what it knows. The
// note reaches the engine attributed to the operator; on any other action a
// note would be dropped, so it is refused.
func TestAResumeDecisionCarriesItsNote(t *testing.T) {
	eng := &fakeEngine{budgetLifted: "n/a", runs: map[string]map[string]interface{}{
		"r-bbdk": {"id": "r-bbdk", "status": "paused", "next": []interface{}{"resume", "abort"}},
		"r-gate": {"id": "r-gate", "status": "paused", "next": []interface{}{"accept", "reject"}},
	}}
	resps := drive(t, eng,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"elena"}}}`,
		callFrame(2, "decide", `{"run_id":"r-bbdk","action":"resume","reason":"the fix is known","note":"2^-9 is 0.001953125"}`),
		callFrame(3, "decide", `{"run_id":"r-gate","action":"accept","reason":"green","note":"also fix the path"}`),
	)
	if text, isErr := toolResultText(t, resps[1]); isErr {
		t.Fatalf("resume with a note failed: %q", text)
	}
	if eng.resumeNote != "2^-9 is 0.001953125" || eng.resumeActor != "mcp:elena" {
		t.Errorf("engine got note %q from %q", eng.resumeNote, eng.resumeActor)
	}
	text, isErr := toolResultText(t, resps[2])
	if !isErr || !strings.Contains(text, "note rides only a resume") {
		t.Errorf("a note on accept was not refused: %q", text)
	}
	if len(eng.accepted) != 0 {
		t.Error("the engine was asked to accept a decision whose note would be dropped")
	}
}
