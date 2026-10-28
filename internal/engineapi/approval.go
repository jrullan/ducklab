package engineapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/jrullan/ducklab/internal/service"
)

// declineRequest is the optional body of a decline: the person's words.
type declineRequest struct {
	Reason string `json:"reason,omitempty"`
}

// handleApprovalRequestFile is an agent's door to a person's decision
// (B-530). Any credential may file; the actor is the credential's, so the
// record names the agent that asked.
func (s *Server) handleApprovalRequestFile(w http.ResponseWriter, r *http.Request) {
	var in service.ApprovalRequestInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	req, err := s.svc.ApprovalRequestFile(r.Context(), r.PathValue("id"), in, s.actor(r, in.Actor))
	if err != nil {
		s.error(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	s.json(w, http.StatusCreated, req)
}

// handleApprovalRequestApprove is the person confirming a request. Only the
// person's credential reaches the decision: an agent approving its own
// request would be B-530 again with an extra step.
func (s *Server) handleApprovalRequestApprove(w http.ResponseWriter, r *http.Request) {
	if !s.requireHuman(w, r, "approving a request") {
		return
	}
	// An approved accept is an accept (B-529): it runs on the run's own
	// context, and this request waits for it only as long as the accept
	// door does. Past that the request answers "approved · accept in
	// progress" and settles when the accept does.
	wait, cancel := context.WithTimeout(r.Context(), s.acceptWait)
	defer cancel()
	out, err := s.svc.ApprovalRequestApprove(wait, r.PathValue("id"), r.PathValue("req"), s.actor(r, ""))
	if err != nil {
		if out != nil {
			// Approved and recorded, but the decision itself was refused:
			// the request's outcome says why, and so does the error.
			s.error(w, http.StatusConflict, "decision_refused", err.Error())
			return
		}
		s.error(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	s.json(w, http.StatusOK, out)
}

// handleApprovalRequestDecline is the person saying no. Nothing is decided.
func (s *Server) handleApprovalRequestDecline(w http.ResponseWriter, r *http.Request) {
	if !s.requireHuman(w, r, "declining a request") {
		return
	}
	var body declineRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			s.error(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	req, err := s.svc.ApprovalRequestDecline(r.Context(), r.PathValue("id"), r.PathValue("req"), body.Reason, s.actor(r, ""))
	if err != nil {
		s.error(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	s.json(w, http.StatusOK, req)
}
