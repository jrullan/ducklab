package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/tools"
)

// recoveredChat writes a chat waiting for the person and recovers it, so its
// consultant has never answered in this engine: the first screenshot check
// goes to the network instead of the capability cache.
func recoveredChat(t *testing.T, f *switchFixture, id, consultant string) *runlog.Run {
	t.Helper()
	entry, err := f.s.registry.Get(f.projectID)
	if err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{
		ID: id, ProjectID: f.projectID, Stage: "chat", Mode: "solo",
		Status: "paused", PendingKind: "chat", Note: "chat about ducklab configuration",
		Roster: map[string]string{"consultant": consultant}, StartedAt: "2026-10-09T10:00:00Z",
	}
	w, err := runlog.NewWriter(entry.Path, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := f.s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	return run
}

// Codex on #165 (P2): a send naming a duckling decided "this is a switch"
// from its snapshot, before the screenshot check. A switch to the SAME
// duckling landing during that check was then recorded twice — the second
// one target→target. The decision is made under the run lock now.
func TestChatSendDoesNotRecordASwitchAlreadyMadeDuringItsImageCheck(t *testing.T) {
	f := newSwitchFixture(t)
	run := recoveredChat(t, f, "r-same", "blind")
	f.mu.Lock()
	f.probeSeen, f.probeGate = make(chan struct{}), make(chan struct{})
	seen, gate := f.probeSeen, f.probeGate
	f.mu.Unlock()
	sent := make(chan error, 1)
	go func() {
		_, err := f.s.ChatSendWith(context.Background(), run.ID, "look at this", ChatSendOptions{Images: []string{switchTestImage}, Duckling: "seer"})
		sent <- err
	}()
	<-seen
	_, switchErr := f.s.ChatSwitch(context.Background(), run.ID, "seer", "")
	// Release the held probe and stop holding later ones (the turn's own
	// capability check must not wait on this test).
	f.mu.Lock()
	f.probeSeen, f.probeGate = nil, nil
	f.mu.Unlock()
	close(gate)
	if switchErr != nil {
		t.Fatal(switchErr)
	}
	if err := <-sent; err != nil {
		t.Fatalf("send to the duckling already switched to = %v", err)
	}
	waitForChatPause(t, f.s, run.ID)
	switches := eventsOfType(switchEvents(t, f.s, run.ID), "consultant_switched")
	if len(switches) != 1 || switches[0].Data["from"] != "blind" || switches[0].Data["to"] != "seer" {
		t.Fatalf("consultant_switched events = %+v; one effective switch must be recorded once", switches)
	}
	// The message and its screenshot went to the seer, which answered it.
	// (Recovered without a streaming probe, this turn's reply comes back
	// unstreamed; the record, not the fixture's stream log, is the witness.)
	events := switchEvents(t, f.s, run.ID)
	starts := eventsOfType(events, "turn_start")
	if len(starts) != 1 || starts[0].Data["duckling"] != "seer" {
		t.Errorf("the turn after the send = %+v, want one on seer", starts)
	}
	shown := false
	for _, e := range eventsOfType(events, "warning") {
		shown = shown || strings.Contains(e.Data["detail"].(string), "1 screenshot(s) shown")
	}
	if !shown {
		t.Error("the screenshot was not shown to the consultant")
	}
}

// Codex on #165 (P1): the replay told the next model "The person switched"
// for an operator's switch. Provenance survives into the prompt: the person
// only when the actor is the person, the operator by name otherwise.
func TestChatReplayNamesWhoSwitched(t *testing.T) {
	f := newSwitchFixture(t)
	run, err := f.s.ChatStart(context.Background(), f.projectID, ChatStartRequest{Duckling: "blind", AboutKind: "ducklab", AboutID: "configuration", Message: "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	waitForChatPause(t, f.s, run.ID)
	if _, err := f.s.ChatSwitch(context.Background(), run.ID, "seer", "mcp:elena"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ChatSwitch(context.Background(), run.ID, "blind", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ChatSend(context.Background(), run.ID, "Go on"); err != nil {
		t.Fatal(err)
	}
	waitForChatPause(t, f.s, run.ID)
	requests := f.streamed()
	prompt := requests[len(requests)-1]
	for _, want := range []string{
		"Here the MCP operator mcp:elena switched the consultant from blind to seer.",
		"Here the person switched the consultant from seer to you (blind).",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("replayed prompt is missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "the person switched the consultant from blind to seer") {
		t.Error("an operator's switch was replayed as the person's")
	}

	// The other renderers of the same event keep the same provenance: the
	// engine's transcript and the consultant's own run_read timeline.
	transcript, err := f.s.RunTranscript(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"consultant switched from blind to seer by the MCP operator mcp:elena", "consultant switched from seer to blind by the person"} {
		if !strings.Contains(transcript, want) {
			t.Errorf("transcript is missing %q:\n%s", want, transcript)
		}
	}
	entry, _ := f.s.registry.Get(f.projectID)
	summary, err := tools.ReadRunSummaryForPrompt(entry.Path, run.ID, 32768)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "consultant switched from blind to seer by the MCP operator mcp:elena") {
		t.Errorf("run_read timeline lost who switched:\n%s", summary)
	}
}

func TestActorPhrase(t *testing.T) {
	for actor, want := range map[string]string{
		"": "the person", "human": "the person", "mcp:elena": "the MCP operator mcp:elena", "autopilot": "autopilot",
	} {
		if got := runlog.ActorPhrase(actor); got != want {
			t.Errorf("ActorPhrase(%q) = %q, want %q", actor, got, want)
		}
	}
}
