package mcp

import (
	"strings"
	"testing"
)

// B-517: an operator holding a failed intake's redo note had no stage door
// that took it — stage_start had no revise — so the only tool accepting a
// note was run_start, which builds a task. The retry of a document stage is
// a revision of that stage, through the same request the desktop sends.
func TestStageStartCarriesARevisionForAStageRetry(t *testing.T) {
	eng := &fakeEngine{}
	resps := drive(t, eng, initFrame,
		callFrame(2, "stage_start", `{"project_id":"p","stage":"intake","revise":"Revise the intake draft to address the failure.\n- [major] REQ-008 invents behavior"}`))
	if _, isErr := toolResultText(t, resps[1]); isErr {
		t.Fatalf("stage_start failed: %v", resps[1])
	}
	got, _ := eng.lastStageReq["revise"].(string)
	if !strings.Contains(got, "REQ-008 invents behavior") {
		t.Errorf("revise = %q, want the redo note", got)
	}
	if eng.lastRunReq != nil {
		t.Errorf("a stage retry reached run_start's build door: %#v", eng.lastRunReq)
	}
}

// The schemas say which door is which: stage_start offers revise, and
// run_start tells an operator it builds a task and where a stage retry goes.
func TestRetrySchemasSeparateStageRevisionFromBuild(t *testing.T) {
	var stageProps map[string]interface{}
	var runDesc string
	for _, tool := range toolList() {
		switch tool["name"] {
		case "stage_start":
			schema, _ := tool["inputSchema"].(map[string]interface{})
			stageProps, _ = schema["properties"].(map[string]interface{})
		case "run_start":
			runDesc, _ = tool["description"].(string)
		}
	}
	if _, ok := stageProps["revise"]; !ok {
		t.Error("stage_start has no revise: a stage retry has no stage door")
	}
	if !strings.Contains(runDesc, "stage_start with revise") || !strings.Contains(runDesc, "never run_start") {
		t.Errorf("run_start's description does not send a stage retry elsewhere: %q", runDesc)
	}
}
