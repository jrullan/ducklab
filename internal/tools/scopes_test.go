package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNamedReadScopeSelectsOnlyAnEngineMountedRoot(t *testing.T) {
	subject := t.TempDir()
	harness := t.TempDir()
	if err := os.WriteFile(filepath.Join(subject, "identity.txt"), []byte("subject tree"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(harness, "identity.txt"), []byte("harness tree"), 0o644); err != nil {
		t.Fatal(err)
	}
	ectx := &ExecContext{
		ProjectRoot: subject,
		ReadScopes: map[string]ReadScope{
			"harness": {ProjectRoot: harness, DocsRoot: harness, ProjectID: "ducklab", Name: "Ducklab"},
		},
	}

	ordinary, err := (&FSRead{}).Execute(context.Background(), ectx, json.RawMessage(`{"path":"identity.txt"}`))
	if err != nil || ordinary.IsError || !strings.Contains(ordinary.Content, "subject tree") {
		t.Fatalf("subject read = %+v, %v", ordinary, err)
	}
	mounted, err := (&FSRead{}).Execute(context.Background(), ectx, json.RawMessage(`{"scope":"harness","path":"identity.txt"}`))
	if err != nil || mounted.IsError || !strings.Contains(mounted.Content, "harness tree") {
		t.Fatalf("harness read = %+v, %v", mounted, err)
	}
	unknown, err := (&FSRead{}).Execute(context.Background(), ectx, json.RawMessage(`{"scope":"/tmp/arbitrary","path":"identity.txt"}`))
	if err != nil || !unknown.IsError || !strings.Contains(unknown.Content, "unknown read scope") {
		t.Fatalf("arbitrary scope = %+v, %v", unknown, err)
	}
}

func TestBugFileUsesTheHumanFixedBoardNotAModelScope(t *testing.T) {
	subject := t.TempDir()
	harness := t.TempDir()
	for _, root := range []string{subject, harness} {
		if err := os.MkdirAll(filepath.Join(root, ".ducklab"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ectx := &ExecContext{
		ProjectRoot: subject,
		ReadScopes: map[string]ReadScope{
			"harness": {ProjectRoot: harness, DocsRoot: harness, ProjectID: "ducklab", Name: "Ducklab"},
		},
		BugReportRoot: harness, BugReportProjectID: "ducklab", BugReportProjectName: "Ducklab",
		Duckling: "consultant",
	}
	res, err := (&BugFile{}).Execute(context.Background(), ectx, json.RawMessage(
		`{"title":"Harness regression","body":"evidence","severity":"high","scope":"subject"}`))
	if err != nil || res.IsError || !strings.Contains(res.Content, "in Ducklab") {
		t.Fatalf("file = %+v, %v", res, err)
	}
	harnessRead, _ := (&BugRead{}).Execute(context.Background(), ectx, json.RawMessage(`{"scope":"harness","id":"B-001"}`))
	if harnessRead.IsError || !strings.Contains(harnessRead.Content, "Harness regression") {
		t.Fatalf("harness board = %+v", harnessRead)
	}
	subjectRead, _ := (&BugRead{}).Execute(context.Background(), ectx, json.RawMessage(`{"id":"B-001"}`))
	if !subjectRead.IsError {
		t.Fatalf("bug leaked to subject board: %+v", subjectRead)
	}
}
