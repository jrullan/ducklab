package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

func projectWithDocs(t *testing.T, s *Service, docs map[artifact.Kind]string) (string, string) {
	t.Helper()
	id, dir := projectWithConfig(t, s, "proj")
	os.MkdirAll(artifact.DocsDir(dir), 0o755)
	for kind, body := range docs {
		os.WriteFile(artifact.Path(dir, kind), []byte(body), 0o644)
	}
	return id, dir
}

const planDoc = `## M-01 — Auth

### T-001 — Issue tokens

**Implements:** SPEC-001
**Complexity:** medium

Tokens must expire.

### T-002 — Refresh tokens

**Implements:** SPEC-001
**Depends on:** T-001
`

const specDoc = "## SPEC-001 — Session tokens\n\n**Implements:** REQ-001\n\nUse JWT with a 24h expiry.\n"

func TestTaskListReadsThePlan(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	tasks, err := s.TaskList(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(tasks))
	}
	if tasks[0].ID != "T-001" || tasks[0].Milestone != "M-01" {
		t.Errorf("task = %+v", tasks[0])
	}
	if tasks[0].Complexity != "medium" {
		t.Errorf("complexity = %q", tasks[0].Complexity)
	}
	if len(tasks[1].DependsOn) != 1 || tasks[1].DependsOn[0] != "T-001" {
		t.Errorf("depends_on = %v", tasks[1].DependsOn)
	}
	// A task nobody has run is todo, not blank.
	if tasks[0].Status != "todo" {
		t.Errorf("status = %q", tasks[0].Status)
	}
}

// The plan says what tasks ARE; run records say what has happened to them.
// Keeping status in the document would let a model rewriting the plan mark its
// own work accepted.
func TestTaskStatusComesFromRunsNotTheDocument(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	run := &runlog.Run{
		ID: "r-1", ProjectID: id, TaskID: "T-001", Status: "done",
		Verdict: "PASSED", Accepted: true,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, _ := runlog.NewWriter(dir, run)
	w.Close()
	s.RecoverRuns(context.Background())

	tasks, _ := s.TaskList(context.Background(), id)
	if tasks[0].Status != "accepted" {
		t.Errorf("T-001 status = %q, want accepted", tasks[0].Status)
	}
	if tasks[1].Status != "todo" {
		t.Errorf("T-002 status = %q", tasks[1].Status)
	}
}

func TestTaskNextRespectsDependencies(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	next, err := s.TaskNext(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	// T-002 depends on T-001, which is not accepted, so T-001 comes first.
	if next == nil || next.ID != "T-001" {
		t.Fatalf("next = %+v, want T-001", next)
	}
}

func TestTaskNextIsNilWhenNothingIsReady(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: "## M-01 — X\n\n### T-005 — Blocked\n\n**Depends on:** T-999\n",
	})
	next, err := s.TaskNext(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if next != nil {
		t.Errorf("returned a task whose dependency is unmet: %+v", next)
	}
}

// AC-41: a re-run must be told what the previous attempt already tried.
func TestBuildPromptCarriesFailedAttempts(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: planDoc, artifact.KindSpec: specDoc,
	})

	failed := &runlog.Run{
		ID: "r-failed", ProjectID: id, TaskID: "T-001", Mode: "solo",
		Status: "failed", Verdict: "FAILED",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, _ := runlog.NewWriter(dir, failed)
	w.AppendEvent("error", map[string]interface{}{"error": "changed the handler signature"})
	w.WriteVerify("FAIL\tTestLogin: nil pointer at auth.go:88")
	w.Close()
	s.RecoverRuns(context.Background())

	prompt := s.buildTaskPrompt(context.Background(), id, dir, "T-001")
	if !strings.Contains(prompt, "already tried and failed") {
		t.Errorf("prompt does not mention prior attempts:\n%s", prompt)
	}
	if !strings.Contains(prompt, "changed the handler signature") {
		t.Errorf("prompt lost the failure summary:\n%s", prompt)
	}
	if !strings.Contains(prompt, "auth.go:88") {
		t.Errorf("prompt lost the gate output:\n%s", prompt)
	}
}

// A task's prompt should carry the spec section it delivers, not just its id.
func TestBuildPromptCarriesTheSpecItDelivers(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: planDoc, artifact.KindSpec: specDoc,
	})
	prompt := s.buildTaskPrompt(context.Background(), id, dir, "T-001")
	for _, want := range []string{"T-001", "Issue tokens", "Tokens must expire", "JWT with a 24h expiry"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildPromptCarriesProjectMemory(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	artifact.SaveMemory(dir, &artifact.Memory{Description: "A billing product for small firms."})

	prompt := s.buildTaskPrompt(context.Background(), id, dir, "T-001")
	if !strings.Contains(prompt, "billing product") {
		t.Errorf("project memory not injected:\n%s", prompt)
	}
}

// A task ducklab knows nothing about must still produce a usable prompt.
func TestBuildPromptForAnUnknownTask(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	prompt := s.buildTaskPrompt(context.Background(), id, dir, "T-999")
	if !strings.Contains(prompt, "T-999") {
		t.Errorf("prompt = %q", prompt)
	}
}

func TestArtifactGetReportsAPendingProposal(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindSpec: "## SPEC-001 — Original\n",
	})
	artifact.WriteProposal(dir, artifact.KindSpec,
		&artifact.Document{Sections: []artifact.Section{{ID: "SPEC-001", Title: "Proposed"}}},
		"r-1", nil)

	got, err := s.ArtifactGet(context.Background(), id, "spec")
	if err != nil {
		t.Fatal(err)
	}
	proposal, ok := got["proposal"].(map[string]interface{})
	if !ok {
		t.Fatal("no proposal reported")
	}
	if diff, _ := proposal["diff"].(string); !strings.Contains(diff, "Proposed") {
		t.Errorf("diff does not show the change: %q", diff)
	}
}

// Promotion runs the trace check immediately, while the person who accepted is
// still looking.
func TestPromoteReportsTraceErrors(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — Login\n\n**Priority:** must\n",
	})
	artifact.WriteProposal(dir, artifact.KindSpec,
		&artifact.Document{Sections: []artifact.Section{{ID: "SPEC-001", Title: "Orphan spec"}}},
		"r-1", nil)

	got, err := s.ArtifactPromote(context.Background(), id, "spec", "human")
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := got["trace_errors"].([]artifact.TraceError)
	if len(errs) == 0 {
		t.Error("promotion into a broken spine reported nothing")
	}
}

func TestStageStartRejectsAnUnknownStage(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, nil)
	if _, err := s.StageStart(context.Background(), id, StageRequest{Stage: "guessing"}); err == nil {
		t.Error("an unknown stage started")
	}
}
