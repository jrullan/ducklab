package engineapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/service"
)

func landedResolutionService(t *testing.T) (*service.Service, string, string) {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(name, filepath.Join(root, name))
	}
	s, err := service.New(config.DefaultGlobal(), service.Options{Bus: bus.New(16)})
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	p, err := s.ProjectInit(context.Background(), service.InitRequest{Path: project, Name: "landed", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	return s, p.ID, project
}

func writeLandedResolutionRun(t *testing.T, project, projectID, id, status, verdict string) {
	t.Helper()
	w, err := runlog.NewWriter(project, &runlog.Run{
		ID: id, ProjectID: projectID, TaskID: "T-120", Stage: "build", Mode: "solo",
		Status: status, Verdict: verdict, StartedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
}

// A manual landing happens after an ordinary reject has closed the run. The
// operator must be able to correct that historical outcome through the public
// reject/resolve door, preserving both the landing SHA and an auditable human
// decision. Live runs are deliberately not eligible for this backfill action.
func TestLandedResolutionCanRecloseOnlyDoneRuns(t *testing.T) {
	s, projectID, project := landedResolutionService(t)
	if err := os.WriteFile(filepath.Join(project, "landed.txt"), []byte("landed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "landed.txt"}, {"-c", "user.name=test", "-c", "user.email=test@test", "commit", "-m", "landed work\n\nDucklab-Run: r-done"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = project
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	shaOut, err := exec.Command("git", "-C", project, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	landingSHA := string(bytes.TrimSpace(shaOut))
	writeLandedResolutionRun(t, project, projectID, "r-done", "done", "FAILED")
	writeLandedResolutionRun(t, project, projectID, "r-running", "running", "")
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(New(s, bus.New(16), "token", "test", ""))
	t.Cleanup(server.Close)
	resolve := func(id string) *http.Response {
		t.Helper()
		body, err := json.Marshal(map[string]string{
			"reason":     "Work ACCEPTED in substance and landed on main",
			"resolution": "landed",
			"commit_sha": landingSHA,
		})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/runs/"+id+"/reject", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	resp := resolve("r-done")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("re-resolve done run status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	detail, err := s.RunGet(context.Background(), "r-done")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.Resolution != "landed" {
		t.Errorf("resolution = %q, want landed", detail.Run.Resolution)
	}
	if detail.Run.Verdict != "PASSED" {
		t.Errorf("verdict = %q, want PASSED for a landed run", detail.Run.Verdict)
	}
	if detail.Run.CommitSHA != landingSHA {
		t.Errorf("landing commit = %q, want %s", detail.Run.CommitSHA, landingSHA)
	}
	events, err := runlog.ReadEvents(s.RunDir("r-done"))
	if err != nil {
		t.Fatal(err)
	}
	foundHumanLanding := false
	for _, event := range events {
		if event.Type == "human" && event.Data["action"] == "landed" {
			foundHumanLanding = true
		}
	}
	if !foundHumanLanding {
		t.Errorf("re-resolution did not record a human landed event: %+v", events)
	}

	resp = resolve("r-running")
	resp.Body.Close()
	if resp.StatusCode < http.StatusBadRequest {
		t.Errorf("re-resolving a running run status = %d, want refusal", resp.StatusCode)
	}
}
