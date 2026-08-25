package engineapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/runlog"
)

func TestReasonedDocumentRejectReturnsRevisionSignpost(t *testing.T) {
	for _, stage := range []string{"intake", "spec", "plan", "release"} {
		t.Run(stage, func(t *testing.T) {
			receipt := documentRejectReceipt(&runlog.Run{Stage: stage}, "Please preserve the structure, but revise the acceptance criteria.")
			if receipt == nil {
				t.Fatal("reasoned document reject produced no receipt")
			}
			if !strings.Contains(receipt["message"], "draft discarded") || receipt["action"] != "request_changes" {
				t.Errorf("receipt = %#v, want discard signpost to request_changes", receipt)
			}
		})
	}
}

func TestPlainRejectsDoNotReturnRevisionSignpost(t *testing.T) {
	if got := documentRejectReceipt(&runlog.Run{Stage: "spec"}, ""); got != nil {
		t.Errorf("reasonless document reject receipt = %#v, want nil", got)
	}
	if got := documentRejectReceipt(&runlog.Run{Stage: "build"}, "Please revise this implementation."); got != nil {
		t.Errorf("reasoned non-document reject receipt = %#v, want nil", got)
	}
}

func TestRunRejectReasonedDocumentReceipt(t *testing.T) {
	s, projectID, project := landedResolutionService(t)
	writeRejectRun := func(id, stage string) {
		t.Helper()
		w, err := runlog.NewWriter(project, &runlog.Run{
			ID: id, ProjectID: projectID, TaskID: "T-136", Stage: stage, Mode: "solo",
			Status: "paused", PendingKind: "gate", Verdict: "UNVERIFIED",
			StartedAt: time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil {
			t.Fatal(err)
		}
		w.Close()
	}
	writeRejectRun("r-document", "spec")
	writeRejectRun("r-reasonless", "plan")
	writeRejectRun("r-build", "build")
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(New(s, bus.New(16), "token", "test", ""))
	t.Cleanup(server.Close)
	reject := func(id, reason string) *http.Response {
		t.Helper()
		payload, err := json.Marshal(map[string]string{"reason": reason})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/runs/"+id+"/reject", bytes.NewReader(payload))
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

	resp := reject("r-document", "Please revise the acceptance criteria.")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reasoned document reject status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var receipt rejectReceipt
	if err := json.NewDecoder(resp.Body).Decode(&receipt); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(receipt.Message, "draft discarded") || receipt.Action != "request_changes" {
		t.Errorf("receipt = %#v, want discarded signpost to request_changes", receipt)
	}
	detail, err := s.RunGet(context.Background(), "r-document")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.Status != "done" || detail.Run.Verdict != "FAILED" {
		t.Errorf("reasoned reject changed discard semantics: status=%q verdict=%q", detail.Run.Status, detail.Run.Verdict)
	}

	for _, tc := range []struct{ id, reason string }{
		{"r-reasonless", ""},
		{"r-build", "Please revise the implementation."},
	} {
		resp := reject(tc.id, tc.reason)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("reject %s status = %d, want %d", tc.id, resp.StatusCode, http.StatusNoContent)
		}
	}
}
