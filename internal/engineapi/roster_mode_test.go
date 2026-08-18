package engineapi

import (
	"net/http/httptest"
	"testing"
)

// The desktop sends a roster write's mode in the body; the CLI in the query.
// Both must reach the service, or a pin made on Solo's column becomes a role
// pin for every mode.
func TestRosterWriteModeComesFromBodyOrQuery(t *testing.T) {
	if got := requestMode(httptest.NewRequest("PUT", "/v1/projects/p/roster", nil), "solo"); got != "solo" {
		t.Errorf("body mode ignored: %q", got)
	}
	if got := requestMode(httptest.NewRequest("PUT", "/v1/projects/p/roster?mode=pair", nil), "solo"); got != "pair" {
		t.Errorf("query mode must win when present: %q", got)
	}
	if got := requestMode(httptest.NewRequest("DELETE", "/v1/projects/p/roster", nil), ""); got != "" {
		t.Errorf("no mode anywhere must stay empty (a role pin): %q", got)
	}
}
