package engineapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/runlog"
)

// B-524: the accept, reject and abort bodies had no note field, so the JSON
// decoder discarded a note and the decision went through as if the words had
// been read. Each now refuses a note before deciding, naming the door that
// carries words; the run is left at its gate. The same body without the
// note decides, and abort's reason is recorded.
func TestADecisionNoteOverHTTPIsRefusedNotDropped(t *testing.T) {
	cases := []struct {
		action, stage, door string
		body                map[string]string
	}{
		{"accept", "triage", "edit the bug's proposal with bug_edit before promoting it", map[string]string{"message": "accepted", "actor": "mcp:opencode"}},
		{"accept", "build", "only resume carries a note", map[string]string{"message": "accepted"}},
		{"reject", "triage", "it is recorded with the rejection", map[string]string{"reason": "not a bug"}},
		{"reject", "spec", "it is recorded with the rejection", map[string]string{"reason": "not a bug"}},
		{"abort", "triage", "it is recorded with the abort", map[string]string{"reason": "withdrawn", "actor": "mcp:opencode"}},
		{"abort", "build", "it is recorded with the abort", map[string]string{"reason": "withdrawn", "actor": "mcp:opencode"}},
	}
	for _, tc := range cases {
		t.Run(tc.action+"/"+tc.stage, func(t *testing.T) {
			s, projectID, project := landedResolutionService(t)
			const runID = "r-note"
			w, err := runlog.NewWriter(project, &runlog.Run{
				ID: runID, ProjectID: projectID, TaskID: "T-1", Stage: tc.stage, Mode: "solo",
				Status: "paused", PendingKind: "gate", Verdict: "UNVERIFIED",
				StartedAt: time.Now().UTC().Format(time.RFC3339),
			})
			if err != nil {
				t.Fatal(err)
			}
			w.Close()
			if err := s.RecoverRuns(context.Background()); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(New(s, bus.New(16), "token", "test", ""))
			t.Cleanup(server.Close)
			post := func(body map[string]string) (int, string) {
				t.Helper()
				payload, _ := json.Marshal(body)
				req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/runs/"+runID+"/"+tc.action, bytes.NewReader(payload))
				req.Header.Set("Authorization", "Bearer token")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				out, _ := io.ReadAll(resp.Body)
				return resp.StatusCode, string(out)
			}

			withNote := map[string]string{"note": "no jsdom"}
			for k, v := range tc.body {
				withNote[k] = v
			}
			code, out := post(withNote)
			if code != http.StatusBadRequest || !strings.Contains(out, tc.action+" takes no note") || !strings.Contains(out, tc.door) {
				t.Fatalf("%s with a note = %d %s, want 400 naming %q", tc.action, code, out, tc.door)
			}
			detail, err := s.RunGet(context.Background(), runID)
			if err != nil {
				t.Fatal(err)
			}
			if detail.Run.Status != "paused" || detail.Run.PendingKind != "gate" {
				t.Fatalf("a refused %s changed the run: %s/%s", tc.action, detail.Run.Status, detail.Run.PendingKind)
			}

			if code, out := post(tc.body); code >= 300 {
				// Accept of a code run without a worktree may refuse for its own
				// reasons; the point here is that it was not refused for a note.
				if strings.Contains(out, "takes no note") {
					t.Fatalf("%s without a note was refused for one: %s", tc.action, out)
				}
				return
			}
			if tc.action == "abort" {
				events, _ := runlog.ReadEvents(s.RunDir(runID))
				recorded := false
				for _, e := range events {
					if e.Type == "decision" && e.Data["action"] == "abort" && e.Data["reason"] == "withdrawn" && e.Data["actor"] == "mcp:opencode" {
						recorded = true
					}
				}
				if !recorded {
					t.Error("abort's reason was not recorded as its decision")
				}
			}
		})
	}
}
