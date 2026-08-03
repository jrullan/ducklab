package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/store"
)

// A run against a task id the project no longer knows used to start fine and
// hand the implementer a one-line prompt — "Implement task T-048" — with no
// title, no body and no bug report. The relaunch panel on an old failed run
// offers exactly this trap, because its task can have been removed since.
func TestARunAgainstAGhostTaskRefusesToStart(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(artifact.Path(dir, artifact.KindPlan)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path(dir, artifact.KindPlan),
		[]byte("## M-001 — First\n\n### T-001 — Real work\n\nDo it.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = s.RunStart(context.Background(), p.ID, RunRequest{TaskID: "T-048", Mode: "solo"})
	if err == nil {
		t.Fatal("a run against a task that exists nowhere was allowed to start")
	}
	if !strings.Contains(err.Error(), "T-048") {
		t.Errorf("the refusal does not name the task: %v", err)
	}
}

// The plan and the database can disagree — that is the wreckage a half-done
// removal leaves — and the database still holds the promoted task's title and
// body. The prompt must be built from that rather than degrading to one line.
func TestFindTaskFallsBackToTheDatabase(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — A\n\n**Priority:** must\n",
		artifact.KindPlan:         "## M-001 — First\n\n### T-001 — Real work\n\nDo it.\n",
	})
	db, err := s.openProjectDB(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(&store.Task{
		ID: "T-048", Title: "Fix edge-length editing", Status: "todo",
		Body: "Fixes B-007.\n\n## Reported\n\nvalid AB length triggers Impossible Geometry",
	}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	got := s.findTask(context.Background(), id, "T-048")
	if got == nil {
		t.Fatal("a task the database knows was reported as nonexistent")
	}
	if !strings.Contains(got.Body, "Impossible Geometry") {
		t.Errorf("the fallback lost the body: %q", got.Body)
	}
}

// Removal edits two records that must move together: the plan section and the
// database row with its bug pointer. The database half used to be best-effort
// — an open failure returned success after editing only the plan, leaving the
// task alive in the database and its bug stuck in_progress, unpromotable.
func TestTaskRemoveRefusesWhenTheDatabaseIsUnreachable(t *testing.T) {
	s := writableService(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — A\n\n**Priority:** must\n",
		artifact.KindPlan:         "## M-001 — First\n\n### T-001 — Real work\n\nDo it.\n",
	})
	// A directory where the database file should be: every open fails.
	dbPath := filepath.Join(dir, ".ducklab", "ducklab.db")
	os.RemoveAll(dbPath)
	if err := os.MkdirAll(dbPath, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := s.TaskRemove(context.Background(), id, "T-001")
	if err == nil {
		t.Fatal("removal proceeded with the database unreachable")
	}
	plan, lErr := artifact.Load(dir, artifact.KindPlan)
	if lErr != nil {
		t.Fatal(lErr)
	}
	for _, m := range plan.Sections {
		for _, c := range m.Children {
			if c.ID == "T-001" {
				return // still in the plan: nothing moved, which is the point
			}
		}
	}
	t.Error("the plan lost the task even though the removal was refused")
}

// The operational state — the live SQLite database, its WAL, the run logs —
// must never be tracked: the engine branches and checks out on every accept,
// and a checkout that rewrote a write-ahead log under the engine's open
// connection would corrupt the database. ProjectInit has always written this
// exclusion; pinned here because nothing else fails if it quietly stops.
func TestProjectInitIgnoresTheOperationalState(t *testing.T) {
	s := writableService(t, "pato-uno")
	dir := t.TempDir()
	if _, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("no .gitignore written: %v", err)
	}
	for _, must := range []string{".ducklab/ducklab.db", ".ducklab/ducklab.db-wal", ".ducklab/runs/"} {
		if !strings.Contains(string(data), must) {
			t.Errorf(".gitignore does not exclude %s", must)
		}
	}
}

// T-023 depended on T-022, T-022 had never been accepted — and T-023 ran
// three times and got ACCEPTED. The plan's dependency was display only: the
// board said "waiting on T-022" and offered Run anyway, and RunStart never
// looked. The person reading the board reasonably concluded T-022 must have
// been removed, because how else could T-023 have started?
func TestADependencyBlockedTaskCannotStart(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(artifact.Path(dir, artifact.KindPlan)), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := "## M-001 — First\n\n" +
		"### T-022 — Browser compatibility\n\nTest it.\n\n" +
		"### T-023 — Ship it\n\n**Depends on:** T-022\n\nAfterwards.\n"
	if err := os.WriteFile(artifact.Path(dir, artifact.KindPlan), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}

	// The engine's stated actions already withhold run.
	tv := s.findTask(context.Background(), p.ID, "T-023")
	if tv == nil {
		t.Fatal("T-023 not found")
	}
	if tv.Status != "blocked" {
		t.Fatalf("status = %q, want blocked", tv.Status)
	}
	for _, action := range tv.Next {
		if action == "run" || action == "test_first" {
			t.Errorf("a dependency-blocked task offers %q", action)
		}
	}

	// And the engine holds its own door when asked anyway.
	_, err = s.RunStart(context.Background(), p.ID, RunRequest{TaskID: "T-023", Mode: "solo"})
	if err == nil {
		t.Fatal("a run started against unmet dependencies")
	}
	if !strings.Contains(err.Error(), "T-022") {
		t.Errorf("the refusal does not name what it waits on: %v", err)
	}

	// A retry after a failure is a different blocked: run stays offered. The
	// unblocked dependency itself proves it — T-022 is plain todo.
	dep := s.findTask(context.Background(), p.ID, "T-022")
	found := false
	for _, action := range dep.Next {
		if action == "run" {
			found = true
		}
	}
	if !found {
		t.Error("T-022, with nothing to wait on, is not offered run")
	}
}
