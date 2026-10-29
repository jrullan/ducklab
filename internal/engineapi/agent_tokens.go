package engineapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jrullan/ducklab/internal/agenttoken"
)

// The person's doors for per-agent tokens (B-550). Every one that issues,
// changes or ends a token needs the person's credential: an agent that could
// widen its own grants would make them decoration.

type agentTokenList struct {
	Items []agenttoken.View `json:"items"`
	Total int               `json:"total"`
	// LegacyEnabled says whether the shared engine.json token still
	// authenticates every agent at full access everywhere.
	LegacyEnabled bool `json:"legacy_enabled"`
}

type agentTokenCreateRequest struct {
	Name   string             `json:"name"`
	Grants []agenttoken.Grant `json:"grants"`
}

type agentTokenCreated struct {
	Token agenttoken.View `json:"token"`
	// Secret is shown this once; the engine keeps only its hash.
	Secret string `json:"secret"`
}

type agentTokenUpdateRequest struct {
	Name *string `json:"name,omitempty"`
	// Grants, when present, replaces every grant; [] removes them all.
	Grants *[]agenttoken.Grant `json:"grants,omitempty"`
}

type agentTokenLegacyRequest struct {
	Enabled bool `json:"enabled"`
}

type agentTokenLegacyResponse struct {
	LegacyEnabled bool `json:"legacy_enabled"`
}

// agentTokenSelf is what a request's own credential is, so an agent can say
// what it may reach instead of discovering it one refusal at a time.
type agentTokenSelf struct {
	// Kind is person, shared (the engine.json token) or token.
	Kind   string             `json:"kind"`
	ID     string             `json:"id,omitempty"`
	Name   string             `json:"name,omitempty"`
	Actor  string             `json:"actor"`
	Grants []agenttoken.Grant `json:"grants,omitempty"`
}

func (s *Server) tokenStore(w http.ResponseWriter) (*agenttoken.Store, bool) {
	if s.agentTokens == nil {
		s.error(w, http.StatusServiceUnavailable, "unavailable", "this engine does not manage per-agent tokens")
		return nil, false
	}
	return s.agentTokens, true
}

// checkGrantProjects refuses a grant to a project the engine does not know:
// a typo would otherwise be a grant that silently never applies.
func (s *Server) checkGrantProjects(r *http.Request, grants []agenttoken.Grant) error {
	if len(grants) == 0 {
		return nil
	}
	projects, err := s.svc.ProjectList(r.Context())
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, p := range projects {
		known[p.ID] = true
	}
	for _, g := range grants {
		if !known[g.Project] {
			return fmt.Errorf("no project %q; `ducklab project list` shows the ids", g.Project)
		}
	}
	return nil
}

func (s *Server) tokenError(w http.ResponseWriter, err error) {
	if errors.Is(err, agenttoken.ErrNotFound) {
		s.error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.error(w, http.StatusBadRequest, "invalid_request", err.Error())
}

func (s *Server) handleAgentTokenList(w http.ResponseWriter, r *http.Request) {
	if !s.requireHuman(w, r, "listing agent tokens") {
		return
	}
	store, ok := s.tokenStore(w)
	if !ok {
		return
	}
	items := store.List()
	s.json(w, http.StatusOK, agentTokenList{Items: items, Total: len(items), LegacyEnabled: store.LegacyEnabled()})
}

func (s *Server) handleAgentTokenCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireHuman(w, r, "issuing an agent token") {
		return
	}
	var req agentTokenCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	store, ok := s.tokenStore(w)
	if !ok {
		return
	}
	if err := s.checkGrantProjects(r, req.Grants); err != nil {
		s.tokenError(w, err)
		return
	}
	view, secret, err := store.Create(req.Name, req.Grants)
	if err != nil {
		s.tokenError(w, err)
		return
	}
	s.json(w, http.StatusCreated, agentTokenCreated{Token: view, Secret: secret})
}

func (s *Server) handleAgentTokenUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireHuman(w, r, "changing an agent token's access") {
		return
	}
	var req agentTokenUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	store, ok := s.tokenStore(w)
	if !ok {
		return
	}
	var grants []agenttoken.Grant
	if req.Grants != nil {
		grants = *req.Grants
		if grants == nil {
			grants = []agenttoken.Grant{}
		}
		if err := s.checkGrantProjects(r, grants); err != nil {
			s.tokenError(w, err)
			return
		}
	}
	view, err := store.Update(r.PathValue("tok"), req.Name, grants)
	if err != nil {
		s.tokenError(w, err)
		return
	}
	s.json(w, http.StatusOK, view)
}

func (s *Server) handleAgentTokenRevoke(w http.ResponseWriter, r *http.Request) {
	if !s.requireHuman(w, r, "revoking an agent token") {
		return
	}
	store, ok := s.tokenStore(w)
	if !ok {
		return
	}
	view, err := store.Revoke(r.PathValue("tok"))
	if err != nil {
		s.tokenError(w, err)
		return
	}
	s.json(w, http.StatusOK, view)
}

func (s *Server) handleAgentTokenLegacy(w http.ResponseWriter, r *http.Request) {
	if !s.requireHuman(w, r, "turning the shared agent token on or off") {
		return
	}
	var req agentTokenLegacyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	store, ok := s.tokenStore(w)
	if !ok {
		return
	}
	if err := store.SetLegacyEnabled(req.Enabled); err != nil {
		s.error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.json(w, http.StatusOK, agentTokenLegacyResponse{LegacyEnabled: req.Enabled})
}

func (s *Server) handleAgentTokenSelf(w http.ResponseWriter, r *http.Request) {
	if who := requestIdentity(r); who != nil {
		out := agentTokenSelf{Kind: "token", ID: who.ID, Name: who.Name, Actor: who.Actor}
		for _, p := range who.Projects() {
			l, _ := who.Level(p)
			out.Grants = append(out.Grants, agenttoken.Grant{Project: p, Level: l})
		}
		s.json(w, http.StatusOK, out)
		return
	}
	if requestCredential(r) == credHuman {
		s.json(w, http.StatusOK, agentTokenSelf{Kind: "person", Actor: "human"})
		return
	}
	s.json(w, http.StatusOK, agentTokenSelf{Kind: "shared", Actor: "agent"})
}
