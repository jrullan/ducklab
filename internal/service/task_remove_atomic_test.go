package service

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/store"
)

// execDB runs raw SQL against a project's database: the failure points below
// are forced with SQLite triggers, so the code under test meets a real
// database refusal rather than a seam.
func execDB(t *testing.T, dir string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, ".ducklab", "ducklab.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func refuseSQL(table, op string) string {
	return "CREATE TRIGGER forced_" + strings.ToLower(op) + "_" + table + " BEFORE " + op + " ON " + table +
		" BEGIN SELECT RAISE(ABORT, 'forced " + strings.ToLower(op) + " failure on " + table + "'); END"
}

// removalState is everything a task removal touches: the plan on disk, HEAD,
// the index, the task row, its trace edges, and the bug that points at it.
type removalState struct {
	plan, head, staged string
	task               *store.Task
	edges              []store.Edge
	bugStatus, bugTask string
}

func captureRemovalState(t *testing.T, s *Service, id, dir, taskID string) removalState {
	t.Helper()
	plan, _ := os.ReadFile(artifact.Path(dir, artifact.KindPlan))
	st := removalState{
		plan:   string(plan),
		head:   strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")),
		staged: gitOut(t, dir, "diff", "--cached", "--name-only"),
	}
	db, err := s.openProjectDB(id)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st.task, _ = db.GetTask(taskID)
	if st.edges, err = db.TaskEdges(taskID); err != nil {
		t.Fatal(err)
	}
	rec, err := db.GetBug("B-001")
	if err != nil {
		t.Fatal(err)
	}
	st.bugStatus, st.bugTask = rec.Status, rec.TaskID
	return st
}

// Review of #153: TaskRemove committed the plan and then ran its database half
// best-effort, so a failed row delete or bug reset returned success with a
// warning while HEAD no longer had the task — the split this PR exists to
// prevent, with no section left for a retry to remove. Every failure point
// now returns an error and leaves all six pieces of state exactly as they were.
func TestTaskRemoveFailureLeavesNoPartialState(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(t *testing.T, dir string)
		want    string
	}{
		// DeleteTask refused: nothing has changed yet. The trace-edge delete
		// that precedes the row delete is rolled back with it.
		{"task row delete refused", func(t *testing.T, dir string) {
			execDB(t, dir, refuseSQL("task", "DELETE"))
		}, "forced delete failure on task"},
		// The edge delete inside DeleteTask refused.
		{"trace edge delete refused", func(t *testing.T, dir string) {
			execDB(t, dir, refuseSQL("traceability", "DELETE"))
		}, "forced delete failure on traceability"},
		// Bug reset refused after the row is gone: the row and its edges come
		// back exactly, timestamps included.
		{"bug reset refused", func(t *testing.T, dir string) {
			execDB(t, dir, refuseSQL("bug", "UPDATE"))
		}, "forced update failure on bug"},
		// The bug that points at the task cannot be read: refused before any
		// change.
		{"bug read refused", func(t *testing.T, dir string) {
			execDB(t, dir, "ALTER TABLE bug RENAME TO bug_unreadable")
		}, "read the bug"},
		// Commit refused after the database half: the bug, the row and the
		// edges are put back and the plan is restored.
		{"commit refused", func(t *testing.T, dir string) {
			refuseCommits(t, dir)
		}, "refused by policy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno")
			id, dir := gitPromotionProject(t, s)
			out, err := s.BugPromote(context.Background(), id, "B-001", "human")
			if err != nil {
				t.Fatal(err)
			}
			taskID := out["task"].(string)
			// Timestamps from another day, so an undo that re-creates the row
			// with "now" instead of restoring it cannot pass by luck.
			execDB(t, dir, "UPDATE task SET created_at = '2020-01-02T03:04:05Z', updated_at = '2020-01-02T03:04:06Z' WHERE id = '"+taskID+"'")
			before := captureRemovalState(t, s, id, dir, taskID)
			if before.task == nil || len(before.edges) == 0 {
				t.Fatalf("fixture lacks the row or edges it is meant to protect: %+v", before)
			}
			tc.arrange(t, dir)

			got, err := s.TaskRemove(context.Background(), id, taskID)
			if err == nil {
				t.Fatalf("removal reported success after a refused step: %v", got)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to name %q", err, tc.want)
			}
			if tc.name == "bug read refused" {
				execDB(t, dir, "ALTER TABLE bug_unreadable RENAME TO bug")
			}
			after := captureRemovalState(t, s, id, dir, taskID)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("a refused removal left partial state:\nbefore %+v\nafter  %+v", before, after)
			}
			assertPersonsWorkUntouched(t, dir)
		})
	}
}

// When the undo itself is refused the error says the halves now disagree and
// which one, rather than reporting a clean failure.
func TestTaskRemoveNamesAFailedUndo(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := gitPromotionProject(t, s)
	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatal(err)
	}
	taskID := out["task"].(string)
	planBefore, _ := os.ReadFile(artifact.Path(dir, artifact.KindPlan))
	refuseCommits(t, dir)
	execDB(t, dir, refuseSQL("task", "INSERT"))

	_, err = s.TaskRemove(context.Background(), id, taskID)
	if err == nil || !strings.Contains(err.Error(), "could not undo") || !strings.Contains(err.Error(), "task "+taskID+" row") {
		t.Fatalf("error = %v, want it to name the row it could not put back", err)
	}
	planAfter, _ := os.ReadFile(artifact.Path(dir, artifact.KindPlan))
	if string(planAfter) != string(planBefore) {
		t.Fatal("the plan was not restored")
	}
}

// Promotion's database failure points, the same class: a refused trace edge
// or bug move after the task rows exist takes the rows back and leaves the
// plan and HEAD untouched.
func TestBugPromotionDatabaseFailureLeavesNoPartialState(t *testing.T) {
	cases := []struct{ name, trigger, want string }{
		{"task row insert refused", refuseSQL("task", "INSERT"), "forced insert failure on task"},
		{"trace edge insert refused", refuseSQL("traceability", "INSERT"), "forced insert failure on traceability"},
		{"bug move refused", refuseSQL("bug", "UPDATE"), "forced update failure on bug"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno")
			id, dir := gitPromotionProject(t, s)
			planBefore, _ := os.ReadFile(artifact.Path(dir, artifact.KindPlan))
			head := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
			execDB(t, dir, tc.trigger)

			if _, err := s.BugPromote(context.Background(), id, "B-001", "human"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			planAfter, _ := os.ReadFile(artifact.Path(dir, artifact.KindPlan))
			if string(planAfter) != string(planBefore) || strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")) != head {
				t.Fatal("the plan or HEAD changed on a refused promotion")
			}
			db, err := s.openProjectDB(id)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.GetTask("T-003"); err == nil {
				t.Error("a task row survived the refused promotion")
			}
			if edges, _ := db.TaskEdges("T-003"); len(edges) != 0 {
				t.Errorf("trace edges survived: %v", edges)
			}
			if rec, _ := db.GetBug("B-001"); rec.Status != "triaged" || rec.TaskID != "" {
				t.Errorf("the bug moved: %+v", rec)
			}
			assertPersonsWorkUntouched(t, dir)
		})
	}
}
