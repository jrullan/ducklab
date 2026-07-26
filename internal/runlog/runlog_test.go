package runlog

import (
	"os"
	"path/filepath"
	"testing"
)

func newRun(id string) *Run {
	return &Run{ID: id, ProjectID: "p", Status: "running", Mode: "solo"}
}

func TestAppendEventAssignsMonotonicSeq(t *testing.T) {
	root := t.TempDir()
	w, err := NewWriter(root, newRun("r-1"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	for i := 0; i < 5; i++ {
		if err := w.AppendEvent("turn_start", map[string]interface{}{"turn": i}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := ReadEvents(w.RunDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 {
		t.Fatalf("got %d events, want 5", len(events))
	}
	for i, e := range events {
		if e.Seq != i+1 {
			t.Errorf("event %d has seq %d, want %d", i, e.Seq, i+1)
		}
	}
}

// OpenWriter must continue the sequence, not restart it. A restarted seq
// makes SSE resume deliver duplicates that look like fresh events.
func TestOpenWriterResumesSeq(t *testing.T) {
	root := t.TempDir()
	run := newRun("r-2")
	w, err := NewWriter(root, run)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		w.AppendEvent("tool_call", nil)
	}
	w.Close()

	w2, err := OpenWriter(root, run)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	w2.AppendEvent("gate", nil)

	events, _ := ReadEvents(w2.RunDir())
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4", len(events))
	}
	if got := events[3].Seq; got != 4 {
		t.Errorf("seq after reopen = %d, want 4 (sequence restarted)", got)
	}
}

func TestOnEventFiresAfterDurableWrite(t *testing.T) {
	root := t.TempDir()
	w, err := NewWriter(root, newRun("r-3"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	var seen []*Event
	w.OnEvent = func(e *Event) {
		// The event must already be on disk when the hook runs, otherwise a
		// subscriber can see an event that a crash would erase.
		events, err := ReadEvents(w.RunDir())
		if err != nil {
			t.Errorf("read during hook: %v", err)
		}
		if len(events) == 0 || events[len(events)-1].Seq != e.Seq {
			t.Errorf("event seq %d not yet durable when hook fired", e.Seq)
		}
		seen = append(seen, e)
	}
	w.AppendEvent("run_start", nil)
	w.AppendEvent("run_end", nil)

	if len(seen) != 2 {
		t.Fatalf("hook fired %d times, want 2", len(seen))
	}
	if seen[0].Type != "run_start" || seen[1].Type != "run_end" {
		t.Errorf("hook saw wrong order: %v", seen)
	}
}

func TestReadEventsStopsAtTornTail(t *testing.T) {
	root := t.TempDir()
	w, _ := NewWriter(root, newRun("r-4"))
	w.AppendEvent("a", nil)
	w.AppendEvent("b", nil)
	w.Close()

	// Simulate a process killed mid-write.
	path := filepath.Join(w.RunDir(), "events.jsonl")
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"ts":"2026-01-01T00:00:00Z","seq":3,"ty`)
	f.Close()

	events, err := ReadEvents(w.RunDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (torn tail not discarded)", len(events))
	}
}

func TestLastSeqIgnoresTornTail(t *testing.T) {
	root := t.TempDir()
	run := newRun("r-5")
	w, _ := NewWriter(root, run)
	w.AppendEvent("a", nil)
	w.Close()

	path := filepath.Join(w.RunDir(), "events.jsonl")
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"seq":99,"broken`)
	f.Close()

	if got := lastSeq(path); got != 1 {
		t.Errorf("lastSeq = %d, want 1", got)
	}
}

func TestReadStateRoundTrip(t *testing.T) {
	root := t.TempDir()
	run := newRun("r-6")
	run.Verdict = "PASSED"
	run.TaskID = "T-001"
	w, err := NewWriter(root, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()

	got, err := ReadState(RunDirFor(root, "r-6"))
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "r-6" || got.Verdict != "PASSED" || got.TaskID != "T-001" {
		t.Errorf("round trip lost fields: %+v", got)
	}
}

func TestListRunsMissingDirIsNotAnError(t *testing.T) {
	ids, err := ListRuns(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("got %d ids, want 0", len(ids))
	}
}
