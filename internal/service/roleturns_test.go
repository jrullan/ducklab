package service

import (
	"context"
	"strings"
	"testing"

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

	if got := s.turnsFor("triager", ScriptRoleTurns["triager"]); got != 6 {
		t.Fatalf("the script's own cap is not the fallback: %d", got)
	}
	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24, RoleTurns: map[string]int{"triager": 20},
	}); err != nil {
		t.Fatal(err)
	}
	if got := s.turnsFor("triager", 6); got != 20 {
		t.Errorf("cap = %d, want the configured 20", got)
	}
	// A role nobody configured keeps the script's own.
	if got := s.turnsFor("reviewer", 8); got != 8 {
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

	// Script-driven modes.
	pair := s.applyRoleTurns(strategy.PairScript(), 0)
	for _, turn := range pair.Turns {
		want := map[config.Role]int{config.RoleImplementer: 30, config.RoleReviewer: 3}[turn.Role]
		if want != 0 && turn.MaxTurns != want {
			t.Errorf("pair %s = %d, want %d", turn.Role, turn.MaxTurns, want)
		}
	}

	// And the ones that build their own.
	caps := s.roleTurnCaps()
	if got := strategy.CapFor(caps, config.RoleImplementer, 24); got != 30 {
		t.Errorf("tournament/split implementer = %d, want 30", got)
	}
	if got := strategy.CapFor(caps, config.RoleJudge, 1); got != 1 {
		t.Errorf("an unconfigured role lost its own cap: %d", got)
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
	pair := s.applyRoleTurns(strategy.PairScript(), 33)
	for _, turn := range pair.Turns {
		if turn.Role == config.RoleHuman {
			continue
		}
		if turn.MaxTurns != 33 {
			t.Errorf("pair %s = %d, want the run's own 33", turn.Role, turn.MaxTurns)
		}
	}
}

// Negative is "no cap", the same word the budget lifts speak: finite in
// letter (I3), beyond use in practice, with the token and cost budgets still
// guarding every call. A human turn keeps its cap — the lift unblocks
// models, not people.
func TestANegativeOverrideLiftsTheCap(t *testing.T) {
	s := writableService(t, "pato-uno")

	review := s.applyRoleTurns(strategy.ReviewScript(true), -1)
	for _, turn := range review.Turns {
		if turn.Role == config.RoleHuman {
			if turn.MaxTurns != 1 {
				t.Errorf("the human turn's cap moved: %d", turn.MaxTurns)
			}
			continue
		}
		if turn.MaxTurns != uncappedTurns {
			t.Errorf("%s = %d, want uncapped (%d)", turn.Role, turn.MaxTurns, uncappedTurns)
		}
	}

	// And the modes that build their own turns read the same lift.
	caps := s.roleTurnCapsFor(-1)
	if got := strategy.CapFor(caps, config.RoleImplementer, 24); got != uncappedTurns {
		t.Errorf("tournament/split implementer = %d, want uncapped (%d)", got, uncappedTurns)
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
}
