package tools

import (
	"context"
	"encoding/json"
	"fmt"
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

func writeRunState(t *testing.T, root, id, started string) {
	t.Helper()
	dir := filepath.Join(root, ".ducklab", "runs", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"id":"` + id + `","stage":"build","status":"done","verdict":"PASSED","started_at":"` + started + `"}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunListClampsLimitAndPaginates(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 117; i++ {
		writeRunState(t, root, fmt.Sprintf("r-%03d", i), fmt.Sprintf("2026-08-11T22:%02d:00Z", i%60))
	}
	res, err := (&RunListTool{}).Execute(context.Background(), &ExecContext{ProjectRoot: root}, json.RawMessage(`{"limit":100}`))
	if err != nil || res.IsError {
		t.Fatalf("run_list: %v %+v", err, res)
	}
	if !strings.Contains(res.Content, "showing 100 of 117; use offset 100 to continue") {
		t.Errorf("dishonest count/pagination: %q", res.Content)
	}
	res, err = (&RunListTool{}).Execute(context.Background(), &ExecContext{ProjectRoot: root}, json.RawMessage(`{"limit":100,"offset":100}`))
	if err != nil || res.IsError {
		t.Fatalf("run_list page 2: %v %+v", err, res)
	}
	if !strings.Contains(res.Content, "showing 17 of 117") || strings.Contains(res.Content, "use offset") {
		t.Errorf("bad second page: %q", res.Content)
	}
}
