package engineapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/service"
)

// Refining a failed task must amend only that task's plan section. Editing the
// plan file by hand was the previous (and dangerously broad) recovery path.
func TestTaskBodyPutAmendsOnlyTheNamedPlanSection(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(name, filepath.Join(root, name))
	}
	svc, err := service.New(config.DefaultGlobal(), service.Options{Bus: bus.New(16)})
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	p, err := svc.ProjectInit(context.Background(), service.InitRequest{Path: project, Name: "task-editor", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(artifact.DocsDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := `## M-001 — Recovery

### T-001 — Narrow the parser

**Owns:** internal/parser
**Depends on:** T-000

The original body.

### T-002 — Keep this task

**Owns:** frontend/src/keep.ts

This body must not change.
`
	if err := os.WriteFile(artifact.Path(project, artifact.KindPlan), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(New(svc, bus.New(16), "token", "test", ""))
	t.Cleanup(server.Close)
	put := func(task, body string) (*http.Response, service.TaskView) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, server.URL+"/v1/projects/"+p.ID+"/tasks/"+task, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var got service.TaskView
		if resp.StatusCode == http.StatusOK {
			defer resp.Body.Close()
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
		}
		return resp, got
	}

	resp, got := put("T-001", `{"body":"Parse only the header first, then validate its bounds."}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got.ID != "T-001" || !strings.Contains(got.Body, "Parse only the header first, then validate its bounds.") || !strings.Contains(got.Body, "**Owns:** internal/parser") || !strings.Contains(got.Body, "**Depends on:** T-000") {
		t.Errorf("PUT response = %+v, want the full amended task section", got)
	}
	stored, err := os.ReadFile(artifact.Path(project, artifact.KindPlan))
	if err != nil {
		t.Fatal(err)
	}
	text := string(stored)
	if strings.Contains(text, "Parse only the header first, then validate its bounds.") {
		t.Error("PUT changed the approved plan without the amendment gate")
	}
	proposal, err := os.ReadFile(artifact.ProposedPath(project, artifact.KindPlan))
	if err != nil {
		t.Fatal(err)
	}
	proposed := string(proposal)
	if !strings.Contains(proposed, "**Owns:** internal/parser") || !strings.Contains(proposed, "**Depends on:** T-000") {
		t.Errorf("amendment discarded the target section's lanes: %s", proposed)
	}
	if !strings.Contains(proposed, "Parse only the header first, then validate its bounds.") || !strings.Contains(proposed, "### T-002 — Keep this task\n\n**Owns:** frontend/src/keep.ts\n\nThis body must not change.") {
		t.Errorf("amendment changed more than T-001's body: %s", proposed)
	}

	// A complete structured body may update the named task's contract without
	// retaining a duplicate task or touching its neighbour.
	if err := os.Remove(artifact.ProposedPath(project, artifact.KindPlan)); err != nil {
		t.Fatal(err)
	}
	structured := "**Implements:** SPEC-009\n\n**Owns:** src/parser.c\n\n**Work unit:** Parse one header\n\n**Acceptance slices:**\n- Rejects invalid bounds\n\n**Acceptance probes:**\n1. `go test ./parser`\n\n**Produces:** file:src/parser.c\n\n**Consumes:** none\n\n**Verification:** `go test ./parser`"
	resp, got = put("T-001", `{"body":`+strconv.Quote(structured)+`}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("structured PUT status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got.Implements[0] != "SPEC-009" || !strings.Contains(got.Body, "Acceptance probes") || strings.Contains(got.Body, "internal/parser") {
		t.Errorf("structured amendment did not replace T-001 contract: %+v", got)
	}
	proposal, err = os.ReadFile(artifact.ProposedPath(project, artifact.KindPlan))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(proposal), "### T-001") != 1 || !strings.Contains(string(proposal), "### T-002 — Keep this task") {
		t.Errorf("structured amendment duplicated or changed task neighbourhood: %s", proposal)
	}

	resp, _ = put("T-404", `{"body":"must not create a task"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown task PUT status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
	afterUnknown, err := os.ReadFile(artifact.Path(project, artifact.KindPlan))
	if err != nil {
		t.Fatal(err)
	}
	if string(afterUnknown) != text {
		t.Error("unknown task PUT changed the plan")
	}
}
