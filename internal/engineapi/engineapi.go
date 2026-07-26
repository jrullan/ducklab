// Package engineapi implements the HTTP API for the ducklab engine.
// Handlers decode DTOs, call service methods, and encode DTOs. No logic here.
package engineapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	// Health (no auth)
	s.mux.HandleFunc("GET /v1/health", s.handleHealth)
	// Auth-protected
	s.mux.HandleFunc("GET /v1/engine", s.auth(s.handleEngine))
	s.mux.HandleFunc("POST /v1/shutdown", s.auth(s.handleShutdown))
	s.mux.HandleFunc("GET /v1/projects", s.auth(s.handleProjectList))
	s.mux.HandleFunc("POST /v1/projects", s.auth(s.handleProjectCreate))
	s.mux.HandleFunc("GET /v1/projects/{id}", s.auth(s.handleProjectGet))
	s.mux.HandleFunc("GET /v1/ducklings", s.auth(s.handleDucklingList))
	s.mux.HandleFunc("POST /v1/ducklings/{id}/test", s.auth(s.handleDucklingTest))
	s.mux.HandleFunc("POST /v1/projects/{id}/runs", s.auth(s.handleRunStart))
	s.mux.HandleFunc("GET /v1/runs", s.auth(s.handleRunList))
	s.mux.HandleFunc("GET /v1/runs/{id}", s.auth(s.handleRunGet))
	s.mux.HandleFunc("POST /v1/runs/{id}/accept", s.auth(s.handleRunAccept))
	s.mux.HandleFunc("POST /v1/runs/{id}/reject", s.auth(s.handleRunReject))
	s.mux.HandleFunc("POST /v1/runs/{id}/abort", s.auth(s.handleRunAbort))
	s.mux.HandleFunc("POST /v1/runs/{id}/resume", s.auth(s.handleRunResume))
	s.mux.HandleFunc("POST /v1/runs/{id}/answer", s.auth(s.handleRunAnswer))
	s.mux.HandleFunc("GET /v1/projects/{id}/report", s.auth(s.handleReport))
	s.mux.HandleFunc("GET /v1/projects/{id}/roster", s.auth(s.handleRosterGet))
	s.mux.HandleFunc("PUT /v1/projects/{id}/roster", s.auth(s.handleRosterSet))
	s.mux.HandleFunc("GET /v1/projects/{id}/roster/suggest", s.auth(s.handleRosterSuggest))
	s.mux.HandleFunc("POST /v1/projects/{id}/roster/suggest", s.auth(s.handleRosterApply))
	s.mux.HandleFunc("POST /v1/ducklings/{id}/probe", s.auth(s.handleDucklingProbe))
	s.mux.HandleFunc("GET /v1/events", s.auth(s.handleEvents))
}

// allowedOrigins are the only origins permitted to call the engine.
// 07 §1 forbids a wildcard: the engine is loopback-only and token-guarded,
// and a wildcard would let any page in a browser probe it.
var allowedOrigins = map[string]bool{
	"wails://wails":           true,
	"wails.localhost":         true,
	"http://wails.localhost":  true,
	"https://wails.localhost": true,
}

func setCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return // non-browser client; no CORS headers needed
	}
	if allowedOrigins[origin] {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Last-Event-ID")
	}
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
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			s.error(w, http.StatusUnauthorized, "unauthorized", "missing Authorization header")
			return
		}
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			s.error(w, http.StatusUnauthorized, "unauthorized", "invalid Authorization header")
			return
		}
		if parts[1] != s.token {
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

func (s *Server) handleProjectGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	project, err := s.svc.ProjectOpen(r.Context(), id)
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
		case e := <-sub.Ch:
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
