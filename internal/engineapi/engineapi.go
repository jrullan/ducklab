// Package engineapi implements the HTTP API for the ducklab engine.
// Handlers decode DTOs, call service methods, and encode DTOs. No logic here.
package engineapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/service"
)

// Server is the engine API server.
type Server struct {
	svc     *service.Service
	bus     *bus.Bus
	token   string
	version string
	mux     *http.ServeMux
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
	s.mux.HandleFunc("POST /v1/projects/{id}/runs", s.auth(s.handleRunStart))
	s.mux.HandleFunc("GET /v1/runs", s.auth(s.handleRunList))
	s.mux.HandleFunc("GET /v1/runs/{id}", s.auth(s.handleRunGet))
	s.mux.HandleFunc("POST /v1/runs/{id}/accept", s.auth(s.handleRunAccept))
	s.mux.HandleFunc("POST /v1/runs/{id}/reject", s.auth(s.handleRunReject))
	s.mux.HandleFunc("POST /v1/runs/{id}/abort", s.auth(s.handleRunAbort))
	s.mux.HandleFunc("GET /v1/events", s.auth(s.handleEvents))
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
	// TODO: implement graceful shutdown
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

// handleEvents handles SSE.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project")
	runID := r.URL.Query().Get("run")
	fromSeq := 0
	if seq := r.URL.Query().Get("from_seq"); seq != "" {
		fmt.Sscanf(seq, "%d", &fromSeq)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.error(w, http.StatusInternalServerError, "internal", "streaming not supported")
		return
	}

	// Subscribe to bus
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
