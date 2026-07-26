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
	// artifact_read is in the reviewer's ceiling but not implemented in v0.1.
	if !RoleAllows(config.RoleReviewer, "artifact_read") {
		t.Fatal("test assumption broken: artifact_read should be in the reviewer ceiling")
	}
	for _, name := range r.Available(config.RoleReviewer) {
		if name == "artifact_read" {
			t.Error("Available returned a tool that is not registered")
		}
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
