package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// B-510: a run's timeline, as a consultant or advisor reads it, says a red
// gate over a passing command is red and why, for every red source; "gate
// exit 0" alone would read as a pass.
func TestB510TimelineSaysWhatMadeTheGateRed(t *testing.T) {
	for _, check := range []string{"task_verification", "acceptance_probes", "app_smoke", "visual", "capability_coverage"} {
		raw := `{"gate":"red","command_gate":"tests","command":"npm test","exit_code":0,"effective_exit_code":1,
			"red_by":[{"check":"` + check + `","summary":"` + check + ` failed: why"}]}`
		var d map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			t.Fatal(err)
		}
		got := runTimelineEntry("gate", d)
		if got != "- gate exit 0: npm test (gate red; "+check+" failed: why)" {
			t.Errorf("%s: %q", check, got)
		}
	}
	for _, c := range []struct{ raw, want string }{
		{`{"gate":"red","command":"npm test","exit_code":1,"effective_exit_code":1,"red_by":[{"check":"command","summary":"npm test exited 1"}]}`, "- gate exit 1: npm test (gate red)"},
		{`{"gate":"red","command":"npm test","exit_code":1}`, "- gate exit 1: npm test (gate red)"},
		{`{"gate":"tests","command":"npm test","exit_code":0,"effective_exit_code":0}`, "- gate exit 0: npm test"},
	} {
		var d map[string]interface{}
		if err := json.Unmarshal([]byte(c.raw), &d); err != nil {
			t.Fatal(err)
		}
		if got := runTimelineEntry("gate", d); got != c.want || strings.Contains(got, "<nil>") {
			t.Errorf("%s: %q, want %q", c.raw, got, c.want)
		}
	}
}
