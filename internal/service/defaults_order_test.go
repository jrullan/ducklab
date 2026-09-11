package service

import (
	"reflect"
	"testing"
)

// B-394: the positional line-up echoed by /v1/defaults/modes is read by the
// desktop with "position is the role" (frontend/src/lib/seats.ts). One global
// order for every mode put the pair's reviewer into the advisor seat and the
// advisor into the reviewer seat on every launcher. Each mode is serialized in
// its own seat order.
func TestModeDefaultsEchoLineUpsInSeatOrder(t *testing.T) {
	s := writableService(t, "terra", "k3", "glm52", "j9", "arch")
	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24,
		ModeSeats: map[string]map[string][]string{
			"pair":       {"advisor": {"k3"}, "implementer": {"terra"}, "reviewer": {"glm52"}},
			"solo":       {"advisor": {"k3"}, "implementer": {"terra"}},
			"tournament": {"advisor": {"k3"}, "implementer": {"terra"}, "judge": {"j9"}},
			"council":    {"advisor": {"k3"}, "architect": {"arch"}, "reviewer": {"glm52"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	got := s.ModeDefaults().Ducklings
	want := map[string][]string{
		"pair":       {"terra", "k3", "glm52"},
		"solo":       {"terra", "k3"},
		"tournament": {"terra"},
		"council":    {"arch", "glm52"},
	}
	for mode, ids := range want {
		if !reflect.DeepEqual(got[mode], ids) {
			t.Errorf("%s line-up = %v, want seat order %v", mode, got[mode], ids)
		}
	}
}
