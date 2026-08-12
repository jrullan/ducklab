package service

import (
	"context"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// B-041 was moved from fixed back to in_progress six minutes after its task
// was accepted, and nobody could say who: the table keeps only the latest
// status, and moves were the one mutation that carried no actor. Asked
// directly, the overnight agent could neither confirm nor deny — an
// unattributed move is indistinguishable from a malfunction. Every status
// transition now leaves a signed line, and the bug carries its history.
func TestEveryBugMoveIsSigned(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	if _, err := s.BugAdd(context.Background(), id, BugRequest{
		Title: "the header forgets the name", Severity: "high",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugMove(context.Background(), id, "B-001", "triaged", "human"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugPromote(context.Background(), id, "B-001", "mcp:elena"); err != nil {
		t.Fatal(err)
	}
	// The overnight scenario itself: an agent knocks promoted work back.
	if _, err := s.BugMove(context.Background(), id, "B-001", "triaged", "mcp:elena"); err != nil {
		t.Fatal(err)
	}

	bugs, err := s.BugList(context.Background(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	hist := bugs[0].History
	if len(hist) != 3 {
		t.Fatalf("history = %d entries, want the 3 transitions: %+v", len(hist), hist)
	}
	if hist[0].Actor != "human" || hist[0].Via != "move" || hist[0].To != "triaged" {
		t.Errorf("first transition unsigned or wrong: %+v", hist[0])
	}
	if hist[1].Actor != "mcp:elena" || hist[1].Via != "promote" || hist[1].Note == "" {
		t.Errorf("promote must carry the agent and the task it created: %+v", hist[1])
	}
	if hist[2].Actor != "mcp:elena" || hist[2].From != "in_progress" {
		t.Errorf("the knock-back must name who did it: %+v", hist[2])
	}
}
