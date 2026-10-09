package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// B-510: the final gate's exit_code is the command's own; a red gate over a
// passing command is red for the checks red_by names. The follow line says
// so for every red source, and never "exit 0" alone under a red gate. It
// used to print e.Data["exit"], which the final gate never set ("exit <nil>").
func TestB510FollowLineNamesWhatMadeTheGateRed(t *testing.T) {
	for _, check := range []string{"task_verification", "acceptance_probes", "app_smoke", "visual", "capability_coverage"} {
		raw := `{"gate":"red","command_gate":"tests","command":"npm test","exit_code":0,"effective_exit_code":1,
			"red_by":[{"check":"` + check + `","summary":"` + check + ` failed: why"}]}`
		var d map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			t.Fatal(err)
		}
		got := gateEventLine(d)
		if got != "gate red: command exit 0; red by "+check+" failed: why" {
			t.Errorf("%s: %q", check, got)
		}
	}
	for _, c := range []struct{ raw, want string }{
		// The command alone: its exit code is the gate's.
		{`{"gate":"red","exit_code":1,"effective_exit_code":1,"red_by":[{"check":"command","summary":"npm test exited 1"}]}`, "gate red: exit 1"},
		{`{"gate":"tests","exit_code":0,"effective_exit_code":0}`, "gate tests: exit 0"},
		// Recorded before B-510: exit_code was the effective code.
		{`{"gate":"red","exit_code":1}`, "gate red: exit 1"},
		// test-first's baseline writes exit.
		{`{"gate":"tests","exit":1,"phase":"before"}`, "gate tests: exit 1"},
	} {
		var d map[string]interface{}
		if err := json.Unmarshal([]byte(c.raw), &d); err != nil {
			t.Fatal(err)
		}
		if got := gateEventLine(d); got != c.want || strings.Contains(got, "<nil>") {
			t.Errorf("%s: %q, want %q", c.raw, got, c.want)
		}
	}
}
