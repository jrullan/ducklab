package service

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/bug"
)

// fixedBug files a bug and carries it to fixed the way a person would.
func fixedBug(t *testing.T, s *Service, project string) string {
	t.Helper()
	ctx := context.Background()
	if _, err := s.BugAdd(ctx, project, BugRequest{Title: "2nd + 0 does not reset", Severity: "low"}); err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{"triaged", "in_progress", "fixed"} {
		if _, err := s.BugMove(ctx, project, "B-001", to, "human"); err != nil {
			t.Fatalf("move to %s: %v", to, err)
		}
	}
	return "B-001"
}

// B-542. An OpenCode coordinator told to "attend B-012" moved it
// fixed→verified→closed itself, verified B-006 that was waiting for the
// person, and batch-closed nine more. bug_move's description said only the
// person verifies; nothing enforced it. An agent's credential now carries a
// bug no further than fixed, and the refusal says who moves it from there.
func TestAnAgentCannotVerifyOrCloseABug(t *testing.T) {
	ctx := context.Background()
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	b := fixedBug(t, s, id)

	for _, actor := range []string{"mcp:opencode", "agent"} {
		for _, to := range []string{"verified", "closed"} {
			_, err := s.BugMove(ctx, id, b, to, actor)
			if err == nil {
				t.Fatalf("%s moved %s to %s; only the person may", actor, b, to)
			}
			for _, want := range []string{"person's judgement", actor, "ducklab bug status " + b + " " + to} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not name %q", err, want)
				}
			}
		}
	}
	bugs, _ := s.BugList(ctx, id, false)
	if bugs[0].Status != bug.Fixed {
		t.Fatalf("a refused move changed the bug: %s", bugs[0].Status)
	}

	// The person's own moves are untouched.
	if _, err := s.BugMove(ctx, id, b, "verified", "human"); err != nil {
		t.Fatalf("the person verifies: %v", err)
	}
	if _, err := s.BugMove(ctx, id, b, "closed", ""); err != nil {
		t.Fatalf("the person closes: %v", err)
	}
}

// Closing is the same verdict said louder, and from wontfix it ends a report
// no person looked at: an agent may mark it, not close it.
func TestAnAgentCannotCloseAWontfix(t *testing.T) {
	ctx := context.Background()
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(ctx, id, BugRequest{Title: "works as designed", Severity: "low"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugMove(ctx, id, "B-001", "wontfix", "mcp:opencode"); err != nil {
		t.Fatalf("an agent may still classify: %v", err)
	}
	if _, err := s.BugMove(ctx, id, "B-001", "closed", "mcp:opencode"); err == nil {
		t.Fatal("an agent closed a wontfix no person looked at")
	}
}

// agentVerdict writes the record B-542's incident left: a bug an agent moved
// fixed→verified (and, when close, →closed) before the engine refused it.
func agentVerdict(t *testing.T, s *Service, project, id, verifier string, close bool) {
	t.Helper()
	entry, err := s.registry.Get(project)
	if err != nil {
		t.Fatal(err)
	}
	db, err := s.openProjectDB(project)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rec, err := db.GetBug(id)
	if err != nil {
		t.Fatal(err)
	}
	rec.Status = string(bug.Verified)
	appendBugAudit(entry.Path, bug.AuditEntry{Bug: id, From: "fixed", To: "verified", Actor: verifier, Via: "move"})
	if close {
		rec.Status = string(bug.Closed)
		appendBugAudit(entry.Path, bug.AuditEntry{Bug: id, From: "verified", To: "closed", Actor: verifier, Via: "move"})
	}
	if err := db.UpdateBug(rec); err != nil {
		t.Fatal(err)
	}
}

// Before the refusal, an agent's verdicts landed, and nothing could undo
// them: verified leads only to closed, and closed is final. A bug whose
// verification was an agent's goes back to fixed — from verified, or from the
// close that followed it — so the person makes the judgement.
func TestAnAgentsVerificationCanBeUndone(t *testing.T) {
	ctx := context.Background()
	for _, close := range []bool{false, true} {
		s := serviceWithDucklings(t, "pato-uno")
		id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
		b := fixedBug(t, s, id)
		agentVerdict(t, s, id, b, "mcp:opencode", close)

		bugs, _ := s.BugList(ctx, id, false)
		if !slices.Contains(bugs[0].Next, bug.Fixed) {
			t.Fatalf("closed=%v: next %v does not offer the undo", close, bugs[0].Next)
		}
		out, err := s.BugMove(ctx, id, b, "fixed", "human")
		if err != nil {
			t.Fatalf("closed=%v: undo refused: %v", close, err)
		}
		if out.Status != bug.Fixed {
			t.Fatalf("closed=%v: undo landed on %s", close, out.Status)
		}
		last := out.History[len(out.History)-1]
		if last.Via != "unverify" || !strings.Contains(last.Note, "mcp:opencode verified it") {
			t.Errorf("closed=%v: the undo must say whose verdict it reverses: %+v", close, last)
		}
		// And the person can now make the judgement themselves.
		if _, err := s.BugMove(ctx, id, b, "verified", "human"); err != nil {
			t.Fatalf("closed=%v: the person verifies after the undo: %v", close, err)
		}
		bugs, _ = s.BugList(ctx, id, false)
		if slices.Contains(bugs[0].Next, bug.Fixed) {
			t.Errorf("closed=%v: the person's own verification offers an undo: %v", close, bugs[0].Next)
		}
	}
}

// The undo reverses an agent's verdict only. The person's verification, and a
// close that came from wontfix rather than from any verification, stay final.
func TestThePersonsVerificationIsNotUndoable(t *testing.T) {
	ctx := context.Background()
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	b := fixedBug(t, s, id)
	agentVerdict(t, s, id, b, "human", true)
	if _, err := s.BugMove(ctx, id, b, "fixed", "human"); err == nil {
		t.Fatal("the person's verification was undone")
	}

	if _, err := s.BugAdd(ctx, id, BugRequest{Title: "not a bug", Severity: "low"}); err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{"wontfix", "closed"} {
		if _, err := s.BugMove(ctx, id, "B-002", to, "human"); err != nil {
			t.Fatal(err)
		}
	}
	bugs, _ := s.BugList(ctx, id, false)
	for _, x := range bugs {
		if x.ID == "B-002" && slices.Contains(x.Next, bug.Fixed) {
			t.Errorf("a wontfix close offers fixed: %v", x.Next)
		}
	}
}
