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
	// mcp_call is in the implementer's ceiling but is not built yet. The
	// ceiling lists the spec's full set so adding a tool later needs no edit
	// there; Available filters it to what actually exists.
	if !RoleAllows(config.RoleImplementer, "mcp_call") {
		t.Fatal("test assumption broken: mcp_call should be in the implementer ceiling")
	}
	for _, name := range r.Available(config.RoleImplementer) {
		if name == "mcp_call" {
			t.Error("Available returned a tool that is not registered")
		}
	}
}

// skill_run executes a script, and only the implementer executes: a reviewer
// or architect that could run a skill could rewrite the tree, and their
// evaluate-only contracts would hold by convention instead of construction.
// Reading is different — a survey guide (a documentation skill) briefs the
// architect before an adopt survey, and reading a skill cannot do anything
// to the project. The architect surveyed MiEmpresa wandering the tree and
// missed four live modules; the guide is how a project steers the sweep.
func TestSkillRunReachesTheImplementerAndNoOneElse(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"skill_list", "skill_read", "skill_run"} {
		if !contains(r.Available(config.RoleImplementer), name) {
			t.Errorf("%s is registered but the implementer cannot see it", name)
		}
	}
	for _, role := range []config.Role{config.RoleReviewer, config.RoleJudge, config.RoleArchitect} {
		if contains(r.Available(role), "skill_run") {
			t.Errorf("skill_run reached the %s", role)
		}
	}
	for _, name := range []string{"skill_list", "skill_read"} {
		if !contains(r.Available(config.RoleArchitect), name) {
			t.Errorf("%s does not reach the architect, so no survey guide can", name)
		}
		for _, role := range []config.Role{config.RoleReviewer, config.RoleJudge} {
			if contains(r.Available(role), name) {
				t.Errorf("%s reached the %s", name, role)
			}
		}
	}
}

func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
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
