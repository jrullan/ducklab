package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/bench"
)

// Both components arrive from a client. A stamp of "../../../etc/passwd" must
// not read anything outside the bench directory.
func TestBenchGetRefusesToEscapeItsDirectory(t *testing.T) {
	s := &Service{}
	for _, c := range []struct{ suite, stamp string }{
		{"std", "../../../etc/passwd"},
		{"../..", "x"},
		{"std", "sub/dir"},
		{"std/../..", "x"},
	} {
		if _, err := s.BenchGet(c.suite, c.stamp); err == nil {
			t.Errorf("BenchGet(%q, %q) was allowed", c.suite, c.stamp)
		} else if !strings.Contains(err.Error(), "bad suite or stamp") {
			t.Errorf("BenchGet(%q, %q) failed for the wrong reason: %v", c.suite, c.stamp, err)
		}
	}
}

// Every bench cell died at "run start: no task B-001 in this project". The
// suite numbers its fixtures B-, the plan parser recognised only T- children,
// so a bench project was a plan with zero tasks — which nothing noticed until
// RunStart learned to refuse a task it could not find, and refused all nine.
func TestABenchTaskIsFindableInItsMaterialisedPlan(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	task := bench.Std().Tasks[0]

	dir := t.TempDir()
	if err := materialise(dir, task); err != nil {
		t.Fatal(err)
	}
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "bench-" + task.ID})
	if err != nil {
		t.Fatal(err)
	}

	tv := s.findTask(context.Background(), p.ID, task.ID)
	if tv == nil {
		t.Fatalf("%s is not findable in the materialised plan", task.ID)
	}
	if tv.Body == "" || tv.Status != "todo" {
		t.Errorf("found %s but half-parsed: status=%q body %d chars", task.ID, tv.Status, len(tv.Body))
	}
	offersRun := false
	for _, a := range tv.Next {
		if a == "run" {
			offersRun = true
		}
	}
	if !offersRun {
		t.Errorf("%s does not offer run: %v", task.ID, tv.Next)
	}
}
