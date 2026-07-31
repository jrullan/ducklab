package service

import (
	"context"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// The preview beside the start button described a different run than the one
// about to start: a person saved k3-then-sonnet for council, and it went on
// warning that sonnet would critique its own draft — reading the roster while
// the run would read the line-up. A preview that lies about who will run tells
// the person their setting did not take.
func TestTheRosterPreviewSpeaksForTheMode(t *testing.T) {
	s := writableService(t, "pato-uno", "pato-dos", "pato-tres")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — A\n\n**Priority:** must\n",
	})
	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24,
		Ducklings:     map[string][]string{"council": {"pato-dos", "pato-tres"}},
	}); err != nil {
		t.Fatal(err)
	}

	view, err := s.RosterGet(context.Background(), id, "council")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]RosterEntry{}
	for _, e := range view.Entries {
		got[e.Role] = e
	}
	if got["architect"].Duckling != "pato-dos" || got["reviewer"].Duckling != "pato-tres" {
		t.Errorf("preview = architect %s / reviewer %s, want the line-up",
			got["architect"].Duckling, got["reviewer"].Duckling)
	}
	// And it says WHERE the answer came from, so a person can find the setting.
	if got["architect"].Source != "council line-up" {
		t.Errorf("source = %q", got["architect"].Source)
	}
	// Two distinct ducklings: no self-critique warning.
	if view.Warning != "" {
		t.Errorf("warning = %q for a decorrelated pair", view.Warning)
	}

	// Without a mode, the bare roster answer stands, as roster editing needs.
	bare, err := s.RosterGet(context.Background(), id, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range bare.Entries {
		if e.Source == "council line-up" {
			t.Errorf("the bare roster claims a line-up: %+v", e)
		}
	}
}

// Three ticked boxes seat three ducklings: the first drafts, the others each
// critique. The preview lists one reviewer entry per critic, because the run
// pins each critique turn to its own model — a preview naming only one of the
// critics is the preview lying about who will run.
func TestTheRosterPreviewListsEveryCritic(t *testing.T) {
	s := writableService(t, "pato-uno", "pato-dos", "pato-tres")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — A\n\n**Priority:** must\n",
	})
	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24,
		Ducklings:     map[string][]string{"council": {"pato-uno", "pato-dos", "pato-tres"}},
	}); err != nil {
		t.Fatal(err)
	}

	view, err := s.RosterGet(context.Background(), id, "council")
	if err != nil {
		t.Fatal(err)
	}
	var reviewers []string
	for _, e := range view.Entries {
		if e.Role == "reviewer" {
			reviewers = append(reviewers, e.Duckling)
			if e.Source != "council line-up" {
				t.Errorf("critic %s attributed to %q", e.Duckling, e.Source)
			}
		}
	}
	if len(reviewers) != 2 || reviewers[0] != "pato-dos" || reviewers[1] != "pato-tres" {
		t.Errorf("preview lists critics %v, want [pato-dos pato-tres]", reviewers)
	}
}

// The critics a stage seats are the line-up minus the drafter, and a line-up
// entry naming a deleted duckling degrades the council instead of closing it.
func TestStageCriticsComeFromTheLineup(t *testing.T) {
	s := writableService(t, "pato-uno", "pato-dos", "pato-tres")
	// Saving validates existence, so a dead entry can only mean a duckling
	// deleted after the line-up was saved — planted directly here.
	s.cfg.Defaults.ModeDucklings = map[string][]string{
		"council": {"pato-uno", "pato-dos", "pato-gone", "pato-tres"},
	}
	got := s.stageCritics("council")
	if len(got) != 2 || got[0] != "pato-dos" || got[1] != "pato-tres" {
		t.Errorf("critics = %v, want [pato-dos pato-tres] with the deleted one skipped", got)
	}
	if s.stageCritics("solo") != nil {
		t.Error("a mode with no line-up invented critics")
	}
}
