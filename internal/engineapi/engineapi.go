// Package engineapi implements the HTTP API for the ducklab engine.
// Handlers decode DTOs, call service methods, and encode DTOs. No logic here.
package engineapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/report"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/service"
)

// Server is the engine API server.
type Server struct {
	svc     *service.Service
	bus     *bus.Bus
	token   string
	version string
	mux     *http.ServeMux

	// OnShutdown, if set, is invoked by POST /v1/shutdown. The daemon owns
	// the actual stop sequence; the API only requests it.
	OnShutdown func()

	// Seams for testing the SSE path without a full Service.
	runDirFn    func(runID string) string
	projectIDFn func(runID string) string
	// hookDuringReplay, if set, runs while the backlog is being replayed.
	// It exists so a test can emit an event exactly inside the
	// replay/subscribe window, which is the only way to prove that window
	// loses nothing.
	hookDuringReplay func()
}

// New creates a new engine API server.
func New(svc *service.Service, b *bus.Bus, token, version string) *Server {
	s := &Server{
		svc:     svc,
		bus:     b,
		token:   token,
		version: version,
		mux:     http.NewServeMux(),
	}
	s.runDirFn = func(runID string) string { return svc.RunDir(runID) }
	s.projectIDFn = func(runID string) string {
		if d, err := svc.RunGet(context.Background(), runID); err == nil && d.Run != nil {
			return d.Run.ProjectID
		}
		return ""
	}
	s.routes()
	return s
}

// ServeHTTP implements http.Handler.
//
// CORS preflight is answered here, before routing. Go's ServeMux matches
// method-specific patterns ("GET /v1/runs"), so an OPTIONS request matches no
// route and would get a 405 with no CORS headers — which means every
// browser-side fetch carrying an Authorization header fails before it is ever
// sent. The desktop app cannot talk to the engine at all without this.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		setCORS(w, r)
		if w.Header().Get("Access-Control-Allow-Origin") == "" {
			// Unknown origin: refuse rather than answer a preflight we would
			// not honour anyway.
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	setCORS(w, r)
	s.mux.ServeHTTP(w, r)
}

// routes wires the table into the mux. Both the mux and the OpenAPI document
// are built from routeTable(), so a route can never exist in one and not the
// other.
func (s *Server) routes() {
	for _, r := range routeTable() {
		h := r.handler(s)
		if r.Auth {
			h = s.auth(h)
		}
		s.mux.HandleFunc(r.Method+" "+r.Path, h)
	}
	s.mux.HandleFunc("GET /v1/openapi.json", s.handleOpenAPI)
}

// allowedOrigins are the only origins permitted to call the engine.
// 07 §1 forbids a wildcard: the engine is loopback-only and token-guarded,
// and a wildcard would let any page in a browser probe it.
func allowedOriginList() string {
	out := make([]string, 0, len(allowedOrigins))
	for o := range allowedOrigins {
		out = append(out, o)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// The Linux webview loads "wails://" and sends Origin: wails://localhost.
// These values were MEASURED from a running app, not guessed: the earlier
// list was invented and every request the desktop made was refused, which the
// browser reports only as "Load failed" with no origin named.
var allowedOrigins = map[string]bool{
	"wails://localhost":       true, // Linux (GTK/WebKitGTK) and macOS
	"wails://wails":           true, // Windows (WebView2)
	"http://wails.localhost":  true, // Windows, older WebView2 builds
	"https://wails.localhost": true,
}

func setCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return // non-browser client; no CORS headers needed
	}
	if !allowedOrigins[origin] {
		// A refused origin is the single hardest failure to diagnose from the
		// client side: the browser reports only "Load failed" and never says
		// which origin was rejected. Say it here.
		log.Printf("cors: refused origin %q (allowed: %s)", origin, allowedOriginList())
		return
	}
	if allowedOrigins[origin] {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		// X-Ducklab-Client must be listed: the engine REQUIRES it for version
		// skew detection, so omitting it here made every browser request fail
		// preflight against the engine's own contract.
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Last-Event-ID, X-Ducklab-Client")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	}
}

func (s *Server) handleRunDiff(w http.ResponseWriter, r *http.Request) {
	diff, err := s.svc.RunDiff(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"diff": diff})
}

func (s *Server) handleRunCandidates(w http.ResponseWriter, r *http.Request) {
	cands, err := s.svc.RunCandidates(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": cands, "total": len(cands)})
}

func (s *Server) handleRunVerify(w http.ResponseWriter, r *http.Request) {
	tail := 500
	if v := r.URL.Query().Get("tail"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			tail = n
		}
	}
	out, err := s.svc.RunVerify(r.Context(), r.PathValue("id"), tail)
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"output": out})
}

func (s *Server) handleRunTranscript(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.RunTranscript(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"markdown": out})
}

func (s *Server) handleRunLLM(w http.ResponseWriter, r *http.Request) {
	fromSeq := 0
	if v := r.URL.Query().Get("from_seq"); v != "" {
		fmt.Sscanf(v, "%d", &fromSeq)
	}
	calls, err := s.svc.RunLLMCalls(r.Context(), r.PathValue("id"), fromSeq)
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": calls, "total": len(calls)})
}

func (s *Server) handleProjectStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.svc.ProjectStatus(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, st)
}

func (s *Server) handleRosterGet(w http.ResponseWriter, r *http.Request) {
	view, err := s.svc.RosterGet(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, view)
}

func (s *Server) handleRosterSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Role     string `json:"role"`
		Duckling string `json:"duckling"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	view, err := s.svc.RosterSet(r.Context(), r.PathValue("id"), body.Role, body.Duckling)
	if err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, view)
}

func (s *Server) handleRosterSuggest(w http.ResponseWriter, r *http.Request) {
	sugg, err := s.svc.RosterSuggest(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": sugg, "total": len(sugg)})
}

func (s *Server) handleRosterApply(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sugg, err := s.svc.RosterSuggest(r.Context(), id)
	if err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	view, err := s.svc.RosterApply(r.Context(), id, sugg)
	if err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.json(w, http.StatusOK, view)
}

func (s *Server) handleDucklingProbe(w http.ResponseWriter, r *http.Request) {
	caps, err := s.svc.DucklingProbeForce(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusFailedDependency, "provider", err.Error())
		return
	}
	s.json(w, http.StatusOK, caps)
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	opts := report.Options{By: r.URL.Query().Get("by")}
	if since := r.URL.Query().Get("since"); since != "" {
		if d, err := parseSince(since); err == nil {
			opts.Since = time.Now().Add(-d)
		}
	}
	rep, err := s.svc.Report(r.Context(), r.PathValue("id"), opts)
	if err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.json(w, http.StatusOK, rep)
}

// parseSince accepts "30d", "12h", "90m".
func parseSince(v string) (time.Duration, error) {
	if strings.HasSuffix(v, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(v, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(v)
}

func (s *Server) handleRunAnswer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		QuestionID string `json:"question_id"`
		Answer     string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.svc.RunAnswer(r.Context(), r.PathValue("id"), body.QuestionID, body.Answer); err != nil {
		s.error(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRunResume(w http.ResponseWriter, r *http.Request) {
	run, err := s.svc.RunResume(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	s.json(w, http.StatusAccepted, run)
}

// auth middleware checks the bearer token.
// bearerToken extracts the caller's token.
//
// EventSource cannot set request headers, so the SSE endpoint alone also
// accepts ?token=. Without this the desktop app's event stream can never
// authenticate — the browser simply has no way to send the header. The
// exception is deliberately narrow: every other endpoint is reached with
// fetch, which can set headers, and a token in a query string is more likely
// to end up in a log.
func bearerToken(r *http.Request) (string, bool) {
	if auth := r.Header.Get("Authorization"); auth != "" {
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			return "", false
		}
		return parts[1], true
	}
	if r.URL.Path == "/v1/events" {
		if t := r.URL.Query().Get("token"); t != "" {
			return t, true
		}
	}
	return "", false
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			s.error(w, http.StatusUnauthorized, "unauthorized", "missing or malformed credentials")
			return
		}
		if token != s.token {
			s.error(w, http.StatusUnauthorized, "unauthorized", "invalid token")
			return
		}
		// Check version
		clientVersion := r.Header.Get("X-Ducklab-Client")
		if clientVersion != "" {
			clientMajor := strings.Split(clientVersion, ".")[0]
			serverMajor := strings.Split(s.version, ".")[0]
			if clientMajor != serverMajor {
				s.error(w, http.StatusConflict, "version_skew", fmt.Sprintf("client major version %s != server major version %s", clientMajor, serverMajor))
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) error(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
		},
	})
}

func (s *Server) json(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.json(w, http.StatusOK, map[string]interface{}{
		"ok":          true,
		"version":     s.version,
		"uptime_s":    0, // TODO: track uptime
		"active_runs": 0, // TODO: count active runs
	})
}

func (s *Server) handleEngine(w http.ResponseWriter, r *http.Request) {
	s.json(w, http.StatusOK, map[string]interface{}{
		"version": s.version,
	})
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	s.json(w, http.StatusAccepted, map[string]string{"status": "shutting down"})
	if s.OnShutdown != nil {
		s.OnShutdown()
	}
}

func (s *Server) handleProjectList(w http.ResponseWriter, r *http.Request) {
	projects, err := s.svc.ProjectList(r.Context())
	if err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": projects, "total": len(projects)})
}

func (s *Server) handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	var req service.InitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	project, err := s.svc.ProjectInit(r.Context(), req)
	if err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.json(w, http.StatusCreated, project)
}

func (s *Server) handleProjectUpdate(w http.ResponseWriter, r *http.Request) {
	var keys map[string]string
	if err := json.NewDecoder(r.Body).Decode(&keys); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	project, err := s.svc.ProjectUpdate(r.Context(), r.PathValue("id"), keys)
	if err != nil {
		// An unknown or mistyped key is the caller's mistake, not a server
		// fault, and it must say which key.
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, project)
}

func (s *Server) handleReleasePlan(w http.ResponseWriter, r *http.Request) {
	var req service.ReleaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	run, err := s.svc.ReleasePlan(r.Context(), r.PathValue("id"), req)
	if err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, run)
}

func (s *Server) handleReleaseCut(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.ReleaseCut(r.Context(), r.PathValue("id"), r.PathValue("version"))
	if err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, out)
}

func (s *Server) handleReviewStart(w http.ResponseWriter, r *http.Request) {
	var req service.ReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	run, err := s.svc.ReviewStart(r.Context(), r.PathValue("id"), req)
	if err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, run)
}

func (s *Server) handleProjectForget(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.ProjectForget(r.Context(), r.PathValue("id")); err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleProjectGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	project, err := s.svc.ProjectGet(r.Context(), id)
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, project)
}

func (s *Server) handleDucklingList(w http.ResponseWriter, r *http.Request) {
	ducklings, err := s.svc.DucklingList(r.Context())
	if err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": ducklings, "total": len(ducklings)})
}

func (s *Server) handleDucklingTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Prompt string `json:"prompt"`
		Stream bool   `json:"stream"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	text, tokensIn, tokensOut, cost, err := s.svc.DucklingTest(r.Context(), id, body.Prompt, body.Stream)
	if err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{
		"text":       text,
		"tokens_in":  tokensIn,
		"tokens_out": tokensOut,
		"cost_usd":   cost,
	})
}

func (s *Server) handleRunStart(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	var req service.RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	run, err := s.svc.RunStart(r.Context(), projectID, req)
	if err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.json(w, http.StatusAccepted, run)
}

func (s *Server) handleRunList(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project")
	status := r.URL.Query().Get("status")
	runs, err := s.svc.RunList(r.Context(), service.RunFilter{
		ProjectID: projectID,
		Status:    status,
	})
	if err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": runs, "total": len(runs)})
}

func (s *Server) handleRunGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	detail, err := s.svc.RunGet(r.Context(), id)
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, detail)
}

func (s *Server) handleRunAccept(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Message string `json:"message"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	result, err := s.svc.RunAccept(r.Context(), id, body.Message)
	if err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.json(w, http.StatusOK, result)
}

func (s *Server) handleRunReject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Reason string `json:"reason"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if err := s.svc.RunReject(r.Context(), id, body.Reason); err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRunAbort(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.svc.RunAbort(r.Context(), id); err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// replayBacklog sends the persisted events for a run before live events and
// returns the highest seq it delivered, so the live loop can skip duplicates.
//
// Replayed events are emitted in the same JSON shape as live bus events; a
// client must not have to parse two shapes for one logical event.
func (s *Server) replayBacklog(w http.ResponseWriter, flusher http.Flusher, runID string, fromSeq int) int {
	runDir := s.runDirFn(runID)
	if runDir == "" {
		return fromSeq
	}
	events, err := runlog.ReadEvents(runDir)
	if err != nil {
		return fromSeq
	}
	projectID := s.projectIDFn(runID)
	if s.hookDuringReplay != nil {
		s.hookDuringReplay()
	}
	last := fromSeq
	for _, e := range events {
		if e.Seq <= fromSeq {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, e.TS)
		if err != nil {
			ts = time.Now()
		}
		data, err := json.Marshal(bus.Event{
			Type:      e.Type,
			RunID:     e.RunID,
			ProjectID: projectID,
			Seq:       e.Seq,
			TS:        ts,
			Data:      e.Data,
		})
		if err != nil {
			continue
		}
		fmt.Fprintf(w, "event: %s\n", e.Type)
		fmt.Fprintf(w, "id: %s:%d\n", e.RunID, e.Seq)
		fmt.Fprintf(w, "data: %s\n\n", data)
		last = e.Seq
	}
	flusher.Flush()
	return last
}

// handleEvents handles SSE.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project")
	runID := r.URL.Query().Get("run")
	fromSeq := 0
	if seq := r.URL.Query().Get("from_seq"); seq != "" {
		fmt.Sscanf(seq, "%d", &fromSeq)
	}

	// Last-Event-ID ("<runID>:<seq>") takes precedence over from_seq so a
	// reconnecting client resumes exactly where it was cut off.
	if lastID := r.Header.Get("Last-Event-ID"); lastID != "" {
		if i := strings.LastIndex(lastID, ":"); i >= 0 {
			if n, err := strconv.Atoi(lastID[i+1:]); err == nil {
				fromSeq = n
				if runID == "" {
					runID = lastID[:i]
				}
			}
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	setCORS(w, r)

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.error(w, http.StatusInternalServerError, "internal", "streaming not supported")
		return
	}

	// Order matters and is the whole correctness argument here.
	//
	// Subscribing BEFORE replaying the backlog means an event emitted during
	// the replay lands in the subscriber buffer instead of being lost. The
	// reverse order leaves a window in which events vanish (07 §6: "backlog
	// then live, no gap and no duplicate"). Duplicates from that overlap are
	// then removed by seq, below.
	filter := func(e bus.Event) bool {
		if projectID != "" && e.ProjectID != projectID {
			return false
		}
		if runID != "" && e.RunID != runID {
			return false
		}
		return true
	}
	sub, unsub := s.bus.Subscribe(fmt.Sprintf("sse-%d", time.Now().UnixNano()), filter)
	defer unsub()

	replayedTo := fromSeq
	if runID != "" {
		replayedTo = s.replayBacklog(w, flusher, runID, fromSeq)
	}

	// Heartbeat ticker
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return
		case e, open := <-sub.Ch:
			if !open {
				// The bus dropped us for overflow. Ending the response is what
				// makes the client reconnect with Last-Event-ID; ranging over a
				// closed channel would spin instead.
				return
			}
			// Drop anything the backlog replay already delivered. Persisted
			// events carry a seq; token_delta and heartbeat do not and always
			// pass through.
			if e.Seq > 0 && e.RunID == runID && e.Seq <= replayedTo {
				continue
			}
			data, err := json.Marshal(e)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\n", e.Type)
			if e.Seq > 0 {
				fmt.Fprintf(w, "id: %s:%d\n", e.RunID, e.Seq)
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprintf(w, "event: heartbeat\n")
			fmt.Fprintf(w, "data: {\"ts\":%q}\n\n", time.Now().UTC().Format(time.RFC3339))
			flusher.Flush()
		}
	}
}

func (s *Server) handleTraceCheck(w http.ResponseWriter, r *http.Request) {
	errs, err := s.svc.TraceCheck(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"errors": errs})
}

// handleOpenAPI serves the document the clients are generated from.
func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	s.json(w, http.StatusOK, BuildOpenAPI(s.version))
}

func (s *Server) handleStageStart(w http.ResponseWriter, r *http.Request) {
	var req service.StageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	req.Stage = r.PathValue("stage")
	run, err := s.svc.StageStart(r.Context(), r.PathValue("id"), req)
	if err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.json(w, http.StatusAccepted, run)
}

func (s *Server) handleArtifactGet(w http.ResponseWriter, r *http.Request) {
	got, err := s.svc.ArtifactGet(r.Context(), r.PathValue("id"), r.PathValue("kind"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, got)
}

func (s *Server) handleArtifactPromote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ApprovedBy string `json:"approved_by"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	got, err := s.svc.ArtifactPromote(r.Context(), r.PathValue("id"), r.PathValue("kind"), body.ApprovedBy)
	if err != nil {
		s.error(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	s.json(w, http.StatusOK, got)
}

func (s *Server) handleTaskList(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.svc.TaskList(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": tasks, "total": len(tasks)})
}

func (s *Server) handleTaskNext(w http.ResponseWriter, r *http.Request) {
	task, err := s.svc.TaskNext(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	if task == nil {
		s.json(w, http.StatusOK, map[string]interface{}{})
		return
	}
	s.json(w, http.StatusOK, task)
}

func (s *Server) handleTraceShow(w http.ResponseWriter, r *http.Request) {
	node, err := s.svc.TraceShow(r.Context(), r.PathValue("id"), r.PathValue("anyID"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, node)
}
