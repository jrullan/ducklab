package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/bug"
)

func projectWithBugs(t *testing.T, s *Service, bugs ...BugRequest) string {
	t.Helper()
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bugs {
		if _, err := s.BugAdd(context.Background(), p.ID, b); err != nil {
			t.Fatal(err)
		}
	}
	return p.ID
}

func TestBugAddNumbersAndDefaults(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id := projectWithBugs(t, s,
		BugRequest{Title: "first"},
		BugRequest{Title: "second", Severity: "critical"})

	bugs, err := s.BugList(context.Background(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(bugs) != 2 {
		t.Fatalf("bugs = %d", len(bugs))
	}
	// Worst first, so the critical one leads regardless of when it arrived.
	if bugs[0].Title != "second" || bugs[0].Severity != bug.Critical {
		t.Errorf("order = %+v", bugs)
	}
	// A severity nobody gave defaults to normal rather than to nothing.
	var first bug.Bug
	for _, b := range bugs {
		if b.Title == "first" {
			first = b
		}
	}
	if first.Severity != bug.Normal || first.Status != bug.Open || first.ID != "B-001" {
		t.Errorf("first = %+v", first)
	}
}

func TestBugAddRefusesWhatItCannotFile(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id := projectWithBugs(t, s)
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "   "}); err == nil {
		t.Error("a bug with no title was filed")
	}
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "x", Severity: "spicy"}); err == nil {
		t.Error("an unknown severity was accepted")
	}
}

// The task's body carries the report verbatim: a fix written from a summary is
// a fix for the summary, and reproduction steps are what paraphrase loses.
func TestPromoteCarriesTheReportAndLinksIt(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id := projectWithBugs(t, s, BugRequest{
		Title: "Login loops", Body: "1. open /login\n2. submit\n3. it returns to /login"})

	if _, err := s.BugMove(context.Background(), id, "B-001", "triaged", "human"); err != nil {
		t.Fatal(err)
	}
	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := out["task"].(string)
	if taskID == "" {
		t.Fatal("no task was created")
	}

	db, err := s.openProjectDB(id)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	task, err := db.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	_ = task
	if !strings.Contains(task.Body, "3. it returns to /login") {
		t.Errorf("the reproduction steps did not survive: %q", task.Body)
	}
	if !strings.Contains(task.Body, "B-001") {
		t.Error("the task does not say which bug it fixes")
	}

	// The edge is what puts the bug in the same graph as everything else.
	edges, err := db.TracesFrom("bug", "B-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || edges[0] != "task:"+taskID {
		t.Errorf("edges = %v, want task:%s", edges, taskID)
	}

	b, _ := db.GetBug("B-001")
	if b.Status != string(bug.InProgress) || b.TaskID != taskID {
		t.Errorf("the bug did not move with its task: %+v", b)
	}
}

// Two tasks for one report splits the work and leaves both halves looking
// unfinished.
func TestPromoteRefusesToDoItTwice(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id := projectWithBugs(t, s, BugRequest{Title: "x"})
	s.BugMove(context.Background(), id, "B-001", "triaged", "human")
	if _, err := s.BugPromote(context.Background(), id, "B-001", "human"); err != nil {
		t.Fatal(err)
	}
	_, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err == nil {
		t.Fatal("a bug was promoted twice")
	}
	if !strings.Contains(err.Error(), "already") {
		t.Errorf("the refusal is unclear: %v", err)
	}
}

func TestReopenFixedBugClearsConsumedTaskAndCanPromoteAgain(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id := projectWithBugs(t, s, BugRequest{Title: "the fix did not work"})
	ctx := context.Background()
	entry, _ := s.registry.Get(id)
	if err := os.WriteFile(filepath.Join(entry.Path, "fix.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ApplyTriage(ctx, id, []map[string]interface{}{{
		"bug": "B-001",
		"proposal": []map[string]interface{}{{
			"title": "first fix", "acceptance": []string{"the old literal slice is true"}, "owns": []string{"fix.go"},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	first, err := s.BugPromote(ctx, id, "B-001", "human")
	if err != nil {
		t.Fatal(err)
	}
	firstTask := first["task"].(string)
	if fixed, err := s.BugFixedByTask(ctx, id, firstTask); err != nil || fixed != "B-001" {
		t.Fatal(err)
	}

	reopened, err := s.BugMove(ctx, id, "B-001", "in_progress", "human")
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status != bug.Triaged || reopened.TaskID != "" || !reopened.NeedsTriage {
		t.Fatalf("reopened bug = %+v, want triaged with no current task", reopened)
	}
	if len(reopened.History) == 0 || !strings.Contains(reopened.History[len(reopened.History)-1].Note, firstTask) {
		t.Fatalf("reopen history does not preserve %s: %+v", firstTask, reopened.History)
	}

	if _, err := s.BugPromote(ctx, id, "B-001", "human"); err == nil || !strings.Contains(err.Error(), "triage it again") {
		t.Fatalf("stale contract was promotable after reopen: %v", err)
	}
	evidence, err := s.BugAdd(ctx, id, BugRequest{Title: "compositor echo exits immediately", Body: "is_local flips to false on the compositor echo"})
	if err != nil {
		t.Fatal(err)
	}
	evidenceDB, err := s.openProjectDB(id)
	if err != nil {
		t.Fatal(err)
	}
	evidenceRec, err := evidenceDB.GetBug(evidence.ID)
	if err != nil {
		t.Fatal(err)
	}
	evidenceRec.Status = string(bug.Duplicate)
	evidenceRec.DuplicateOf = "B-001"
	if err := evidenceDB.UpdateBug(evidenceRec); err != nil {
		t.Fatal(err)
	}
	evidenceDB.Close()
	if _, err := s.ApplyTriage(ctx, id, []map[string]interface{}{{
		"bug": "B-001",
		"proposal": []map[string]interface{}{{
			"title": "second fix", "acceptance": []string{"new evidence is observed on the failing path"}, "owns": []string{"fix.go"},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	second, err := s.BugPromoteWithNote(ctx, id, "B-001", "human", "observe the compositor echo before releasing the hold")
	if err != nil {
		t.Fatal(err)
	}
	if second["task"] == firstTask {
		t.Fatalf("re-promote reused consumed task %s", firstTask)
	}
	listed, err := s.BugList(ctx, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed[0].History) < 2 {
		t.Fatalf("prior task history was lost: %+v", listed[0].History)
	}
	db, err := s.openProjectDB(id)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	secondTask, err := db.GetTask(second["task"].(string))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{reopenEvidenceHeading, firstTask, "new evidence is observed", evidence.ID, "is_local flips to false", "observe the compositor echo"} {
		if !strings.Contains(secondTask.Body, want) {
			t.Errorf("rework task misses %q:\n%s", want, secondTask.Body)
		}
	}
	if strings.Contains(secondTask.Body, "old literal slice") {
		t.Errorf("rework task reused the consumed contract:\n%s", secondTask.Body)
	}
}

func TestTriageDoesNotRetireReportAsDuplicateOfFixedBug(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id := projectWithBugs(t, s,
		BugRequest{Title: "original"},
		BugRequest{Title: "fix does not work"},
	)
	ctx := context.Background()
	if _, err := s.BugMove(ctx, id, "B-001", "triaged", "human"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugMove(ctx, id, "B-001", "in_progress", "human"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugMove(ctx, id, "B-001", "fixed", "human"); err != nil {
		t.Fatal(err)
	}

	_, err := s.ApplyTriage(ctx, id, []map[string]interface{}{{
		"bug": "B-002", "duplicate_of": "B-001", "reason": "same symptom",
	}})
	if err != nil {
		t.Fatal(err)
	}
	db, err := s.openProjectDB(id)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := db.GetBug("B-002")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != string(bug.Triaged) || got.DuplicateOf != "" {
		t.Fatalf("report disappeared behind fixed duplicate: %+v", got)
	}
	if !strings.Contains(got.TriageReason, "B-001") || !strings.Contains(got.TriageReason, "fixed") {
		t.Fatalf("triage reason does not explain the refused duplicate: %q", got.TriageReason)
	}
}

func TestRetriagePromptCarriesReopenAndLinkedEvidence(t *testing.T) {
	reopened := bug.Bug{
		ID: "B-017", Title: "clipboard exits early", Severity: bug.High,
		Status: bug.Triaged, NeedsTriage: true,
		History: []bug.AuditEntry{{Via: "reopen", Note: "reopened after T-030 was accepted"}},
	}
	linked := bug.Bug{
		ID: "B-020", Title: "compositor echo", Body: "is_local flips to false",
		Status: bug.Duplicate, DuplicateOf: "B-017",
	}
	prompt := triagePrompt(reopened, []bug.Bug{reopened, linked}, "T-030 old task\n\n- old literal slice")
	for _, want := range []string{"Reopen evidence", "T-030", "B-020", "is_local flips", "Previous consumed task contract", "old literal slice", "Write new acceptance slices"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("retriage prompt misses %q:\n%s", want, prompt)
		}
	}
}

func TestPromoteRefusesADecidedBug(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id := projectWithBugs(t, s, BugRequest{Title: "x"})
	if _, err := s.BugMove(context.Background(), id, "B-001", "wontfix", "human"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugPromote(context.Background(), id, "B-001", "human"); err == nil {
		t.Error("a wontfix bug was promoted")
	}
}

// Triage classifies bugs that are open. One already triaged is not asked about
// again, and a batch with nothing to do says so rather than starting a run.
func TestTriageRefusesWhenThereIsNothingToTriage(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id := projectWithBugs(t, s, BugRequest{Title: "x"})
	if _, err := s.BugMove(context.Background(), id, "B-001", "triaged", "human"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugTriage(context.Background(), id, ""); err == nil {
		t.Error("a triage run started with no untriaged bugs")
	}
}

// The check used to run after the task and the edge existed, so promoting an
// untriaged bug created both and then failed on the status move — leaving a
// task nobody asked for wired to a bug that had not moved.
func TestPromotingAnUntriagedBugCreatesNothing(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id := projectWithBugs(t, s, BugRequest{Title: "x"})

	_, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err == nil {
		t.Fatal("an untriaged bug was promoted")
	}
	if !strings.Contains(err.Error(), "triage") {
		t.Errorf("the refusal does not say what to do: %v", err)
	}

	db, _ := s.openProjectDB(id)
	defer db.Close()
	tasks, _ := db.ListTasks()
	if len(tasks) != 0 {
		t.Errorf("%d task(s) were created by a refused promote", len(tasks))
	}
	edges, _ := db.TracesFrom("bug", "B-001")
	if len(edges) != 0 {
		t.Errorf("an edge was left behind: %v", edges)
	}
}

// Tasks live in docs/plan.md — it is what `task list`, the board and
// `ducklab run` all read. A sequence that knew only the bug table handed out
// T-001 in a project whose plan already had T-001 through T-010: the promoted
// task was invisible to every command, and the one the CLI told you to run was
// a different task with the same name.
func TestPromotedTaskJoinsThePlanWithAFreeID(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "Login loops"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugMove(context.Background(), id, "B-001", "triaged", "human"); err != nil {
		t.Fatal(err)
	}

	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := out["task"].(string)

	tasks, err := s.TaskList(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	var promoted bool
	for _, task := range tasks {
		seen[task.ID]++
		if task.ID == taskID {
			promoted = true
			if task.Title != "Login loops" {
				t.Errorf("the promoted task reads %q", task.Title)
			}
		}
	}
	if !promoted {
		t.Errorf("the promoted task %s is not in the plan: %v", taskID, seen)
	}
	for tid, n := range seen {
		if n > 1 {
			t.Errorf("%s appears %d times; the id collided", tid, n)
		}
	}
}

// The bugs milestone gets a real id. The first version used "M-BUGS", which is
// not an id this project's own parser accepts — ids are PREFIX-<digits> — so
// the heading was silently unrecognised and the task under it was read as a
// child of whatever milestone came before.
func TestThePromotedTaskGetsItsOwnMilestone(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	s.BugAdd(context.Background(), id, BugRequest{Title: "Login loops"})
	s.BugMove(context.Background(), id, "B-001", "triaged", "human")
	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := out["task"].(string)

	tasks, _ := s.TaskList(context.Background(), id)
	var milestone string
	for _, task := range tasks {
		if task.ID == taskID {
			milestone = task.Milestone
		}
	}
	if milestone == "" {
		t.Fatalf("%s has no milestone; its heading was not recognised", taskID)
	}
	// It must not have been absorbed into a milestone that already existed.
	for _, task := range tasks {
		if task.ID != taskID && task.Milestone == milestone {
			t.Errorf("%s landed in %s alongside %s, an existing milestone", taskID, milestone, task.ID)
		}
	}
}

// The button inside ONE bug's panel used to fire the whole batch — a panel
// acting far beyond its own context. Naming a bug triages exactly that bug;
// naming a non-open one is an error, not a silent batch.
func TestTriageCanTargetOneBug(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	ctx := context.Background()
	b1, err := s.BugAdd(ctx, projectID, BugRequest{Title: "one", Severity: "normal"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugAdd(ctx, projectID, BugRequest{Title: "two", Severity: "normal"}); err != nil {
		t.Fatal(err)
	}

	run, err := s.BugTriage(ctx, projectID, b1.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.waitForRun(ctx, run.ID)
	// Only the named bug rode the run: the record's own run_start says how
	// many reports were taken.
	entry, _ := s.registry.Get(projectID)
	data, err := os.ReadFile(filepath.Join(entry.Path, ".ducklab", "runs", run.ID, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	took := -1
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var e map[string]interface{}
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if e["type"] == "run_start" {
			if d, ok := e["data"].(map[string]interface{}); ok {
				if n, ok := d["bugs"].(float64); ok {
					took = int(n)
				}
			}
		}
	}
	if took != 1 {
		t.Errorf("targeted triage took %d bugs, want exactly the named one", took)
	}

	if _, err := s.BugTriage(ctx, projectID, "B-999"); err == nil {
		t.Error("triaging a ghost bug id did not error")
	}
}
