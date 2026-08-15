package service

import (
	"context"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/store"
)

// ProjectStatus counted tasks from the db `task` table — a secondary mirror
// written once at bug promotion and never pruned when a re-plan drops a task.
// A task removed from the plan lingered in the mirror, so status / next_steps
// reported a phantom task the board (which reads the plan) did not show. This
// is the wreckage ghost_task_test documents, seen from the counting side.
// Counts must come from the plan, the authoritative source.
func TestProjectStatusCountsThePlanNotTheOrphanedMirror(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — A\n\n**Priority:** must\n",
		// The plan carries exactly one task.
		artifact.KindPlan: "## M-001 — First\n\n### T-001 — Real work\n\nDo it.\n",
	})

	// The mirror holds both tasks that were once promoted — T-001, still in the
	// plan, and T-002, which a later re-plan dropped from the plan but did not
	// prune here. Counting the mirror therefore sees two tasks; the plan has one.
	db, err := s.openProjectDB(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(&store.Task{ID: "T-001", Title: "Real work", Status: "todo"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(&store.Task{ID: "T-002", Title: "Dropped work", Status: "todo"}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err := s.ProjectStatus(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, n := range st.TaskCounts {
		total += n
	}
	if total != 1 {
		t.Errorf("status counted %d tasks (counts=%v); the plan has exactly one — "+
			"the orphaned mirror row leaked in", total, st.TaskCounts)
	}
}
