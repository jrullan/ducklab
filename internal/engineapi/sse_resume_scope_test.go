package engineapi

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
)

// openStream starts a stream request and returns its non-heartbeat events
// on a channel.
func openStream(t *testing.T, url, lastEventID string) <-chan bus.Event {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream answered %d", resp.StatusCode)
	}
	out := make(chan bus.Event, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var e bus.Event
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e) != nil || e.Type == "heartbeat" {
				continue
			}
			out <- e
		}
	}()
	return out
}

// awaitRun publishes an event for run until the stream delivers one of
// that run's events (the subscription may not exist yet on the first send).
func awaitRun(t *testing.T, h *sseHarness, events <-chan bus.Event, run string) bool {
	t.Helper()
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case e := <-events:
			if e.RunID == run {
				return true
			}
		case <-tick.C:
			h.bus.Publish(bus.Event{Type: "human_needed", RunID: run, ProjectID: "p", Seq: 1, TS: time.Now()})
		case <-deadline:
			return false
		}
	}
}

// Every SSE client sends Last-Event-ID when it reconnects. On the all-runs
// stream it used to become the run filter, so a coordinator that resumed
// after an engine restart heard only the run its last event came from —
// long finished — and missed every question after (B-537).
func TestAResumedAllRunsStreamStillCoversEveryRun(t *testing.T) {
	h := newSSEHarness(t, "r-finished")
	events := openStream(t, h.URL+"/v1/events", "r-finished:42")
	if !awaitRun(t, h, events, "r-asking") {
		t.Fatal("the resumed all-runs stream never delivered another run's event: Last-Event-ID narrowed it to r-finished")
	}
}

// The same for a project stream: resuming keeps the project scope, not the
// last event's run.
func TestAResumedProjectStreamStillCoversEveryRunOfTheProject(t *testing.T) {
	h := newSSEHarness(t, "r-finished")
	events := openStream(t, h.URL+"/v1/events?project=p", "r-finished:42")
	if !awaitRun(t, h, events, "r-asking") {
		t.Fatal("the resumed project stream never delivered another run's event")
	}
}

// A seq is per run: another run's Last-Event-ID must not move this run's
// resume point.
func TestAnotherRunsLastEventIDDoesNotMoveTheResumePoint(t *testing.T) {
	h := newSSEHarness(t, "r-sse-own")
	for i := 0; i < 5; i++ {
		h.writer.AppendEvent("turn_start", nil)
	}
	events := openStream(t, h.URL+"/v1/events?run="+h.runID+"&from_seq=1", "r-other:4")
	select {
	case e := <-events:
		if e.Seq != 2 {
			t.Fatalf("first replayed seq = %d, want 2 (from_seq=1; r-other's id must not apply)", e.Seq)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no event replayed")
	}
}

// An unknown parameter used to be ignored, so ?project_id= streamed every
// project without a word.
func TestTheEventStreamRefusesAnUnknownParameter(t *testing.T) {
	h := newSSEHarness(t, "r-sse-param")
	resp, err := http.Get(h.URL + "/v1/events?project_id=p")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("?project_id= answered %d, want 400", resp.StatusCode)
	}
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if msg, _ := json.Marshal(body); !strings.Contains(string(msg), "project_id") {
		t.Fatalf("refusal does not name the parameter: %s", msg)
	}
}
