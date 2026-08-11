package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The consultant's whole point is "why didn't T-097 pass?" — and it was
// blind to exactly that: run records live under .ducklab, which the fs
// denylist rightly protects. run_list and run_read are the front doors.
func TestRunHistoryTools(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".ducklab", "runs", "r-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"id":"r-1","stage":"test","task_id":"T-097","status":"done","verdict":"FAILED","started_at":"2026-08-11T22:00:00Z","failure":"the gate is still green"}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	events := strings.Join([]string{
		`{"type":"turn_start","data":{"round":1,"turn":0,"role":"implementer","duckling":"luna"}}`,
		`{"type":"message","data":{"round":1,"role":"reviewer","verdict":"request-changes","findings":[{"severity":"major","issue":"weak assertion"}]}}`,
		`{"type":"round_gate","data":{"round":1,"result":"red"}}`,
		`{"type":"run_end","data":{"verdict":"FAILED"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	ectx := &ExecContext{ProjectRoot: root}

	list := &RunListTool{}
	res, err := list.Execute(context.Background(), ectx, json.RawMessage(`{"task":"T-097"}`))
	if err != nil || res.IsError {
		t.Fatalf("run_list: %v %+v", err, res)
	}
	if !strings.Contains(res.Content, "r-1") || !strings.Contains(res.Content, "FAILED") {
		t.Errorf("list missing the run: %q", res.Content)
	}

	read := &RunReadTool{}
	res, err = read.Execute(context.Background(), ectx, json.RawMessage(`{"id":"r-1"}`))
	if err != nil || res.IsError {
		t.Fatalf("run_read: %v %+v", err, res)
	}
	for _, must := range []string{"still green", "request-changes", "weak assertion", "gate: red", "luna"} {
		if !strings.Contains(res.Content, must) {
			t.Errorf("run_read lost %q:\n%s", must, res.Content)
		}
	}

	// A path in the id must not escape the record directory.
	res, _ = read.Execute(context.Background(), ectx, json.RawMessage(`{"id":"../../secret"}`))
	if !res.IsError {
		t.Error("a path-shaped id was accepted")
	}
}
