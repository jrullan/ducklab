package service

import (
	"os"
	"strings"
	"testing"
)

// The setting was saved, reported back, and gone on the next read.
//
// Every test of these wrote through the service and read back through the
// service, so the value never left memory and a failure to persist it looked
// exactly like success. The user changed a triager's cap from 6 to 20, watched
// it save, ran the work, and found 6 again.
func TestModeDefaultsSurviveTheConfigFile(t *testing.T) {
	s := writableService(t, "pato-uno")

	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24,
		Rounds:        map[string]int{"pair": 5},
		RoleTurns:     map[string]int{"triager": 20},
		Ducklings:     map[string][]string{"pair": {"pato-uno"}},
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(s.configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"role_turns", "triager", "rounds", "mode_ducklings"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the config file does not mention %q:\n%s", want, raw)
		}
	}
}
