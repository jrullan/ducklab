package run

import (
	"path/filepath"
	"testing"
)

func TestRunLifecycle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runs", "task-1")
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.State.State != "INTAKE" {
		t.Fatalf("fresh run state = %q", r.State.State)
	}
	r.Advance("SOLVE")
	r.Set("winner", "beelink")
	r.Write("judge.md", "DECISION: A")

	// reopen: state persists
	r2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if r2.State.State != "SOLVE" {
		t.Errorf("resumed state = %q, want SOLVE", r2.State.State)
	}
	if v, ok := r2.Get("winner"); !ok || v != "beelink" {
		t.Errorf("winner not persisted: %v %v", v, ok)
	}
	if c, ok := r2.Read("judge.md"); !ok || c != "DECISION: A" {
		t.Errorf("artifact not persisted: %q", c)
	}
	if len(r2.State.History) != 1 {
		t.Errorf("history = %d transitions, want 1", len(r2.State.History))
	}
}
