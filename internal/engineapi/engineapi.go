// Package engineapi implements the HTTP API for the ducklab engine.
// Handlers decode DTOs, call service methods, and encode DTOs. No logic here.
package engineapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	netpprof "net/http/pprof"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/bench"
	"github.com/jrullan/ducklab/internal/bug"
	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/report"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/service"
	"github.com/jrullan/ducklab/internal/verify"
)

// Server is the engine API server.
type Server struct {
	svc        *service.Service
	bus        *bus.Bus
	token      string
	version    string
	provenance string
	mux        *http.ServeMux

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
func New(svc *service.Service, b *bus.Bus, token, version, provenance string) *Server {
	p := provenance
	if p == "" {
		p = "unknown@unknown"
	}
	s := &Server{
		svc:        svc,
		bus:        b,
		token:      token,
		version:    version,
		provenance: p,
		mux:        http.NewServeMux(),
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
	// pprof, token-guarded, outside the route table (debug surface, not API).
	// The one time a goroutine dump was needed — an exec that survived its
	// abort — the engine's stderr pointed at /dev/null and the diagnosis ran
	// blind. Loopback-only plus auth: same trust boundary as everything else.
	s.mux.HandleFunc("GET /debug/pprof/", s.auth(netpprof.Index))
	s.mux.HandleFunc("GET /debug/pprof/profile", s.auth(netpprof.Profile))
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

func (s *Server) handleSkillList(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.SkillList(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": items})
}

func (s *Server) handleSkillGet(w http.ResponseWriter, r *http.Request) {
	sk, problems, err := s.svc.SkillGet(r.PathValue("id"), r.PathValue("name"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	// raw is the whole SKILL.md, frontmatter included: the desktop editor
	// edits the file, not a projection of it. dir is where it lives, so a
	// person can find it on disk.
	raw, _ := os.ReadFile(filepath.Join(sk.Dir, "SKILL.md"))
	s.json(w, http.StatusOK, map[string]interface{}{
		"name": sk.Name, "description": sk.Description, "version": sk.Version,
		"scope": sk.Scope, "entry": sk.Entry, "body": sk.Body,
		"args": sk.Args, "problems": problems,
		"raw": string(raw), "dir": sk.Dir,
	})
}

func (s *Server) handleSkillSave(w http.ResponseWriter, r *http.Request) {
	var req skillSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	problems, err := s.svc.SkillSave(r.PathValue("id"), r.PathValue("name"), req.Content)
	if err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"problems": problems})
}

func (s *Server) handleSkillDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.SkillDelete(r.PathValue("id"), r.PathValue("name")); err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"deleted": true})
}

func (s *Server) handleSkillNew(w http.ResponseWriter, r *http.Request) {
	var req skillNewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	dir, err := s.svc.SkillNew(r.PathValue("id"), req.Name, req.Runnable)
	if err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"dir": dir})
}

func (s *Server) handleSkillRun(w http.ResponseWriter, r *http.Request) {
	var req skillRunRequest
	// An empty body is a skill with no arguments, not a malformed request.
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.error(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	// Two different failures, kept apart. A skill that ran and exited non-zero
	// is an answer and carries its output; a project that could not be opened
	// is an error and carries a message. Collapsing them reported "the skill
	// failed" with nothing in it, for a skill that had never run.
	out, failed, err := s.svc.SkillRun(r.Context(), r.PathValue("id"), r.PathValue("name"), req.Args)
	if err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"output": out, "failed": failed})
}

func (s *Server) handleBenchStart(w http.ResponseWriter, r *http.Request) {
	var req benchRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.error(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	out, err := s.svc.BenchStart(service.BenchOptions{
		Suite: req.Suite, Ducklings: req.Ducklings, Modes: req.Modes, Keep: req.Keep,
	})
	if err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, out)
}

func (s *Server) handleBench(w http.ResponseWriter, r *http.Request) {
	var req benchRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.error(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	res, path, err := s.svc.BenchRun(r.Context(), service.BenchOptions{
		Suite: req.Suite, Ducklings: req.Ducklings, Modes: req.Modes, Keep: req.Keep,
	})
	if err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	// Rendered by the engine, like the report table: the CLI may not import
	// bench either (AC-16), and one renderer cannot drift from another.
	s.json(w, http.StatusOK, map[string]interface{}{
		"result": res, "path": path, "rendered": bench.Render(*res),
	})
}

func (s *Server) handleBenchList(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.BenchList()
	if err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": items})
}

func (s *Server) handleBenchGet(w http.ResponseWriter, r *http.Request) {
	res, err := s.svc.BenchGet(r.PathValue("suite"), r.PathValue("stamp"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{
		"result": res, "rendered": bench.Render(*res),
	})
}

func (s *Server) handleProviderList(w http.ResponseWriter, r *http.Request) {
	s.json(w, http.StatusOK, map[string]interface{}{"items": s.svc.ProviderList()})
}

func (s *Server) handleBudgetDefaults(w http.ResponseWriter, r *http.Request) {
	s.json(w, http.StatusOK, s.svc.BudgetDefaults())
}

func (s *Server) handleBudgetDefaultsSet(w http.ResponseWriter, r *http.Request) {
	var view service.BudgetView
	if err := json.NewDecoder(r.Body).Decode(&view); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.svc.BudgetDefaultsSet(view); err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	// The saved values back, so a client never shows a number the engine did
	// not accept.
	s.json(w, http.StatusOK, s.svc.BudgetDefaults())
}

func (s *Server) handleModeDefaults(w http.ResponseWriter, r *http.Request) {
	s.json(w, http.StatusOK, s.svc.ModeDefaults())
}

func (s *Server) handleProjectInstall(w http.ResponseWriter, r *http.Request) {
	res, err := s.svc.ProjectInstall(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, res)
}

func (s *Server) handleCandidateCriteria(w http.ResponseWriter, r *http.Request) {
	s.json(w, http.StatusOK, s.svc.CandidateCriteria())
}

func (s *Server) handleCandidateCriteriaSet(w http.ResponseWriter, r *http.Request) {
	var body candidateCriteriaRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.svc.CandidateCriteriaSet(body.Criteria); err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, s.svc.CandidateCriteria())
}

func (s *Server) handleModeDefaultsSet(w http.ResponseWriter, r *http.Request) {
	var view service.ModeDefaultsView
	if err := json.NewDecoder(r.Body).Decode(&view); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.svc.ModeDefaultsSet(view); err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, s.svc.ModeDefaults())
}

func (s *Server) handleProviderSet(w http.ResponseWriter, r *http.Request) {
	var view service.ProviderView
	if err := json.NewDecoder(r.Body).Decode(&view); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.svc.ProviderSet(r.PathValue("id"), view); err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": s.svc.ProviderList()})
}

func (s *Server) handleProviderRemove(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.ProviderRemove(r.PathValue("id")); err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": s.svc.ProviderList()})
}

func (s *Server) handleScorecards(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.Scorecards(r.Context())
	if err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": items, "total": len(items)})
}

func (s *Server) handleDucklingSet(w http.ResponseWriter, r *http.Request) {
	var view service.DucklingView
	if err := json.NewDecoder(r.Body).Decode(&view); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.svc.DucklingSet(r.PathValue("id"), view); err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	list, err := s.svc.DucklingList(r.Context())
	if err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": list})
}

func (s *Server) handleDucklingRemove(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DucklingRemove(r.PathValue("id")); err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	list, err := s.svc.DucklingList(r.Context())
	if err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": list})
}

func (s *Server) handleTestStart(w http.ResponseWriter, r *http.Request) {
	var req service.TestFirstRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.error(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	run, err := s.svc.TestStart(r.Context(), r.PathValue("id"), req)
	if err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, run)
}

func (s *Server) handleTestRetire(w http.ResponseWriter, r *http.Request) {
	run, err := s.svc.TestRetire(r.Context(), r.PathValue("id"), r.PathValue("task"))
	if err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, run)
}

func (s *Server) handleAppStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.svc.AppStatus(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, st)
}

func (s *Server) handleAppStart(w http.ResponseWriter, r *http.Request) {
	st, err := s.svc.AppStart(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	s.json(w, http.StatusOK, st)
}

func (s *Server) handleAppStop(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.AppStop(r.Context(), r.PathValue("id")); err != nil {
		s.error(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGateRun(w http.ResponseWriter, r *http.Request) {
	res, err := s.svc.GateRun(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, res)
}

func (s *Server) handleProjectGate(w http.ResponseWriter, r *http.Request) {
	st, err := s.svc.ProjectGate(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, st)
}

func (s *Server) handleProjectGateAdopt(w http.ResponseWriter, r *http.Request) {
	st, err := s.svc.ProjectGateAdopt(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, st)
}

func (s *Server) handleRunBrief(w http.ResponseWriter, r *http.Request) {
	// Absent for most runs, which is not an error: only a seeded stage has one.
	brief, _ := s.svc.RunBrief(r.Context(), r.PathValue("id"))
	s.json(w, http.StatusOK, map[string]interface{}{"brief": brief})
}

func (s *Server) handleRunDiff(w http.ResponseWriter, r *http.Request) {
	diff, err := s.svc.RunDiff(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	// tests.patch exists only for a flagged run, so its absence is the normal
	// case and not an error.
	tests, _ := s.svc.RunTestHunks(r.Context(), r.PathValue("id"))
	warning := ""
	if tests != "" {
		warning = verify.TamperMessage
	}
	s.json(w, http.StatusOK, map[string]interface{}{"diff": diff, "tests": tests, "warning": warning})
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

func (s *Server) handleProjectRecover(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.ProjectRecover(r.Context(), r.PathValue("id"), r.PathValue("action")); err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleProjectStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.svc.ProjectStatus(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, st)
}

func (s *Server) handleGlobalRosterGet(w http.ResponseWriter, r *http.Request) {
	view, err := s.svc.GlobalRosterGet(r.Context(), r.URL.Query().Get("mode"))
	if err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, view)
}
func (s *Server) handleGlobalRosterSet(w http.ResponseWriter, r *http.Request) {
	var body rosterSetRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.error(w, 400, "bad_request", err.Error())
		return
	}
	ids := body.Ducklings
	if len(ids) == 0 && body.Duckling != "" {
		ids = []string{body.Duckling}
	}
	view, err := s.svc.GlobalRosterSet(r.Context(), body.Mode, body.Role, ids)
	if err != nil {
		s.error(w, 400, "bad_request", err.Error())
		return
	}
	s.json(w, 200, view)
}

func (s *Server) handleRosterGet(w http.ResponseWriter, r *http.Request) {
	view, err := s.svc.RosterGet(r.Context(), r.PathValue("id"), r.URL.Query().Get("mode"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, view)
}

func (s *Server) handleRosterSet(w http.ResponseWriter, r *http.Request) {
	var body rosterSetRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ids := body.Ducklings
	if len(ids) == 0 && body.Duckling != "" {
		ids = []string{body.Duckling}
	}
	view, err := s.svc.RosterSetManyMode(r.Context(), r.PathValue("id"), requestMode(r, body.Mode), body.Role, ids)
	if err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, view)
}

// handleRosterUnpin is an MCP/service route; desktop roster UI remains out of scope.
func (s *Server) handleRosterUnpin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	view, err := s.svc.RosterUnpin(r.Context(), r.PathValue("id"), requestMode(r, body.Mode), body.Role)
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
	// The table travels rendered. The CLI may not import report (AC-16), so it
	// grew a second renderer that drifted — it lost the avg_wall column, the
	// underscored token counts and the estimated marker, and nothing noticed
	// because both sides passed their own tests. One renderer, one table.
	s.json(w, http.StatusOK, map[string]interface{}{
		"by": rep.By, "baseline": rep.Baseline, "rows": rep.Rows,
		"deltas": rep.Deltas, "resolved": rep.Resolved,
		"rendered": report.Render(rep),
	})
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

// bugItems is the filed-findings response shape.
type bugItems struct {
	Items []bug.Bug `json:"items"`
}

func (s *Server) handleRunFileFindings(w http.ResponseWriter, r *http.Request) {
	bugs, err := s.svc.RunFileFindings(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	s.json(w, http.StatusOK, bugItems{Items: bugs})
}

// attachRequest carries one bug attachment, base64-encoded.
type attachRequest struct {
	Filename string `json:"filename"`
	Data     string `json:"data"`
}

func (s *Server) handleBugAttach(w http.ResponseWriter, r *http.Request) {
	var req attachRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 12<<20)).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", "data must be base64: "+err.Error())
		return
	}
	items, err := s.svc.BugAttach(r.Context(), r.PathValue("id"), r.PathValue("bug"), req.Filename, data)
	if err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": items})
}

func (s *Server) handleBugAttachment(w http.ResponseWriter, r *http.Request) {
	p, err := s.svc.BugAttachmentPath(r.Context(), r.PathValue("id"), r.PathValue("bug"), r.PathValue("name"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	http.ServeFile(w, r, p)
}

type chatSendRequest struct {
	Message string   `json:"message"`
	Images  []string `json:"images,omitempty"`
}

func (s *Server) handleChatStart(w http.ResponseWriter, r *http.Request) {
	var req service.ChatStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	run, err := s.svc.ChatStart(r.Context(), r.PathValue("id"), req)
	if err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, run)
}

func (s *Server) handleChatEnd(w http.ResponseWriter, r *http.Request) {
	run, err := s.svc.ChatEnd(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	s.json(w, http.StatusOK, run)
}

func (s *Server) handleChatSend(w http.ResponseWriter, r *http.Request) {
	var req chatSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	run, err := s.svc.ChatSend(r.Context(), r.PathValue("id"), req.Message, req.Images)
	if err != nil {
		if strings.HasPrefix(err.Error(), "invalid_request:") || err.Error() == "say something" {
			s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		} else {
			s.error(w, http.StatusConflict, "conflict", err.Error())
		}
		return
	}
	s.json(w, http.StatusOK, run)
}

// liftRequest names the one cap to remove.
type liftRequest struct {
	Kind string `json:"kind"`
}

func (s *Server) handleRunBudgetLift(w http.ResponseWriter, r *http.Request) {
	var req liftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	run, err := s.svc.RunBudgetLift(r.Context(), r.PathValue("id"), req.Kind)
	if err != nil {
		s.error(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	s.json(w, http.StatusOK, run)
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
			// Tags carry a v; semver comparison does not. Trim BOTH sides:
			// a stamped "v0.5.0" once locked every client out with
			// "0" != "v0" — a letter, not a version, deciding compatibility.
			clientMajor := strings.Split(strings.TrimPrefix(clientVersion, "v"), ".")[0]
			serverMajor := strings.Split(strings.TrimPrefix(s.version, "v"), ".")[0]
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
	running, waiting, limit := s.svc.QueueStats()
	s.json(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"version":    s.version,
		"provenance": s.provenance,
		// The queue's live counters. The one time they were needed — a run
		// stuck in "queued" with nothing visibly running — they were
		// invisible, and the diagnosis ran through disk archaeology.
		"queue": map[string]int{"running": running, "waiting": waiting, "limit": limit},
	})
}

func (s *Server) handleEngine(w http.ResponseWriter, r *http.Request) {
	s.json(w, http.StatusOK, map[string]interface{}{
		"version":    s.version,
		"provenance": s.provenance,
	})
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	s.json(w, http.StatusAccepted, map[string]string{"status": "shutting down"})
	if s.OnShutdown != nil {
		s.OnShutdown()
	}
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	var req restartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if strings.TrimSpace(req.Requester) == "" {
		s.error(w, http.StatusBadRequest, "invalid_request", "restart requester is required")
		return
	}
	if err := s.svc.RequestRestart(r.Context(), req.Requester); err != nil {
		s.error(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	s.json(w, http.StatusAccepted, map[string]string{"status": "restart requested"})
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

func (s *Server) handleBugAdd(w http.ResponseWriter, r *http.Request) {
	var req service.BugRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	b, err := s.svc.BugAdd(r.Context(), r.PathValue("id"), req)
	if err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, b)
}

func (s *Server) handleBugList(w http.ResponseWriter, r *http.Request) {
	bugs, err := s.svc.BugList(r.Context(), r.PathValue("id"), r.URL.Query().Get("open") == "true")
	if err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if r.URL.Query().Get("summary") == "true" {
		for i := range bugs {
			bugs[i].Body = ""
			bugs[i].History = nil
			bugs[i].Attachments = nil
		}
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": bugs, "total": len(bugs)})
}

func (s *Server) handleBugGet(w http.ResponseWriter, r *http.Request) {
	bugs, err := s.svc.BugList(r.Context(), r.PathValue("id"), false)
	if err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	for _, b := range bugs {
		if b.ID == r.PathValue("bug") {
			s.json(w, http.StatusOK, b)
			return
		}
	}
	s.error(w, http.StatusNotFound, "not_found", "bug not found")
}

func (s *Server) handleBugEdit(w http.ResponseWriter, r *http.Request) {
	var req service.BugRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	b, err := s.svc.BugEdit(r.Context(), r.PathValue("id"), r.PathValue("bug"), req)
	if err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, b)
}

func (s *Server) handleTaskRemove(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.TaskRemove(r.Context(), r.PathValue("id"), r.PathValue("task"))
	if err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, out)
}

func (s *Server) handleArtifactDiscard(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.ArtifactDiscard(r.Context(), r.PathValue("id"), r.PathValue("kind")); err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"discarded": r.PathValue("kind")})
}

func (s *Server) handleBugTriage(w http.ResponseWriter, r *http.Request) {
	// The body is optional: empty triages the whole inbox, {bug_id} one bug.
	var req struct {
		BugID string `json:"bug_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	run, err := s.svc.BugTriage(r.Context(), r.PathValue("id"), req.BugID)
	if err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, run)
}

func (s *Server) handleBugPromote(w http.ResponseWriter, r *http.Request) {
	// The actor is optional and self-declared; "human" is the desktop's
	// silence. Self-declaration is enough for an audit whose reader is the
	// project's own operator — the point is distinguishing the person's
	// clicks from the agents acting for them, not authentication.
	var req struct {
		Actor string `json:"actor"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}
	out, err := s.svc.BugPromote(r.Context(), r.PathValue("id"), r.PathValue("bug"), req.Actor)
	if err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, out)
}

func (s *Server) handleBugMove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Status string `json:"status"`
		Actor  string `json:"actor"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	b, err := s.svc.BugMove(r.Context(), r.PathValue("id"), r.PathValue("bug"), req.Status, req.Actor)
	if err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, b)
}

func (s *Server) handleReviewList(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.ReviewList(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": items, "total": len(items)})
}

func (s *Server) handleReviewGet(w http.ResponseWriter, r *http.Request) {
	md, err := s.svc.ReviewGet(r.Context(), r.PathValue("id"), r.PathValue("task"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"markdown": md})
}

func (s *Server) handleReleaseList(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.ReleaseList(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": items, "total": len(items)})
}

func (s *Server) handleReleaseGet(w http.ResponseWriter, r *http.Request) {
	md, err := s.svc.ReleaseGet(r.Context(), r.PathValue("id"), r.PathValue("version"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"markdown": md})
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

func (s *Server) handleAutopilotDefaults(w http.ResponseWriter, r *http.Request) {
	s.json(w, http.StatusOK, s.svc.AutopilotDefaults())
}

func (s *Server) handleAutopilotDefaultsSet(w http.ResponseWriter, r *http.Request) {
	var req service.AutopilotDefaultsView
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.svc.AutopilotDefaultsSet(req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, s.svc.AutopilotDefaults())
}

func (s *Server) handleProjectAutonomyGet(w http.ResponseWriter, r *http.Request) {
	a, err := s.svc.ProjectAutonomy(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"autonomy": a})
}

func (s *Server) handleProjectAutonomySet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Autonomy string `json:"autonomy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.svc.ProjectAutonomySet(r.PathValue("id"), req.Autonomy); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"autonomy": req.Autonomy})
}

func (s *Server) handleAutopilotGet(w http.ResponseWriter, r *http.Request) {
	s.json(w, http.StatusOK, s.svc.AutopilotStatus(r.PathValue("id")))
}

func (s *Server) handleAutopilotSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On       bool `json:"on"`
		MaxTasks int  `json:"max_tasks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	st, err := s.svc.AutopilotSet(r.Context(), r.PathValue("id"), req.On, req.MaxTasks)
	if err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, st)
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

// unknownRunListParam names the first query key the run list does not filter
// by, or "".
func unknownRunListParam(q url.Values) string {
	for k := range q {
		if k != "project" && k != "status" {
			return k
		}
	}
	return ""
}

func (s *Server) handleRunList(w http.ResponseWriter, r *http.Request) {
	// A mistyped filter must refuse, not silently widen. ?project_id= (the
	// wrong name) used to be ignored and the answer was every project's runs
	// — which a reader then took for one project's history. An answer to a
	// different question than the one asked is worse than an error.
	if bad := unknownRunListParam(r.URL.Query()); bad != "" {
		s.error(w, http.StatusBadRequest, "bad_request",
			fmt.Sprintf("unknown query parameter %q; this endpoint filters by project= and status=", bad))
		return
	}
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
		// Actor names the decider when it is not a person: an MCP operator
		// sends "mcp:<client>". Empty means human. The record must never say
		// a human decided what a model decided.
		Actor string `json:"actor"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	result, err := s.svc.RunAcceptAs(r.Context(), id, body.Message, body.Actor)
	if err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.json(w, http.StatusOK, result)
}

func (s *Server) handleRunReject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body rejectRequest
	json.NewDecoder(r.Body).Decode(&body)
	if body.Resolution == "landed" {
		if err := s.svc.RunLand(r.Context(), id, body.CommitSHA, "human", body.Reason); err != nil {
			s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if body.Resolution != "" {
		s.error(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("unknown resolution %q", body.Resolution))
		return
	}
	if err := s.svc.RunReject(r.Context(), id, body.Reason); err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRunLand(w http.ResponseWriter, r *http.Request) {
	var body landRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.svc.RunLand(r.Context(), r.PathValue("id"), body.CommitSHA, "human", body.Note); err != nil {
		s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
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

type renderedResponse struct {
	Rendered string `json:"rendered"`
}

func (s *Server) handleTraceReport(w http.ResponseWriter, r *http.Request) {
	rendered, err := s.svc.TraceReport(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, renderedResponse{Rendered: rendered})
}

func (s *Server) handleTraceCheck(w http.ResponseWriter, r *http.Request) {
	res, err := s.svc.TraceCheck(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, res)
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
	if r.URL.Query().Get("summary") == "true" {
		for i := range tasks {
			tasks[i].Body = ""
		}
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": tasks, "total": len(tasks)})
}

func (s *Server) handleProjectNext(w http.ResponseWriter, r *http.Request) {
	steps, err := s.svc.ProjectNext(r.Context(), r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.json(w, http.StatusOK, map[string]interface{}{"items": steps, "total": len(steps)})
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

func (s *Server) handleRunReseat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	run, err := s.svc.RunReseat(r.Context(), r.PathValue("id"), req.From, req.To)
	if err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.json(w, http.StatusOK, run)
}

// requestMode reads a roster write's mode from the query (?mode=, the CLI's
// habit) or the body ({"mode": …}, the desktop's). Reading only the query
// turned every pin made on a mode's board column into a mode-independent
// role pin — assign an implementer for Solo, and Pair, Split and Tournament
// changed with it.
func requestMode(r *http.Request, bodyMode string) string {
	if q := r.URL.Query().Get("mode"); q != "" {
		return q
	}
	return bodyMode
}
