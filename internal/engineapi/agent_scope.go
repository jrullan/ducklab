package engineapi

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
	// scopeRules is a project route that changes the project's rules rather
	// than doing its work. Not even a full grant reaches it.
	scopeRules
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

// agentProjectRules are the routes that change what a project lets work do:
// its config (which names the commands the engine runs — the render
// command, the gate, the install chain), its autonomy, its gate and visual
// gate, its skills, and whether it is registered at all. Review of B-550: a
// full grant that could set render.command could run anything as the person
// and read human.token — every grant would be host access. A full grant does
// the work; the person sets the rules.
var agentProjectRules = map[string]bool{
	"PATCH /v1/projects/{id}":                 true,
	"DELETE /v1/projects/{id}":                true,
	"PUT /v1/projects/{id}/autonomy":          true,
	"POST /v1/projects/{id}/gate":             true,
	"PUT /v1/projects/{id}/visual-check":      true,
	"POST /v1/projects/{id}/skills":           true,
	"PUT /v1/projects/{id}/skills/{name}":     true,
	"DELETE /v1/projects/{id}/skills/{name}":  true,
	"POST /v1/projects/{id}/recover/{action}": true,
	// Turning autopilot on or off is autonomy by another door; the roster
	// chooses which models — local or hosted — see the project; a task's
	// owns is its lane, and widening a lane is the person's (B-530).
	"POST /v1/projects/{id}/autopilot":   true,
	"PUT /v1/projects/{id}/roster":       true,
	"DELETE /v1/projects/{id}/roster":    true,
	"PUT /v1/projects/{id}/tasks/{task}": true,
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
	case agentProjectRules[key]:
		return agentRule{kind: scopeRules, need: agenttoken.LevelFull}
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
		case scopeRules:
			s.error(w, http.StatusForbidden, "agent_scope", fmt.Sprintf(
				"agent token %q may work in a project but not change its rules: %s %s is the person's "+
					"(the desktop, or ask with an approval request)", who.Name, rt.Method, rt.Path))
			return
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

// agentPathError is a per-agent token naming a file outside its project.
type agentPathError struct{ msg string }

func (e *agentPathError) Error() string { return e.msg }

// agentPathGuard vets file paths for a per-agent token: each must resolve,
// through symlinks, inside the project — and so must every file in a named
// directory, since a reference directory is walked and its links followed.
// Review of B-550: a stage's from and refs, and a reference import, name
// files the engine reads with the person's rights; {"from":
// "~/.local/state/ducklab/human.token"} read the person's credential into a
// brief the agent could fetch. Relative paths resolve against the project,
// as the service resolves references; callers pass a path the service reads
// some other way (the brief, an import) already absolute. Nil for the person
// and the shared token.
func (s *Server) agentPathGuard(r *http.Request, projectID string) func([]string) error {
	who := requestIdentity(r)
	if who == nil {
		return nil
	}
	return func(paths []string) error {
		if len(paths) == 0 {
			return nil
		}
		project, err := s.svc.ProjectGet(r.Context(), projectID)
		if err != nil {
			return err
		}
		root := resolvedPath(project.Path)
		inside := func(p string) bool {
			rel, err := filepath.Rel(root, resolvedPath(p))
			return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
		}
		refuse := func(p string) error {
			return &agentPathError{fmt.Sprintf(
				"agent token %q may name files only inside project %q; %s is outside it. "+
					"A reference from elsewhere is the person's to give", who.Name, projectID, p)}
		}
		for _, p := range paths {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if p == "~" || strings.HasPrefix(p, "~/") {
				if home, err := os.UserHomeDir(); err == nil {
					p = filepath.Join(home, strings.TrimPrefix(p, "~"))
				}
			} else if !filepath.IsAbs(p) {
				p = filepath.Join(project.Path, p)
			}
			if !inside(p) {
				return refuse(p)
			}
			if info, err := os.Stat(p); err == nil && info.IsDir() {
				var bad string
				filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
					if err == nil && path != p && !inside(path) {
						bad = path
						return filepath.SkipAll
					}
					return nil
				})
				if bad != "" {
					return refuse(bad)
				}
			}
		}
		return nil
	}
}

// agentPathsInside is agentPathGuard answering the request itself.
func (s *Server) agentPathsInside(w http.ResponseWriter, r *http.Request, projectID string, paths []string) bool {
	guard := s.agentPathGuard(r, projectID)
	if guard == nil {
		return true
	}
	if err := guard(paths); err != nil {
		s.agentPathRefusal(w, err)
		return false
	}
	return true
}

// agentPathRefusal answers a guard's error: a scope refusal as one, anything
// else as the lookup failure it is.
func (s *Server) agentPathRefusal(w http.ResponseWriter, err error) {
	var scope *agentPathError
	if errors.As(err, &scope) {
		s.error(w, http.StatusForbidden, "agent_scope", scope.msg)
		return
	}
	s.error(w, http.StatusNotFound, "not_found", err.Error())
}

// resolvedPath is p made absolute with every symlink followed, as far as the
// path exists; the missing tail is kept as written, so a link cannot lead
// out through a parent and a not-yet-existing file still resolves.
func resolvedPath(p string) string {
	p, _ = filepath.Abs(p)
	p = filepath.Clean(p)
	tail := ""
	for cur := p; ; {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, tail)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		tail = filepath.Join(filepath.Base(cur), tail)
		cur = parent
	}
}
