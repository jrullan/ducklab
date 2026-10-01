package service

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// B-461: the road, from an empty project to a released one.
func TestLifecycleMapWalksTheWholeRoad(t *testing.T) {
	cases := []struct {
		name       string
		progress   map[string]string
		tasks      map[string]int
		unreleased int
		current    string
		code       bool
		next       string
	}{
		{"empty", map[string]string{"intake": "empty", "spec": "empty", "plan": "empty"}, nil, 0, "requirements", false, "Describe what you want to build"},
		{"requirements draft", map[string]string{"intake": "proposed", "spec": "empty", "plan": "empty"}, nil, 0, "requirements", false, "No code is written until a plan is accepted"},
		{"requirements accepted", map[string]string{"intake": "approved", "spec": "empty", "plan": "empty"}, nil, 0, "spec", false, "draft the specification"},
		{"plan draft", map[string]string{"intake": "approved", "spec": "approved", "plan": "proposed"}, nil, 0, "plan", false, "turns it into tasks"},
		{"plan accepted, nothing built", map[string]string{"intake": "approved", "spec": "approved", "plan": "approved"}, map[string]int{"todo": 4}, 0, "build", false, "no code exists yet (0 of 4"},
		{"building", map[string]string{"intake": "approved", "spec": "approved", "plan": "approved"}, map[string]int{"todo": 1, "accepted": 3}, 3, "build", true, "3 of 4 tasks accepted"},
		{"all built, unshipped", map[string]string{"intake": "approved", "spec": "approved", "plan": "approved"}, map[string]int{"accepted": 4}, 4, "release", true, "cut a release (4"},
		{"released", map[string]string{"intake": "approved", "spec": "approved", "plan": "approved"}, map[string]int{"accepted": 4}, 0, "", true, "built and released"},
	}
	for _, c := range cases {
		pending := map[string]bool{}
		for stage, state := range c.progress {
			if state == "proposed" {
				pending[stage] = true
			}
		}
		l := lifecycleOf(lifecycleFacts{Progress: c.progress, Pending: pending, TaskCounts: c.tasks, Unreleased: c.unreleased})
		if l.Current != c.current || l.CodeExists != c.code || !strings.Contains(l.Next, c.next) {
			t.Errorf("%s: current=%q code=%v next=%q", c.name, l.Current, l.CodeExists, l.Next)
		}
		if len(l.Stages) != 5 {
			t.Fatalf("%s: %d stages", c.name, len(l.Stages))
		}
	}
	// A draft waiting for the person is a decision, not ordinary progress.
	if l := lifecycleOf(lifecycleFacts{Progress: map[string]string{"intake": "proposed"}, Pending: map[string]bool{"intake": true}}); l.Stages[0].State != "decision" || l.Stages[1].State != "pending" {
		t.Fatalf("stages = %+v", l.Stages)
	}
}

// Codex on #119: a pending revision of an accepted document is a decision,
// not "done" — stageProgress reports it as approved.
func TestARevisionOfAnAcceptedDocumentIsADecision(t *testing.T) {
	l := lifecycleOf(lifecycleFacts{
		Progress:   map[string]string{"intake": "approved", "spec": "approved", "plan": "approved"},
		Pending:    map[string]bool{"intake": true},
		TaskCounts: map[string]int{"accepted": 2, "todo": 1},
	})
	if l.Stages[0].State != "decision" || l.Current != "requirements" || !strings.Contains(l.Next, "revision of the accepted requirements") {
		t.Fatalf("pending revision read as %+v / %q", l.Stages[0], l.Next)
	}
}

// Codex on #119: an adopted repository has code before any task is accepted.
func TestAnAdoptedRepositoryHasCodeBeforeAnyTask(t *testing.T) {
	l := lifecycleOf(lifecycleFacts{Progress: map[string]string{"intake": "empty"}, HasCode: true})
	if !l.CodeExists {
		t.Fatal("a repository with committed code was reported as having none")
	}
}

// Codex's reproduction on #119, through ProjectStatus: an approved document
// plus requirements.md.proposed.
func TestProjectStatusShowsAPendingRevisionAsADecision(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "---\nkind: requirements\napproved_by: human\n---\n\n## REQ-001 — Log time\n\n**Priority:** must\n\nBody.\n",
	})
	if err := os.WriteFile(artifact.ProposedPath(dir, artifact.KindRequirements), []byte("## REQ-001 — Log time\n\n**Priority:** must\n\nRevised.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := s.ProjectStatus(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if st.Lifecycle.Stages[0].State != "decision" {
		t.Fatalf("pending revision shown as %q", st.Lifecycle.Stages[0].State)
	}
}
