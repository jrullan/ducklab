package service

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jrullan/ducklab/internal/provider"
)

// B-521 × B-520: the TI-36X guidebook was the scope of INT-002 — "fill the
// gap between the implemented and the intended calculator". A revision that
// inherited the path but not the role would read the guidebook as background
// and stop building toward it. The reference entry is inherited whole: the
// role survives into the revision's request and its prompt still labels the
// guidebook SCOPE.
func TestAnIntakeRevisionInheritsAScopeReference(t *testing.T) {
	s, projectID, root, guide := revisionChainProject(t)
	fake := s.providers["fake"].(*provider.Fake)
	draft := fake.ScriptFunc
	var revising atomic.Bool
	var mu sync.Mutex
	var prompts []string
	fake.ScriptFunc = func(req provider.ChatRequest, n int) *provider.ChatResponse {
		if revising.Load() {
			mu.Lock()
			for _, m := range req.Messages {
				prompts = append(prompts, m.Content)
			}
			mu.Unlock()
		}
		return draft(req, n)
	}

	r0 := startAndSettle(t, s, projectID, StageRequest{
		Stage: "intake", Mode: "solo", From: "Fill the gap between the implemented and the intended calculator.",
		Refs: []StageRef{{Path: guide, Role: RefRoleScope}},
	})
	revising.Store(true)
	r1 := startAndSettle(t, s, projectID, StageRequest{Stage: "intake", RevisesRun: r0.ID, Revise: "Keep the wording close to the guidebook."})

	req := persistedStageRequest(t, root, r1.ID)
	var inherited *StageRef
	for i := range req.Refs {
		if req.Refs[i].Path == guide {
			inherited = &req.Refs[i]
		}
	}
	if inherited == nil || inherited.Role != RefRoleScope {
		t.Fatalf("revision refs = %+v, want the guidebook inherited as scope", req.Refs)
	}
	mu.Lock()
	defer mu.Unlock()
	labelled := false
	for _, p := range prompts {
		if strings.Contains(p, "guidebook.md") && strings.Contains(p, "role: SCOPE") {
			labelled = true
		}
	}
	if !labelled {
		t.Fatalf("the revision's prompt does not label the guidebook SCOPE (%d messages)", len(prompts))
	}
	detail, _ := s.RunGet(context.Background(), r1.ID)
	if strings.Contains(detail.Run.Warning, "SCOPE") {
		t.Fatalf("a whole scope reference warned on the revision: %q", detail.Run.Warning)
	}
}
