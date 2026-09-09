package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/strategy"
)

// A triager used all six of its turns calling tools, never answered, and its own
// failure message told the reader to raise the turn cap for that role. There was
// nowhere to raise it: the caps were literals in five files — council 12, pair
// 24 and 8, triage 6 — and Settings had only the global fallback, which no turn
// ever used because every turn sets its own.
func TestARolesTurnCapCanBeRaised(t *testing.T) {
	s := writableService(t, "pato-uno")

	if got := strategy.CapFor(s.resolveTurnCaps("triage", 0).Caps, config.RoleTriager, ScriptRoleTurns["triager"]); got != 6 {
		t.Fatalf("the script's own cap is not the fallback: %d", got)
	}
	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24, RoleTurns: map[string]int{"triager": 20},
	}); err != nil {
		t.Fatal(err)
	}
	if got := strategy.CapFor(s.resolveTurnCaps("triage", 0).Caps, config.RoleTriager, 6); got != 20 {
		t.Errorf("cap = %d, want the configured 20", got)
	}
	// A role nobody configured keeps the script's own.
	if got := strategy.CapFor(s.resolveTurnCaps("triage", 0).Caps, config.RoleReviewer, 8); got != 8 {
		t.Errorf("reviewer = %d, want 8", got)
	}
}

// Walking a script reaches four modes; tournament and split build their turns
// themselves. A setting that applies to some modes is worse than none.
func TestTheCapReachesEveryMode(t *testing.T) {
	s := writableService(t, "pato-uno")
	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24, RoleTurns: map[string]int{"implementer": 30, "reviewer": 3},
	}); err != nil {
		t.Fatal(err)
	}

	caps := s.resolveTurnCaps("build", 0).Caps
	if got := strategy.CapFor(caps, config.RoleImplementer, 24); got != 30 {
		t.Errorf("script implementer = %d, want 30", got)
	}
	if got := strategy.CapFor(caps, config.RoleReviewer, 8); got != 3 {
		t.Errorf("script reviewer = %d, want 3", got)
	}
	if got := strategy.CapFor(caps, config.RoleJudge, 1); got != 1 {
		t.Errorf("an unconfigured role lost its own cap: %d", got)
	}
}

// B-275 adds phase portions; it does not erase the script's role-specific
// design. With untouched Settings a judge must remain a one-call chooser and
// a triager a six-call classifier, not inherit the global implementer plate.
func TestUntouchedConfigurationKeepsScriptRoleCaps(t *testing.T) {
	s := writableService(t, "pato-uno")
	resolved := s.resolveTurnCaps("review", 0)
	if _, ok := resolved.Caps[config.RoleJudge]; ok {
		t.Fatalf("default resolver populated judge: %#v", resolved.Caps)
	}
	if got := strategy.CapFor(resolved.Caps, config.RoleTriager, 6); got != 6 {
		t.Fatalf("triager = %d, want script default 6", got)
	}

	var got int
	script := &strategy.Script{
		Name: "judge-default-probe", MaxRounds: 1, Until: "round == 1",
		Turns: []strategy.Turn{{
			Role: config.RoleJudge, Toolbelt: "read-only", Contract: "freeform", MaxTurns: 1,
		}},
	}
	_, err := strategy.ExecuteScript(context.Background(), script, &strategy.ExecuteParams{
		TurnCaps: resolved.Caps, TurnCapSources: resolved.Sources,
		Roster: map[config.Role]config.DucklingID{config.RoleJudge: "pato-uno"},
		Runner: func(_ context.Context, turn *strategy.Turn, _ config.DucklingID, _ string, _ []string, _ strategy.TurnContext) (*agent.Outcome, error) {
			got = turn.MaxTurns
			return &agent.Outcome{Text: "chosen"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("judge ran with %d calls, want script default 1", got)
	}
}

func TestAnUnknownRoleIsRefused(t *testing.T) {
	s := writableService(t, "pato-uno")
	err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24, RoleTurns: map[string]int{"inspector": 5},
	})
	if err == nil || !strings.Contains(err.Error(), "inspector") {
		t.Errorf("err = %v", err)
	}
}

// Above forty a turn stops being bounded in any useful sense (I3).
func TestTheCapIsBounded(t *testing.T) {
	s := writableService(t, "pato-uno")
	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24, RoleTurns: map[string]int{"implementer": 500},
	}); err == nil {
		t.Error("500 calls in one turn was accepted")
	}
}

// The ceiling was 40 when a turn meant reviewing a diff. A critic verifying
// an adoption survey against a real codebase spent all 40 on honest reads and
// searches, twice — the person raising the cap found a wall where a setting
// should be. The real resources are bounded by the run budget; this cap only
// guards against circling.
func TestTheTurnCeilingFitsASurveyingCritic(t *testing.T) {
	s := writableService(t, "pato-uno")
	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24, RoleTurns: map[string]int{"reviewer": 120},
	}); err != nil {
		t.Fatalf("120 reviewer turns refused: %v", err)
	}
	// Still a ceiling, not an absence of one (I3).
	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24, RoleTurns: map[string]int{"reviewer": 500},
	}); err == nil {
		t.Error("an effectively unbounded cap was accepted")
	}
}

// The per-run override rode only ExecuteParams.TurnCaps — which tournament
// and split read, and the script modes never did. So the launcher's
// "calls/reply" was accepted, recorded, and silently ignored in exactly the
// modes most runs use.
func TestThePerRunOverrideReachesScriptModes(t *testing.T) {
	s := writableService(t, "pato-uno")
	resolved := s.resolveTurnCaps("build", 33)
	for _, turn := range strategy.PairScript().Turns {
		if turn.Role == config.RoleHuman {
			continue
		}
		if got := strategy.CapFor(resolved.Caps, turn.Role, turn.MaxTurns); got != 33 {
			t.Errorf("pair %s request = %d, want 33", turn.Role, got)
		}
	}
}

// The UI exposed several numbers without saying which one won. Keep the
// resolution executable in one place: an implementer's global/phase portion,
// then role, then run. Script ceilings are applied when the turn is known.
func TestCallsPerReplyPrecedenceIsGlobalPhaseRoleRun(t *testing.T) {
	s := writableService(t, "pato-uno")
	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24,
		PhaseTurns:    map[string]int{"build": 40, "test": 60},
		RoleTurns:     map[string]int{"reviewer": 100},
	}); err != nil {
		t.Fatal(err)
	}

	build := s.resolveTurnCaps("build", 0)
	if got := build.Caps[config.RoleImplementer]; got != 40 {
		t.Fatalf("build implementer = %d, want phase default 40", got)
	}
	if got := build.Sources[config.RoleImplementer]; got != "build default" {
		t.Fatalf("build implementer source = %q", got)
	}
	if got := build.Caps[config.RoleReviewer]; got != 100 {
		t.Fatalf("build reviewer = %d, want role override 100", got)
	}
	if got := build.Sources[config.RoleReviewer]; got != "reviewer role default" {
		t.Fatalf("build reviewer source = %q", got)
	}

	testCaps := s.resolveTurnCaps("test", 0)
	if got := testCaps.Caps[config.RoleImplementer]; got != 60 {
		t.Fatalf("test implementer = %d, want phase default 60", got)
	}
	if got := testCaps.Sources[config.RoleImplementer]; got != "test default" {
		t.Fatalf("test implementer source = %q", got)
	}

	run := s.resolveTurnCaps("build", 70)
	if got := run.Caps[config.RoleReviewer]; got != 70 {
		t.Fatalf("run reviewer = %d, want run override 70", got)
	}
	if got := run.Sources[config.RoleReviewer]; got != "run override" {
		t.Fatalf("run reviewer source = %q", got)
	}

	lifted := s.resolveTurnCaps("build", -1)
	if got := lifted.Caps[config.RoleReviewer]; got != uncappedTurns {
		t.Fatalf("lifted reviewer = %d, want %d", got, uncappedTurns)
	}
	if got := lifted.Sources[config.RoleReviewer]; got != "run no-cap" {
		t.Fatalf("lifted reviewer source = %q", got)
	}
}

func TestEmptyPhaseUsesGlobalFallbackForImplementerOnly(t *testing.T) {
	s := writableService(t, "pato-uno")
	resolved := s.resolveTurnCaps("test", 0)
	if got := resolved.Caps[config.RoleImplementer]; got != 24 {
		t.Fatalf("test implementer = %d, want global fallback 24", got)
	}
	if got := resolved.Sources[config.RoleImplementer]; got != "global default" {
		t.Fatalf("test implementer source = %q", got)
	}
	if _, ok := resolved.Caps[config.RoleReviewer]; ok {
		t.Fatalf("phase fallback replaced reviewer script cap: %#v", resolved.Caps)
	}
}

func TestModeDefaultsPublishesPhaseDefaultsAndScriptCeilings(t *testing.T) {
	s := writableService(t, "pato-uno")
	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24,
		PhaseTurns:    map[string]int{"build": 40, "test": 60},
	}); err != nil {
		t.Fatal(err)
	}

	got := s.ModeDefaults()
	if got.PhaseTurns["build"] != 40 || got.PhaseTurns["test"] != 60 {
		t.Fatalf("phase defaults = %#v", got.PhaseTurns)
	}
	if got.TurnCeilings["pair.reviewer"] != 8 {
		t.Fatalf("pair reviewer ceiling = %d, want 8", got.TurnCeilings["pair.reviewer"])
	}
}

func TestTaskSeatClassificationRecognizesLocalBuildImplementer(t *testing.T) {
	s := serviceWithDucklings(t, "pato-local")
	s.cfgMu.Lock()
	provider := s.cfg.Providers["fake"]
	provider.BaseURL = "http://127.0.0.1:1234/v1"
	s.cfg.Providers["fake"] = provider
	s.cfgMu.Unlock()

	projectID, _ := projectWithConfig(t, s, "local-seat")
	if !s.smallImplementerSeat(projectID) {
		t.Fatal("local provider selected by the build roster was not classified as a small implementer seat")
	}
}

func TestDeclaredTierOverridesProviderLocality(t *testing.T) {
	s := serviceWithDucklings(t, "pato-local")
	s.cfgMu.Lock()
	provider := s.cfg.Providers["fake"]
	provider.BaseURL = "http://127.0.0.1:1234/v1"
	s.cfg.Providers["fake"] = provider
	duck := s.cfg.Ducklings["pato-local"]
	duck.Tier = config.ModelTierLarge
	s.cfg.Ducklings["pato-local"] = duck
	s.cfgMu.Unlock()

	projectID, _ := projectWithConfig(t, s, "declared-large-seat")
	if s.smallImplementerSeat(projectID) {
		t.Fatal("declared large tier was overridden by local provider address")
	}
	if name, source, small := s.stageSupportProfile(projectID, "small"); name != "small" || source != "request" || !small {
		t.Fatalf("explicit support profile = %q/%q/%v", name, source, small)
	}
	if name, source, small := s.stageSupportProfile(projectID, "auto"); name != "standard" || source != "implementer tier" || small {
		t.Fatalf("automatic support profile = %q/%q/%v", name, source, small)
	}
}

// Negative is "no cap", the same word the budget lifts speak: finite in
// letter (I3), beyond use in practice, with the token and cost budgets still
// guarding every call. A human turn keeps its cap — the lift unblocks
// models, not people.
func TestANegativeOverrideLiftsTheCap(t *testing.T) {
	s := writableService(t, "pato-uno")
	caps := s.resolveTurnCaps("review", -1).Caps
	if got := strategy.CapFor(caps, config.RoleImplementer, 24); got != uncappedTurns {
		t.Errorf("implementer = %d, want uncapped (%d)", got, uncappedTurns)
	}
	if got := strategy.CapFor(caps, config.RoleReviewer, 8); got != uncappedTurns {
		t.Errorf("reviewer = %d, want uncapped (%d)", got, uncappedTurns)
	}
	if _, ok := caps[config.RoleHuman]; ok {
		t.Error("the lift reached the human role")
	}
}

// The reviewer that died on exactly its hundredth call had no remedy: resume
// re-entered the same ceiling. The calls lift is the remedy — durable on the
// record for the resume, atomic for any reply still in flight.
func TestTheCallsCapCanBeLiftedOnALiveRun(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)
	writeRun(t, entry.Path, projectID, "r-cap", "running")
	s.RecoverRuns(context.Background())

	s.runsMu.RLock()
	rs := s.runs["r-cap"]
	s.runsMu.RUnlock()
	rs.run.Status = "running"

	run, err := s.RunBudgetLift(context.Background(), "r-cap", "calls")
	if err != nil {
		t.Fatal(err)
	}
	if run.AgentTurns != -1 {
		t.Errorf("agent_turns = %d, want -1 (lifted) on the record", run.AgentTurns)
	}
	if !rs.capLifted.Load() {
		t.Error("the live flag never flipped; a reply in flight would die at the old cap")
	}
}

// A resumed run keeps EVERYTHING it was started with. The note and the calls
// cap were dropped here once: the instruction the person wrote and the
// ceiling they lifted both quietly reverted at exactly the moment they were
// resuming past.
func TestResumeCarriesTheNoteAndTheLiftedCap(t *testing.T) {
	req := resumeRequest(&runlog.Run{
		TaskID: "T-051", Mode: "pair", Autonomy: "guarded", Stream: true,
		Note:       "close every connection — the fix leaked",
		AgentTurns: -1,
		Roster:     map[string]string{"implementer": "glm52", "reviewer": "qwen38-max", "architect": "pato-sonnet"},
	})
	if req.Note == "" {
		t.Error("the human's note was dropped on resume")
	}
	if req.AgentTurns != -1 {
		t.Errorf("agent_turns = %d, want the lifted -1", req.AgentTurns)
	}
	if !req.resumed {
		t.Error("a resume request must say it is one")
	}
	// The seats, in seat order — glm52's resumed run once came back
	// speaking through the config default, and only the upstream field
	// told on it.
	if len(req.Ducklings) != 2 || req.Ducklings[0] != "glm52" || req.Ducklings[1] != "qwen38-max" {
		t.Errorf("ducklings = %v, want the recorded seats [glm52 qwen38-max]", req.Ducklings)
	}
}

// A spec architect that asked the human died as "human input needed" with no
// question on the record and no way back in: the stage's Execute closure
// returned the raw error (the pendingErr wrap lived only in the mode
// dispatch), and RunResume refused every non-build stage. The request now
// outlives the goroutine, and an answer re-enters the stage that asked.
func TestAStageRunResumesThroughItsPersistedRequest(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)

	run := &runlog.Run{
		ID: "r-spec", ProjectID: projectID, Stage: "spec", Mode: "council",
		Status: "paused", PendingKind: "question", Autonomy: "guarded",
		PendingData: map[string]interface{}{"question_id": "q1", "question": "OAuth or sessions?"},
		StartedAt:   "2026-08-10T21:54:18Z",
	}
	w, err := runlog.NewWriter(entry.Path, run)
	if err != nil {
		t.Fatal(err)
	}
	rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: entry.Path, done: make(chan struct{})}
	close(rs.done)
	s.runsMu.Lock()
	s.runs[run.ID] = rs
	s.runsMu.Unlock()

	writeStageRequest(rs.runDir, StageRequest{Stage: "spec", Mode: "council", Revise: "add OAuth"})

	got, rerr := s.RunResume(context.Background(), "r-spec")
	if rerr != nil {
		t.Fatalf("a stage run with a persisted request refused to resume: %v", rerr)
	}
	if got.Status != "running" || got.PendingKind != "" {
		t.Errorf("status = %s pending = %q, want running with the wait cleared", got.Status, got.PendingKind)
	}

	// The round trip preserves what was asked.
	req, ok := loadStageRequest(rs.runDir)
	if !ok || req.Stage != "spec" || req.Revise != "add OAuth" {
		t.Errorf("request did not survive the disk: %+v ok=%v", req, ok)
	}

	// The re-entered stage runs in a goroutine that must not outlive the
	// test: wait for it (it fails fast — no fleet is configured) so the
	// TempDir cleanup does not race its writes.
	select {
	case <-rs.done:
	case <-time.After(15 * time.Second):
		t.Fatal("the resumed stage never finished")
	}
}

// A stage run from before requests were persisted has nothing to re-enter
// with; it must refuse, not guess.
func TestAStageRunWithoutARequestStillRefuses(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)
	run := &runlog.Run{
		ID: "r-old", ProjectID: projectID, Stage: "spec", Mode: "council",
		Status: "paused", PendingKind: "question", StartedAt: "2026-08-10T21:00:00Z",
	}
	w, err := runlog.NewWriter(entry.Path, run)
	if err != nil {
		t.Fatal(err)
	}
	rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: entry.Path, done: make(chan struct{})}
	close(rs.done)
	s.runsMu.Lock()
	s.runs[run.ID] = rs
	s.runsMu.Unlock()

	if _, err := s.RunResume(context.Background(), "r-old"); err == nil {
		t.Error("resumed a stage run that has no persisted request")
	}
}
