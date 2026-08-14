package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"encoding/json"
	"fmt"
	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
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

// A run asked to ADD one section replaced the whole spec with only that
// section — sixteen approved sections gone, promote silent, and the person
// learned days later from a model asking about a contract whose section no
// longer existed. The proposal's gate now states what accepting would erase.
func TestAProposalSaysWhatItWouldErase(t *testing.T) {
	current := []artifact.Section{{ID: "SPEC-001"}, {ID: "SPEC-002"}, {ID: "SPEC-003"}}
	proposed := []artifact.Section{{ID: "SPEC-017"}, {ID: "SPEC-002"}}
	got := missingSectionIDs(current, proposed)
	if len(got) != 2 || got[0] != "SPEC-001" || got[1] != "SPEC-003" {
		t.Errorf("removed = %v, want [SPEC-001 SPEC-003]", got)
	}
	// A pure addition erases nothing and warns about nothing.
	if extra := missingSectionIDs(current, append(current, artifact.Section{ID: "SPEC-018"})); len(extra) != 0 {
		t.Errorf("an addition reported removals: %v", extra)
	}
}

// A fresh project's tree is born knowing about virtualenvs and node_modules:
// accept commits the whole tree, and a .venv created before .gitignore was
// swept into a task commit — 2,010 files of it.
func TestAFreshProjectIgnoresCommonJunk(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	if _, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".venv/", "__pycache__/", "node_modules/", ".pytest_cache/"} {
		if !strings.Contains(string(data), want) {
			t.Errorf(".gitignore is missing %q", want)
		}
	}
}

// The subtler wipe: every section id survives while the bodies become
// placeholders — invisible to the id check, caught by the council's own
// reviewer, and landed with no warning of its own. Shrinkage is the tell.
func TestSectionBodySizeSeesThroughPlaceholders(t *testing.T) {
	full := []artifact.Section{
		{ID: "SPEC-001", Title: "Domain model", Body: strings.Repeat("real content ", 100)},
		{ID: "SPEC-002", Title: "Profiles", Body: strings.Repeat("more real content ", 100)},
	}
	gutted := []artifact.Section{
		{ID: "SPEC-001", Title: "Domain model", Body: "[Content remains unchanged]"},
		{ID: "SPEC-002", Title: "Profiles", Body: "[Content remains unchanged]"},
	}
	if ids := missingSectionIDs(full, gutted); len(ids) != 0 {
		t.Fatalf("the id check sees removals where there are none: %v", ids)
	}
	cur, prop := sectionsBodySize(full), sectionsBodySize(gutted)
	if !(cur > 500 && prop*100 < cur*60) {
		t.Errorf("the shrinkage guard would not fire: %d -> %d", cur, prop)
	}
}

// An older accepted run outranks a newer failure: the accepted work is in
// the tree, and a redundant rerun that died does not un-commit it. T-101
// read "blocked — the last run failed" over a build accepted one minute
// earlier, and its Now card offered redoing finished work.
func TestAnAcceptedRunOutranksALaterFailure(t *testing.T) {
	runs := []*runlog.Run{
		{ID: "r-3", TaskID: "T-101", Stage: "build", Status: "failed", Verdict: "ABORTED", StartedAt: "2026-08-12T01:57:30Z"},
		{ID: "r-2", TaskID: "T-101", Stage: "build", Status: "done", Verdict: "PASSED", Accepted: true, StartedAt: "2026-08-12T01:56:07Z"},
		{ID: "r-1", TaskID: "T-101", Stage: "test", Status: "done", Verdict: "PASSED", Accepted: true, StartedAt: "2026-08-12T01:53:45Z"},
	}
	status, blocked, _, _, _ := deriveTaskRunState(runs)
	if status["T-101"] != "accepted" {
		t.Errorf("status = %q, want accepted despite the newer aborted rerun", status["T-101"])
	}
	if blocked["T-101"] != "" {
		t.Errorf("blocked hint survived: %q", blocked["T-101"])
	}
}

// Review's light exit: a small change deserves tasks, not a redesign. The
// only doors were the full brief or disguising the enhancement as a bug —
// which is how bug boards everywhere rot. The amendment needs a plan to
// amend; without one, the refusal points at the brief.
func TestPlanExtendNeedsAPlanToAmend(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{})

	_, err := s.StageStart(context.Background(), id, StageRequest{
		Stage: "plan", Extend: "add a CSV export next to the JSON one",
	})
	if err == nil {
		t.Fatal("an amendment without a plan was accepted")
	}
	if !strings.Contains(err.Error(), "brief") {
		t.Errorf("the refusal does not point at the brief: %v", err)
	}
}

// The amendment's toll, computed: a task no spec section covers wears the
// marker; wired tasks and bug-born tasks do not; a spec-less project owes
// nothing because there is nothing to be behind.
func TestSpecDebtMarksOnlyTheUncovered(t *testing.T) {
	spec := map[string]bool{"SPEC-001": true}
	bugs := map[string]bool{"T-003": true}
	if taskSpecDebt("T-001", []string{"SPEC-001"}, spec, bugs) {
		t.Error("a wired task owes nothing")
	}
	if !taskSpecDebt("T-002", nil, spec, bugs) {
		t.Error("an uncovered task must wear the marker")
	}
	if taskSpecDebt("T-002", []string{"SPEC-999"}, spec, bugs) == false {
		t.Error("an invented section id is not coverage")
	}
	if taskSpecDebt("T-003", nil, spec, bugs) {
		t.Error("a bug's task traces to its report, not the spec")
	}
	if taskSpecDebt("T-004", nil, map[string]bool{}, bugs) {
		t.Error("a project with no spec owes none")
	}
}

// The settle prompt is assembled BY THE ENGINE from the debt itself — the
// person clicks, never writes. Its contract: honest as-built sections, a
// Covers: field per section, everything else untouched.
func TestTheSettleNoteNamesTheDebtAndTheContract(t *testing.T) {
	note := specSettleNote([]TaskView{
		{ID: "T-110", Title: "Weight indicator format", Body: "Fixes the sign."},
		{ID: "T-112", Title: "CSV export"},
	})
	for _, must := range []string{
		"WITHOUT redesigning",
		"T-110 — Weight indicator format",
		"T-112 — CSV export",
		"As-built:",
		"Covers:",
		"invent nothing aspirational",
		"exactly as it is",
		// The upward wiring: settle flows UP the spine — as-built sections
		// wire to the requirement that genuinely covers them, and what
		// cannot wire surfaces honestly as requirements-debt.
		"Wire UPWARD",
		"requirements-debt",
	} {
		if !strings.Contains(note, must) {
			t.Errorf("the note lost %q", must)
		}
	}
}

// The settle's other half: the accepted spec's Covers: fields wire the named
// tasks' Implements in the plan — mechanically, because a person accepted the
// document that declares the coverage. The marker comes off by consequence.
func TestAcceptedCoversFieldsWireThePlan(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(artifact.DocsDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := "## M-001 — Core\n\n### T-110 — Weight indicator\n\nFix the sign.\n\n### T-111 — Wired already\n\n**Implements:** SPEC-001\n\nDone.\n"
	spec := "## SPEC-001 — Snapshot\n\nShows weight.\n\n## SPEC-009 — Weight indicator format\n\n**As-built:** yes\n**Covers:** T-110\n\nSigned one-decimal pounds.\n"
	if err := os.WriteFile(artifact.Path(dir, artifact.KindPlan), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path(dir, artifact.KindSpec), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}

	wired := wireCoveredTasks(dir)
	if got := wired["T-110"]; len(got) != 1 || got[0] != "SPEC-009" {
		t.Fatalf("T-110 wired = %v, want [SPEC-009]", wired)
	}
	after, err := artifact.Load(dir, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	var t110 *artifact.Section
	for i := range after.Sections {
		for j := range after.Sections[i].Children {
			if after.Sections[i].Children[j].ID == "T-110" {
				t110 = &after.Sections[i].Children[j]
			}
		}
	}
	if t110 == nil || len(t110.Implements) != 1 || t110.Implements[0] != "SPEC-009" {
		t.Errorf("the plan on disk does not carry the wiring: %+v", t110)
	}
	// Idempotent: promoting the same spec twice adds nothing twice.
	if again := wireCoveredTasks(dir); again != nil {
		t.Errorf("a second promotion re-wired: %v", again)
	}
}

// The spec documents what EXISTS. An amendment task still todo wears its
// debt on the board, but the settle refuses to write it up as as-built
// behaviour nobody built — it settles after its build is accepted.
func TestUnbuiltDebtDoesNotSettle(t *testing.T) {
	spec := map[string]bool{"SPEC-001": true}
	none := map[string]bool{}
	if !taskSpecDebt("T-115", nil, spec, none) {
		t.Fatal("precondition: T-115 wears debt")
	}
	// The service-level filter is status-based; pin the counter the guide
	// uses so "teach the spec what was built" never counts the unbuilt.
	if n := specDebtCount([]TaskView{
		{ID: "T-115", SpecDebt: true, Status: "todo"},
		{ID: "T-110", SpecDebt: true, Status: "accepted"},
	}); n != 1 {
		t.Errorf("settleable debt = %d, want only the accepted one", n)
	}
}

// The intake that died at 12/12 had no way to be launched with more calls.
// The stage request now carries its own cap — recorded on the run so the
// budget card shows the number instead of "default".
func TestAStageCarriesItsOwnCallCap(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	run, err := s.StageStart(context.Background(), id, StageRequest{
		Stage: "plan", Extend: "small change", AgentTurns: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.AgentTurns != 30 {
		t.Errorf("agent_turns = %d, want the request's 30 on the record", run.AgentTurns)
	}
	s.RunAbort(context.Background(), run.ID)
	s.waitForRun(context.Background(), run.ID)
}

// The declared-fallback door: provider weather paused a spec mid-draft, the
// person clicked once, and the run resumed with its seats on the stand-in —
// recorded, never a router's silent choice. The stage's persisted request
// must carry the swap too, or resume would re-resolve the line-up from
// config and quietly undo it.
func TestReseatSwapsTheSeatsAndResumes(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	run, err := s.StageStart(context.Background(), id, StageRequest{
		Stage: "plan", Extend: "small change", Ducklings: []string{"pato-uno"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	// Wait for the roster, then simulate the weather pause.
	deadline := time.Now().Add(5 * time.Second)
	for len(rs.run.Roster) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	s.RunAbort(context.Background(), run.ID)
	s.waitForRun(context.Background(), run.ID)
	rs.run.Status = "paused"
	rs.run.PendingKind = "provider"
	rs.run.Failure = "provider unavailable: timeout"

	out, err := s.RunReseat(context.Background(), run.ID, "pato-uno", "pato-dos")
	if err != nil {
		t.Fatal(err)
	}
	if out.Roster["architect"] != "pato-dos" {
		t.Errorf("architect = %q, want the fallback pato-dos", out.Roster["architect"])
	}
	sreq, ok := loadStageRequest(rs.runDir)
	if !ok || len(sreq.Ducklings) == 0 || sreq.Ducklings[0] != "pato-dos" {
		t.Errorf("the persisted request does not carry the swap: %v", sreq.Ducklings)
	}
	found := false
	for _, line := range readEventTypes(t, rs.runDir) {
		if line == "seat_failover" {
			found = true
		}
	}
	if !found {
		t.Error("no seat_failover on the record — an unrecorded swap is a router")
	}
	s.RunAbort(context.Background(), run.ID)
	s.waitForRun(context.Background(), run.ID)
}

func readEventTypes(t *testing.T, runDir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(runDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var e struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, e.Type)
		}
	}
	return out
}

// A document that does not fit its author's output cap is a settings problem
// wearing a run's clothes: the pause keeps the run alive so the person can
// raise max_tokens — or reseat — and resume replays with the fresh config.
// Failing it lost the draft AND the fix, twice in one night.
func TestATruncatedDocumentPausesInsteadOfDying(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	_ = dir

	run, err := s.StageStart(context.Background(), id, StageRequest{
		Stage: "plan", Extend: "small change",
	})
	if err != nil {
		t.Fatal(err)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	<-rs.done // let the real execution settle first

	// The failure the field produced, replayed through the same door.
	rs.run.Status = "running"
	rs.run.Verdict = ""
	rs.done = make(chan struct{})
	s.failRun(rs, fmt.Errorf("%w: the whole document did not fit in pato-uno's output cap (20000 tokens)", agent.ErrTruncated))

	if rs.run.Status != "paused" || rs.run.PendingKind != "error" {
		t.Fatalf("truncation must pause resumable, got %s/%s", rs.run.Status, rs.run.PendingKind)
	}
	if !strings.Contains(rs.run.Failure, "resume") {
		t.Errorf("the pause does not name the way out: %q", rs.run.Failure)
	}
	s.RunAbort(context.Background(), run.ID)
}

// The engine knows the prompt AND every seat's window before a token is
// paid: a stage whose opening prompt eats a small local seat's context was a
// predictable loop — predicted now, at the door. Warned when cramped,
// refused when impossible, silent about seats that never declared a window.
func TestContextFitSpeaksAtTheDoor(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	small, err := s.ducklings.Get("pato-uno")
	if err != nil {
		t.Fatal(err)
	}
	small.Caps.ContextTokens = 32768
	big, _ := s.ducklings.Get("pato-dos")
	big.Caps.ContextTokens = 0 // never declared: skipped, not guessed

	seats := []config.DucklingID{"pato-uno", "pato-dos"}

	// Comfortable: ~2k tokens in a 32k seat says nothing.
	if w, f := s.contextFitNotes(8_000, seats); len(w) != 0 || f != "" {
		t.Errorf("comfortable fit complained: %v %q", w, f)
	}
	// Cramped: ~16k tokens is half the window — warned, with the chip named.
	w, f := s.contextFitNotes(64_000, seats)
	if f != "" || len(w) != 1 {
		t.Fatalf("cramped fit = warns %v fatal %q, want one warning", w, f)
	}
	if !strings.Contains(w[0], "pato-uno") || !strings.Contains(w[0], "chip") {
		t.Errorf("the warning does not name the seat and the lever: %q", w[0])
	}
	// Impossible: ~30k tokens in a 32k window — refused before spending.
	if _, f := s.contextFitNotes(120_000, seats); !strings.Contains(f, "cannot meaningfully work") {
		t.Errorf("an impossible fit was not refused: %q", f)
	}
}
