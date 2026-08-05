package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// A person asked for one change to a spec draft; the revision ran and was
// accepted; the ORIGINAL spec run sat at its gate for good, waiting for an
// approval that had already been given in another form. Requesting changes IS
// the decision on the draft it revises.
func TestRequestingChangesResolvesTheOriginalRun(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "---\nkind: requirements\napproved_by: human\n---\n\n" +
			"## REQ-001 — A\n\n**Priority:** must\n\nIt does A.\n",
	})

	// The original spec run, paused at its gate with its proposal on disk.
	orig, err := s.StageStart(context.Background(), id, StageRequest{Stage: "spec", Mode: "solo"})
	if err != nil {
		t.Fatal(err)
	}
	// waitForRun reports a pause as an error; a paused gate is the setup.
	_, _ = s.waitForRun(context.Background(), orig.ID)
	got, _ := s.RunGet(context.Background(), orig.ID)
	if got.Run.Status != "paused" || got.Run.PendingKind != "gate" {
		t.Fatalf("setup: original run is %s/%s (failure: %s), want paused/gate",
			got.Run.Status, got.Run.PendingKind, got.Run.Failure)
	}
	// The proposal must name the run that produced it, or nothing links them.
	prop, _ := artifact.LoadProposed(dir, artifact.KindSpec)
	if prop == nil || prop.Front.RunID != orig.ID {
		t.Fatalf("setup: proposal run_id = %v", prop)
	}

	// The person asks for a change.
	rev, err := s.StageStart(context.Background(), id, StageRequest{
		Stage: "spec", Mode: "solo", Revise: "SPEC-001 must also cover B",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.waitForRun(context.Background(), rev.ID)

	after, _ := s.RunGet(context.Background(), orig.ID)
	if after.Run.Status != "done" {
		t.Errorf("original run = %s, want done", after.Run.Status)
	}
	if !strings.Contains(after.Run.Resolution, "changes requested") {
		t.Errorf("resolution = %q, want the decision named", after.Run.Resolution)
	}
	if after.Run.PendingKind != "" {
		t.Error("the original run still claims to wait for someone")
	}
}

// And accepting one spec proposal closes any OTHER spec run still holding a
// gate: its proposal file is gone, so its decision no longer exists to make.
func TestAcceptingAProposalSupersedesStaleSiblingGates(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	// Accepting commits, so this project needs a real repo.
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	id := p.ID
	if err := os.MkdirAll(filepath.Dir(artifact.Path(dir, artifact.KindRequirements)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path(dir, artifact.KindRequirements),
		[]byte("---\nkind: requirements\napproved_by: human\n---\n\n"+
			"## REQ-001 — A\n\n**Priority:** must\n\nIt does A.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := s.StageStart(context.Background(), id, StageRequest{Stage: "spec", Mode: "solo"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.waitForRun(context.Background(), first.ID)
	second, err := s.StageStart(context.Background(), id, StageRequest{Stage: "spec", Mode: "solo"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.waitForRun(context.Background(), second.ID)

	if _, err := s.RunAccept(context.Background(), second.ID, ""); err != nil {
		t.Fatal(err)
	}
	after, _ := s.RunGet(context.Background(), first.ID)
	if after.Run.Status != "done" || !strings.Contains(after.Run.Resolution, "superseded") {
		t.Errorf("stale sibling = %s %q, want done/superseded", after.Run.Status, after.Run.Resolution)
	}
}
