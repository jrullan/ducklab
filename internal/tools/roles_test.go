package tools

import (
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

func TestEveryValidRoleHasACeiling(t *testing.T) {
	for _, role := range config.ValidRoles() {
		if _, ok := roleToolbelts[role]; !ok {
			t.Errorf("role %q has no toolbelt entry; it would silently fail closed", role)
		}
	}
}

func TestRoleToolbeltReturnsACopy(t *testing.T) {
	got := RoleToolbelt(config.RoleJudge)
	if len(got) == 0 {
		t.Fatal("judge ceiling is empty")
	}
	got[0] = "mutated"
	if RoleToolbelt(config.RoleJudge)[0] == "mutated" {
		t.Error("RoleToolbelt exposes the underlying slice; a caller can widen a role at runtime")
	}
}

func TestAvailableFiltersUnimplementedTools(t *testing.T) {
	r := NewRegistry()
	// skill_run is in the implementer's ceiling but arrives in v0.6. The
	// ceiling lists the spec's full set so adding a tool later needs no edit
	// there; Available filters it to what actually exists.
	if !RoleAllows(config.RoleImplementer, "skill_run") {
		t.Fatal("test assumption broken: skill_run should be in the implementer ceiling")
	}
	for _, name := range r.Available(config.RoleImplementer) {
		if name == "skill_run" {
			t.Error("Available returned a tool that is not registered")
		}
	}
}

// artifact_read landed in v0.4 and must now reach the roles that need it.
func TestArtifactReadIsAvailableToTheRolesThatNeedIt(t *testing.T) {
	r := NewRegistry()
	for _, role := range []config.Role{config.RoleArchitect, config.RoleReviewer, config.RoleImplementer} {
		found := false
		for _, name := range r.Available(role) {
			if name == "artifact_read" {
				found = true
			}
		}
		if !found {
			t.Errorf("role %q cannot read the cycle's documents", role)
		}
	}
}

// A model changes an artifact by proposing through a stage, never by writing
// the file: there is deliberately no artifact_write.
func TestThereIsNoArtifactWriteTool(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Get("artifact_write"); err == nil {
		t.Error("artifact_write exists; a model could bypass the human gate")
	}
}

func TestAvailableIsSortedAndStable(t *testing.T) {
	r := NewRegistry()
	first := r.Available(config.RoleImplementer)
	for i := 0; i < 20; i++ {
		got := r.Available(config.RoleImplementer)
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("Available is not deterministic: %v vs %v", first, got)
			}
		}
	}
}

func TestReadOnlyExcludesEveryMutatingTool(t *testing.T) {
	r := NewRegistry()
	for _, name := range r.ReadOnly(config.RoleImplementer) {
		tool, err := r.Get(name)
		if err != nil {
			t.Fatal(err)
		}
		if tool.Mutating() {
			t.Errorf("ReadOnly returned mutating tool %q", name)
		}
	}
}

func TestMutatingFlagsAreCorrect(t *testing.T) {
	r := NewRegistry()
	want := map[string]bool{
		"fs_list": false, "fs_read": false, "fs_search": false,
		"fs_write": true, "fs_patch": true, "fs_delete": true,
		"shell": true, "verify_run": false,
		"git_status": false, "git_diff": false, "git_log": false,
	}
	for name, mutating := range want {
		tool, err := r.Get(name)
		if err != nil {
			t.Errorf("tool %q not registered", name)
			continue
		}
		if tool.Mutating() != mutating {
			t.Errorf("%s.Mutating() = %v, want %v", name, tool.Mutating(), mutating)
		}
	}
}

func TestNarrowToolbeltEmptySpecMeansFull(t *testing.T) {
	r := NewRegistry()
	empty, err := r.NarrowToolbelt(config.RoleImplementer, "")
	if err != nil {
		t.Fatal(err)
	}
	full, _ := r.NarrowToolbelt(config.RoleImplementer, "full")
	if len(empty) != len(full) {
		t.Errorf("empty spec gave %d tools, full gave %d", len(empty), len(full))
	}
}

func TestNarrowToolbeltHumanHasNoTools(t *testing.T) {
	r := NewRegistry()
	got, err := r.NarrowToolbelt(config.RoleHuman, "full")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("human turn got tools %v; a human turn runs no agent loop", got)
	}
}
