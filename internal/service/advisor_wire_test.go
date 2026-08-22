package service

import (
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/tools"
)

// ask_advisor on a test-first run answered "no advisor is seated" while the
// seat chip showed qwen38-max sitting right there: the closure was wired on
// the build path only (B-115). One helper now arms both paths; this pins it
// at the helper so a third path cannot quietly repeat the family.
func TestWireAdvisorArmsTheToolWhenASeatExists(t *testing.T) {
	s := writableService(t, "pato-uno")
	rs := &runState{run: &runlog.Run{ProjectID: "p", Roster: map[string]string{"advisor": "pato-uno"}}}
	ectx := &tools.ExecContext{}
	s.wireAdvisor(rs, ectx)
	if ectx.OnAskAdvisor == nil {
		t.Fatal("a seated advisor did not arm ask_advisor")
	}

	// A fleet with zero ducklings is the one honest way to have nobody to
	// ask: every pickAdvisor fallback ends at the fleet.
	bare := writableService(t)
	ectx2 := &tools.ExecContext{}
	bare.wireAdvisor(&runState{run: &runlog.Run{ProjectID: "p", Roster: map[string]string{}}}, ectx2)
	if ectx2.OnAskAdvisor != nil {
		t.Fatal("with nobody to ask, the tool must stay unarmed so its refusal stays true")
	}
}
