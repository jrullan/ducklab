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
// A stored split is a recommendation, not an automatic task creation. Once a
// person promotes it, however, every portion becomes independently schedulable
// work with its own lane. The bug cannot claim fixed when only one portion has
// landed.
func TestPromotingAStoredSplitCreatesLanesAndWaitsForEveryPortion(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(context.Background(), id, BugRequest{
		Title: "saving a profile loses its avatar and leaves the cache stale",
		Body:  "Both persistence and the cache update fail on save.",
	}); err != nil {
		t.Fatal(err)
	}

	proposal := []interface{}{
		map[string]interface{}{
			"title":      "Persist the avatar on profile save",
			"acceptance": []interface{}{"a saved avatar is present after reload"},
			"owns":       []interface{}{"internal/profile/persist.go"},
		},
		map[string]interface{}{
			"title":      "Refresh the avatar cache after profile save",
			"acceptance": []interface{}{"the cache serves the newly saved avatar"},
			"owns":       []interface{}{"internal/profile/cache.go"},
		},
	}
	if _, err := s.ApplyTriage(context.Background(), id, []map[string]interface{}{{
		"bug": "B-001", "severity": "high", "reason": "the report spans persistence and caching",
		"proposal": proposal,
	}}); err != nil {
		t.Fatal(err)
	}
	before, err := s.TaskList(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 2 {
		t.Fatalf("expected exactly the 2 pre-existing tasks before promotion, got %d", len(before))
	}

	if _, err := s.BugPromote(context.Background(), id, "B-001", "human"); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.TaskList(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 4 {
		t.Fatalf("promotion created %d tasks, want one for each of 2 portions", len(tasks)-len(before))
	}

	plan, err := artifact.Load(dir, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	var reportedBugs []artifact.Section
	for _, milestone := range plan.Sections {
		if milestone.Title == "Reported bugs" {
			reportedBugs = append(reportedBugs, milestone)
		}
	}
	if len(reportedBugs) != 1 {
		t.Fatalf("Reported bugs milestones = %d, want one shared milestone", len(reportedBugs))
	}
	if len(reportedBugs[0].Children) != 2 {
		t.Fatalf("tasks under the Reported bugs milestone = %d, want both promoted portions", len(reportedBugs[0].Children))
	}
	want := map[string]struct {
		owns       string
		acceptance string
	}{
		"Persist the avatar on profile save": {
			owns: "internal/profile/persist.go", acceptance: "a saved avatar is present after reload",
		},
		"Refresh the avatar cache after profile save": {
			owns: "internal/profile/cache.go", acceptance: "the cache serves the newly saved avatar",
		},
	}
	reportedPortions := make(map[string]bool)
	for _, task := range reportedBugs[0].Children {
		portion, ok := want[task.Title]
		if !ok {
			t.Errorf("Reported bugs contains unexpected task %q", task.Title)
			continue
		}
		reportedPortions[task.Title] = true
		if len(task.Owns) != 1 || task.Owns[0] != portion.owns {
			t.Errorf("Reported bugs child %q owns %v, want its lane %q", task.Title, task.Owns, portion.owns)
		}
	}
	for title := range want {
		if !reportedPortions[title] {
			t.Errorf("Reported bugs does not contain promoted portion %q", title)
		}
	}
	var portionIDs []string
	for _, task := range tasks {
		portion, ok := want[task.Title]
		if !ok {
			continue
		}
		portionIDs = append(portionIDs, task.ID)
		section := plan.Section(task.ID)
		if section == nil || len(section.Owns) != 1 || section.Owns[0] != portion.owns {
			t.Errorf("%q owns %v, want its disjoint lane %q", task.Title, section, portion.owns)
		}
		if !strings.Contains(task.Body, portion.acceptance) {
			t.Errorf("%q lost its acceptance criterion:\n%s", task.Title, task.Body)
		}
	}
	if len(portionIDs) != len(want) {
		t.Fatalf("promoted portions = %v, want both %v", portionIDs, want)
	}

	if fixed, err := s.BugFixedByTask(context.Background(), id, portionIDs[0]); err != nil || fixed != "" {
		t.Fatalf("first accepted portion fixed %q, %v; the other portion is still open", fixed, err)
	}
	bugs, err := s.BugList(context.Background(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	if bugs[0].Status != "in_progress" {
		t.Fatalf("status after one accepted portion = %q, want in_progress", bugs[0].Status)
	}
	if fixed, err := s.BugFixedByTask(context.Background(), id, portionIDs[1]); err != nil || fixed != "B-001" {
		t.Fatalf("all accepted portions fixed %q, %v; want B-001", fixed, err)
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
