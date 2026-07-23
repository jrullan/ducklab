package project

import (
	"strings"
	"testing"
)

func TestSaveLoadRoundtrip(t *testing.T) {
	repo := t.TempDir()
	in := Project{Description: "one-page 3D earth app", Goals: []string{"build the globe", "add starfield"}}
	if err := Save(repo, in); err != nil {
		t.Fatal(err)
	}
	out, ok := Load(repo)
	if !ok {
		t.Fatal("note should exist after save")
	}
	if out.Description != in.Description || len(out.Goals) != 2 || out.Goals[1] != "add starfield" {
		t.Fatalf("roundtrip mismatch: %+v", out)
	}
}

func TestAddGoalDedup(t *testing.T) {
	repo := t.TempDir()
	AddGoal(repo, "make the globe spin")
	AddGoal(repo, "make the globe spin") // duplicate of last → ignored
	AddGoal(repo, "add dark mode")
	p, _ := Load(repo)
	if len(p.Goals) != 2 {
		t.Fatalf("expected 2 goals after dedup, got %v", p.Goals)
	}
}

func TestContext(t *testing.T) {
	if (Project{}).Context() != "" {
		t.Error("empty project should yield empty context")
	}
	c := Project{Description: "earth app", Goals: []string{"globe", "stars"}}.Context()
	if !strings.Contains(c, "earth app") || !strings.Contains(c, "- globe") {
		t.Errorf("context missing content: %q", c)
	}
	if !strings.Contains(c, "continuing work") {
		t.Errorf("context missing framing: %q", c)
	}
}

func TestContextCapsGoals(t *testing.T) {
	var goals []string
	for i := 0; i < 20; i++ {
		goals = append(goals, "goal")
	}
	c := Project{Description: "x", Goals: goals}.Context()
	if strings.Count(c, "- goal") > maxGoalsInContext {
		t.Errorf("context should cap goals at %d", maxGoalsInContext)
	}
}
