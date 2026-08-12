package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// Promoting a bug carried the reporter's prose and nothing else.
//
// The triage had computed the component, the suspected files and the reasoning,
// shown them once at a gate, and discarded them — so the model that had to fix
// the bug went looking for a location somebody had already found. Measured: an
// implementer spent all 24 of its turns reading one file, with the answer to
// "where" thrown away two steps earlier.
func TestAPromotedTaskCarriesWhatTheTriageFound(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	if _, err := s.BugAdd(context.Background(), id, BugRequest{
		Title: "when dragging a vertex one edge value does not change",
		Body:  "In the default triangle the left edge label does not update.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyTriage(context.Background(), id, []map[string]interface{}{{
		"bug":             "B-001",
		"severity":        "high",
		"component":       "edge label rendering",
		"suspected_files": []string{"index.html"},
		"task_title":      "Recompute the edge label for the dragged vertex",
		"reason":          "drawLabels reads a cached length that the drag path never refreshes",
	}}); err != nil {
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
	var body, title string
	for _, tk := range tasks {
		if tk.ID == taskID {
			body, title = tk.Body, tk.Title
		}
	}
	if title != "Recompute the edge label for the dragged vertex" {
		t.Errorf("title = %q; a report names the symptom, a task names the change", title)
	}
	for _, want := range []string{"index.html", "edge label rendering", "drawLabels reads a cached length"} {
		if !strings.Contains(body, want) {
			t.Errorf("the task does not carry %q:\n%s", want, body)
		}
	}
	// The reporter's own words survive alongside it.
	if !strings.Contains(body, "left edge label does not update") {
		t.Errorf("the report was replaced rather than added to:\n%s", body)
	}
	// And it is a model's reading, not a fact. An implementer that finds the
	// cause elsewhere should not doubt itself.
	if !strings.Contains(body, "not the reporter's") {
		t.Errorf("the triage is presented as fact:\n%s", body)
	}
}

// A bug promoted without being triaged keeps working: the report is all there
// is, and it is enough to start from.
func TestAnUntriagedBugStillPromotes(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "it crashes", Body: "on save"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugMove(context.Background(), id, "B-001", "triaged", "human"); err != nil {
		t.Fatal(err)
	}
	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatal(err)
	}
	tasks, _ := s.TaskList(context.Background(), id)
	taskID, _ := out["task"].(string)
	for _, tk := range tasks {
		if tk.ID == taskID {
			if tk.Title != "it crashes" {
				t.Errorf("title = %q", tk.Title)
			}
			if strings.Contains(tk.Body, "## Triage") {
				t.Errorf("an empty triage section was added:\n%s", tk.Body)
			}
		}
	}
}
