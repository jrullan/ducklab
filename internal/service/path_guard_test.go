package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// B-550 review: a revision inherits its parent's references and brief, so
// the guard must see them after inheritance — a check on the request as sent
// saw an empty list and let the parent's outside reference through.
func TestThePathGuardSeesInheritedReferences(t *testing.T) {
	s, projectID, root, guide := revisionChainProject(t)
	parent := startAndSettle(t, s, projectID, StageRequest{Stage: "intake", From: "a polar calculator",
		Refs: []StageRef{{Path: guide}}})

	var seen []string
	refused := errors.New("outside")
	_, err := s.StageStart(context.Background(), projectID, StageRequest{
		Stage: "intake", RevisesRun: parent.ID, Revise: "tighten it",
		PathGuard: func(paths []string) error {
			seen = paths
			for _, p := range paths {
				if !strings.HasPrefix(p, root) {
					return refused
				}
			}
			return nil
		},
	})
	if !errors.Is(err, refused) {
		t.Fatalf("revision launched past the guard: err=%v, guard saw %v", err, seen)
	}
	if !slices.Contains(seen, guide) {
		t.Errorf("guard saw %v, want the inherited reference %s", seen, guide)
	}
}
