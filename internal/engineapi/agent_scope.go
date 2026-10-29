package engineapi

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jrullan/ducklab/internal/agenttoken"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/service"
)

// B-550. A per-agent token reaches only the projects the person granted it,
// at the level granted. Where a route stands is derived from the route table
// here, once, so a new route is covered the day it is added — and a new route
// outside any project is refused to a per-agent token until someone decides,
// in agentGlobalReads, that every agent may read it.
//
// The shared agent token and the person's are not narrowed here: what they
// may do is what B-530 already decided.

type scopeKind int

const (
	// scopeOutside is a route outside any project that only the person or
	// the shared agent token reaches: engine control, defaults, providers,
	// ducklings, creating projects.
	scopeOutside scopeKind = iota
	// scopeGlobalRead is a route outside any project that every agent may
	// read: what the engine is and which ducklings and providers exist.
	scopeGlobalRead
	// scopeProject is /v1/projects/{id}/...: the grant for {id} decides.
	scopeProject
	// scopeRun is /v1/runs/{id}/...: the grant for the run's project decides.
	scopeRun
	// scopeNarrowed is a listing the handler itself narrows to the granted
	// projects: project and run lists, the event stream.
	scopeNarrowed
)

type agentRule struct {
	kind scopeKind
	need agenttoken.Level
}

// agentGlobalReads are the routes outside any project a per-agent token may
// read. Every one is configuration an agent needs to choose ducklings or
// describe the engine; none names a project's work.
var agentGlobalReads = map[string]bool{
	"GET /v1/engine":                         true,
	"GET /v1/project-presets":                true,
	"GET /v1/ducklings":                      true,
	"GET /v1/ducklings/scorecards":           true,
	"GET /v1/providers":                      true,
	"GET /v1/providers/{id}/models":          true,
	"GET /v1/providers/{id}/model-endpoints": true,
	"GET /v1/providers/{id}/login":           true,
	"GET /v1/bench":                          true,
	"GET /v1/bench/{suite}/{stamp}":          true,
	"GET /v1/agent-tokens/self":              true,
}

// agentNarrowed are the listings whose handlers narrow the answer.
var agentNarrowed = map[string]bool{
	"GET /v1/projects": true,
	"GET /v1/runs":     true,
	"GET /v1/events":   true,
}

// agentReadPosts are POSTs that only compute an answer, so read-only access
// covers them.
var agentReadPosts = map[string]bool{
	"POST /v1/projects/{id}/artifacts/{kind}/lint": true,
}

func agentRuleFor(rt Route) agentRule {
	key := rt.Method + " " + rt.Path
	need := agenttoken.LevelFull
	if rt.Method == http.MethodGet || agentReadPosts[key] {
		need = agenttoken.LevelRead
	}
	switch {
	case agentNarrowed[key]:
		return agentRule{kind: scopeNarrowed, need: agenttoken.LevelRead}
	case agentGlobalReads[key],
		rt.Method == http.MethodGet && strings.HasPrefix(rt.Path, "/v1/defaults/"):
		return agentRule{kind: scopeGlobalRead, need: agenttoken.LevelRead}
	case strings.HasPrefix(rt.Path, "/v1/projects/{id}"):
		return agentRule{kind: scopeProject, need: need}
	case strings.HasPrefix(rt.Path, "/v1/runs/{id}"):
		return agentRule{kind: scopeRun, need: need}
	}
	return agentRule{kind: scopeOutside, need: need}
}

// agentScope wraps a route's handler with its rule. It runs after auth, so a
// per-agent token's identity is already on the request.
func (s *Server) agentScope(rt Route, next http.HandlerFunc) http.HandlerFunc {
	rule := agentRuleFor(rt)
	return func(w http.ResponseWriter, r *http.Request) {
		who := requestIdentity(r)
		if who == nil {
			next(w, r)
			return
		}
		switch rule.kind {
		case scopeGlobalRead, scopeNarrowed:
		case scopeProject:
			if !s.agentMay(w, who, r.PathValue("id"), rule.need) {
				return
			}
		case scopeRun:
			runID := r.PathValue("id")
			project := s.projectIDFn(runID)
			if project == "" {
				s.error(w, http.StatusNotFound, "not_found", fmt.Sprintf("run %s not found", runID))
				return
			}
			if !s.agentMay(w, who, project, rule.need) {
				return
			}
		default:
			s.error(w, http.StatusForbidden, "agent_scope", fmt.Sprintf(
				"agent token %q reaches only the projects it is granted, and %s %s is outside any project; "+
					"it is the person's to do from the desktop", who.Name, rt.Method, rt.Path))
			return
		}
		next(w, r)
	}
}

// unscopedOnly keeps a per-agent token out of a debug surface that sits
// outside the route table.
func (s *Server) unscopedOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if who := requestIdentity(r); who != nil {
			s.error(w, http.StatusForbidden, "agent_scope",
				fmt.Sprintf("agent token %q cannot reach the engine's debug surface", who.Name))
			return
		}
		next(w, r)
	}
}

// agentMay answers whether who may act at need in project, refusing with the
// way out when not.
func (s *Server) agentMay(w http.ResponseWriter, who *agenttoken.Identity, project string, need agenttoken.Level) bool {
	have, ok := who.Level(project)
	if !ok {
		s.error(w, http.StatusForbidden, "agent_scope", fmt.Sprintf(
			"agent token %q has no access to project %q; the person grants it in Settings → Agent access "+
				"or with `ducklab token grant %s %s read-only|full`", who.Name, project, who.ID, project))
		return false
	}
	if !have.Allows(need) {
		s.error(w, http.StatusForbidden, "agent_scope", fmt.Sprintf(
			"agent token %q has read-only access to project %q and this request changes it; the person can "+
				"raise it to full in Settings → Agent access or with `ducklab token grant %s %s full`",
			who.Name, project, who.ID, project))
		return false
	}
	return true
}

// visibleProjects narrows a project list to the request's grants.
func visibleProjects(r *http.Request, projects []*service.Project) []*service.Project {
	who := requestIdentity(r)
	if who == nil {
		return projects
	}
	out := make([]*service.Project, 0, len(projects))
	for _, p := range projects {
		if _, ok := who.Level(p.ID); ok {
			out = append(out, p)
		}
	}
	return out
}

// visibleRuns narrows a run list to the request's grants.
func visibleRuns(r *http.Request, runs []*runlog.Run) []*runlog.Run {
	who := requestIdentity(r)
	if who == nil {
		return runs
	}
	out := make([]*runlog.Run, 0, len(runs))
	for _, run := range runs {
		if _, ok := who.Level(run.ProjectID); ok {
			out = append(out, run)
		}
	}
	return out
}
