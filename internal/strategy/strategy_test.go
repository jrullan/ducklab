package strategy

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/conv"
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

func TestNoneToolbeltIsActuallyEmpty(t *testing.T) {
	turn := &Turn{Role: config.RoleArchitect, Toolbelt: "none"}
	got, err := turn.ResolveToolbelt(testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("none toolbelt = %v, want empty", got)
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

// Every other script drives towards a green gate. This one is the opposite,
// and reusing solo's condition cost two wasted rounds on a real run: the model
// kept trying to make its own new test pass, which it cannot — the write guard
// allows only test files — and should not, because passing is the failure.
// Reversed from "stops on red": with one round there is no second attempt
// for any expression to buy, and `gate == "red"` made the round gate look
// load-bearing — so the service wired one in and the suite ran twice on an
// unchanged tree, minutes per test-first. The stage's own after-gate is the
// honest measurement; solo runs no round gate at all.
func TestTheTestFirstScriptRunsOneRoundWithoutAGate(t *testing.T) {
	s := TestFirstScript()
	if s.Until != "round == 1" {
		t.Errorf("Until = %q, want the one round stated plainly", s.Until)
	}
	if s.MaxRounds != 1 {
		t.Errorf("MaxRounds = %d; there is no second attempt to loop towards", s.MaxRounds)
	}
	if len(s.Turns) != 1 || s.Turns[0].Role != config.RoleImplementer {
		t.Errorf("turns = %+v", s.Turns)
	}
	// And it must still compile as an expression, or the run dies at load.
	if _, err := conv.Compile(s.Until); err != nil {
		t.Errorf("Until does not compile: %v", err)
	}
}

// A stage always ran a council, whatever anyone asked for: the mode field
// existed on the request and was never read. Council stays the default —
// it is what 05 §4.4 names — but the choice is real, because council's value
// is a second model critiquing the draft and that is not always worth its
// cost.
func TestAnArtifactStageCanRunSolo(t *testing.T) {
	council := ArtifactScript("REQ", "", nil)
	if council.Name != "council" || len(council.Turns) < 3 {
		t.Errorf("the default is not a council: %+v", council)
	}
	if got := ArtifactScript("REQ", "council", nil); got.Name != "council" {
		t.Errorf("explicit council = %q", got.Name)
	}

	solo := ArtifactScript("REQ", "solo", nil)
	if solo.Name != "solo" {
		t.Fatalf("solo = %q", solo.Name)
	}
	if len(solo.Turns) != 1 || solo.Turns[0].Role != config.RoleArchitect {
		t.Errorf("solo should be one architect and nothing else: %+v", solo.Turns)
	}
	// No reviewer means no verdict to wait on, so waiting for one would hang
	// the stage for its whole round budget.
	if !strings.Contains(solo.Until, "round") {
		t.Errorf("solo waits on a verdict nobody produces: %q", solo.Until)
	}
	if _, err := conv.Compile(solo.Until); err != nil {
		t.Errorf("solo's Until does not compile: %v", err)
	}
	// Both must produce the same shape of document, or the mode changes what
	// gets written rather than who writes it.
	if solo.Turns[0].Contract != council.Turns[0].Contract {
		t.Errorf("contracts differ: %q vs %q", solo.Turns[0].Contract, council.Turns[0].Contract)
	}
}

// A typo should not stop someone drafting.
func TestAnUnknownArtifactModeFallsBackToCouncil(t *testing.T) {
	if got := ArtifactScript("REQ", "tournament", nil); got.Name != "council" {
		t.Errorf("got %q, want the default", got.Name)
	}
}

// T-067: frontend/dist's minified bundle rode the reviewer's prompt at 644KB
// and was re-sent on all 22 calls of its loop — 4.7M of the run's 6M tokens
// were one generated file. The prompt assembler, not the diff producer, is
// where the bound belongs: the record and the diff tab still carry the full
// change.
func TestTheReviewerPromptBoundsAGiantFileDiff(t *testing.T) {
	bundle := "diff --git a/dist/app.min.js b/dist/app.min.js\n+++ b/dist/app.min.js\n" +
		strings.Repeat("+"+strings.Repeat("z", 200)+"\n", 500)
	source := "diff --git a/src/units.js b/src/units.js\n+++ b/src/units.js\n+export const KG_PER_LB = 0.4536;\n"

	params := &ExecuteParams{
		Prompt: "## Your task\n\nT-067",
		Diff:   func() (string, error) { return source + bundle, nil },
	}
	turn := &Turn{Role: config.RoleReviewer}
	prompt, err := buildPrompt(turn, params, &conv.Transcript{}, nil, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "KG_PER_LB") {
		t.Error("the real change fell out of the reviewer's prompt")
	}
	if strings.Contains(prompt, strings.Repeat("z", 200)) {
		t.Error("the generated file still rides the reviewer's prompt whole")
	}
	if !strings.Contains(prompt, "dist/app.min.js") {
		t.Error("the omitted file is not named")
	}
}
