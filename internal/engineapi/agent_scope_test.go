package engineapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/agenttoken"
	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/runlog"
)

// scopedServer is an engine with a token store and one project ("landed",
// with run r-lane).
func scopedServer(t *testing.T) (*httptest.Server, *agenttoken.Store, string) {
	t.Helper()
	s, projectID, project := landedResolutionService(t)
	w, err := runlog.NewWriter(project, &runlog.Run{
		ID: "r-lane", ProjectID: projectID, TaskID: "T-004", Stage: "test", Mode: "solo",
		Status: "paused", PendingKind: "gate",
		PendingSince: time.Now().UTC().Format(time.RFC3339), StartedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	store, err := agenttoken.Open(filepath.Join(t.TempDir(), "agent-tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	api := New(s, bus.New(16), humanTok, "test", "")
	api.SetAgentToken(agentTok)
	api.SetAgentTokens(store)
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	return server, store, projectID
}

func issue(t *testing.T, store *agenttoken.Store, name string, grants ...agenttoken.Grant) string {
	t.Helper()
	_, secret, err := store.Create(name, grants)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func wantScopeRefusal(t *testing.T, status int, out, what string) {
	t.Helper()
	if status != http.StatusForbidden || !strings.Contains(out, "agent_scope") {
		t.Errorf("%s = %d %s, want an agent_scope refusal", what, status, out)
	}
}

func TestAPerAgentTokenReachesOnlyItsGrantedProjects(t *testing.T) {
	server, store, project := scopedServer(t)
	granted := issue(t, store, "OpenCode", agenttoken.Grant{Project: project, Level: agenttoken.LevelFull})
	other := issue(t, store, "Elsewhere", agenttoken.Grant{Project: "some-other-project", Level: agenttoken.LevelFull})

	if status, out := call(t, server, granted, "GET", "/v1/projects/"+project+"/tasks", ""); status != http.StatusOK {
		t.Errorf("granted GET tasks = %d %s", status, out)
	}
	status, out := call(t, server, other, "GET", "/v1/projects/"+project+"/tasks", "")
	wantScopeRefusal(t, status, out, "ungranted GET tasks")
	if !strings.Contains(out, "Settings") {
		t.Errorf("refusal does not say the way out: %s", out)
	}

	// A run answers to its project's grant.
	if status, out := call(t, server, granted, "GET", "/v1/runs/r-lane", ""); status != http.StatusOK {
		t.Errorf("granted GET run = %d %s", status, out)
	}
	status, out = call(t, server, other, "GET", "/v1/runs/r-lane", "")
	wantScopeRefusal(t, status, out, "ungranted GET run")
	status, out = call(t, server, other, "POST", "/v1/runs/r-lane/abort", `{}`)
	wantScopeRefusal(t, status, out, "ungranted abort")
}

func TestReadOnlyReadsAndRefusesChanges(t *testing.T) {
	server, store, project := scopedServer(t)
	reader := issue(t, store, "Reader", agenttoken.Grant{Project: project, Level: agenttoken.LevelRead})

	if status, out := call(t, server, reader, "GET", "/v1/projects/"+project+"/bugs", ""); status != http.StatusOK {
		t.Errorf("read-only GET bugs = %d %s", status, out)
	}
	status, out := call(t, server, reader, "POST", "/v1/projects/"+project+"/bugs", `{"title":"x","body":"y"}`)
	wantScopeRefusal(t, status, out, "read-only bug file")
	if !strings.Contains(out, "read-only") {
		t.Errorf("refusal does not name the level: %s", out)
	}
	status, out = call(t, server, reader, "POST", "/v1/runs/r-lane/reject", `{}`)
	wantScopeRefusal(t, status, out, "read-only reject")
	// A lint only computes an answer.
	status, out = call(t, server, reader, "POST", "/v1/projects/"+project+"/artifacts/requirements/lint", `{"content":"# x"}`)
	if strings.Contains(out, "agent_scope") {
		t.Errorf("read-only lint refused: %d %s", status, out)
	}
}

func TestOutsideAnyProjectAPerAgentTokenOnlyReadsConfiguration(t *testing.T) {
	server, store, project := scopedServer(t)
	tok := issue(t, store, "Agent", agenttoken.Grant{Project: project, Level: agenttoken.LevelFull})
	for _, path := range []string{"/v1/ducklings", "/v1/providers", "/v1/defaults/budget", "/v1/engine"} {
		if status, out := call(t, server, tok, "GET", path, ""); status != http.StatusOK {
			t.Errorf("GET %s = %d %s", path, status, out)
		}
	}
	for _, c := range []struct{ method, path, body string }{
		{"PUT", "/v1/defaults/budget", `{}`},
		{"POST", "/v1/projects", `{"path":"/tmp/x"}`},
		{"POST", "/v1/projects/start", `{}`},
		{"PUT", "/v1/ducklings/x", `{}`},
		{"POST", "/v1/shutdown", `{}`},
		{"GET", "/debug/pprof/", ""},
	} {
		status, out := call(t, server, tok, c.method, c.path, c.body)
		wantScopeRefusal(t, status, out, c.method+" "+c.path)
	}
}

func TestListingsNarrowToTheGrants(t *testing.T) {
	server, store, project := scopedServer(t)
	granted := issue(t, store, "Granted", agenttoken.Grant{Project: project, Level: agenttoken.LevelRead})
	none := issue(t, store, "None")

	count := func(tok, path string) int {
		t.Helper()
		status, out := call(t, server, tok, "GET", path, "")
		if status != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, status, out)
		}
		var body struct {
			Total int `json:"total"`
		}
		json.Unmarshal([]byte(out), &body)
		return body.Total
	}
	if got := count(granted, "/v1/projects"); got != 1 {
		t.Errorf("granted sees %d projects, want 1", got)
	}
	if got := count(none, "/v1/projects"); got != 0 {
		t.Errorf("an ungranted token sees %d projects", got)
	}
	if got := count(granted, "/v1/runs"); got != 1 {
		t.Errorf("granted sees %d runs, want 1", got)
	}
	if got := count(none, "/v1/runs"); got != 0 {
		t.Errorf("an ungranted token sees %d runs", got)
	}
	status, out := call(t, server, none, "GET", "/v1/runs?project="+project, "")
	wantScopeRefusal(t, status, out, "run list for an ungranted project")
	status, out = call(t, server, none, "GET", "/v1/events", "")
	wantScopeRefusal(t, status, out, "all-projects event stream")
	status, out = call(t, server, none, "GET", "/v1/events?run=r-lane", "")
	wantScopeRefusal(t, status, out, "event stream of an ungranted run")
}

func TestAPerAgentTokenIsRecordedUnderItsOwnName(t *testing.T) {
	server, store, project := scopedServer(t)
	tok := issue(t, store, "OpenCode ti36", agenttoken.Grant{Project: project, Level: agenttoken.LevelFull})
	status, out := call(t, server, tok, "POST", "/v1/projects/"+project+"/bugs",
		`{"title":"t","body":"b","reporter":"human"}`)
	if status != http.StatusOK || !strings.Contains(out, `"reporter":"agent:opencode-ti36"`) {
		t.Errorf("bug filed with a per-agent token = %d %s, want reporter agent:opencode-ti36", status, out)
	}
	// The shared token may name itself, never the person.
	status, out = call(t, server, agentTok, "POST", "/v1/projects/"+project+"/bugs",
		`{"title":"t","body":"b","reporter":"human"}`)
	if status != http.StatusOK || !strings.Contains(out, `"reporter":"agent"`) {
		t.Errorf("shared token claiming the person = %d %s, want reporter agent", status, out)
	}
}

func TestOnlyThePersonManagesTokens(t *testing.T) {
	server, store, project := scopedServer(t)
	tok := issue(t, store, "Agent", agenttoken.Grant{Project: project, Level: agenttoken.LevelFull})
	for _, cred := range []string{agentTok, tok} {
		for _, c := range []struct{ method, path, body string }{
			{"GET", "/v1/agent-tokens", ""},
			{"POST", "/v1/agent-tokens", `{"name":"mine","grants":[]}`},
			{"PATCH", "/v1/agent-tokens/at_x", `{"grants":[]}`},
			{"DELETE", "/v1/agent-tokens/at_x", ""},
			{"PUT", "/v1/agent-tokens/legacy", `{"enabled":true}`},
		} {
			status, out := call(t, server, cred, c.method, c.path, c.body)
			if status != http.StatusForbidden {
				t.Errorf("agent %s %s = %d %s, want refused", c.method, c.path, status, out)
			}
		}
	}

	status, out := call(t, server, humanTok, "POST", "/v1/agent-tokens",
		`{"name":"Claude Code","grants":[{"project":"`+project+`","level":"full"}]}`)
	if status != http.StatusCreated {
		t.Fatalf("person issuing = %d %s", status, out)
	}
	var created struct {
		Token  agenttoken.View `json:"token"`
		Secret string          `json:"secret"`
	}
	json.Unmarshal([]byte(out), &created)
	if created.Secret == "" {
		t.Fatal("no secret in the create answer")
	}
	_, list := call(t, server, humanTok, "GET", "/v1/agent-tokens", "")
	if strings.Contains(list, created.Secret) || strings.Contains(list, `"hash"`) {
		t.Error("the list leaks a secret or hash")
	}
	if status, out := call(t, server, created.Secret, "GET", "/v1/agent-tokens/self", ""); status != http.StatusOK ||
		!strings.Contains(out, `"actor":"agent:claude-code"`) {
		t.Errorf("self = %d %s", status, out)
	}

	status, out = call(t, server, humanTok, "POST", "/v1/agent-tokens",
		`{"name":"Typo","grants":[{"project":"no-such-project","level":"full"}]}`)
	if status != http.StatusBadRequest {
		t.Errorf("grant to an unknown project = %d %s, want refused", status, out)
	}

	if status, out := call(t, server, humanTok, "DELETE", "/v1/agent-tokens/"+created.Token.ID, ""); status != http.StatusOK {
		t.Fatalf("revoke = %d %s", status, out)
	}
	if status, _ := call(t, server, created.Secret, "GET", "/v1/projects/"+project+"/tasks", ""); status != http.StatusUnauthorized {
		t.Errorf("a revoked token got %d, want 401", status)
	}
}

func TestTurningTheSharedTokenOffLeavesOnlyOwnTokens(t *testing.T) {
	server, store, project := scopedServer(t)
	tok := issue(t, store, "Agent", agenttoken.Grant{Project: project, Level: agenttoken.LevelRead})
	if status, out := call(t, server, humanTok, "PUT", "/v1/agent-tokens/legacy", `{"enabled":false}`); status != http.StatusOK {
		t.Fatalf("legacy off = %d %s", status, out)
	}
	status, out := call(t, server, agentTok, "GET", "/v1/projects", "")
	if status != http.StatusUnauthorized || !strings.Contains(out, "shared_agent_token_off") || !strings.Contains(out, "DUCKLAB_TOKEN") {
		t.Errorf("shared token with legacy off = %d %s", status, out)
	}
	if status, _ := call(t, server, tok, "GET", "/v1/projects", ""); status != http.StatusOK {
		t.Errorf("own token with legacy off = %d", status)
	}
	if status, _ := call(t, server, humanTok, "GET", "/v1/projects", ""); status != http.StatusOK {
		t.Errorf("the person with legacy off = %d", status)
	}
}

// The routes a per-agent token reaches outside any project are a decision,
// pinned here: a new route lands outside every grant until someone adds it.
func TestTheRoutesOutsideProjectsAnAgentMayReachArePinned(t *testing.T) {
	var reach []string
	for _, rt := range routeTable() {
		if !rt.Auth {
			continue
		}
		switch agentRuleFor(rt).kind {
		case scopeGlobalRead, scopeNarrowed:
			reach = append(reach, rt.Method+" "+rt.Path)
		case scopeProject, scopeRun:
		default:
			if strings.HasPrefix(rt.Path, "/v1/projects/{id}") || strings.HasPrefix(rt.Path, "/v1/runs/{id}") {
				t.Errorf("%s %s is a project route ruled outside", rt.Method, rt.Path)
			}
		}
	}
	sort.Strings(reach)
	want := []string{
		"GET /v1/agent-tokens/self",
		"GET /v1/bench",
		"GET /v1/bench/{suite}/{stamp}",
		"GET /v1/defaults/autopilot",
		"GET /v1/defaults/budget",
		"GET /v1/defaults/candidates",
		"GET /v1/defaults/diagnostics",
		"GET /v1/defaults/engine",
		"GET /v1/defaults/modes",
		"GET /v1/defaults/projects",
		"GET /v1/defaults/roster",
		"GET /v1/ducklings",
		"GET /v1/ducklings/scorecards",
		"GET /v1/engine",
		"GET /v1/events",
		"GET /v1/project-presets",
		"GET /v1/projects",
		"GET /v1/providers",
		"GET /v1/providers/{id}/login",
		"GET /v1/providers/{id}/model-endpoints",
		"GET /v1/providers/{id}/models",
		"GET /v1/runs",
	}
	if strings.Join(reach, "\n") != strings.Join(want, "\n") {
		t.Errorf("routes a per-agent token reaches outside projects changed:\n got %q\nwant %q", reach, want)
	}
}
