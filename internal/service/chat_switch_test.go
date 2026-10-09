package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
)

const switchTestImage = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL/bwAAAABJRU5ErkJggg=="

// switchFixture is an engine with a text-only duckling ("blind") and a seeing
// one ("seer") on one recording endpoint. Each consultant reply names the
// model that wrote it, so the next prompt shows whose words it replayed.
type switchFixture struct {
	s         *Service
	projectID string
	mu        sync.Mutex
	requests  []string // streamed chat bodies, in order
	gate      chan struct{}
	// probeSeen/probeGate hold the image probe open, so a test can act while
	// a send is still checking its screenshot.
	probeSeen chan struct{}
	probeGate chan struct{}
}

func newSwitchFixture(t *testing.T) *switchFixture {
	t.Helper()
	f := &switchFixture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"stream":true`) {
			// The vision probe (a plain, non-streamed chat).
			f.mu.Lock()
			probeSeen, probeGate := f.probeSeen, f.probeGate
			f.mu.Unlock()
			if probeSeen != nil && strings.Contains(string(body), "image_url") {
				probeSeen <- struct{}{}
				<-probeGate
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
			return
		}
		f.mu.Lock()
		f.requests = append(f.requests, string(body))
		gate := f.gate
		f.mu.Unlock()
		if gate != nil {
			<-gate
		}
		var req struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "text/event-stream")
		reply := fmt.Sprintf("reply written by %s", req.Model)
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n", reply)
	}))
	t.Cleanup(server.Close)

	isolate(t)
	seeing := true
	cfg := config.DefaultGlobal()
	cfg.Providers = map[config.ProviderID]config.Provider{"test": {Kind: config.ProviderKindOpenAI, BaseURL: server.URL}}
	cfg.Ducklings = map[config.DucklingID]config.Duckling{
		"blind": {Provider: "test", Model: "m-blind"},
		"seer":  {Provider: "test", Model: "m-seer", Caps: config.Caps{Vision: &seeing}},
	}
	s, err := New(cfg, Options{Bus: bus.New(64)})
	if err != nil {
		t.Fatal(err)
	}
	f.s = s
	f.projectID, _ = projectWithConfig(t, s, "chat-switch")
	return f
}

func (f *switchFixture) streamed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func switchEvents(t *testing.T, s *Service, runID string) []*runlog.Event {
	t.Helper()
	detail, err := s.RunGet(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	return detail.Events
}

func eventsOfType(events []*runlog.Event, kind string) []*runlog.Event {
	var out []*runlog.Event
	for _, e := range events {
		if e.Type == kind {
			out = append(out, e)
		}
	}
	return out
}

// B-513: the person needed a screenshot examined and the chat's duckling was
// blind. Switching keeps the conversation: the next reply comes from the
// new duckling, with the dossier and every earlier exchange replayed, the
// earlier reply still attributed to the duckling that wrote it, and the
// screenshot that was refused before the switch accepted after it.
func TestChatSwitchCarriesTheConversationToASeeingDuckling(t *testing.T) {
	f := newSwitchFixture(t)
	run, err := f.s.ChatStart(context.Background(), f.projectID, ChatStartRequest{Duckling: "blind", AboutKind: "ducklab", AboutID: "configuration", Message: "Why does the login return 401?"})
	if err != nil {
		t.Fatal(err)
	}
	waitForChatPause(t, f.s, run.ID)

	if _, err := f.s.ChatSend(context.Background(), run.ID, "Look at this screenshot", []string{switchTestImage}); err == nil || !strings.Contains(err.Error(), "seeing duckling") {
		t.Fatalf("blind consultant accepted a screenshot: %v", err)
	}

	switched, err := f.s.ChatSwitch(context.Background(), run.ID, "seer", "")
	if err != nil {
		t.Fatal(err)
	}
	if switched.Roster["consultant"] != "seer" {
		t.Fatalf("roster after switch = %v", switched.Roster)
	}
	recorded := eventsOfType(switchEvents(t, f.s, run.ID), "consultant_switched")
	if len(recorded) != 1 || recorded[0].Data["from"] != "blind" || recorded[0].Data["to"] != "seer" || recorded[0].Data["actor"] != "human" {
		t.Fatalf("consultant_switched events = %+v", recorded)
	}

	if _, err := f.s.ChatSend(context.Background(), run.ID, "Look at this screenshot", []string{switchTestImage}); err != nil {
		t.Fatalf("seeing consultant refused the screenshot: %v", err)
	}
	waitForChatPause(t, f.s, run.ID)

	requests := f.streamed()
	if len(requests) != 2 {
		t.Fatalf("consultant requests = %d, want one per answered message", len(requests))
	}
	second := requests[1]
	for _, want := range []string{
		`"model":"m-seer"`, "image_url",
		"Why does the login return 401?",                       // the earlier question
		"EARLIER CONSULTANT (blind): reply written by m-blind", // the earlier answer, still blind's
		"switched the consultant here from blind to you (seer)",
		"Look at this screenshot",
		"Ducklab is a full-cycle software development harness", // the dossier is rebuilt
	} {
		if !strings.Contains(second, want) {
			t.Errorf("the switched turn's request is missing %q:\n%s", want, second)
		}
	}
	if strings.Contains(second, "YOU: reply written by m-blind") {
		t.Error("the new consultant was told it wrote the old consultant's reply")
	}

	events := switchEvents(t, f.s, run.ID)
	var replies []string
	for _, e := range events {
		if e.Type == "message" && e.Data["role"] == "consultant" {
			replies = append(replies, fmt.Sprintf("%v:%v", e.Data["duckling"], e.Data["content"]))
		}
	}
	if strings.Join(replies, "|") != "blind:reply written by m-blind|seer:reply written by m-seer" {
		t.Errorf("per-turn attribution = %v", replies)
	}
	starts := eventsOfType(events, "turn_start")
	if len(starts) != 2 || starts[1].Data["duckling"] != "seer" {
		t.Errorf("turn_start attribution = %+v", starts)
	}
	detail, err := f.s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.Roster["consultant"] != "seer" {
		t.Errorf("the record's consultant seat = %v", detail.Run.Roster)
	}
	persisted, err := runlog.ReadState(f.s.runs[run.ID].runDir)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Roster["consultant"] != "seer" {
		t.Errorf("state.json consultant seat = %v; an engine restart would forget the switch", persisted.Roster)
	}
}

// A blind duckling switched in must never receive an image, including the
// ones a seeing duckling was shown earlier. The transcript tells it the
// screenshots existed instead.
func TestChatSwitchToABlindDucklingSendsNoEarlierImages(t *testing.T) {
	f := newSwitchFixture(t)
	run, err := f.s.ChatStart(context.Background(), f.projectID, ChatStartRequest{Duckling: "seer", AboutKind: "ducklab", AboutID: "configuration", Message: "What does this show?", Images: []string{switchTestImage}})
	if err != nil {
		t.Fatal(err)
	}
	waitForChatPause(t, f.s, run.ID)
	if _, err := f.s.ChatSwitch(context.Background(), run.ID, "blind", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ChatSend(context.Background(), run.ID, "And what should I change?"); err != nil {
		t.Fatal(err)
	}
	waitForChatPause(t, f.s, run.ID)
	requests := f.streamed()
	if len(requests) != 2 {
		t.Fatalf("consultant requests = %d", len(requests))
	}
	if !strings.Contains(requests[0], "image_url") {
		t.Error("the seeing consultant did not receive its screenshot")
	}
	blind := requests[1]
	if strings.Contains(blind, "image_url") || strings.Contains(blind, switchTestImage) {
		t.Errorf("a blind duckling was sent an image:\n%s", blind)
	}
	for _, want := range []string{`"model":"m-blind"`, "1 screenshot(s) were attached to this message", "not re-sent", "EARLIER CONSULTANT (seer)"} {
		if !strings.Contains(blind, want) {
			t.Errorf("the blind turn's request is missing %q:\n%s", want, blind)
		}
	}
}

// A switch named in the same message: the switch is recorded before the
// message, and that message's screenshot goes to — and is checked against —
// the new duckling, not the blind one being left.
func TestChatSendWithADucklingSwitchesBeforeTheMessage(t *testing.T) {
	f := newSwitchFixture(t)
	run, err := f.s.ChatStart(context.Background(), f.projectID, ChatStartRequest{Duckling: "blind", AboutKind: "ducklab", AboutID: "configuration", Message: "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	waitForChatPause(t, f.s, run.ID)
	if _, err := f.s.ChatSendWith(context.Background(), run.ID, "Here is the screen", ChatSendOptions{Images: []string{switchTestImage}, Duckling: "seer"}); err != nil {
		t.Fatal(err)
	}
	waitForChatPause(t, f.s, run.ID)
	events := switchEvents(t, f.s, run.ID)
	switchAt, messageAt := -1, -1
	for i, e := range events {
		if e.Type == "consultant_switched" {
			switchAt = i
		}
		if e.Type == "message" && e.Data["content"] == "Here is the screen" {
			messageAt = i
		}
	}
	if switchAt < 0 || messageAt < 0 || switchAt > messageAt {
		t.Fatalf("switch at %d, message at %d: the switch must be recorded before the message it carried", switchAt, messageAt)
	}
	requests := f.streamed()
	if last := requests[len(requests)-1]; !strings.Contains(last, `"model":"m-seer"`) || !strings.Contains(last, "image_url") {
		t.Errorf("the message's screenshot did not reach the new duckling:\n%s", last)
	}

	// A refused image changes nothing: no switch, no message.
	before := len(switchEvents(t, f.s, run.ID))
	if _, err := f.s.ChatSendWith(context.Background(), run.ID, "and this", ChatSendOptions{Images: []string{switchTestImage}, Duckling: "blind"}); err == nil {
		t.Fatal("a screenshot was accepted for a switch to a blind duckling")
	}
	if after := switchEvents(t, f.s, run.ID); len(after) != before {
		t.Errorf("a refused send left %d events on the record", len(after)-before)
	}
	if detail, _ := f.s.RunGet(context.Background(), run.ID); detail.Run.Roster["consultant"] != "seer" {
		t.Errorf("a refused send moved the seat to %v", detail.Run.Roster)
	}
}

// While a reply is being written the switch is refused, with words the
// composer can show: the turn in flight belongs to the old duckling. An
// unknown duckling, or the one already seated, changes nothing.
func TestChatSwitchRefusals(t *testing.T) {
	f := newSwitchFixture(t)
	f.gate = make(chan struct{})
	run, err := f.s.ChatStart(context.Background(), f.projectID, ChatStartRequest{Duckling: "blind", AboutKind: "ducklab", AboutID: "configuration", Message: "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.s.ChatSwitch(context.Background(), run.ID, "seer", "")
	f.mu.Lock()
	close(f.gate)
	f.gate = nil
	f.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "still answering") {
		t.Fatalf("switch during a reply = %v, want a refusal saying the consultant is still answering", err)
	}
	waitForChatPause(t, f.s, run.ID)
	if len(eventsOfType(switchEvents(t, f.s, run.ID), "consultant_switched")) != 0 {
		t.Fatal("a refused switch was recorded")
	}

	if _, err := f.s.ChatSwitch(context.Background(), run.ID, "nobody", ""); err == nil || !strings.HasPrefix(err.Error(), "invalid_request:") || !strings.Contains(err.Error(), "nobody") {
		t.Errorf("unknown duckling = %v, want invalid_request naming it", err)
	}
	if _, err := f.s.ChatSwitch(context.Background(), run.ID, "", ""); err == nil {
		t.Error("an empty duckling was accepted")
	}
	if _, err := f.s.ChatSwitch(context.Background(), run.ID, "blind", ""); err != nil {
		t.Errorf("switching to the seated duckling = %v, want a no-op", err)
	}
	if len(eventsOfType(switchEvents(t, f.s, run.ID), "consultant_switched")) != 0 {
		t.Error("a no-op switch was recorded")
	}
	if _, err := f.s.ChatSwitch(context.Background(), run.ID, "seer", "mcp:elena"); err != nil {
		t.Fatal(err)
	}
	if got := eventsOfType(switchEvents(t, f.s, run.ID), "consultant_switched"); len(got) != 1 || got[0].Data["actor"] != "mcp:elena" {
		t.Errorf("operator switch = %+v, want actor mcp:elena", got)
	}

	if _, err := f.s.ChatEnd(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ChatSwitch(context.Background(), run.ID, "blind", ""); err == nil || !strings.Contains(err.Error(), "not waiting") {
		t.Errorf("switch on an ended chat = %v", err)
	}
}

// A send checks its screenshot against the seated duckling over the network
// before it takes the run lock. What happens in that window must not slip
// through: a switch made meanwhile refuses the send (its images were checked
// for a duckling no longer seated), and an ended chat stays ended.
func TestChatSendRechecksTheChatAfterItsImageCheck(t *testing.T) {
	for _, tc := range []struct {
		name  string
		act   func(*Service, string) error
		wants string
	}{
		{"switched meanwhile", func(s *Service, id string) error {
			_, err := s.ChatSwitch(context.Background(), id, "blind", "")
			return err
		}, "consultant changed to blind"},
		{"ended meanwhile", func(s *Service, id string) error {
			_, err := s.ChatEnd(context.Background(), id)
			return err
		}, "not waiting"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSwitchFixture(t)
			// A chat recovered from disk, waiting for the person: its seer has
			// never answered here, so the send's screenshot check really goes
			// to the network (a turn would have probed and cached it).
			dir := ""
			if entry, err := f.s.registry.Get(f.projectID); err == nil {
				dir = entry.Path
			}
			run := &runlog.Run{
				ID: "r-window", ProjectID: f.projectID, Stage: "chat", Mode: "solo",
				Status: "paused", PendingKind: "chat", Note: "chat about ducklab configuration",
				Roster: map[string]string{"consultant": "seer"}, StartedAt: "2026-10-09T10:00:00Z",
			}
			w, err := runlog.NewWriter(dir, run)
			if err != nil {
				t.Fatal(err)
			}
			w.Close()
			if err := f.s.RecoverRuns(context.Background()); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			f.probeSeen, f.probeGate = make(chan struct{}), make(chan struct{})
			seen, gate := f.probeSeen, f.probeGate
			f.mu.Unlock()
			sent := make(chan error, 1)
			go func() {
				_, err := f.s.ChatSend(context.Background(), run.ID, "look", []string{switchTestImage})
				sent <- err
			}()
			<-seen
			actErr := tc.act(f.s, run.ID)
			close(gate)
			if actErr != nil {
				t.Fatal(actErr)
			}
			if err := <-sent; err == nil || !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("send after the chat changed under it = %v, want a refusal containing %q", err, tc.wants)
			}
			for _, e := range switchEvents(t, f.s, run.ID) {
				if e.Type == "message" && e.Data["content"] == "look" {
					t.Fatal("the refused message reached the record")
				}
			}
		})
	}
}
