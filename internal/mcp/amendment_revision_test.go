package mcp

import "testing"

// An amendment's request_changes is not a generic plan revision: it replays
// the persisted amendment request so the follow-up remains the same small,
// solo operation rather than becoming the stage's default council run.
func TestRequestChangesReplaysAnAmendmentStageRequest(t *testing.T) {
	eng := &fakeEngine{runs: map[string]map[string]interface{}{
		"r-amendment": {
			"id": "r-amendment", "project_id": "calc", "stage": "plan",
			"mode": "solo", "next": []interface{}{"request_changes"},
			"stage_request": map[string]interface{}{
				"stage": "plan", "extend": "add the small flow", "mode": "solo", "rounds": float64(1),
			},
		},
	}}

	server := NewServer(eng)
	server.client = "claude"
	note := "add Depends on: T-060 to T-061 and T-062"
	if _, err := server.decide("r-amendment", "request_changes", note); err != nil {
		t.Fatal(err)
	}

	if got, _ := eng.lastStageReq["extend"].(string); got != "add the small flow" {
		t.Errorf("extend = %q, want original amendment text", got)
	}
	if got, _ := eng.lastStageReq["revise"].(string); got != note {
		t.Errorf("revise = %q, want operator note", got)
	}
	if got, _ := eng.lastStageReq["mode"].(string); got != "solo" {
		t.Errorf("mode = %q, want original solo mode", got)
	}
	if got, _ := eng.lastStageReq["rounds"].(float64); got != 1 {
		t.Errorf("rounds = %v, want original one round", got)
	}
}
