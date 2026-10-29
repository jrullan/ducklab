package engineapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/service"
)

const (
	humanTok = "human-credential-0123456789"
	agentTok = "agent-credential-9876543210"
)

// B-530: the actor is the credential's, not the body's. A body may name the
// agent; it can narrow, never elevate.
func TestActorComesFromTheCredential(t *testing.T) {
	cases := []struct {
		cred     credential
		declared string
		want     string
	}{
		{credHuman, "", "human"},
		{credHuman, "human", "human"},
		{credHuman, "mcp:claude", "mcp:claude"},
		{credAgent, "", "agent"},
		{credAgent, "human", "agent"},
		{credAgent, " human ", "agent"},
		{credAgent, "desktop", "agent"},
		{credAgent, "engine", "agent"},
		{credAgent, "auto:yolo", "agent"},
		{credAgent, "mcp:", "agent"},
		{credAgent, "mcp:opencode", "mcp:opencode"},
		{credAgent, "agent:ci", "agent:ci"},
	}
	for _, c := range cases {
		if got := actorFor(c.cred, c.declared); got != c.want {
			t.Errorf("actorFor(%s, %q) = %q, want %q", c.cred, c.declared, got, c.want)
		}
	}
}

// laneWideningServer is B-530's run: paused at its gate with a lane widening
// on offer, behind an engine holding both credentials.
func laneWideningServer(t *testing.T) (*service.Service, *httptest.Server, string) {
	t.Helper()
	s, projectID, project := landedResolutionService(t)
	w, err := runlog.NewWriter(project, &runlog.Run{
		ID: "r-lane", ProjectID: projectID, TaskID: "T-004", Stage: "test", Mode: "solo",
		Status: "paused", Verdict: "PASSED", PendingKind: "gate",
		PendingSince: time.Now().UTC().Format(time.RFC3339), StartedAt: time.Now().UTC().Format(time.RFC3339),
		PendingData: map[string]interface{}{"lane_widening": []string{"tests/parser.test.mjs"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	api := New(s, bus.New(16), humanTok, "test", "")
	api.SetAgentToken(agentTok)
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	return s, server, "r-lane"
}

func call(t *testing.T, server *httptest.Server, token, method, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, server.URL+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func runEvents(t *testing.T, s *service.Service, runID, typ string) []*runlog.Event {
	t.Helper()
	detail, err := s.RunGet(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	var out []*runlog.Event
	for _, e := range detail.Events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// The incident, replayed: the agent credential with an empty actor, then
// with "human", POSTs the accept with lane_widening. Both are refused, and
// the refusals are on the record as the agent's.
func TestTheAgentCredentialCannotWidenALaneAsHuman(t *testing.T) {
	s, server, runID := laneWideningServer(t)
	for i, body := range []string{
		`{"lane_widening":["tests/parser.test.mjs"]}`,
		`{"lane_widening":["tests/parser.test.mjs"],"actor":"human"}`,
		`{"lane_widening":["tests/parser.test.mjs"],"actor":""}`,
	} {
		status, out := call(t, server, agentTok, "POST", "/v1/runs/"+runID+"/accept", body)
		if status == http.StatusOK || !strings.Contains(out, "direct human approval") || !strings.Contains(out, `actor \"agent\"`) {
			t.Fatalf("body %s: agent accept = %d %s; want the human-only refusal naming actor agent", body, status, out)
		}
		refused := runEvents(t, s, runID, "accept_refused")
		if len(refused) != i+1 {
			t.Fatalf("accept_refused events = %d, want %d", len(refused), i+1)
		}
		if got := refused[i].Data["actor"]; got != "agent" {
			t.Fatalf("refusal recorded with actor %v, want the credential's (agent)", got)
		}
	}
	// The MCP's own name survives: narrowing within the agents is allowed.
	_, _ = call(t, server, agentTok, "POST", "/v1/runs/"+runID+"/accept", `{"lane_widening":["tests/parser.test.mjs"],"actor":"mcp:opencode"}`)
	refused := runEvents(t, s, runID, "accept_refused")
	if got := refused[len(refused)-1].Data["actor"]; got != "mcp:opencode" {
		t.Fatalf("named agent recorded as %v, want mcp:opencode", got)
	}
	if detail, _ := s.RunGet(context.Background(), runID); detail.Run.Accepted || len(runEvents(t, s, runID, "lane_widened")) != 0 {
		t.Fatal("the agent credential widened the lane")
	}
}

// The person's credential still passes the human-only rule: whatever stops
// it afterwards, it is not "requires direct human approval".
func TestTheHumanCredentialStillDecidesAsThePerson(t *testing.T) {
	s, server, runID := laneWideningServer(t)
	_, out := call(t, server, humanTok, "POST", "/v1/runs/"+runID+"/accept", `{"lane_widening":["tests/parser.test.mjs"]}`)
	if strings.Contains(out, "direct human approval") {
		t.Fatalf("the person's credential was refused as non-human: %s", out)
	}
	refused := runEvents(t, s, runID, "accept_refused")
	if len(refused) == 0 || refused[len(refused)-1].Data["actor"] != "human" {
		t.Fatalf("the person's attempt was not recorded as human: %+v", refused)
	}
	if status, _ := call(t, server, "not-a-token", "GET", "/v1/runs/"+runID, ""); status != http.StatusUnauthorized {
		t.Fatalf("unknown credential = %d, want 401", status)
	}
}

// The approval door over HTTP: the agent files, an agent cannot approve (403
// with the way through), the person can, and both actors are recorded.
func TestApprovalRequestsNeedThePersonsCredentialToDecide(t *testing.T) {
	s, server, runID := laneWideningServer(t)
	status, out := call(t, server, agentTok, "POST", "/v1/runs/"+runID+"/approval-requests",
		`{"action":"accept","reason":"Jose said Ok in chat","actor":"human"}`)
	if status != http.StatusCreated {
		t.Fatalf("file = %d %s", status, out)
	}
	var req runlog.ApprovalRequest
	if err := json.Unmarshal([]byte(out), &req); err != nil {
		t.Fatal(err)
	}
	if req.RequestedBy != "agent" || req.Status != "pending" {
		t.Fatalf("filed = %+v; want pending, requested by the credential's actor even though the body said human", req)
	}
	for _, verb := range []string{"approve", "decline"} {
		status, out = call(t, server, agentTok, "POST", "/v1/runs/"+runID+"/approval-requests/"+req.ID+"/"+verb, `{}`)
		if status != http.StatusForbidden || !strings.Contains(out, "approval_request") {
			t.Fatalf("agent %s = %d %s; want 403 naming the door", verb, status, out)
		}
	}
	// The person approves. The fixture's accept cannot complete (there is no
	// plan to amend), so the approval is recorded and the refusal named.
	status, out = call(t, server, humanTok, "POST", "/v1/runs/"+runID+"/approval-requests/"+req.ID+"/approve", `{}`)
	if status == http.StatusForbidden || strings.Contains(out, "direct human approval") {
		t.Fatalf("person approve = %d %s", status, out)
	}
	granted := runEvents(t, s, runID, "approval_granted")
	if len(granted) != 1 || granted[0].Data["requested_by"] != "agent" || granted[0].Data["approved_by"] != "human" {
		t.Fatalf("approval_granted = %+v; want both actors", granted)
	}
}

// No response carries a credential: an agent that reads every answer the
// engine gives learns neither token.
func TestNoResponseEchoesACredential(t *testing.T) {
	_, server, runID := laneWideningServer(t)
	for _, c := range []struct{ token, method, path, body string }{
		{agentTok, "GET", "/v1/engine", ""},
		{agentTok, "GET", "/v1/health", ""},
		{agentTok, "GET", "/v1/runs/" + runID, ""},
		{agentTok, "GET", "/v1/runs", ""},
		{agentTok, "POST", "/v1/runs/" + runID + "/accept", `{"lane_widening":["tests/parser.test.mjs"]}`},
		{agentTok, "POST", "/v1/runs/" + runID + "/approval-requests", `{"action":"accept","reason":"r"}`},
		{agentTok, "POST", "/v1/runs/" + runID + "/approval-requests/ar-1/approve", `{}`},
		{humanTok, "GET", "/v1/runs/" + runID, ""},
	} {
		_, out := call(t, server, c.token, c.method, c.path, c.body)
		if strings.Contains(out, humanTok) || strings.Contains(out, agentTok) {
			t.Fatalf("%s %s echoed a credential: %s", c.method, c.path, out)
		}
	}
}

// Every handler that records an actor derives it from the credential. A
// handler reading `.Actor` straight from a decoded body, or writing a literal
// "human", is how B-530 happened; this keeps the next route from doing it.
func TestNoHandlerTrustsABodyActor(t *testing.T) {
	src, err := os.ReadFile("engineapi.go")
	if err != nil {
		t.Fatal(err)
	}
	allowed := regexp.MustCompile(`s\.actor\(r, (body|req|in)\.(Actor|ApprovedBy)\)`)
	uses := regexp.MustCompile(`\b(body|req|in)\.(Actor|ApprovedBy)\b`)
	for i, line := range strings.Split(string(src), "\n") {
		code := strings.TrimSpace(line)
		if strings.HasPrefix(code, "//") {
			continue
		}
		rest := allowed.ReplaceAllString(code, "")
		if uses.MatchString(rest) && !strings.HasPrefix(code, "req.Actor = s.actor(") {
			t.Errorf("engineapi.go:%d reads a body actor without deriving it from the credential: %s", i+1, code)
		}
		if strings.Contains(code, `"human"`) {
			t.Errorf("engineapi.go:%d names \"human\" itself; only the credential may: %s", i+1, code)
		}
	}
}

// B-530 audit: the MCP sent its name with a budget lift and the engine
// dropped it, recording "by: human" for an operator's lift. B-541 went
// further: a cap is the person's limit on what an agent may spend, so an
// agent credential is refused outright — whatever actor it names — and only
// the person's lift lands, recorded as theirs.
func TestOnlyThePersonsCredentialLiftsABudget(t *testing.T) {
	s, server, runID := laneWideningServer(t)
	for _, body := range []string{`{"kind":"calls","actor":"human"}`, `{"kind":"calls","actor":"mcp:opencode"}`, `{"kind":"calls"}`} {
		status, out := call(t, server, agentTok, "POST", "/v1/runs/"+runID+"/budget/lift", body)
		if status != http.StatusConflict || !strings.Contains(out, "approval_request") {
			t.Fatalf("agent lift %s = %d %s, want a refusal naming the approval request", body, status, out)
		}
	}
	if lifted := runEvents(t, s, runID, "budget_lifted"); len(lifted) != 0 {
		t.Fatalf("a refused lift was recorded: %+v", lifted[0].Data)
	}
	if status, out := call(t, server, humanTok, "POST", "/v1/runs/"+runID+"/budget/lift", `{"kind":"calls"}`); status != http.StatusOK {
		t.Fatalf("the person's lift = %d %s", status, out)
	}
	lifted := runEvents(t, s, runID, "budget_lifted")
	if len(lifted) != 1 || lifted[0].Data["by"] != "human" {
		t.Fatalf("the person's lift recorded as %+v", lifted)
	}
}

// Review of B-541: refused the lift, an agent could abort and launch again
// with a budget of its choosing, or agent_turns -1, or raise the defaults
// every later run starts from. Those are the person's too.
func TestAnAgentCannotSetItsOwnCaps(t *testing.T) {
	_, server, _ := laneWideningServer(t)
	for _, c := range []struct{ method, path, body string }{
		{"PUT", "/v1/defaults/budget", `{"max_usd":1000}`},
		{"POST", "/v1/projects/p/runs", `{"task_id":"T-001","budget":{"max_usd":1000}}`},
		{"POST", "/v1/projects/p/runs", `{"task_id":"T-001","agent_turns":-1}`},
		{"POST", "/v1/projects/p/tests", `{"task_id":"T-001","agent_turns":-1}`},
		{"POST", "/v1/projects/p/stages/spec", `{"agent_turns":-1}`},
	} {
		status, out := call(t, server, agentTok, c.method, c.path, c.body)
		if status != http.StatusForbidden || !strings.Contains(out, "human_credential_required") {
			t.Errorf("agent %s %s %s = %d %s, want refused", c.method, c.path, c.body, status, out)
		}
	}
}

// Signing a subscription in or out decides whose plan the ducklings spend:
// the person's call. Refused before the provider is even looked up.
func TestAnAgentCannotSignAProviderInOrOut(t *testing.T) {
	_, server, _ := laneWideningServer(t)
	for _, c := range []struct{ method, path string }{
		{"POST", "/v1/providers/chatgpt/login"},
		{"DELETE", "/v1/providers/chatgpt/login"},
	} {
		status, out := call(t, server, agentTok, c.method, c.path, `{}`)
		if status != http.StatusForbidden || !strings.Contains(out, "human_credential_required") {
			t.Errorf("agent %s %s = %d %s, want refused", c.method, c.path, status, out)
		}
	}
}
