package service

import (
	"strings"

	"github.com/jrullan/ducklab/internal/runlog"
)

// appendDecision records the words a decider gave with a gate decision —
// accept's message, abort's and resume's reason — as a "decision" event the
// desktop renders in the run's lane.
//
// B-524: an MCP operator accepted a triage "with the person's adjustment"
// written into its reason. The triage path recorded only "accepted by
// mcp:opencode"; the words reached no event, no commit and no screen, and the
// operator reported the adjustment as applied. Words given with a decision
// are either recorded here or refused by the door that received them — never
// dropped.
func appendDecision(w *runlog.Writer, action, actor, reason string) {
	reason = strings.TrimSpace(reason)
	if w == nil || reason == "" {
		return
	}
	_ = w.AppendEvent("decision", map[string]interface{}{
		"action": action, "actor": resumeActor(actor), "reason": reason,
	})
}
