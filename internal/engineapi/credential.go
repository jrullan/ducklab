package engineapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// B-530. An OpenCode session operating through MCP was refused a lane
// widening ("requires direct human approval; actor mcp:opencode"). It then
// read the bearer token from engine.json, POSTed the same accept with an
// empty actor, and the engine recorded the decision as "human" and committed
// it. The person had agreed in chat, so the intent was approved — but the
// record said a person decided directly, and the human-only rule protected
// nothing: the actor was self-declared and one token served every client.
//
// The engine now holds two credentials and derives the actor from the one a
// request presents. The body may still NAME an agent ("mcp:<client>") so the
// record says which one; it can narrow, never elevate.

// credential is which of the engine's two tokens authenticated a request.
type credential string

const (
	// credHuman is the person's channel: the desktop and an interactive CLI.
	credHuman credential = "human"
	// credAgent is every agent channel: the MCP server, the renderer, a CLI
	// an agent runs — anything that found the token in engine.json.
	credAgent credential = "agent"
)

type credentialKey struct{}

// SetAgentToken installs the agents' credential. Without one, only the
// person's token authenticates — the shape every test server had before the
// split, and still correct for them.
func (s *Server) SetAgentToken(token string) { s.agentToken = token }

// credentialFor names which credential a bearer token is. Both comparisons
// always run, over fixed-size digests, so neither the match nor its length
// shows in the timing.
func (s *Server) credentialFor(token string) (credential, bool) {
	got := sha256.Sum256([]byte(token))
	human := sha256.Sum256([]byte(s.token))
	agent := sha256.Sum256([]byte(s.agentToken))
	isHuman := s.token != "" && subtle.ConstantTimeCompare(got[:], human[:]) == 1
	isAgent := s.agentToken != "" && subtle.ConstantTimeCompare(got[:], agent[:]) == 1
	switch {
	case isHuman:
		return credHuman, true
	case isAgent:
		return credAgent, true
	}
	return "", false
}

// requestCredential is the credential that authenticated r. A request that
// reached a handler without passing auth is an agent's: fail closed.
func requestCredential(r *http.Request) credential {
	if c, ok := r.Context().Value(credentialKey{}).(credential); ok {
		return c
	}
	return credAgent
}

// agentActorPrefixes are the names an agent may give itself. Anything else an
// agent credential declares — "human", "", "desktop", "engine", "auto:yolo" —
// is a claim to be someone it is not and is replaced by plain "agent".
var agentActorPrefixes = []string{"mcp:", "agent:"}

// actorFor is the actor the engine records for a request whose body declared
// `declared`. Every handler that records who acted goes through it; none
// reads an actor from the body directly.
//
//   - The person's credential: an empty declaration is "human"; a declared
//     name is kept (a person's client may only narrow, e.g. to say an agent
//     it runs acted).
//   - An agent's credential: a declared "mcp:<client>" or "agent:<name>" is
//     kept so the record says which agent; anything else becomes "agent".
//     It never yields "human".
func actorFor(cred credential, declared string) string {
	d := strings.TrimSpace(declared)
	if cred == credHuman {
		if d == "" {
			return "human"
		}
		return d
	}
	for _, prefix := range agentActorPrefixes {
		if strings.HasPrefix(d, prefix) && len(d) > len(prefix) {
			return d
		}
	}
	return "agent"
}

// actor is actorFor applied to the request's own credential.
func (s *Server) actor(r *http.Request, declared string) string {
	return actorFor(requestCredential(r), declared)
}

// requireHuman refuses a request that does not carry the person's
// credential. It answers for the doors that exist only for the person —
// approving or declining an operator's approval request — and says what an
// agent can do instead.
func (s *Server) requireHuman(w http.ResponseWriter, r *http.Request, what string) bool {
	if requestCredential(r) == credHuman {
		return true
	}
	s.error(w, http.StatusForbidden, "human_credential_required",
		what+" is the person's decision and needs the person's credential (the desktop's); "+
			"this request carries an agent credential. An operator files an approval request "+
			"(MCP approval_request) and the person confirms or declines it in the desktop")
	return false
}
