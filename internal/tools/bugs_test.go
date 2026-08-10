package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bugProject(t *testing.T) *ExecContext {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".ducklab"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &ExecContext{ProjectRoot: root, Duckling: "k3"}
}

// The chat's last mile: a consultant with the complete report written had no
// way to land it, and the person carried it to the form by hand.
func TestBugFileLandsOnTheBoard(t *testing.T) {
	ectx := bugProject(t)
	file := &BugFile{}

	res, err := file.Execute(context.Background(), ectx, json.RawMessage(
		`{"title":"Unit preference ignored on most screens","body":"Repro: set imperial...","severity":"high"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("filing failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "B-001") {
		t.Errorf("the result does not name the new id: %q", res.Content)
	}

	// And the record is really there, wearing its provenance.
	read := &BugRead{}
	got, err := read.Execute(context.Background(), ectx, json.RawMessage(`{"id":"B-001"}`))
	if err != nil || got.IsError {
		t.Fatalf("read back: %v %v", err, got)
	}
	if !strings.Contains(got.Content, "Unit preference ignored") || !strings.Contains(got.Content, "high") {
		t.Errorf("record lost the report: %q", got.Content)
	}
}

func TestBugFileValidatesLikeAnyReporter(t *testing.T) {
	ectx := bugProject(t)
	file := &BugFile{}

	res, _ := file.Execute(context.Background(), ectx, json.RawMessage(`{"title":"  ","body":"x"}`))
	if !res.IsError {
		t.Error("a bug with no title was accepted")
	}
	res, _ = file.Execute(context.Background(), ectx, json.RawMessage(`{"title":"t","body":"x","severity":"catastrophic"}`))
	if !res.IsError {
		t.Error("an invented severity was accepted")
	}
	// Severity defaults rather than blocks: triage is where that judgement belongs.
	res, _ = file.Execute(context.Background(), ectx, json.RawMessage(`{"title":"t","body":"x"}`))
	if res.IsError || !strings.Contains(res.Content, "normal") {
		t.Errorf("no-severity filing = %+v, want default normal", res)
	}
}

// Without an id the tool lists the whole board — the duplicate check the
// persona demands before any filing.
func TestBugReadListsTheBoardForDuplicateChecks(t *testing.T) {
	ectx := bugProject(t)
	file := &BugFile{}
	for _, title := range []string{"first", "second"} {
		if res, _ := file.Execute(context.Background(), ectx, json.RawMessage(`{"title":"`+title+`","body":"b"}`)); res.IsError {
			t.Fatalf("seed %s: %s", title, res.Content)
		}
	}
	read := &BugRead{}
	res, err := read.Execute(context.Background(), ectx, nil)
	if err != nil || res.IsError {
		t.Fatalf("list: %v %v", err, res)
	}
	for _, want := range []string{"B-001", "B-002", "first", "second"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("list is missing %q: %q", want, res.Content)
		}
	}

	res, _ = read.Execute(context.Background(), ectx, json.RawMessage(`{"id":"B-999"}`))
	if !res.IsError {
		t.Error("an unknown id read as success")
	}
}

// bug_read was named in the triager's ceiling and the chat's belt since both
// existed — and never registered, so the registry silently dropped it from
// every belt that asked. A named tool that does not resolve is a lie told to
// a prompt; the registry must carry both now.
func TestTheBugToolsAreActuallyRegistered(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"bug_read", "bug_file"} {
		if _, err := r.Get(name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
