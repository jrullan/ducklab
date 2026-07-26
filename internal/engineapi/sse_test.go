package engineapi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/runlog"
)

// sseHarness serves handleEvents against a real run directory without needing
// a full Service: only RunDir and RunGet are exercised by the SSE path.
type sseHarness struct {
	*httptest.Server
	bus    *bus.Bus
	writer *runlog.Writer
	srv    *Server
	runID  string
}

func newSSEHarness(t *testing.T, runID string) *sseHarness {
	t.Helper()
	root := t.TempDir()
	run := &runlog.Run{ID: runID, ProjectID: "p", Status: "running", Mode: "solo"}
	w, err := runlog.NewWriter(root, run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })

	b := bus.New(256)
	// Same wiring the service uses: every persisted event reaches the bus
	// carrying its seq.
	w.OnEvent = func(e *runlog.Event) {
		b.Publish(bus.Event{
			Type: e.Type, RunID: e.RunID, ProjectID: "p",
			Seq: e.Seq, TS: time.Now(), Data: e.Data,
		})
	}

	srv := &Server{bus: b, token: "t", version: "0.1.0", mux: http.NewServeMux()}
	srv.runDirFn = func(string) string { return w.RunDir() }
	srv.projectIDFn = func(string) string { return "p" }
	srv.mux.HandleFunc("GET /v1/events", srv.handleEvents)

	ts := httptest.NewServer(srv.mux)
	t.Cleanup(ts.Close)
	return &sseHarness{Server: ts, bus: b, writer: w, srv: srv, runID: runID}
}

// collect reads SSE events until n are seen or the deadline passes.
func collect(t *testing.T, url string, n int, d time.Duration) []bus.Event {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var got []bus.Event
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var e bus.Event
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e) != nil {
				continue
			}
			if e.Type == "heartbeat" {
				continue
			}
			got = append(got, e)
			if len(got) >= n {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(d):
	}
	return got
}

// AC-14: a subscriber attaching mid-run receives the full backlog and then
// live events, with no gap and no duplicate.
func TestSSEBacklogThenLiveNoGapNoDuplicate(t *testing.T) {
	h := newSSEHarness(t, "r-sse-1")

	// 10 events before anyone subscribes: these must arrive as backlog.
	for i := 0; i < 10; i++ {
		h.writer.AppendEvent("turn_start", map[string]interface{}{"n": i})
	}

	url := h.URL + "/v1/events?run=" + h.runID + "&from_seq=0"
	var got []bus.Event
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		got = collect(t, url, 20, 3*time.Second)
	}()

	// Give the subscriber time to attach and replay, then emit 10 more live.
	time.Sleep(150 * time.Millisecond)
	for i := 10; i < 20; i++ {
		h.writer.AppendEvent("tool_call", map[string]interface{}{"n": i})
	}
	wg.Wait()

	if len(got) != 20 {
		t.Fatalf("got %d events, want 20 (gap or duplicate)", len(got))
	}
	for i, e := range got {
		if e.Seq != i+1 {
			t.Fatalf("event %d has seq %d, want %d — stream is not gapless", i, e.Seq, i+1)
		}
	}

	// The stream must match what is on disk, exactly.
	onDisk, err := runlog.ReadEvents(h.writer.RunDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(onDisk) != len(got) {
		t.Fatalf("stream has %d events, disk has %d", len(got), len(onDisk))
	}
	for i := range onDisk {
		if onDisk[i].Seq != got[i].Seq || onDisk[i].Type != got[i].Type {
			t.Errorf("event %d: disk=%s/%d stream=%s/%d",
				i, onDisk[i].Type, onDisk[i].Seq, got[i].Type, got[i].Seq)
		}
	}
}

// The replay/subscribe window is the actual bug this guards. An event emitted
// while the backlog is being replayed must still be delivered exactly once.
//
// This test fails if handleEvents replays before subscribing: the injected
// event is published when no subscriber is attached yet, and vanishes.
func TestSSENoLossDuringReplayOverlap(t *testing.T) {
	h := newSSEHarness(t, "r-sse-2")
	for i := 0; i < 10; i++ {
		h.writer.AppendEvent("turn_start", map[string]interface{}{"n": i})
	}

	// Fire exactly once, from inside the replay.
	var once sync.Once
	h.srv.hookDuringReplay = func() {
		once.Do(func() {
			h.writer.AppendEvent("injected_during_replay", nil)
		})
	}

	url := h.URL + "/v1/events?run=" + h.runID + "&from_seq=0"
	got := collect(t, url, 11, 3*time.Second)

	seen := map[int]int{}
	injected := false
	for _, e := range got {
		seen[e.Seq]++
		if e.Type == "injected_during_replay" {
			injected = true
		}
	}
	if !injected {
		t.Fatal("event emitted during backlog replay was lost — handleEvents must subscribe BEFORE replaying")
	}
	for seq, n := range seen {
		if n != 1 {
			t.Fatalf("seq %d delivered %d times — duplicate across replay and live", seq, n)
		}
	}
	if len(got) != 11 {
		t.Fatalf("got %d events, want 11", len(got))
	}
}

// from_seq lets a reconnecting client skip what it already has.
func TestSSEFromSeqSkipsBacklog(t *testing.T) {
	h := newSSEHarness(t, "r-sse-3")
	for i := 0; i < 10; i++ {
		h.writer.AppendEvent("turn_start", nil)
	}
	url := fmt.Sprintf("%s/v1/events?run=%s&from_seq=7", h.URL, h.runID)
	got := collect(t, url, 3, 2*time.Second)
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	if got[0].Seq != 8 {
		t.Errorf("first seq = %d, want 8", got[0].Seq)
	}
}

// Last-Event-ID takes precedence over from_seq so a reconnect resumes exactly.
func TestSSELastEventIDResumes(t *testing.T) {
	h := newSSEHarness(t, "r-sse-4")
	for i := 0; i < 10; i++ {
		h.writer.AppendEvent("turn_start", nil)
	}
	req, _ := http.NewRequest("GET", h.URL+"/v1/events?run="+h.runID, nil)
	req.Header.Set("Last-Event-ID", h.runID+":6")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var e bus.Event
		json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e)
		if e.Type == "heartbeat" {
			continue
		}
		if e.Seq != 7 {
			t.Errorf("first resumed seq = %d, want 7", e.Seq)
		}
		return
	}
	t.Fatal("no event received")
}

func TestCORSRejectsUnknownOrigin(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/events", nil)
	r.Header.Set("Origin", "https://evil.example")
	setCORS(w, r)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("unknown origin was allowed: %q", got)
	}

	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/v1/events", nil)
	r2.Header.Set("Origin", "wails://wails")
	setCORS(w2, r2)
	if got := w2.Header().Get("Access-Control-Allow-Origin"); got != "wails://wails" {
		t.Errorf("allowed origin = %q, want wails://wails", got)
	}
}

// AC-26: token_delta reaches subscribers but is never persisted — it is
// display state, and writing it would bloat events.jsonl with data no resume
// needs (01 §5.3).
func TestTokenDeltaIsStreamedButNotPersisted(t *testing.T) {
	h := newSSEHarness(t, "r-sse-delta")
	h.writer.AppendEvent("run_start", nil)

	url := h.URL + "/v1/events?run=" + h.runID + "&from_seq=0"
	var got []bus.Event
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		got = collect(t, url, 4, 3*time.Second)
	}()

	time.Sleep(150 * time.Millisecond)
	for i := 0; i < 3; i++ {
		h.bus.Publish(bus.Event{
			Type: "token_delta", RunID: h.runID, ProjectID: "p",
			Data: map[string]interface{}{"text": "chunk"},
		})
	}
	wg.Wait()

	deltas := 0
	for _, e := range got {
		if e.Type == "token_delta" {
			deltas++
			if e.Seq != 0 {
				t.Errorf("token_delta carried seq %d; it must not be sequenced", e.Seq)
			}
		}
	}
	if deltas != 3 {
		t.Errorf("received %d token_delta events, want 3", deltas)
	}

	// None of them may be on disk.
	onDisk, err := runlog.ReadEvents(h.writer.RunDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range onDisk {
		if e.Type == "token_delta" {
			t.Error("token_delta was persisted to events.jsonl")
		}
	}
}

// I11: a subscriber that stops reading is dropped with an overflow marker and
// the run continues unaffected. One slow client must never stall a run.
func TestSlowSubscriberIsDroppedAndPublishingContinues(t *testing.T) {
	h := newSSEHarness(t, "r-sse-slow")

	// Subscribe directly with a small buffer and never read from it.
	sub, unsub := h.bus.Subscribe("slow-client", nil)
	defer unsub()

	// A healthy subscriber attached alongside must keep receiving.
	healthy, unsubHealthy := h.bus.Subscribe("healthy", nil)
	defer unsubHealthy()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 2000; i++ {
			h.bus.Publish(bus.Event{Type: "token_delta", RunID: h.runID,
				Data: map[string]interface{}{"n": i}})
		}
	}()

	// Drain the healthy subscriber so it never overflows.
	received := 0
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for range healthy.Ch {
			received++
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("publishing blocked on a subscriber that stopped reading — a slow client stalled the run")
	}

	// The slow one must have been told to resync.
	sawOverflow := false
	for {
		select {
		case e, ok := <-sub.Ch:
			if !ok {
				goto checked
			}
			if e.Type == "overflow" {
				sawOverflow = true
			}
			continue
		default:
		}
		break
	}
checked:
	if !sawOverflow {
		t.Error("the slow subscriber was never sent an overflow marker; it would believe it was up to date")
	}

	unsubHealthy()
	<-drainDone
	if received == 0 {
		t.Error("the healthy subscriber received nothing while another was overflowing")
	}
}
