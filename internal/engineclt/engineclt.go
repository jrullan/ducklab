// Package engineclt is the Go client for the engine API.
// Used by cmd/ducklab.
package engineclt

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/daemon"
)

// Client is the engine API client.
type Client struct {
	BaseURL    string
	Token      string
	Version    string
	HTTPClient *http.Client
}

// New creates a new engine client from daemon info.
func New(info *daemon.EngineInfo) *Client {
	return &Client{
		BaseURL: fmt.Sprintf("http://127.0.0.1:%d", info.Port),
		Token:   info.Token,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) do(method, path string, body interface{}) (*http.Response, error) {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	build := func() (*http.Request, error) {
		var bodyReader io.Reader
		if data != nil {
			bodyReader = bytes.NewReader(data)
		}
		req, err := http.NewRequest(method, c.BaseURL+path, bodyReader)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		if c.Version != "" {
			req.Header.Set("X-Ducklab-Client", c.Version)
		}
		if data != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		return req, nil
	}
	req, err := build()
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTPClient.Do(req)
	// A client that outlives the engine follows it. The MCP server is a
	// long-lived process holding a port and token resolved once at startup;
	// an engine restart moved both, and every tool answered "connection
	// refused on 127.0.0.1:<dead port>" until someone restarted the agent's
	// whole gateway. engine.json is the engine's forwarding address — when
	// the dial fails and the file says somewhere new, go there. One retry;
	// an engine that is genuinely down still says so.
	if err != nil {
		info, rerr := daemon.ReadEngineJSON()
		if rerr != nil {
			return resp, err
		}
		fresh := fmt.Sprintf("http://127.0.0.1:%d", info.Port)
		if fresh == c.BaseURL && info.Token == c.Token {
			return resp, err
		}
		c.BaseURL, c.Token = fresh, info.Token
		req, berr := build()
		if berr != nil {
			return nil, berr
		}
		return c.HTTPClient.Do(req)
	}
	// The other face of a restart: the OS reused the port, so the dial
	// succeeded and the stale token earned a 401 instead.
	if resp.StatusCode == http.StatusUnauthorized {
		if info, rerr := daemon.ReadEngineJSON(); rerr == nil {
			fresh := fmt.Sprintf("http://127.0.0.1:%d", info.Port)
			if fresh != c.BaseURL || info.Token != c.Token {
				resp.Body.Close()
				c.BaseURL, c.Token = fresh, info.Token
				req, berr := build()
				if berr != nil {
					return nil, berr
				}
				return c.HTTPClient.Do(req)
			}
		}
	}
	return resp, err
}

// APIError is an error the engine reported, unwrapped from its JSON envelope.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string { return e.Message }

// httpError turns a non-2xx response into the message the engine actually
// sent. Without it every failure reached the user as the whole envelope —
// the URL, the status line, and the raw JSON — burying the one sentence that
// says what to fix.
func httpError(method, path string, resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &env) == nil && env.Error.Message != "" {
		return &APIError{Status: resp.StatusCode, Code: env.Error.Code, Message: env.Error.Message}
	}
	return &APIError{
		Status:  resp.StatusCode,
		Message: fmt.Sprintf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(body))),
	}
}

func (c *Client) get(path string, result interface{}) error {
	resp, err := c.do("GET", path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return httpError("GET", path, resp)
	}
	return json.NewDecoder(resp.Body).Decode(result)
}

func (c *Client) post(path string, body interface{}, result interface{}) error {
	resp, err := c.do("POST", path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return httpError("POST", path, resp)
	}
	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}

func (c *Client) put(path string, body interface{}, result interface{}) error {
	resp, err := c.do("PUT", path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return httpError("PUT", path, resp)
	}
	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}

func (c *Client) delete(path string) error {
	resp, err := c.do("DELETE", path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return httpError("DELETE", path, resp)
	}
	return nil
}

func (c *Client) patch(path string, body interface{}, result interface{}) error {
	resp, err := c.do("PATCH", path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return httpError("PATCH", path, resp)
	}
	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}

// Health checks the engine health.
func (c *Client) Health() (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.get("/v1/health", &result)
	return result, err
}

// ProjectList lists projects.
func (c *Client) ProjectList() ([]map[string]interface{}, error) {
	var result struct {
		Items []map[string]interface{} `json:"items"`
	}
	err := c.get("/v1/projects", &result)
	return result.Items, err
}

// ProjectInit initializes a project.
func (c *Client) ProjectInit(path, name, describe string, gitInit bool) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post("/v1/projects", map[string]interface{}{
		"path":     path,
		"name":     name,
		"describe": describe,
		"git_init": gitInit,
	}, &result)
	return result, err
}

// SkillList lists the skills available to a project.
func (c *Client) SkillList(projectID string) ([]map[string]interface{}, error) {
	var result struct {
		Items []map[string]interface{} `json:"items"`
	}
	err := c.get("/v1/projects/"+projectID+"/skills", &result)
	return result.Items, err
}

// SkillGet returns one skill.
func (c *Client) SkillGet(projectID, name string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.get("/v1/projects/"+projectID+"/skills/"+name, &result)
	return result, err
}

// SkillNew scaffolds a skill directory.
func (c *Client) SkillNew(projectID, name string, runnable bool) (string, error) {
	var result struct {
		Dir string `json:"dir"`
	}
	err := c.post("/v1/projects/"+projectID+"/skills",
		map[string]interface{}{"name": name, "runnable": runnable}, &result)
	return result.Dir, err
}

// SkillRun runs a skill. No model is involved.
func (c *Client) SkillRun(projectID, name string, args map[string]interface{}) (string, bool, error) {
	var result struct {
		Output string `json:"output"`
		Failed bool   `json:"failed"`
	}
	err := c.post("/v1/projects/"+projectID+"/skills/"+name+"/run",
		map[string]interface{}{"args": args}, &result)
	return result.Output, result.Failed, err
}

// Bench runs a benchmark suite. It can take a long time: every cell is a real
// run against a real model.
func (c *Client) Bench(suite string, ducklings, modes []string, keep bool) (rendered, path string, err error) {
	var result struct {
		Rendered string `json:"rendered"`
		Path     string `json:"path"`
	}
	// The default 30s timeout is right for asking the engine a question and
	// wrong for asking it to spend an afternoon. Nothing here is unbounded
	// (I3): the engine bounds every cell itself, and the suite is that bound
	// times the number of cells.
	long := *c
	client := *c.HTTPClient
	client.Timeout = 12 * time.Hour
	long.HTTPClient = &client
	err = long.post("/v1/bench", map[string]interface{}{
		"suite": suite, "ducklings": ducklings, "modes": modes, "keep": keep,
	}, &result)
	return result.Rendered, result.Path, err
}

// ProviderList returns configured providers. Never carries a key (I10).
func (c *Client) ProviderList() ([]map[string]interface{}, error) {
	var result struct {
		Items []map[string]interface{} `json:"items"`
	}
	err := c.get("/v1/providers", &result)
	return result.Items, err
}

// ProviderSet adds or replaces a provider.
func (c *Client) ProviderSet(id string, body map[string]interface{}) error {
	return c.put("/v1/providers/"+id, body, nil)
}

// ProviderRemove removes a provider.
func (c *Client) ProviderRemove(id string) error {
	return c.delete("/v1/providers/" + id)
}

// DucklingSet adds or replaces a duckling.
func (c *Client) DucklingSet(id string, body map[string]interface{}) error {
	return c.put("/v1/ducklings/"+id, body, nil)
}

// DucklingRemove removes a duckling.
func (c *Client) DucklingRemove(id string) error {
	return c.delete("/v1/ducklings/" + id)
}

// TestStart writes the failing test for a task.
func (c *Client) TestStart(projectID, taskID, duckling string, thenBuild, redo bool, note string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post("/v1/projects/"+projectID+"/tests",
		map[string]interface{}{"task_id": taskID, "duckling": duckling, "then_build": thenBuild, "redo": redo, "note": note}, &result)
	return result, err
}

// ProjectNext is the engine's own guidance: the ordered next steps the
// guide rail renders and the autopilot drives.
func (c *Client) ProjectNext(projectID string) ([]map[string]interface{}, error) {
	var result []map[string]interface{}
	err := c.get("/v1/projects/"+projectID+"/next", &result)
	return result, err
}

// AppStatus reports the project app's run configuration and process state.
func (c *Client) AppStatus(projectID string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.get("/v1/projects/"+projectID+"/app", &result)
	return result, err
}

// AppStart launches the app under development via its run.command.
func (c *Client) AppStart(projectID string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post("/v1/projects/"+projectID+"/app/start", nil, &result)
	return result, err
}

// AppStop stops the engine-managed app process.
func (c *Client) AppStop(projectID string) error {
	return c.post("/v1/projects/"+projectID+"/app/stop", nil, nil)
}

// ProjectGate reports the configured gate and the detectable one.
func (c *Client) ProjectGate(projectID string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.get("/v1/projects/"+projectID+"/gate", &result)
	return result, err
}

// ProjectGateAdopt writes the detected gate into the project.
func (c *Client) ProjectGateAdopt(projectID string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post("/v1/projects/"+projectID+"/gate", nil, &result)
	return result, err
}

// RunDiff returns a run's diff and, when it was flagged, the part of it that
// touches tests.
func (c *Client) RunDiff(id string) (diff, tests, warning string, err error) {
	var result struct {
		Diff    string `json:"diff"`
		Tests   string `json:"tests"`
		Warning string `json:"warning"`
	}
	if err := c.get("/v1/runs/"+id+"/diff", &result); err != nil {
		return "", "", "", err
	}
	return result.Diff, result.Tests, result.Warning, nil
}

// RunStart starts a run.
func (c *Client) RunStart(projectID string, req map[string]interface{}) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post(fmt.Sprintf("/v1/projects/%s/runs", projectID), req, &result)
	return result, err
}

// BudgetDefaults reads the budget every run starts with.
func (c *Client) BudgetDefaults() (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.get("/v1/defaults/budget", &result)
	return result, err
}

// BudgetDefaultsSet replaces the default run budget.
func (c *Client) BudgetDefaultsSet(body map[string]interface{}) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.put("/v1/defaults/budget", body, &result)
	return result, err
}

// RunList lists runs.
func (c *Client) RunList(projectID string) ([]map[string]interface{}, error) {
	var result struct {
		Items []map[string]interface{} `json:"items"`
	}
	path := "/v1/runs"
	if projectID != "" {
		path += "?project=" + projectID
	}
	err := c.get(path, &result)
	return result.Items, err
}

// RunGet gets a run.
// The endpoint answers {"run": {...}, "events": [...]}, so it unwraps to the
// run itself. Returning the envelope made every caller read a missing key and
// print "%!s(<nil>)" for the whole record — id, status and verdict alike.
func (c *Client) RunGet(id string) (map[string]interface{}, error) {
	var result map[string]interface{}
	if err := c.get("/v1/runs/"+id, &result); err != nil {
		return nil, err
	}
	if run, ok := result["run"].(map[string]interface{}); ok {
		return run, nil
	}
	return result, nil
}

// RunEvents returns a run's event log, which RunGet deliberately drops.
func (c *Client) RunEvents(id string) ([]interface{}, error) {
	var result map[string]interface{}
	if err := c.get("/v1/runs/"+id, &result); err != nil {
		return nil, err
	}
	events, _ := result["events"].([]interface{})
	return events, nil
}

// ProjectGet returns one project by id.
func (c *Client) ProjectGet(id string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.get("/v1/projects/"+id, &result)
	return result, err
}

// ProjectStatus returns a project's stage progress, task counts and active runs.
func (c *Client) ProjectStatus(id string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.get("/v1/projects/"+id+"/status", &result)
	return result, err
}

// ProjectForget unregisters a project, leaving the directory alone.
func (c *Client) ProjectForget(id string) error {
	resp, err := c.do("DELETE", "/v1/projects/"+id, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return httpError("DELETE", "/v1/projects/"+id, resp)
	}
	return nil
}

// BugAdd reports a bug.
func (c *Client) BugAdd(projectID string, req map[string]string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post("/v1/projects/"+projectID+"/bugs", req, &result)
	return result, err
}

// BugList lists a project's bugs, worst first.
func (c *Client) BugList(projectID string, openOnly bool) ([]map[string]interface{}, error) {
	var result struct {
		Items []map[string]interface{} `json:"items"`
	}
	path := "/v1/projects/" + projectID + "/bugs"
	if openOnly {
		path += "?open=true"
	}
	err := c.get(path, &result)
	return result.Items, err
}

// BugTriage classifies bugs: every open one when bugID is empty, exactly
// that one otherwise.
func (c *Client) BugTriage(projectID, bugID string) (map[string]interface{}, error) {
	var result map[string]interface{}
	var body interface{}
	if bugID != "" {
		body = map[string]string{"bug_id": bugID}
	}
	err := c.post("/v1/projects/"+projectID+"/bugs/triage", body, &result)
	return result, err
}

// BugAttach stores one attachment (base64 bytes) on a bug — the screenshot
// that says what a paragraph cannot, sent from wherever the reporter is.
func (c *Client) BugAttach(projectID, bugID, filename, dataB64 string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post("/v1/projects/"+projectID+"/bugs/"+bugID+"/attachments",
		map[string]string{"filename": filename, "data": dataB64}, &result)
	return result, err
}

// BugPromote turns a bug into a task. The actor signs the audit trail.
func (c *Client) BugPromote(projectID, bugID, actor string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post("/v1/projects/"+projectID+"/bugs/"+bugID+"/promote",
		map[string]string{"actor": actor}, &result)
	return result, err
}

// BugMove changes a bug's status. The actor signs the audit trail.
func (c *Client) BugMove(projectID, bugID, status, actor string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post("/v1/projects/"+projectID+"/bugs/"+bugID+"/status",
		map[string]string{"status": status, "actor": actor}, &result)
	return result, err
}

// ReleasePlan drafts the notes for the next release.
func (c *Client) ReleasePlan(projectID, bump string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post("/v1/projects/"+projectID+"/releases", map[string]string{"bump": bump}, &result)
	return result, err
}

// ReleaseCut promotes a drafted release and tags it.
func (c *Client) ReleaseCut(projectID, version string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post("/v1/projects/"+projectID+"/releases/"+version+"/cut", nil, &result)
	return result, err
}

// ReviewStart reviews an accepted task's committed diff.
func (c *Client) ReviewStart(projectID, taskID, mode string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post("/v1/projects/"+projectID+"/reviews",
		map[string]string{"task_id": taskID, "mode": mode}, &result)
	return result, err
}

// ProjectUpdate applies dotted config keys to a project.
func (c *Client) ProjectUpdate(id string, keys map[string]string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.patch("/v1/projects/"+id, keys, &result)
	return result, err
}

// RunAccept accepts a run.
// RunAcceptAs accepts with the decider named — an operator, not a person.
func (c *Client) RunAcceptAs(id, message, actor string) (map[string]interface{}, error) {
	var out map[string]interface{}
	err := c.post("/v1/runs/"+id+"/accept", map[string]string{"message": message, "actor": actor}, &out)
	return out, err
}

func (c *Client) RunAccept(id, message string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post("/v1/runs/"+id+"/accept", map[string]string{"message": message}, &result)
	return result, err
}

// RunReject rejects a run.
func (c *Client) RunReject(id, reason string) error {
	return c.post("/v1/runs/"+id+"/reject", map[string]string{"reason": reason}, nil)
}

// RunAbort aborts a run.
func (c *Client) RunAbort(id string) error {
	return c.post("/v1/runs/"+id+"/abort", nil, nil)
}

// DucklingList lists ducklings.
func (c *Client) DucklingList() ([]map[string]interface{}, error) {
	var result struct {
		Items []map[string]interface{} `json:"items"`
	}
	err := c.get("/v1/ducklings", &result)
	return result.Items, err
}

// DucklingTest tests a duckling.
func (c *Client) DucklingTest(id, prompt string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post(fmt.Sprintf("/v1/ducklings/%s/test", id), map[string]interface{}{
		"prompt": prompt,
		"stream": false,
	}, &result)
	return result, err
}

// Report fetches the aggregated report for a project.
func (c *Client) Report(projectID, by, since string) (map[string]interface{}, error) {
	path := fmt.Sprintf("/v1/projects/%s/report?by=%s", projectID, by)
	if since != "" {
		path += "&since=" + since
	}
	var result map[string]interface{}
	err := c.get(path, &result)
	return result, err
}

// RosterGet returns the resolved roster for a project.
func (c *Client) RosterGet(projectID string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.get(fmt.Sprintf("/v1/projects/%s/roster", projectID), &result)
	return result, err
}

// RosterSet assigns a duckling to a role.
func (c *Client) RosterSet(projectID, role, ducklingID string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.put(fmt.Sprintf("/v1/projects/%s/roster", projectID),
		map[string]string{"role": role, "duckling": ducklingID}, &result)
	return result, err
}

// RosterSuggest returns a ranked assignment, without applying it.
func (c *Client) RosterSuggest(projectID string) ([]map[string]interface{}, error) {
	var result struct {
		Items []map[string]interface{} `json:"items"`
	}
	err := c.get(fmt.Sprintf("/v1/projects/%s/roster/suggest", projectID), &result)
	return result.Items, err
}

// RosterApply writes the suggested assignment to project.toml.
func (c *Client) RosterApply(projectID string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post(fmt.Sprintf("/v1/projects/%s/roster/suggest", projectID), nil, &result)
	return result, err
}

// DucklingProbe re-probes a duckling's capabilities.
func (c *Client) DucklingProbe(id string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post(fmt.Sprintf("/v1/ducklings/%s/probe", id), nil, &result)
	return result, err
}

// EventsURL returns the SSE events URL.
func (c *Client) EventsURL() string {
	return c.BaseURL + "/v1/events"
}

// SSEEvent is one decoded server-sent event.
type SSEEvent struct {
	Type      string                 `json:"type"`
	RunID     string                 `json:"run_id"`
	ProjectID string                 `json:"project_id"`
	Seq       int                    `json:"seq"`
	Data      map[string]interface{} `json:"data"`
}

// RunResume asks the engine to resume a paused run.
func (c *Client) RunResume(id string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post("/v1/runs/"+id+"/resume", nil, &result)
	return result, err
}

// RunBudgetLift removes one cap from a live or paused run.
func (c *Client) RunBudgetLift(id, kind, actor string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post("/v1/runs/"+id+"/budget/lift", map[string]string{"kind": kind, "actor": actor}, &result)
	return result, err
}

// RunFileFindings files the run's final reviewer findings as attributed bug reports.
func (c *Client) RunFileFindings(id string) ([]map[string]interface{}, error) {
	var result struct {
		Items []map[string]interface{} `json:"items"`
	}
	err := c.post("/v1/runs/"+id+"/findings/file", nil, &result)
	return result.Items, err
}

// RunAnswer answers a run's pending question.
func (c *Client) RunAnswer(id, questionID, answer string) error {
	return c.post("/v1/runs/"+id+"/answer", map[string]string{
		"question_id": questionID,
		"answer":      answer,
	}, nil)
}

// StreamRunEvents follows a run's event stream, calling fn for each event.
// It returns when fn returns false, the stream ends, or ctx is cancelled.
//
// The connection has no client-side timeout: a run may sit at a human gate
// for hours, and the engine's heartbeat keeps the socket alive.
func (c *Client) StreamRunEvents(ctx context.Context, runID string, fromSeq int, fn func(SSEEvent) bool) error {
	return c.streamEvents(ctx, fmt.Sprintf("%s?run=%s&from_seq=%d", c.EventsURL(), runID, fromSeq), fn)
}

// StreamEvents follows every event the engine emits, not one run's.
//
// A bench has no run id of its own — it is many runs — so following it means
// following the engine.
func (c *Client) StreamEvents(ctx context.Context, fn func(SSEEvent) bool) error {
	return c.streamEvents(ctx, c.EventsURL(), fn)
}

func (c *Client) streamEvents(ctx context.Context, url string, fn func(SSEEvent) bool) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "text/event-stream")
	if c.Version != "" {
		req.Header.Set("X-Ducklab-Client", c.Version)
	}

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("events: %s: %s", resp.Status, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var e SSEEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e); err != nil {
			continue
		}
		if !fn(e) {
			return nil
		}
	}
	return scanner.Err()
}

// --- the cycle ---------------------------------------------------------------

// StageStart runs intake, spec or plan.
func (c *Client) StageStart(projectID, stage string, req map[string]interface{}) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post(fmt.Sprintf("/v1/projects/%s/stages/%s", projectID, stage), req, &result)
	return result, err
}

// ArtifactGet reads an artifact and any pending proposal.
func (c *Client) ArtifactGet(projectID, kind string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.get(fmt.Sprintf("/v1/projects/%s/artifacts/%s", projectID, kind), &result)
	return result, err
}

// ArtifactPromote accepts a pending proposal.
func (c *Client) ArtifactPromote(projectID, kind, approvedBy string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post(fmt.Sprintf("/v1/projects/%s/artifacts/%s/promote", projectID, kind),
		map[string]string{"approved_by": approvedBy}, &result)
	return result, err
}

// TaskList reads the plan's tasks.
func (c *Client) TaskList(projectID string) ([]map[string]interface{}, error) {
	var result struct {
		Items []map[string]interface{} `json:"items"`
	}
	err := c.get(fmt.Sprintf("/v1/projects/%s/tasks", projectID), &result)
	return result.Items, err
}

// TaskNext returns the first task whose dependencies are met.
func (c *Client) TaskNext(projectID string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.get(fmt.Sprintf("/v1/projects/%s/tasks/next", projectID), &result)
	return result, err
}

// TraceCheck walks the whole spine. The second return names the stages read
// from a pending proposal rather than the approved artifact, so a caller can
// say whether a break is in what is being decided or in what was accepted.
func (c *Client) TraceCheck(projectID string) ([]map[string]interface{}, []string, error) {
	var result struct {
		Errors   []map[string]interface{} `json:"errors"`
		Proposed []string                 `json:"proposed"`
	}
	err := c.get(fmt.Sprintf("/v1/projects/%s/trace/check", projectID), &result)
	return result.Errors, result.Proposed, err
}

// TraceShow walks the spine from one id.
func (c *Client) TraceShow(projectID, id string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.get(fmt.Sprintf("/v1/projects/%s/trace/%s", projectID, id), &result)
	return result, err
}

// TraceReport fetches the rendered development report.
func (c *Client) TraceReport(projectID string) (string, error) {
	var out struct {
		Rendered string `json:"rendered"`
	}
	if err := c.get("/v1/projects/"+projectID+"/trace/report", &out); err != nil {
		return "", err
	}
	return out.Rendered, nil
}

// Shutdown asks the engine to stop gracefully. The daemon's own stop path:
// used by supervision (restart) and by `ducklab engine stop`.
func (c *Client) Shutdown() error {
	return c.post("/v1/shutdown", nil, nil)
}

// ActiveRuns returns the ids of runs that are running or queued — the work a
// restart would cut off mid-call. Paused runs do not count: a run waiting at
// a gate survives a restart by design (I9) and resumes from where it stood.
func (c *Client) ActiveRuns() ([]string, error) {
	var out struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
	}
	if err := c.get("/v1/runs", &out); err != nil {
		return nil, err
	}
	var active []string
	for _, r := range out.Items {
		if r.Status == "running" || r.Status == "queued" {
			active = append(active, r.ID)
		}
	}
	return active, nil
}

// ProviderKeyEnvs returns the environment variable names the engine's
// providers read keys from. A restart guard checks them against the
// environment about to spawn the replacement: an engine restarted from a
// shell without the key silently loses every hosted model (I10 — the key
// lives only in the engine's environment).
func (c *Client) ProviderKeyEnvs() ([]string, error) {
	var out struct {
		Items []struct {
			KeyEnv string `json:"api_key_env"`
		} `json:"items"`
	}
	if err := c.get("/v1/providers", &out); err != nil {
		return nil, err
	}
	var envs []string
	for _, p := range out.Items {
		if p.KeyEnv != "" {
			envs = append(envs, p.KeyEnv)
		}
	}
	return envs, nil
}

// RunReseat moves a weather-paused run's seats onto a fallback duckling and
// resumes it.
func (c *Client) RunReseat(runID, from, to string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post("/v1/runs/"+runID+"/reseat", map[string]string{"from": from, "to": to}, &result)
	return result, err
}
