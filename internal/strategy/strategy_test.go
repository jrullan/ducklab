package strategy

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/tools"
)

func testRegistry(t *testing.T) *tools.Registry {
	t.Helper()
	return tools.NewRegistry()
}

func TestSoloScriptShape(t *testing.T) {
	s := SoloScript()
	if s.Name != "solo" {
		t.Errorf("name = %q, want solo", s.Name)
	}
	if len(s.Turns) != 1 {
		t.Fatalf("solo has %d turns, want 1 — solo is the single-model baseline", len(s.Turns))
	}
	if s.Turns[0].Role != config.RoleImplementer {
		t.Errorf("role = %q, want implementer", s.Turns[0].Role)
	}
	// I3: nothing unbounded.
	if s.MaxRounds <= 0 {
		t.Error("MaxRounds must be bounded")
	}
	if s.Turns[0].MaxTurns <= 0 {
		t.Error("Turn.MaxTurns must be bounded")
	}
	if s.Until == "" {
		t.Error("Until must be set or the loop only ends at MaxRounds")
	}
}

func TestResolveToolbeltFull(t *testing.T) {
	reg := testRegistry(t)
	turn := &Turn{Toolbelt: "full"}
	got, err := turn.ResolveToolbelt(reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("full toolbelt is empty")
	}
	has := func(name string) bool {
		for _, n := range got {
			if n == name {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"fs_read", "fs_write", "verify_run"} {
		if !has(want) {
			t.Errorf("full toolbelt missing %q: %v", want, got)
		}
	}
}

// I1: no git-mutating tool may ever appear in a toolbelt.
func TestNoToolbeltExposesGitMutation(t *testing.T) {
	reg := testRegistry(t)
	forbidden := []string{
		"git_commit", "git_checkout", "git_branch",
		"git_merge", "git_push", "git_worktree",
	}
	for _, belt := range []string{"full", "read-only"} {
		turn := &Turn{Toolbelt: belt}
		got, err := turn.ResolveToolbelt(reg)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range got {
			for _, bad := range forbidden {
				if name == bad {
					t.Errorf("toolbelt %q exposes %q — models must never mutate version control (I1)", belt, bad)
				}
			}
		}
	}
}

// A reviewer's belt must not be able to change the working tree.
func TestReadOnlyToolbeltHasNoMutatingTools(t *testing.T) {
	reg := testRegistry(t)
	turn := &Turn{Toolbelt: "read-only"}
	got, err := turn.ResolveToolbelt(reg)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range got {
		if strings.HasPrefix(name, "fs_write") || strings.HasPrefix(name, "fs_patch") ||
			strings.HasPrefix(name, "fs_delete") || name == "shell" {
			t.Errorf("read-only toolbelt contains mutating tool %q", name)
		}
	}
}

func TestResolveToolbeltExplicitList(t *testing.T) {
	reg := testRegistry(t)
	turn := &Turn{Toolbelt: "fs_read,fs_search"}
	got, err := turn.ResolveToolbelt(reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %v, want exactly two tools", got)
	}
}

func TestResolveToolbeltRejectsUnknownTool(t *testing.T) {
	reg := testRegistry(t)
	turn := &Turn{Toolbelt: "fs_read,not_a_tool"}
	if _, err := turn.ResolveToolbelt(reg); err == nil {
		t.Error("unknown tool name accepted; a typo would silently drop a tool")
	}
}
