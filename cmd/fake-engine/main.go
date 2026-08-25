// Command fake-engine serves the real engine HTTP contract with scripted data.
//
// It exists so the frontend can be tested against streaming, reconnection,
// overflow and the human gate without a model, a GPU or a repo (06 Appendix B).
// It speaks the SAME contract as ducklab-engine — if it drifts, the tests stop
// meaning anything, so it reuses internal/bus rather than reimplementing
// fan-out.
//
// Never reachable in a release build: it lives in cmd/ and is excluded from the
// release matrix.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
)

func main() {
	port := flag.Int("port", 0, "port to listen on (0 = ephemeral)")
	token := flag.String("token", "fake-token", "bearer token clients must present")
	scenario := flag.String("scenario", "pair", "scripted scenario: pair | tournament | question | flood | idle")
	allowOrigin := flag.String("allow-origin", "*", "CORS origin the test harness is served from")
	delay := flag.Int("delay-ms", 40, "delay between scripted events")
	flag.Parse()

	srv := newFakeEngine(*token, *scenario, time.Duration(*delay)*time.Millisecond, *allowOrigin)

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	ln, err := listen(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: listen: %v\n", err)
		os.Exit(1)
	}
	// Print the real port so a test harness can find an ephemeral one.
	fmt.Printf("fake-engine listening on %s token=%s scenario=%s\n", ln.Addr(), *token, *scenario)

	if err := http.Serve(ln, srv); err != nil {
		fmt.Fprintf(os.Stderr, "error: serve: %v\n", err)
		os.Exit(1)
	}
}

const fakeRunID = "r-20260726-120000-fake"

type fakeEngine struct {
	mux      *http.ServeMux
	bus      *bus.Bus
	token    string
	scenario string
	delay    time.Duration
	// allowOrigin exists only because the harness serves the frontend from a
	// vite preview on a different port. The REAL engine keeps its strict
	// wails-only allowlist (07 §1); loosening that here would make the double
	// weaker than the thing it stands in for, so the flag lives on the fake
	// alone.
	allowOrigin string

	// firstClient closes when a client first attaches to the event stream.
	// Playback waits on it so a test never races the scenario: deltas are
	// live-only by design, and a client that attaches late would silently
	// see none of them.
	firstClient chan struct{}
	clientOnce  sync.Once

	mu       sync.Mutex
	events   []map[string]interface{}
	seq      int
	run      map[string]interface{}
	answered bool
	accepted bool
	aborted  bool
}

func newFakeEngine(token, scenario string, delay time.Duration, allowOrigin string) *fakeEngine {
	f := &fakeEngine{
		mux: http.NewServeMux(), bus: bus.New(256),
		token: token, scenario: scenario, delay: delay,
		allowOrigin: allowOrigin,
		firstClient: make(chan struct{}),
		run: map[string]interface{}{
			"id": fakeRunID, "project_id": "demo", "stage": "build",
			"mode": scenarioMode(scenario), "task_id": "T-001",
			"status": "running", "verdict": "",
			"roster":     map[string]string{"implementer": "pato-uno", "reviewer": "pato-dos"},
			"started_at": time.Now().UTC().Format(time.RFC3339),
		},
	}
	f.routes()
	go f.play()
	return f
}

func scenarioMode(s string) string {
	switch s {
	case "tournament":
		return "tournament"
	case "idle":
		return "solo"
	default:
		return "pair"
	}
}

// ServeHTTP answers CORS preflight before routing, for the same reason the
// real engine does: Go's method-specific mux patterns do not match OPTIONS.
func (f *fakeEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		f.cors(w, r)
		w.Header().Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	f.cors(w, r)
	f.mux.ServeHTTP(w, r)
}

func (f *fakeEngine) routes() {
	f.mux.HandleFunc("GET /v1/health", f.health)
	f.mux.HandleFunc("GET /v1/engine", f.auth(f.engine))
	f.mux.HandleFunc("GET /v1/projects", f.auth(f.projects))
	f.mux.HandleFunc("GET /v1/projects/{id}", f.auth(f.project))
	f.mux.HandleFunc("GET /v1/projects/{id}/status", f.auth(f.projectStatus))
	f.mux.HandleFunc("GET /v1/ducklings", f.auth(f.ducklings))
	f.mux.HandleFunc("GET /v1/providers", f.auth(f.providers))
	f.mux.HandleFunc("GET /v1/defaults/budget", f.auth(f.budgetDefaults))
	f.mux.HandleFunc("GET /v1/projects/{id}/roster", f.auth(f.roster))
	f.mux.HandleFunc("GET /v1/projects/{id}/skills", f.auth(f.skills))
	f.mux.HandleFunc("GET /v1/projects/{id}/tasks", f.auth(f.tasks))
	f.mux.HandleFunc("GET /v1/projects/{id}/bugs", f.auth(f.bugs))
	f.mux.HandleFunc("GET /v1/runs", f.auth(f.runs))
	f.mux.HandleFunc("GET /v1/runs/{id}", f.auth(f.runGet))
	f.mux.HandleFunc("GET /v1/runs/{id}/diff", f.auth(f.runDiff))
	f.mux.HandleFunc("GET /v1/runs/{id}/candidates", f.auth(f.runCandidates))
	f.mux.HandleFunc("GET /v1/runs/{id}/verify", f.auth(f.runVerify))
	f.mux.HandleFunc("POST /v1/runs/{id}/accept", f.auth(f.runAccept))
	f.mux.HandleFunc("POST /v1/runs/{id}/reject", f.auth(f.runReject))
	f.mux.HandleFunc("POST /v1/runs/{id}/abort", f.auth(f.runAbort))
	f.mux.HandleFunc("POST /v1/runs/{id}/answer", f.auth(f.runAnswer))
	f.mux.HandleFunc("GET /v1/events", f.auth(f.events_))
}

// cors mirrors the real engine's behaviour, with the harness origin allowed.
func (f *fakeEngine) cors(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}
	if f.allowOrigin == "*" || f.allowOrigin == origin {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Last-Event-ID, X-Ducklab-Client")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	}
}

func (f *fakeEngine) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.cors(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// EventSource cannot set headers, so /v1/events also accepts ?token=,
		// exactly as the real engine does.
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" && r.URL.Path == "/v1/events" {
			got = r.URL.Query().Get("token")
		}
		if got != f.token {
			f.write(w, http.StatusUnauthorized, map[string]interface{}{
				"error": map[string]string{"code": "unauthorized", "message": "invalid token"},
			})
			return
		}
		next(w, r)
	}
}

func (f *fakeEngine) write(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// --- scripted playback -------------------------------------------------------

// emit records an event and publishes it, mirroring how the real engine only
// publishes what it has already persisted.
func (f *fakeEngine) emit(typ string, data map[string]interface{}) {
	f.mu.Lock()
	f.seq++
	e := map[string]interface{}{
		"ts": time.Now().UTC().Format(time.RFC3339Nano), "seq": f.seq,
		"type": typ, "run_id": fakeRunID, "data": data,
	}
	f.events = append(f.events, e)
	seq := f.seq
	f.mu.Unlock()

	f.bus.Publish(bus.Event{
		Type: typ, RunID: fakeRunID, ProjectID: "demo", Seq: seq,
		TS: time.Now(), Data: data,
	})
}

func (f *fakeEngine) play() {
	// Wait for a client, then give its subscription a moment to settle.
	select {
	case <-f.firstClient:
	case <-time.After(30 * time.Second):
		return // nobody ever connected; nothing to demonstrate
	}
	time.Sleep(50 * time.Millisecond)
	f.emit("run_start", map[string]interface{}{"mode": scenarioMode(f.scenario)})

	switch f.scenario {
	case "idle":
		return

	case "flood":
		// A long run, for the virtualisation and frame-rate check (AC-33).
		for i := 0; i < 5000; i++ {
			if i%2 == 0 {
				f.emit("turn_start", map[string]interface{}{
					"round": i/2 + 1, "turn": 0, "role": "implementer", "duckling": "pato-uno",
				})
			} else {
				f.emit("turn_end", map[string]interface{}{"round": i / 2, "turn": 0})
			}
		}
		f.emit("gate", map[string]interface{}{"gate": "tests", "cmd": "go test ./...", "exit": 0})
		f.emit("verdict", map[string]interface{}{"verdict": "PASSED"})
		f.humanGate("PASSED")
		return

	case "question":
		f.emit("turn_start", map[string]interface{}{"round": 1, "turn": 0, "role": "implementer", "duckling": "pato-uno"})
		f.streamText("pato-uno", "implementer", "I need to know which behaviour you want. ")
		f.setStatus("paused", "")
		f.mu.Lock()
		f.run["pending_kind"] = "question"
		f.run["pending_since"] = time.Now().UTC().Format(time.RFC3339)
		f.run["pending_data"] = map[string]interface{}{
			"question_id": "q-fake-1", "question": "Should Add saturate or wrap on overflow?",
		}
		f.mu.Unlock()
		f.emit("human_needed", map[string]interface{}{
			"kind": "question", "question_id": "q-fake-1",
			"question": "Should Add saturate or wrap on overflow?",
		})
		return

	case "tournament":
		f.emit("turn_start", map[string]interface{}{"round": 1, "turn": 0, "role": "implementer", "duckling": "pato-uno"})
		f.emit("turn_start", map[string]interface{}{"round": 1, "turn": 1, "role": "implementer", "duckling": "pato-dos"})
		time.Sleep(f.delay)
		f.emit("gate", map[string]interface{}{"gate": "green", "cmd": "go test ./...", "exit": 0})
		f.emit("resolution", map[string]interface{}{
			"resolution": "short_circuit", "winner": "A",
			"reason": "only candidate whose verification passed",
		})
		f.emit("verdict", map[string]interface{}{"verdict": "PASSED"})
		f.humanGate("PASSED")
		return

	default: // pair
		f.emit("turn_start", map[string]interface{}{"round": 1, "turn": 0, "role": "implementer", "duckling": "pato-uno"})
		f.streamText("pato-uno", "implementer", "func Add(a, b int) int { return a + b }")
		f.emit("tool_call", map[string]interface{}{"tool": "fs_patch", "ok": true, "ms": 12})
		f.emit("turn_end", map[string]interface{}{"round": 1, "turn": 0, "role": "implementer"})

		f.emit("turn_start", map[string]interface{}{"round": 1, "turn": 1, "role": "reviewer", "duckling": "pato-dos"})
		f.streamText("pato-dos", "reviewer", `{"verdict":"approve","findings":[]}`)
		f.emit("turn_end", map[string]interface{}{"round": 1, "turn": 1, "role": "reviewer"})

		f.emit("gate", map[string]interface{}{"gate": "tests", "cmd": "go test ./...", "exit": 0})
		f.emit("verdict", map[string]interface{}{"verdict": "PASSED"})
		f.humanGate("PASSED")
	}
}

// streamText emits token_delta events, which are never given a seq and never
// recorded — exactly as the real engine treats them.
func (f *fakeEngine) streamText(duckling, role, text string) {
	for _, word := range strings.SplitAfter(text, " ") {
		if word == "" {
			continue
		}
		f.bus.Publish(bus.Event{
			Type: "token_delta", RunID: fakeRunID, ProjectID: "demo", TS: time.Now(),
			Data: map[string]interface{}{"duckling": duckling, "role": role, "text": word},
		})
		time.Sleep(f.delay)
	}
}

func (f *fakeEngine) humanGate(verdict string) {
	f.setStatus("paused", verdict)
	f.mu.Lock()
	f.run["pending_kind"] = "gate"
	f.run["pending_since"] = time.Now().UTC().Format(time.RFC3339)
	f.mu.Unlock()
	f.emit("human_needed", map[string]interface{}{"kind": "gate", "verdict": verdict})
}

func (f *fakeEngine) setStatus(status, verdict string) {
	f.mu.Lock()
	f.run["status"] = status
	if verdict != "" {
		f.run["verdict"] = verdict
	}
	f.mu.Unlock()
}

// --- handlers ---------------------------------------------------------------

func (f *fakeEngine) health(w http.ResponseWriter, r *http.Request) {
	f.cors(w, r)
	f.write(w, http.StatusOK, map[string]interface{}{
		"ok": true, "version": "0.3.0-fake", "uptime_s": 1, "active_runs": 1,
	})
}

func (f *fakeEngine) engine(w http.ResponseWriter, r *http.Request) {
	f.write(w, http.StatusOK, map[string]interface{}{"version": "0.3.0-fake", "fake": true})
}

func (f *fakeEngine) projects(w http.ResponseWriter, r *http.Request) {
	f.write(w, http.StatusOK, map[string]interface{}{
		"items": []map[string]interface{}{{
			"id": "demo", "path": "/tmp/demo", "name": "Demo",
			"gate": "tests", "autonomy": "guarded",
		}}, "total": 1,
	})
}

func (f *fakeEngine) project(w http.ResponseWriter, r *http.Request) {
	f.write(w, http.StatusOK, map[string]interface{}{
		"id": "demo", "path": "/tmp/demo", "name": "Demo",
		"gate": "tests", "autonomy": "guarded",
	})
}

func (f *fakeEngine) projectStatus(w http.ResponseWriter, r *http.Request) {
	f.write(w, http.StatusOK, map[string]interface{}{
		"stage_progress":     map[string]string{"build": "1 of 3"},
		"task_counts":        map[string]int{"todo": 2, "accepted": 1},
		"budget_spent_today": 0.014,
		"active_runs":        1,
	})
}

func (f *fakeEngine) ducklings(w http.ResponseWriter, r *http.Request) {
	f.write(w, http.StatusOK, map[string]interface{}{
		"items": []map[string]interface{}{
			{"id": "pato-uno", "provider": "beelink", "model": "gemma-4-26b",
				"caps": map[string]interface{}{"native_tools": false, "context_tokens": 65536},
				"cost": map[string]interface{}{"input_per_mtok": 0.0, "output_per_mtok": 0.0}},
			{"id": "pato-dos", "provider": "openrouter", "model": "qwen/qwen3.6",
				"caps": map[string]interface{}{"native_tools": true, "context_tokens": 131072},
				"cost": map[string]interface{}{"input_per_mtok": 0.2, "output_per_mtok": 0.6}},
		}, "total": 2,
	})
}

func (f *fakeEngine) providers(w http.ResponseWriter, r *http.Request) {
	f.write(w, http.StatusOK, map[string]interface{}{"items": []map[string]interface{}{
		{"id": "beelink", "kind": "openai", "base_url": "http://127.0.0.1:8080", "api_key_env": "BEELINK_API_KEY", "key_present": true},
		{"id": "openrouter", "kind": "openai", "base_url": "https://openrouter.ai/api/v1", "api_key_env": "OPENROUTER_API_KEY", "key_present": true},
	}, "total": 2})
}

func (f *fakeEngine) budgetDefaults(w http.ResponseWriter, r *http.Request) {
	f.write(w, http.StatusOK, map[string]interface{}{"max_usd": 10.0, "max_tokens": 400000, "max_turns": 20, "max_wallclock_s": 1800})
}

func (f *fakeEngine) roster(w http.ResponseWriter, r *http.Request) {
	f.write(w, http.StatusOK, map[string]interface{}{"entries": []map[string]interface{}{
		{"role": "implementer", "duckling": "pato-uno", "source": "default"},
		{"role": "reviewer", "duckling": "pato-dos", "source": "default"},
	}, "warning": "fake roster for mode " + r.URL.Query().Get("mode")})
}

func (f *fakeEngine) skills(w http.ResponseWriter, r *http.Request) {
	f.write(w, http.StatusOK, map[string]interface{}{"items": []map[string]interface{}{{"name": "example", "description": "A scripted example skill", "scope": "project"}}, "total": 1})
}

func (f *fakeEngine) tasks(w http.ResponseWriter, r *http.Request) {
	f.write(w, http.StatusOK, map[string]interface{}{"items": []map[string]interface{}{{"id": "T-001", "title": "Implement the example", "milestone": "M-001", "status": "todo"}}, "total": 1})
}

func (f *fakeEngine) bugs(w http.ResponseWriter, r *http.Request) {
	f.write(w, http.StatusOK, map[string]interface{}{"items": []map[string]interface{}{{"id": "B-001", "title": "Example issue", "status": "open", "severity": "medium"}}, "total": 1})
}

func (f *fakeEngine) runs(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	run := copyMap(f.run)
	f.mu.Unlock()
	f.write(w, http.StatusOK, map[string]interface{}{"items": []interface{}{run}, "total": 1})
}

func (f *fakeEngine) runGet(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	run := copyMap(f.run)
	events := append([]map[string]interface{}{}, f.events...)
	f.mu.Unlock()
	f.write(w, http.StatusOK, map[string]interface{}{"run": run, "events": events})
}

func (f *fakeEngine) runDiff(w http.ResponseWriter, r *http.Request) {
	f.write(w, http.StatusOK, map[string]interface{}{
		"diff": "--- a/add.go\n+++ b/add.go\n@@ -1,3 +1,3 @@\n func Add(a, b int) int {\n-\treturn a - b\n+\treturn a + b\n }\n",
	})
}

// runCandidates mirrors the real payload: labels and diffs, never authorship.
func (f *fakeEngine) runCandidates(w http.ResponseWriter, r *http.Request) {
	f.write(w, http.StatusOK, map[string]interface{}{
		"items": []map[string]interface{}{
			{"label": "A", "diff": "--- a/add.go\n+++ b/add.go\n+\treturn a + b\n", "gate": "green"},
			{"label": "B", "diff": "--- a/add.go\n+++ b/add.go\n+\treturn b + a\n", "gate": "red"},
		}, "total": 2,
	})
}

func (f *fakeEngine) runVerify(w http.ResponseWriter, r *http.Request) {
	f.write(w, http.StatusOK, map[string]interface{}{"output": "ok  \tfixture\t0.003s\n"})
}

func (f *fakeEngine) runAccept(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.accepted = true
	f.run["status"] = "done"
	f.run["accepted"] = true
	f.run["commit_sha"] = "e60dc7fe1234567890abcdef"
	f.run["pending_kind"] = ""
	f.mu.Unlock()
	f.emit("human", map[string]interface{}{"action": "accept"})
	f.emit("run_end", map[string]interface{}{"verdict": "PASSED"})
	f.write(w, http.StatusOK, map[string]interface{}{"commit_sha": "e60dc7fe1234567890abcdef"})
}

func (f *fakeEngine) runReject(w http.ResponseWriter, r *http.Request) {
	f.setStatus("done", "FAILED")
	f.emit("human", map[string]interface{}{"action": "reject"})
	f.emit("run_end", map[string]interface{}{"verdict": "FAILED"})
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeEngine) runAbort(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.aborted = true
	f.run["status"] = "failed"
	f.run["verdict"] = "ABORTED"
	f.mu.Unlock()
	f.emit("run_end", map[string]interface{}{"verdict": "ABORTED"})
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeEngine) runAnswer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		QuestionID string `json:"question_id"`
		Answer     string `json:"answer"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	f.mu.Lock()
	f.answered = true
	f.run["status"] = "running"
	f.run["pending_kind"] = ""
	f.run["pending_data"] = nil
	f.mu.Unlock()

	f.emit("human", map[string]interface{}{"action": "answer", "question_id": body.QuestionID})
	go func() {
		f.streamText("pato-uno", "implementer", "Understood, proceeding. ")
		f.emit("gate", map[string]interface{}{"gate": "tests", "cmd": "go test ./...", "exit": 0})
		f.emit("verdict", map[string]interface{}{"verdict": "PASSED"})
		f.humanGate("PASSED")
	}()
	w.WriteHeader(http.StatusNoContent)
}

// events_ is the SSE endpoint, with the same backlog-then-live contract as the
// real engine: subscribe first, replay second, deduplicate by seq.
func (f *fakeEngine) events_(w http.ResponseWriter, r *http.Request) {
	runID := r.URL.Query().Get("run")
	fromSeq := 0
	if v := r.URL.Query().Get("from_seq"); v != "" {
		fromSeq, _ = strconv.Atoi(v)
	}
	if last := r.Header.Get("Last-Event-ID"); last != "" {
		if i := strings.LastIndex(last, ":"); i >= 0 {
			if n, err := strconv.Atoi(last[i+1:]); err == nil {
				fromSeq = n
			}
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	sub, unsub := f.bus.Subscribe(fmt.Sprintf("sse-%d", time.Now().UnixNano()), func(e bus.Event) bool {
		return runID == "" || e.RunID == runID
	})
	defer unsub()

	f.clientOnce.Do(func() { close(f.firstClient) })

	f.mu.Lock()
	backlog := append([]map[string]interface{}{}, f.events...)
	f.mu.Unlock()

	replayedTo := fromSeq
	for _, e := range backlog {
		seq, _ := e["seq"].(int)
		if seq <= fromSeq {
			continue
		}
		writeSSE(w, flusher, e["type"].(string), fakeRunID, seq, e)
		replayedTo = seq
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-sub.Ch:
			if !ok {
				return
			}
			if e.Seq > 0 && e.Seq <= replayedTo {
				continue
			}
			payload := map[string]interface{}{
				"type": e.Type, "run_id": e.RunID, "project_id": e.ProjectID,
				"seq": e.Seq, "data": e.Data,
			}
			writeSSE(w, flusher, e.Type, e.RunID, e.Seq, payload)
		case <-heartbeat.C:
			fmt.Fprintf(w, "event: heartbeat\ndata: {\"ts\":%q}\n\n", time.Now().UTC().Format(time.RFC3339))
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, f http.Flusher, typ, runID string, seq int, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\n", typ)
	if seq > 0 {
		fmt.Fprintf(w, "id: %s:%d\n", runID, seq)
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	f.Flush()
}

func copyMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
