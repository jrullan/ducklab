package service

import (
	"fmt"
	"strings"

	"github.com/jrullan/ducklab/internal/runlog"
)

// appendDecisionEvent is the one durable write behind every recorded
// decision. A variable so a test can make the run log refuse it.
var appendDecisionEvent = func(w *runlog.Writer, data map[string]interface{}) error {
	return w.AppendEvent("decision", data)
}

// appendDecision records the words a decider gave with a gate decision —
// accept's message, reject's, abort's and resume's reason — as a "decision"
// event the desktop renders in the run's lane, attributed to actor.
//
// B-524: an MCP operator accepted a triage "with the person's adjustment"
// written into its reason. The triage path recorded only "accepted by
// mcp:opencode": the words reached no event, no commit and no screen, and the
// operator reported the adjustment as applied. Words given with a decision
// are either recorded here or refused by the door that received them — never
// dropped.
//
// Every caller records BEFORE it changes the run, and refuses the decision
// when this fails (review of #177): recording after the fact, with the error
// swallowed, let a log that stopped accepting writes drop the words again
// while the decision went through.
func appendDecision(w *runlog.Writer, action, actor, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil
	}
	err := fmt.Errorf("no run log")
	if w != nil {
		err = appendDecisionEvent(w, map[string]interface{}{
			"action": action, "actor": resumeActor(actor), "reason": reason,
		})
	}
	if err != nil {
		return fmt.Errorf("%s not taken: its reason could not be recorded (%v); nothing was decided — retry once the run log accepts writes", action, err)
	}
	return nil
}
