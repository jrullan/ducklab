package service

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A chat is a run: the person picks a duckling, asks about a subject, the
// consultant answers with the dossier in hand and read-only tools, and the
// conversation pauses for the next message — memory in the event log, so
// nothing is lost between turns or restarts.
func TestAChatConversesAndPausesForTheNextMessage(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.BugAdd(context.Background(), p.ID, BugRequest{
		Title: "401 on every view", Body: "the frontend never sends X-User-ID",
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := s.ChatStart(context.Background(), p.ID, ChatStartRequest{
		Duckling: "pato-dos", AboutKind: "bug", AboutID: b.ID,
		Message: "this bug is not fixed; check why the 401 still happens",
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Stage != "chat" || run.Roster["consultant"] != "pato-dos" {
		t.Fatalf("run = %s/%v", run.Stage, run.Roster)
	}

	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	waitPaused := func() {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if rs.run.Status == "paused" && rs.run.PendingKind == "chat" {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("chat never paused for the next message: %s/%s (%s)", rs.run.Status, rs.run.PendingKind, rs.run.Failure)
	}
	waitPaused()
	if got := runNext(rs.run); len(got) == 0 || got[0] != "reply" {
		t.Errorf("next = %v, want reply first", got)
	}

	// The next message re-enters with the conversation intact.
	if _, err := s.ChatSend(context.Background(), run.ID, "and what should I do about it?"); err != nil {
		t.Fatal(err)
	}
	waitPaused()

	// The dossier reached the prompt: the consultant's context named the bug.
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	msgs := 0
	for _, e := range detail.Events {
		if e.Type == "message" {
			msgs++
		}
	}
	if msgs < 4 {
		t.Errorf("the record holds %d messages, want the full alternation (2 human + 2 consultant)", msgs)
	}
	if !strings.Contains(detail.Run.Note, b.ID) {
		t.Errorf("the run does not say what it is about: %q", detail.Run.Note)
	}

	// A chat cannot be fed while it is thinking or after it ends.
	if _, err := s.ChatSend(context.Background(), "r-nope", "hi"); err == nil {
		t.Error("sent to a chat that does not exist")
	}
}
