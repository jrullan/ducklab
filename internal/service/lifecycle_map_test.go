package service

import (
	"strings"
	"testing"
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
		l := lifecycleOf(c.progress, c.tasks, c.unreleased)
		if l.Current != c.current || l.CodeExists != c.code || !strings.Contains(l.Next, c.next) {
			t.Errorf("%s: current=%q code=%v next=%q", c.name, l.Current, l.CodeExists, l.Next)
		}
		if len(l.Stages) != 5 {
			t.Fatalf("%s: %d stages", c.name, len(l.Stages))
		}
	}
	// A draft waiting for the person is a decision, not ordinary progress.
	if l := lifecycleOf(map[string]string{"intake": "proposed"}, nil, 0); l.Stages[0].State != "decision" || l.Stages[1].State != "pending" {
		t.Fatalf("stages = %+v", l.Stages)
	}
}
