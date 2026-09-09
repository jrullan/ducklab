package mcp

import (
	"strings"
	"testing"
)

// B-345: release runs advertise request_changes, so that action must use the
// release proposal endpoint rather than the intake/spec/plan stage endpoint.
func TestRequestChangesDispatchesReleaseToReleasePlan(t *testing.T) {
	eng := &fakeEngine{runs: map[string]map[string]interface{}{
		"r-release": {
			"id": "r-release", "project_id": "calc", "stage": "release",
			"next": []interface{}{"accept", "request_changes", "reject"},
		},
	}}
	server := NewServer(eng)
	server.client = "claude"
	note := "Correct the shipped-task inventory before publishing."

	out, err := server.decide("r-release", "request_changes", note)
	if err != nil {
		t.Fatal(err)
	}
	if eng.lastStageReq != nil {
		t.Fatalf("release revision went through generic StageStart: %#v", eng.lastStageReq)
	}
	if eng.lastReleaseBump != "" || eng.lastReleaseRevise != note {
		t.Fatalf("release request = bump %q revise %q", eng.lastReleaseBump, eng.lastReleaseRevise)
	}
	if got := string(out["content"].([]map[string]interface{})[0]["text"].(string)); !strings.Contains(got, "r-release-rev") {
		t.Errorf("response does not identify revision run: %s", got)
	}
}

func TestRequestChangesRejectsAnAdvertisedStageWithoutADispatcher(t *testing.T) {
	eng := &fakeEngine{runs: map[string]map[string]interface{}{
		"r-build": {
			"id": "r-build", "project_id": "calc", "stage": "build",
			"next": []interface{}{"request_changes"},
		},
	}}

	_, err := NewServer(eng).decide("r-build", "request_changes", "try again")
	if err == nil || !strings.Contains(err.Error(), `no revision dispatcher is registered`) {
		t.Fatalf("error = %v, want explicit dispatcher error", err)
	}
	if eng.lastStageReq != nil {
		t.Fatalf("unsupported stage leaked into StageStart: %#v", eng.lastStageReq)
	}
}
