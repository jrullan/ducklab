package service

import (
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

func planWithTask(t *testing.T, id, body string) *artifact.Document {
	t.Helper()
	return &artifact.Document{Sections: []artifact.Section{{
		ID: "M-01", Title: "Milestone",
		Children: []artifact.Section{{ID: id, Title: "Task", Body: body}},
	}}}
}

// Accepting a spec-alignment that added ONE **Implements:** line to 64 tasks
// orphaned every accepted run and the whole board reappeared in todo (B-093).
// The hash covers the task's substance: annotating traceability keeps the
// history; rewriting the work does not.
func TestTraceabilityEditsKeepTaskHistory(t *testing.T) {
	oldBody := "Fixes B-061.\n\n## Reported\n\nThe gate died on a missing node_modules."
	newBody := "**Implements:** SPEC-060\n\n" + oldBody
	rewritten := "A genuinely different assignment."

	// The accepted run recorded the RAW hash of the body as it stood then —
	// the pre-fix spelling.
	accepted := &runlog.Run{TaskID: "T-071", TaskBodyHash: rawTaskBodyHash(oldBody), StartedAt: "2026-08-10T00:00:00Z"}

	hashes := taskBodyHashes(planWithTask(t, "T-071", newBody))
	if got := runsForCurrentTaskBodies([]*runlog.Run{accepted}, hashes); len(got) != 1 {
		t.Fatal("an Implements-only edit orphaned the task's accepted run")
	}

	hashes = taskBodyHashes(planWithTask(t, "T-071", rewritten))
	if got := runsForCurrentTaskBodies([]*runlog.Run{accepted}, hashes); len(got) != 0 {
		t.Fatal("a substance rewrite kept history that belongs to the old assignment")
	}

	// A run recorded AFTER the fix stores the normalized hash and matches
	// the same task with or without its Implements line.
	fresh := &runlog.Run{TaskID: "T-071", TaskBodyHash: taskBodyHash(newBody), StartedAt: "2026-08-19T00:00:00Z"}
	if got := runsForCurrentTaskBodies([]*runlog.Run{fresh}, taskBodyHashes(planWithTask(t, "T-071", oldBody))); len(got) != 1 {
		t.Fatal("a normalized record did not survive removing the Implements line")
	}
}
