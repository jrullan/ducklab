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

// A task's status is its latest run. The loop used to assign on every branch,
// so an older run overwrote a newer one and an accepted task fell back into
// "in progress" because a stale run happened to be visited last.
func TestTaskStatusFollowsTheNewestRun(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	older := &runlog.Run{
		ID: "r-old", ProjectID: id, TaskID: "T-001", Status: "paused",
		StartedAt: "2026-07-01T00:00:00Z",
	}
	newer := &runlog.Run{
		ID: "r-new", ProjectID: id, TaskID: "T-001", Status: "done",
		Verdict: "PASSED", Accepted: true,
		StartedAt: "2026-07-20T00:00:00Z",
	}
	for _, r := range []*runlog.Run{older, newer} {
		w, err := runlog.NewWriter(dir, r)
		if err != nil {
			t.Fatal(err)
		}
		w.Close()
	}
	s.RecoverRuns(context.Background())

	tasks, err := s.TaskList(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if tasks[0].Status != "accepted" {
		t.Errorf("T-001 = %q, want accepted: the newest run was accepted, the older one only paused", tasks[0].Status)
	}
}

// Promoting an artifact answers the gate its run is paused on. Without this
// the run stays paused forever, and the inbox fills with gates that were
// decided hours ago — three had piled up on a real project before anyone
// noticed, because no view listed runs until one did.
//
// The run and the proposal are built directly rather than produced by a stage:
// what is under test is the promote path, not whether a fake model can write
// requirements.
func TestPromotingAnArtifactClosesItsRun(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	entry, err := s.registry.Get(id)
	if err != nil {
		t.Fatal(err)
	}

	run := &runlog.Run{
		ID: "r-stage", ProjectID: id, Stage: "intake", Mode: "council",
		Status: "paused", Verdict: "UNVERIFIED", PendingKind: "gate",
		PendingSince: time.Now().UTC().Format(time.RFC3339),
		StartedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())

	doc := &artifact.Document{
		Front:    artifact.Frontmatter{Kind: artifact.KindRequirements, RunID: run.ID},
		Sections: []artifact.Section{{ID: "REQ-001", Title: "A requirement", Body: "Text."}},
	}
	if err := artifact.WriteProposal(entry.Path, artifact.KindRequirements, doc, run.ID, []string{"pato-uno"}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ArtifactPromote(context.Background(), id, "requirements", "human"); err != nil {
		t.Fatal(err)
	}

	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := detail.Run
	if got.Status != "done" {
		t.Errorf("status = %q, want done: the gate it waits on has been answered", got.Status)
	}
	if got.PendingKind != "" {
		t.Errorf("pending_kind = %q, want empty: it is not waiting for anyone", got.PendingKind)
	}
	if !got.Accepted {
		t.Error("accepted = false after a human promoted its artifact")
	}
}

// A stage run pauses for a human, and the human accepts the run. That must
// promote the document, because the decision is the same decision.
//
// Reported from a real session: intake ran, its run was accepted, and the
// Cycle view still said "proposal awaiting your decision" — because accepting
// a run committed the tree while promoting the artifact was a separate action
// on a different screen. Two buttons for one decision, and the one people
// reach for first did nothing visible.
func TestAcceptingAStageRunPromotesItsArtifact(t *testing.T) {
	for _, stage := range []string{"intake", "spec", "plan"} {
		if kind := artifactKindForStage(stage); kind == "" {
			t.Errorf("stage %q has no artifact, so accepting its run promotes nothing", stage)
		}
	}
	// A build run has no artifact to promote and must be left alone.
	for _, stage := range []string{"build", "review", "release", ""} {
		if kind := artifactKindForStage(stage); kind != "" {
			t.Errorf("stage %q was mapped to artifact %q", stage, kind)
		}
	}
}

// Blocked was a column no task could ever enter. Nothing in the engine ever
// assigned it, and the board's own test asserted it stayed empty.
//
// Meanwhile the plan had said "Depends on: T-001" since it was written, and
// TaskNext had been reading it — it was the only thing in the product that knew
// a task could be waiting on another. The board never showed it, so the only
// way to learn a task was not ready was to run it and watch a model invent the
// thing it depended on.
func TestATaskWaitingOnAnotherIsBlocked(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	tasks, err := s.TaskList(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if tasks[1].Status != "blocked" {
		t.Errorf("T-002 depends on an unaccepted T-001 but is %q", tasks[1].Status)
	}
	// A blocked task that does not say what blocked it sends you to the logs.
	if !strings.Contains(tasks[1].Blocked, "T-001") {
		t.Errorf("the reason does not name what it waits on: %q", tasks[1].Blocked)
	}
	// And T-001 itself waits on nothing.
	if tasks[0].Status != "todo" || tasks[0].Blocked != "" {
		t.Errorf("T-001 = %q / %q", tasks[0].Status, tasks[0].Blocked)
	}
}

// A run that failed used to drop its task back into Todo, where it looked
// exactly like one nobody had ever touched.
func TestATaskWhoseRunFailedIsBlockedNotTodo(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	run := &runlog.Run{
		ID: "r-1", ProjectID: id, TaskID: "T-001", Status: "failed",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, _ := runlog.NewWriter(dir, run)
	w.Close()
	s.RecoverRuns(context.Background())

	tasks, _ := s.TaskList(context.Background(), id)
	if tasks[0].Status != "blocked" {
		t.Errorf("T-001's run failed but the task is %q", tasks[0].Status)
	}
	if !strings.Contains(tasks[0].Blocked, "failed") {
		t.Errorf("the reason does not say the run failed: %q", tasks[0].Blocked)
	}

	// And it is not offered as the next thing to start: it needs a human to
	// read the run first, not another lap against the same wall.
	next, err := s.TaskNext(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if next != nil {
		t.Errorf("TaskNext offered %s while every task is blocked", next.ID)
	}
}

// The one moment the spine check can change a decision is before Accept, and
// that was the one moment it could not see.
//
// LoadSpine read only approved artifacts, so a check run while a proposal sat
// at its gate described the document the human had already accepted. The trace
// rail in the desktop sits beside the Accept button and was reporting on last
// week's plan.
func TestTraceCheckReadsTheProposalYouAreAboutToAccept(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — Login\n\n**Priority:** must\n",
		artifact.KindSpec:         "## SPEC-001 — Sessions\n\n**Implements:** REQ-001\n",
		artifact.KindPlan:         "## M-01 — Auth\n\n### T-001 — Tokens\n\n**Implements:** SPEC-001\n",
	})

	// The approved spine is clean.
	res, err := s.TraceCheck(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("the approved spine is not clean: %v", res.Errors)
	}
	if len(res.Proposed) != 0 {
		t.Errorf("nothing is proposed but the check claims %v", res.Proposed)
	}

	// A stage proposes a spec that implements a requirement nobody wrote.
	artifact.WriteProposal(dir, artifact.KindSpec, &artifact.Document{
		Sections: []artifact.Section{{
			ID: "SPEC-001", Title: "Sessions", Implements: []string{"REQ-404"},
		}},
	}, "r-1", nil)

	res, err = s.TraceCheck(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) == 0 {
		t.Fatal("the proposal breaks the spine and the check said nothing — " +
			"it read the approved spec instead")
	}
	// And it must say what it read, or a break in a pending document is
	// indistinguishable from a break in the accepted one.
	if len(res.Proposed) != 1 || res.Proposed[0] != "spec" {
		t.Errorf("proposed = %v, want [spec]", res.Proposed)
	}
}

// A stage that produced nothing must not blank the spine: substituting an empty
// proposal for an approved document would report every requirement orphaned and
// bury the real findings.
func TestAnEmptyProposalIsNotASubstitute(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — Login\n\n**Priority:** must\n",
		artifact.KindSpec:         "## SPEC-001 — Sessions\n\n**Implements:** REQ-001\n",
		artifact.KindPlan:         "## M-01 — Auth\n\n### T-001 — Tokens\n\n**Implements:** SPEC-001\n",
	})
	artifact.WriteProposal(dir, artifact.KindSpec, &artifact.Document{}, "r-1", nil)

	res, err := s.TraceCheck(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("an empty proposal blanked the spine: %v", res.Errors)
	}
	if len(res.Proposed) != 0 {
		t.Errorf("an empty proposal was reported as the checked document: %v", res.Proposed)
	}
}
