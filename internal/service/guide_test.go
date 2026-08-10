package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/bug"
	"github.com/jrullan/ducklab/internal/runlog"
)

func ids(steps []NextStep) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.ID
	}
	return out
}

// The guide walks the document pipeline in the loop's own order, one gate at
// a time — a new user is never shown a step whose prerequisites do not exist.
func TestTheGuideWalksTheDocumentPipeline(t *testing.T) {
	cases := []struct {
		name string
		st   projectSnapshot
		want string
	}{
		{"empty project", projectSnapshot{}, "intake"},
		{"requirements only", projectSnapshot{HasRequirements: true}, "spec"},
		{"spec with open work", projectSnapshot{HasRequirements: true, HasSpec: true, OpenSpecSections: 3}, "plan"},
	}
	for _, c := range cases {
		steps := nextSteps(c.st)
		if len(steps) != 1 || steps[0].ID != c.want {
			t.Errorf("%s: steps = %v, want exactly [%s]", c.name, ids(steps), c.want)
		}
		if steps[0].Reason == "" {
			t.Errorf("%s: a step with no reason is an order, not guidance", c.name)
		}
	}
	// Outcome language leads; the harness term follows. New users do not
	// share the vocabulary yet (intake, spec…), and a guide that speaks only
	// jargon re-creates the mental load it exists to remove.
	first := nextSteps(projectSnapshot{})[0]
	if !strings.Contains(first.Action, "Describe what you want to build") {
		t.Errorf("intake step leads with jargon: %q", first.Action)
	}
}

// Work already paid for outranks everything: a paused run waits on one click,
// and sending a new user off to start new work while a question sits
// unanswered teaches them to ignore the inbox.
func TestPausedRunsOutrankEverything(t *testing.T) {
	st := projectSnapshot{
		HasRequirements: true, HasSpec: true, HasPlan: true,
		Tasks:  []TaskView{{ID: "T-001", Status: "todo"}},
		Paused: []*runlog.Run{{ID: "r-q", Stage: "spec", Status: "paused", PendingKind: "question"}},
	}
	steps := nextSteps(st)
	if steps[0].ID != "answer-run" || steps[0].Ref != "r-q" {
		t.Fatalf("first step = %+v, want the paused run's question", steps[0])
	}
}

// One task, not the backlog: the guide picks the next buildable — test-ready
// first, else the first todo whose dependencies are all accepted. The board
// already lists everything; a guide that does too is a second board.
func TestTheGuideSuggestsOneBuildableTask(t *testing.T) {
	st := projectSnapshot{
		HasRequirements: true, HasSpec: true, HasPlan: true,
		Tasks: []TaskView{
			{ID: "T-001", Status: "accepted"},
			{ID: "T-002", Status: "todo", DependsOn: []string{"T-009"}}, // dep not accepted
			{ID: "T-003", Status: "todo", DependsOn: []string{"T-001"}},
			{ID: "T-004", Status: "todo"},
		},
	}
	steps := nextSteps(st)
	if len(steps) != 1 || steps[0].ID != "test-first" || steps[0].Ref != "T-003" {
		t.Errorf("steps = %v (ref %s), want one test-first step for T-003", ids(steps), steps[0].Ref)
	}

	// A committed failing test is the strongest pull: done is already defined.
	st.Tasks = append(st.Tasks, TaskView{ID: "T-005", Status: "todo", TestReady: true})
	steps = nextSteps(st)
	if steps[0].ID != "build" || steps[0].Ref != "T-005" {
		t.Errorf("with a test ready, first = %+v, want build T-005", steps[0])
	}
}

// Bugs are classified before new work starts, and a quiet project is told so
// instead of shown an empty list.
func TestBugsThenQuiet(t *testing.T) {
	st := projectSnapshot{
		HasRequirements: true, HasSpec: true, HasPlan: true,
		Bugs: []bug.Bug{
			{ID: "B-001", Status: bug.Open},
			{ID: "B-002", Status: bug.Triaged},
		},
		Tasks: []TaskView{{ID: "T-001", Status: "todo"}},
	}
	got := ids(nextSteps(st))
	want := []string{"triage", "promote", "test-first"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}

	quiet := nextSteps(projectSnapshot{
		HasRequirements: true, HasSpec: true, HasPlan: true,
		Tasks: []TaskView{{ID: "T-001", Status: "accepted"}},
	})
	if len(quiet) != 1 || quiet[0].ID != "brief" {
		t.Errorf("a finished project's guide = %v, want [brief]", ids(quiet))
	}
}

// The real gather: a freshly initialized project asks for its requirements.
func TestProjectNextOnAFreshProject(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	steps, err := s.ProjectNext(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 || steps[0].ID != "intake" {
		t.Errorf("fresh project guide = %v, want intake first", ids(steps))
	}
}

// The engine-side dissent check: the desktop's reviewerDissent only protects
// the person watching, and under auto nobody is. The LAST verdict decides —
// an early request-changes answered by a later approve is agreement — and a
// green gate with standing dissent must reach a human, not acceptRun.
func TestFinalDissentReadsTheLastVerdict(t *testing.T) {
	write := func(t *testing.T, events []string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "events.jsonl"),
			[]byte(strings.Join(events, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	// Dissent standing: request_changes is the final word.
	dir := write(t, []string{
		`{"type":"message","data":{"role":"implementer","content":"done"}}`,
		`{"type":"message","data":{"role":"reviewer","verdict":"approve","findings":[]}}`,
		`{"type":"message","data":{"role":"reviewer","verdict":"request_changes","findings":[{"issue":"a"},{"issue":"b"}]}}`,
	})
	v, n, dissent := finalDissent(dir)
	if !dissent || v != "request_changes" || n != 2 {
		t.Errorf("= %q %d %v, want standing request_changes with 2 findings", v, n, dissent)
	}

	// Dissent answered: a later approve settles it.
	dir = write(t, []string{
		`{"type":"message","data":{"role":"reviewer","verdict":"request_changes","findings":[{"issue":"a"}]}}`,
		`{"type":"message","data":{"role":"reviewer","verdict":"approve","findings":[]}}`,
	})
	if _, _, dissent := finalDissent(dir); dissent {
		t.Error("an answered objection still reads as dissent")
	}

	// No verdicts at all (solo run): nothing to dissent.
	dir = write(t, []string{`{"type":"message","data":{"role":"implementer","content":"done"}}`})
	if _, _, dissent := finalDissent(dir); dissent {
		t.Error("a run with no reviewer invented a dissent")
	}
}
