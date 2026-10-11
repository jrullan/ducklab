package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// B-521: an operator's request_changes names the run it revises, so the
// engine inherits its references, mode and rounds and keeps the revision
// under the intake's intent — and a replayed request's own revision record
// and folded findings never ride along as if the operator sent them.
func TestRequestChangesNamesTheRunItRevises(t *testing.T) {
	eng := &fakeEngine{runs: map[string]map[string]interface{}{
		"r-eett": {
			"id": "r-eett", "project_id": "ti", "stage": "intake", "mode": "council",
			"intent": "INT-002", "revises_run": "r-tpfn", "revision": float64(1),
			"next": []interface{}{"accept", "request_changes", "reject"},
			"stage_request": map[string]interface{}{
				"stage": "intake", "extend": "", "revises_run": "r-tpfn",
				"revision": map[string]interface{}{"parent": "r-tpfn", "intent": "INT-002", "number": float64(1)},
			},
		},
		"r-amend": {
			"id": "r-amend", "project_id": "ti", "stage": "plan", "mode": "solo",
			"next": []interface{}{"request_changes"},
			"stage_request": map[string]interface{}{
				"stage": "plan", "extend": "add CSV", "revises_run": "r-older", "revise_findings": "- old",
				"revision": map[string]interface{}{"parent": "r-older"},
			},
		},
	}}
	server := NewServer(eng)
	server.client = "claude"
	if _, err := server.decide("r-eett", "request_changes", "restore the Ans sentence", ""); err != nil {
		t.Fatal(err)
	}
	if got := eng.lastStageReq["revises_run"]; got != "r-eett" {
		t.Fatalf("revises_run = %v, want the run being revised", got)
	}
	if _, err := server.decide("r-amend", "request_changes", "one task", ""); err != nil {
		t.Fatal(err)
	}
	if got := eng.lastStageReq["revises_run"]; got != "r-amend" {
		t.Fatalf("amendment replay revises %v, want r-amend", got)
	}
	if _, ok := eng.lastStageReq["revision"]; ok {
		t.Fatal("a replayed request carried the engine's revision record")
	}
	if _, ok := eng.lastStageReq["revise_findings"]; ok {
		t.Fatal("a replayed request re-sent folded findings")
	}

	// run_get shows the chain.
	out, err := server.runGet("r-eett")
	if err != nil {
		t.Fatal(err)
	}
	text, _ := json.Marshal(out)
	for _, want := range []string{`INT-002`, `revises_run`, `r-tpfn`} {
		if !strings.Contains(string(text), want) {
			t.Fatalf("run_get lacks %s: %s", want, text)
		}
	}
}

// stage_start carries the person's note and the reviewer's findings apart,
// and the parent run when the operator names it.
func TestStageStartCarriesFindingsApartFromTheNote(t *testing.T) {
	eng := &fakeEngine{}
	resps := drive(t, eng, initFrame,
		callFrame(2, "stage_start", `{"project_id":"p","stage":"intake","revise":"keep it short","revise_findings":"- [major] REQ-008 invents behavior","revises_run":"r-tpfn"}`))
	if _, isErr := toolResultText(t, resps[1]); isErr {
		t.Fatalf("stage_start failed: %v", resps[1])
	}
	if eng.lastStageReq["revise"] != "keep it short" || eng.lastStageReq["revise_findings"] != "- [major] REQ-008 invents behavior" || eng.lastStageReq["revises_run"] != "r-tpfn" {
		t.Fatalf("stage request = %#v", eng.lastStageReq)
	}
}
