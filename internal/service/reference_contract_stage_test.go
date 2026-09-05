package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/provider"
)

func TestSpecMaterializesDeclaredReferenceContractInsteadOfTrustingModelCopy(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	projectID, projectRoot := projectWithDocs(t, s, map[artifact.Kind]string{
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
	sawExactBlock := false
	fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
		for _, message := range req.Messages {
			sawExactBlock = sawExactBlock || (strings.Contains(message.Content, "```ducklab-reference-contracts") &&
				strings.Contains(message.Content, `"required": [`) &&
				strings.Contains(message.Content, `"optional": []`) &&
				strings.Contains(message.Content, `"additional_properties": false`))
		}
		text := "## SPEC-001 — Gate observation\n\n**Implements:** REQ-001\n\nThe model emits useful prose but no control metadata.\n"
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
	if detail.Run.Status != "paused" || detail.Run.PendingKind != "gate" {
		t.Fatalf("materialized contract did not reach a decision gate: status=%s pending=%s verdict=%s failure=%s", detail.Run.Status, detail.Run.PendingKind, detail.Run.Verdict, detail.Run.Failure)
	}
	if !sawExactBlock {
		t.Fatal("architect prompt did not receive the exact authoritative block")
	}
	proposal, err := artifact.LoadProposed(projectRoot, artifact.KindSpec)
	if err != nil {
		t.Fatal(err)
	}
	rendered := artifact.RenderBody(proposal)
	for _, want := range []string{"ducklab-reference-contracts:begin", `"observe_gate"`, `"findings"`, contractPath, "sha256:"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("materialized proposal lacks %q: %s", want, rendered)
		}
	}
	if strings.Contains(rendered, `"error"`) {
		t.Errorf("model-authored forbidden field survived machine-owned replacement: %s", rendered)
	}
	for _, next := range detail.Run.Next {
		if next == "accept" {
			return
		}
	}
	t.Fatalf("materialized valid contract does not offer accept: next=%v warning=%s pending=%v", detail.Run.Next, detail.Run.Warning, detail.Run.PendingData)
}

func TestSpecGateMaterializesReferenceContractDespiteTypedProse(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	projectID, _ := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — Gate observation\n\n**Priority:** must\n\nExpose gate observations.\n",
	})
	refs := t.TempDir()
	os.WriteFile(filepath.Join(refs, "observe_gate.json"), []byte(`{
  "schema_version":"fledge.capability-conformance/v1",
  "operation":"observe_gate",
  "contract":{"output":{"required":["findings"],"optional":[],"additional_properties":false}},
  "cases":[{"id":"empty","expected":{"findings":[]}}]
}`), 0o644)

	fake := s.providers["fake"].(*provider.Fake)
	fake.ScriptFunc = func(provider.ChatRequest, int) *provider.ChatResponse {
		text := "## SPEC-001 — Gate observation\n\n**Implements:** REQ-001\n\n" +
			"The typed output is `observe_gate: findings: [Finding]`; another narrative mention of `observe_gate` is harmless.\n"
		return &provider.ChatResponse{Choices: []provider.Choice{{
			Message: provider.Message{Role: "assistant", Content: text}, FinishReason: provider.FinishStop,
		}}}
	}
	run, err := s.StageStart(context.Background(), projectID, StageRequest{Stage: "spec", Mode: "solo", Refs: []string{refs}})
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
	if detail.Run.Status != "paused" || detail.Run.PendingKind != "gate" {
		t.Fatalf("valid contract did not reach decision gate: status=%s pending=%s failure=%s", detail.Run.Status, detail.Run.PendingKind, detail.Run.Failure)
	}
	for _, next := range detail.Run.Next {
		if next == "accept" {
			return
		}
	}
	t.Fatalf("valid contract gate does not offer accept: next=%v warning=%s pending=%v", detail.Run.Next, detail.Run.Warning, detail.Run.PendingData)
}
