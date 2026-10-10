package engineapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/jrullan/ducklab/internal/service"
)

// B-517: the desktop's retry on a failed intake POSTed a build with no task
// in council mode, and the engine answered 202 with a run that failed a
// second later. The request is refused with 400 and a message the desktop
// shows as-is; no run is recorded.
func TestRunStartRefusesATasklessBuildWith400AndNoRun(t *testing.T) {
	server, svc := resumeServer(t)
	projects, err := svc.ProjectList(context.Background())
	if err != nil || len(projects) != 1 {
		t.Fatalf("projects = %v, %v", projects, err)
	}
	body := `{"task_id":"","mode":"council","note":"Retry the task after addressing the failure."}`
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/projects/"+projects[0].ID+"/runs", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d (%s), want 400", resp.StatusCode, raw)
	}
	var out struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Error.Code != "invalid_request" || out.Error.Message == "" {
		t.Errorf("error = %+v, want invalid_request with the engine's reason", out.Error)
	}
	runs, err := svc.RunList(context.Background(), service.RunFilter{ProjectID: projects[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Errorf("a refused build left %d run(s) on the record", len(runs))
	}
}
