package service

import (
	"context"
	"strings"
	"testing"

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

	if _, err := s.BugMove(context.Background(), id, "B-001", "triaged"); err != nil {
		t.Fatal(err)
	}
	out, err := s.BugPromote(context.Background(), id, "B-001")
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
	s.BugMove(context.Background(), id, "B-001", "triaged")
	if _, err := s.BugPromote(context.Background(), id, "B-001"); err != nil {
		t.Fatal(err)
	}
	_, err := s.BugPromote(context.Background(), id, "B-001")
	if err == nil {
		t.Fatal("a bug was promoted twice")
	}
	if !strings.Contains(err.Error(), "already") {
		t.Errorf("the refusal is unclear: %v", err)
	}
}

func TestPromoteRefusesADecidedBug(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id := projectWithBugs(t, s, BugRequest{Title: "x"})
	if _, err := s.BugMove(context.Background(), id, "B-001", "wontfix"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugPromote(context.Background(), id, "B-001"); err == nil {
		t.Error("a wontfix bug was promoted")
	}
}

// Triage classifies bugs that are open. One already triaged is not asked about
// again, and a batch with nothing to do says so rather than starting a run.
func TestTriageRefusesWhenThereIsNothingToTriage(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id := projectWithBugs(t, s, BugRequest{Title: "x"})
	if _, err := s.BugMove(context.Background(), id, "B-001", "triaged"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugTriage(context.Background(), id); err == nil {
		t.Error("a triage run started with no untriaged bugs")
	}
}

// The check used to run after the task and the edge existed, so promoting an
// untriaged bug created both and then failed on the status move — leaving a
// task nobody asked for wired to a bug that had not moved.
func TestPromotingAnUntriagedBugCreatesNothing(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id := projectWithBugs(t, s, BugRequest{Title: "x"})

	_, err := s.BugPromote(context.Background(), id, "B-001")
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
