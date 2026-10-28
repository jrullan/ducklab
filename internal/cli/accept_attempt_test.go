package cli

import (
	"bytes"
	"strings"
	"testing"
)

// B-529 (second review of #181): `run show` names the accept attempt, in
// progress and settled, so a person can tell which accept a settlement is.
func TestRunShowNamesTheAcceptAttempt(t *testing.T) {
	var b bytes.Buffer
	printRunSummary(&b, map[string]interface{}{"id": "r-1", "status": "paused", "accept_in_progress": true, "accept_attempt": "acc-2"})
	if !strings.Contains(b.String(), "accept: in progress (attempt acc-2)") {
		t.Fatalf("in-progress summary lacks the attempt:\n%s", b.String())
	}
	b.Reset()
	printRunSummary(&b, map[string]interface{}{"id": "r-1", "status": "done",
		"last_accept": map[string]interface{}{"attempt": "acc-2", "outcome": "accepted", "commit_sha": "abc"}})
	if !strings.Contains(b.String(), "last accept: attempt acc-2: accepted abc") {
		t.Fatalf("settled summary lacks the attempt:\n%s", b.String())
	}
}
