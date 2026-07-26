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

func has(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
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
	if s.MaxRounds <= 0 || s.Turns[0].MaxTurns <= 0 {
		t.Error("solo has an unbounded loop (I3)")
	}
	if s.Until == "" {
		t.Error("Until must be set or the loop only ends at MaxRounds")
	}
}

func TestSoloScriptValidates(t *testing.T) {
	if err := SoloScript().Validate(testRegistry(t)); err != nil {
		t.Fatalf("the built-in solo script does not validate: %v", err)
	}
}

func TestImplementerFullBeltHasWriteTools(t *testing.T) {
	turn := &Turn{Role: config.RoleImplementer, Toolbelt: "full"}
	got, err := turn.ResolveToolbelt(testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fs_read", "fs_write", "fs_patch", "verify_run", "shell"} {
		if !has(got, want) {
			t.Errorf("implementer belt missing %q: %v", want, got)
		}
	}
}

// The central guarantee: the ROLE sets the ceiling, not the script. Before
// this, Toolbelt "full" returned registry.List() and a reviewer could write.
func TestReviewerFullBeltIsStillReadOnly(t *testing.T) {
	turn := &Turn{Role: config.RoleReviewer, Toolbelt: "full"}
	got, err := turn.ResolveToolbelt(testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("reviewer belt is empty")
	}
	for _, forbidden := range []string{"fs_write", "fs_patch", "fs_delete", "shell"} {
		if has(got, forbidden) {
			t.Errorf("reviewer with Toolbelt=\"full\" got %q — the role's ceiling was not applied", forbidden)
		}
	}
	if !has(got, "fs_read") || !has(got, "git_diff") {
		t.Errorf("reviewer belt lost its read tools: %v", got)
	}
}

func TestJudgeBeltIsMinimal(t *testing.T) {
	turn := &Turn{Role: config.RoleJudge, Toolbelt: "full"}
	got, err := turn.ResolveToolbelt(testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range got {
		if name != "fs_read" && name != "git_diff" {
			t.Errorf("judge belt contains %q; a judge reads candidates and nothing else", name)
		}
	}
}

// A turn may narrow.
func TestExplicitListNarrowsWithinCeiling(t *testing.T) {
	turn := &Turn{Role: config.RoleImplementer, Toolbelt: "fs_read,fs_patch"}
	got, err := turn.ResolveToolbelt(testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !has(got, "fs_read") || !has(got, "fs_patch") {
		t.Fatalf("got %v, want exactly fs_read and fs_patch", got)
	}
}

// A turn may NOT widen. This is the case that used to pass silently.
func TestExplicitListCannotWidenBeyondCeiling(t *testing.T) {
	turn := &Turn{Role: config.RoleReviewer, Toolbelt: "fs_read,fs_write"}
	_, err := turn.ResolveToolbelt(testRegistry(t))
	if err == nil {
		t.Fatal("a reviewer was granted fs_write; a turn must never widen a role's toolbelt")
	}
	if !strings.Contains(err.Error(), "narrow") {
		t.Errorf("error should explain the narrowing rule, got: %v", err)
	}
}

func TestUnknownToolStillRejected(t *testing.T) {
	turn := &Turn{Role: config.RoleImplementer, Toolbelt: "fs_read,not_a_tool"}
	if _, err := turn.ResolveToolbelt(testRegistry(t)); err == nil {
		t.Error("unknown tool name accepted; a typo would silently drop a tool")
	}
}

// An unknown role must fail closed, never open.
func TestUnknownRoleGetsNoTools(t *testing.T) {
	turn := &Turn{Role: config.Role("wizard"), Toolbelt: "full"}
	got, err := turn.ResolveToolbelt(testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("unknown role got %v; it must fail closed", got)
	}
}

// I1: no role's ceiling may contain a git-mutating tool.
func TestNoRoleCeilingExposesGitMutation(t *testing.T) {
	forbidden := []string{"git_commit", "git_checkout", "git_branch", "git_merge", "git_push", "git_worktree"}
	for _, role := range config.ValidRoles() {
		for _, name := range tools.RoleToolbelt(role) {
			for _, bad := range forbidden {
				if name == bad {
					t.Errorf("role %q may use %q — models must never mutate version control (I1)", role, bad)
				}
			}
		}
	}
}

// Only the implementer may change the working tree.
func TestOnlyImplementerHasMutatingTools(t *testing.T) {
	reg := testRegistry(t)
	for _, role := range config.ValidRoles() {
		if role == config.RoleImplementer {
			continue
		}
		for _, name := range reg.Available(role) {
			tool, err := reg.Get(name)
			if err != nil {
				continue
			}
			if tool.Mutating() {
				t.Errorf("role %q may use mutating tool %q", role, name)
			}
		}
	}
}

func TestValidateRejectsWideningTurn(t *testing.T) {
	s := &Script{
		Name:      "bad",
		MaxRounds: 1,
		Turns: []Turn{{
			Role: config.RoleReviewer, Toolbelt: "shell", Contract: "verdict", MaxTurns: 4,
		}},
	}
	err := s.Validate(testRegistry(t))
	if err == nil {
		t.Fatal("Validate accepted a script that widens a reviewer's toolbelt")
	}
	if !strings.Contains(err.Error(), "turn 0") {
		t.Errorf("error should locate the offending turn, got: %v", err)
	}
}

func TestValidateRejectsUnboundedLoops(t *testing.T) {
	base := func() *Script {
		return &Script{
			Name: "x", MaxRounds: 1,
			Turns: []Turn{{Role: config.RoleImplementer, Toolbelt: "full", MaxTurns: 4}},
		}
	}
	noRounds := base()
	noRounds.MaxRounds = 0
	if err := noRounds.Validate(testRegistry(t)); err == nil {
		t.Error("Validate accepted MaxRounds = 0 (I3)")
	}
	noTurns := base()
	noTurns.Turns[0].MaxTurns = 0
	if err := noTurns.Validate(testRegistry(t)); err == nil {
		t.Error("Validate accepted Turn.MaxTurns = 0 (I3)")
	}
}

func TestValidateRejectsUnknownRole(t *testing.T) {
	s := &Script{
		Name: "x", MaxRounds: 1,
		Turns: []Turn{{Role: config.Role("wizard"), Toolbelt: "full", MaxTurns: 4}},
	}
	if err := s.Validate(testRegistry(t)); err == nil {
		t.Error("Validate accepted an unknown role")
	}
}

// A human turn runs no agent loop, so it needs no toolbelt and must not be
// rejected for having none.
func TestValidateAllowsHumanTurn(t *testing.T) {
	s := &Script{
		Name: "council", MaxRounds: 2,
		Turns: []Turn{
			{Role: config.RoleArchitect, Toolbelt: "full", MaxTurns: 8},
			{Role: config.RoleHuman, MaxTurns: 1},
		},
	}
	if err := s.Validate(testRegistry(t)); err != nil {
		t.Errorf("human turn rejected: %v", err)
	}
}
