package agent

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

// A document seat is not told that tests will run after it: told so, a
// reviewer called verify_run twice on a requirements draft.
func TestDocumentTurnsAreNotPromisedATestGate(t *testing.T) {
	doc := []*Turn{
		{Role: config.RoleArchitect, Contract: "markdown_sections:REQ"},
		{Role: config.RoleReviewer, Contract: "markdown_sections:SPEC"},
		{Role: config.RoleScribe},
		{Role: config.RoleTriager},
	}
	for _, turn := range doc {
		if got := gateDescFor(turn); !strings.Contains(got, "no tests run") || !strings.Contains(got, "verify_run has nothing to run") {
			t.Errorf("%s/%s promised a test gate: %q", turn.Role, turn.Contract, got)
		}
	}
	code := &Turn{Role: config.RoleReviewer, Contract: "verdict"}
	if got := gateDescFor(code); !strings.Contains(got, "will run tests") {
		t.Errorf("a code reviewer lost its gate description: %q", got)
	}
	if got := gateDescFor(&Turn{Role: config.RoleImplementer}); !strings.Contains(got, "will run tests") {
		t.Errorf("the implementer lost its gate description: %q", got)
	}
}

func TestFirstAndLastCharsBoundWithoutCuttingRunes(t *testing.T) {
	s := strings.Repeat("ñ", 10)
	if got := firstChars(s, 4); got != "ññññ…" {
		t.Errorf("firstChars = %q", got)
	}
	if got := lastChars(s, 4); got != "…ññññ" {
		t.Errorf("lastChars = %q", got)
	}
	if got := firstChars("short", 10); got != "short" {
		t.Errorf("firstChars kept short input wrong: %q", got)
	}
}
