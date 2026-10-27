package mcp

import (
	"strings"
	"testing"
)

// B-522: an operator reading run_get learns whether an escalation actually
// paused the run, will pause it at its safe point, or left it running — the
// desktop card once said "paused" over a triage that never stopped.
func TestRunGetCarriesTheEscalationsTruthfulState(t *testing.T) {
	eng := &fakeEngine{runs: map[string]map[string]interface{}{
		"r-triage": {"id": "r-triage", "stage": "triage", "status": "running", "escalation": map[string]interface{}{
			"state": "pausing", "role": "triager", "state_detail": "pausing when the current bug's turn ends",
		}},
	}}
	resps := drive(t, eng, initFrame, callFrame(2, "run_get", `{"run_id":"r-triage"}`))
	text, isErr := toolResultText(t, resps[1])
	if isErr || !strings.Contains(text, `"pausing"`) || !strings.Contains(text, "triager") {
		t.Fatalf("run_get omitted the escalation state: error=%v body=%s", isErr, text)
	}
}

// Relaunching a triage with another triager is a real door for an operator
// too: the earlier run's id rides to the engine with the picked seat.
func TestBugTriageCarriesARelaunch(t *testing.T) {
	eng := &fakeEngine{}
	resps := drive(t, eng, initFrame,
		callFrame(2, "bug_triage", `{"project_id":"p","relaunch_of":"r-slow","ducklings":["pato-dos"]}`))
	if _, isErr := toolResultText(t, resps[1]); isErr {
		t.Fatalf("bug_triage failed: %v", resps[1])
	}
	if eng.lastTriageReq["relaunch_of"] != "r-slow" {
		t.Fatalf("relaunch_of = %#v, want the run being replaced", eng.lastTriageReq["relaunch_of"])
	}
}
