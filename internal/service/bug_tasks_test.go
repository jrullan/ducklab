package service

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/bug"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/store"
)

// splitBugFixture plants a bug promoted as a split into T-001 + T-002 — the
// shape of TI-36X B-003 (T-014 + T-015) — with the audit trail promote leaves.
func splitBugFixture(t *testing.T) (*Service, string, string) {
	t.Helper()
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "two halves of one report", Severity: "high"}); err != nil {
		t.Fatal(err)
	}
	db, err := s.openProjectDB(id)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rec, _ := db.GetBug("B-001")
	rec.Status = "in_progress"
	rec.TaskID = "T-001"
	rec.Proposal = `[{"title":"first half","acceptance":["a"],"owns":["a.go"]},{"title":"second half","acceptance":["b"],"owns":["b.go"]}]`
	if err := db.UpdateBug(rec); err != nil {
		t.Fatal(err)
	}
	for _, taskID := range []string{"T-001", "T-002"} {
		if err := db.CreateTask(&store.Task{ID: taskID, Title: taskID, Status: "todo"}); err != nil {
			t.Fatal(err)
		}
		if err := db.AddTrace("bug", "B-001", "task", taskID); err != nil {
			t.Fatal(err)
		}
	}
	appendBugAudit(dir, bug.AuditEntry{Bug: "B-001", From: "open", To: "triaged", Actor: "human", Via: "triage"})
	appendBugAudit(dir, bug.AuditEntry{Bug: "B-001", From: "triaged", To: "in_progress", Actor: "human", Via: "promote", Note: "T-001"})
	return s, id, dir
}

func plantRun(t *testing.T, s *Service, dir string, run *runlog.Run) {
	t.Helper()
	run.StartedAt = time.Now().UTC().Format(time.RFC3339)
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func listedBug(t *testing.T, s *Service, projectID, bugID string) bug.Bug {
	t.Helper()
	bugs, err := s.BugList(context.Background(), projectID, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bugs {
		if b.ID == bugID {
			return b
		}
	}
	t.Fatalf("%s not listed", bugID)
	return bug.Bug{}
}

// TI-36X B-003: T-014 accepted, T-015 paused at its gate. Now read task_id
// alone, saw the accepted half with no run, called the report "reopened" and
// offered to rerun it. The list must carry both halves with the status the
// runs give them, and must not call a never-reopened report reopened.
func TestBugListCarriesEverySplitTaskWithItsRunStatus(t *testing.T) {
	s, id, dir := splitBugFixture(t)
	plantRun(t, s, dir, &runlog.Run{ID: "r-first", ProjectID: id, TaskID: "T-001", Stage: "build", Status: "done", Verdict: "PASSED", Accepted: true})
	plantRun(t, s, dir, &runlog.Run{ID: "r-second", ProjectID: id, TaskID: "T-002", Stage: "build", Status: "paused", Verdict: "PASSED", PendingKind: "gate"})

	b := listedBug(t, s, id, "B-001")
	want := []bug.Task{{ID: "T-001", Status: "accepted", Current: true}, {ID: "T-002", Status: "review", Current: true}}
	if !reflect.DeepEqual(b.Tasks, want) {
		t.Fatalf("tasks = %+v, want %+v", b.Tasks, want)
	}
	if b.Reopened {
		t.Fatal("a report only ever triaged and promoted was listed as reopened")
	}
}

// The second half with no run yet: its status must say todo, so a client can
// offer THAT task rather than the accepted one.
func TestBugListShowsTheUnstartedHalfOfASplitAsTodo(t *testing.T) {
	s, id, dir := splitBugFixture(t)
	plantRun(t, s, dir, &runlog.Run{ID: "r-first", ProjectID: id, TaskID: "T-001", Stage: "build", Status: "done", Verdict: "PASSED", Accepted: true})

	b := listedBug(t, s, id, "B-001")
	want := []bug.Task{{ID: "T-001", Status: "accepted", Current: true}, {ID: "T-002", Status: "todo", Current: true}}
	if !reflect.DeepEqual(b.Tasks, want) {
		t.Fatalf("tasks = %+v, want %+v", b.Tasks, want)
	}
}

// A real reopen: the person sent the fixed report back. The engine refuses
// new work until it is triaged again, so the list must say reopened, and the
// consumed tasks stay as provenance but are not current.
func TestBugListMarksARealReopenAndRetiresItsConsumedTasks(t *testing.T) {
	s, id, _ := splitBugFixture(t)
	db, err := s.openProjectDB(id)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := db.GetBug("B-001")
	rec.Status = "fixed"
	db.UpdateBug(rec)
	db.Close()
	moved, err := s.BugMove(context.Background(), id, "B-001", "in_progress", "human")
	if err != nil {
		t.Fatal(err)
	}
	if !moved.Reopened {
		t.Fatal("the reopen's own response does not say the report was reopened")
	}

	b := listedBug(t, s, id, "B-001")
	if !b.Reopened || !b.NeedsTriage {
		t.Fatalf("reopened=%v needs_triage=%v, want both true", b.Reopened, b.NeedsTriage)
	}
	for _, task := range b.Tasks {
		if task.Current {
			t.Fatalf("consumed task %s listed as current work of a reopened report", task.ID)
		}
	}
	if len(b.Tasks) != 2 {
		t.Fatalf("tasks = %+v, want both old halves kept as provenance", b.Tasks)
	}
}

// After a reopen, a fresh promotion answers the person's "still broken"; the
// new task is the current one and the report is no longer reopened.
func TestBugListAfterReopenAndRepromotionTracksOnlyTheNewTask(t *testing.T) {
	s, id, dir := splitBugFixture(t)
	db, err := s.openProjectDB(id)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := db.GetBug("B-001")
	rec.Status = "in_progress"
	rec.TaskID = "T-003"
	db.UpdateBug(rec)
	db.CreateTask(&store.Task{ID: "T-003", Title: "new fix", Status: "todo"})
	db.AddTrace("bug", "B-001", "task", "T-003")
	db.Close()
	appendBugAudit(dir, bug.AuditEntry{Bug: "B-001", From: "fixed", To: "triaged", Actor: "human", Via: "reopen"})
	appendBugAudit(dir, bug.AuditEntry{Bug: "B-001", From: "triaged", To: "triaged", Actor: "engine", Via: "retriage"})
	appendBugAudit(dir, bug.AuditEntry{Bug: "B-001", From: "triaged", To: "in_progress", Actor: "human", Via: "promote", Note: "T-003"})

	b := listedBug(t, s, id, "B-001")
	if b.Reopened {
		t.Fatal("a reopened report re-promoted into new work is still listed as reopened")
	}
	var current []string
	for _, task := range b.Tasks {
		if task.Current {
			current = append(current, task.ID)
		}
	}
	if !reflect.DeepEqual(current, []string{"T-003"}) {
		t.Fatalf("current tasks = %v, want [T-003]", current)
	}
}

func TestReopenedSinceFixReadsTheLatestReopenAgainstPromotion(t *testing.T) {
	e := func(via string) bug.AuditEntry { return bug.AuditEntry{Via: via} }
	cases := []struct {
		name    string
		history []bug.AuditEntry
		want    bool
	}{
		{"no history", nil, false},
		{"triaged and promoted only (TI-36X B-003)", []bug.AuditEntry{e("triage"), e("promote")}, false},
		{"accepted then reopened", []bug.AuditEntry{e("promote"), e("task-accepted"), e("reopen")}, true},
		{"reopened then re-triaged, not yet promoted", []bug.AuditEntry{e("promote"), e("reopen"), e("retriage")}, true},
		{"reopened then promoted again", []bug.AuditEntry{e("promote"), e("reopen"), e("retriage"), e("promote")}, false},
		{"reopened a second time", []bug.AuditEntry{e("promote"), e("reopen"), e("promote"), e("reopen")}, true},
	}
	for _, c := range cases {
		if got := reopenedSinceFix(c.history); got != c.want {
			t.Errorf("%s: reopenedSinceFix = %v, want %v", c.name, got, c.want)
		}
	}
}
