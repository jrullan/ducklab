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
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
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
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.HTTPClient.Do(req)
}

func (c *Client) get(path string, result interface{}) error {
	resp, err := c.do("GET", path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET %s: %s: %s", path, resp.Status, string(body))
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
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST %s: %s: %s", path, resp.Status, string(body))
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
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("PUT %s: %s: %s", path, resp.Status, string(b))
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
func (c *Client) ProjectInit(path, name string, gitInit bool) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post("/v1/projects", map[string]interface{}{
		"path":     path,
		"name":     name,
		"git_init": gitInit,
	}, &result)
	return result, err
}

// RunStart starts a run.
func (c *Client) RunStart(projectID string, req map[string]interface{}) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.post(fmt.Sprintf("/v1/projects/%s/runs", projectID), req, &result)
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
func (c *Client) RunGet(id string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.get("/v1/runs/"+id, &result)
	return result, err
}

// RunAccept accepts a run.
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
	url := fmt.Sprintf("%s?run=%s&from_seq=%d", c.EventsURL(), runID, fromSeq)
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

// TraceCheck walks the whole spine.
func (c *Client) TraceCheck(projectID string) ([]map[string]interface{}, error) {
	var result struct {
		Errors []map[string]interface{} `json:"errors"`
	}
	err := c.get(fmt.Sprintf("/v1/projects/%s/trace/check", projectID), &result)
	return result.Errors, err
}

// TraceShow walks the spine from one id.
func (c *Client) TraceShow(projectID, id string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := c.get(fmt.Sprintf("/v1/projects/%s/trace/%s", projectID, id), &result)
	return result, err
}
