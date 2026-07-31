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
