package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/provider"
)

func TestSpecGateBlocksADeclaredReferenceContractViolation(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	projectID, _ := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — Gate observation\n\n**Priority:** must\n\nExpose gate observations.\n",
	})
	refs := t.TempDir()
	contractPath := filepath.Join(refs, "observe_gate.json")
	os.WriteFile(contractPath, []byte(`{
  "schema_version":"fledge.capability-conformance/v1",
  "operation":"observe_gate",
  "contract":{"output":{"required":["findings"],"optional":[],"additional_properties":false}},
  "cases":[{"id":"empty","expected":{"findings":[]}}]
}`), 0o644)

	fake := s.providers["fake"].(*provider.Fake)
	fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
		text := "## SPEC-001 — Gate observation\n\n**Implements:** REQ-001\n\n- `observe_gate`: `findings`, `error`\n"
		return &provider.ChatResponse{Choices: []provider.Choice{{
			Message: provider.Message{Role: "assistant", Content: text}, FinishReason: provider.FinishStop,
		}}}
	}
	run, err := s.StageStart(context.Background(), projectID, StageRequest{
		Stage: "spec", Mode: "solo", Refs: []string{refs},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	<-rs.done
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.Status != "paused" || detail.Run.PendingKind != "gate" || detail.Run.Verdict != "FAILED" {
		t.Fatalf("contract violation reached an approvable gate: status=%s pending=%s verdict=%s failure=%s", detail.Run.Status, detail.Run.PendingKind, detail.Run.Verdict, detail.Run.Failure)
	}
	joined := detail.Run.Warning
	if blockers, ok := detail.Run.PendingData["proposal_blockers"].([]string); ok {
		joined += " " + strings.Join(blockers, " ")
	} else {
		joined += " " + fmt.Sprint(detail.Run.PendingData["proposal_blockers"])
	}
	for _, want := range []string{"observe_gate", contractPath, "sha256:", `forbidden field "error"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("gate evidence lacks %q: %s", want, joined)
		}
	}
	for _, next := range detail.Run.Next {
		if next == "accept" {
			t.Fatalf("failed contract gate still offers accept: %v", detail.Run.Next)
		}
	}
}
