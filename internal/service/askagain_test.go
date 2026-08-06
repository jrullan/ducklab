package service

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/tools"
)

// The person answered "which contract for the personal-challenge endpoint",
// the run resumed, and it asked the SAME question again in new words — the
// answer was filed under a hash of the exact original wording, which the
// replayed model no longer used. The replayed prompt must carry the decisions
// themselves; the hash match is a bonus, not the mechanism.
func TestAnsweredDecisionsRideOnTheReplayedPrompt(t *testing.T) {
	rs := &runState{}
	q := "What response contract should the acceptance test enforce for the challenge API?"
	rs.recordAnswer(tools.QuestionID(q), q, "Bare snake_case JSON; grouped lists; value/target progress.")

	got := rs.answeredDecisions()
	for _, want := range []string{
		q,
		"value/target progress",
		"binding",
		"do not ask about them again, in any wording",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the replayed prompt is missing %q:\n%s", want, got)
		}
	}

	// And a run that was never asked anything adds nothing.
	if fresh := (&runState{}).answeredDecisions(); fresh != "" {
		t.Errorf("a fresh run carries a decisions section: %q", fresh)
	}
}
