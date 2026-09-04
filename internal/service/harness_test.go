package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/tools"
)

func TestReviewerRemediesContradictingActiveCapabilitiesAreNormalized(t *testing.T) {
	ectx := &tools.ExecContext{ActiveCapabilities: []string{"gtk4-ui"}}
	attachReviewContractValidator(ectx)
	verdict := &agent.Verdict{Verdict: "request-changes", Findings: []agent.Finding{{
		Severity: "major", File: "ui.c", Issue: "overlay is not always above", Fix: "Add gtk_window_set_keep_above(window, TRUE).",
	}}}
	changed, err := ectx.NormalizeContract(config.RoleReviewer, "verdict:native", verdict)
	if err != nil || !changed {
		t.Fatalf("normalize = %v, %v", changed, err)
	}
	if !verdict.Approved() || len(verdict.Findings) != 0 {
		t.Fatalf("normalized verdict = %+v", verdict)
	}
}

func TestSelfNegatingReviewerFindingIsNormalized(t *testing.T) {
	ectx := &tools.ExecContext{}
	attachReviewContractValidator(ectx)
	verdict := &agent.Verdict{Verdict: "request-changes", Findings: []agent.Finding{{
		Severity: "major", File: "worker.c", Issue: "field may be uninitialized",
		Fix: "Initialize it with init_result(), which already does this; verify it is called.",
	}}}
	changed, err := ectx.NormalizeContract(config.RoleReviewer, "verdict:native", verdict)
	if err != nil || !changed {
		t.Fatalf("normalize = %v, %v", changed, err)
	}
	if !verdict.Approved() || len(verdict.Findings) != 0 {
		t.Fatalf("self-negating finding survived: %+v", verdict)
	}
}

func TestSpeculativeReviewerFindingCannotDelegateVerification(t *testing.T) {
	ectx := &tools.ExecContext{}
	attachReviewContractValidator(ectx)
	verdict := &agent.Verdict{Verdict: "request-changes", Findings: []agent.Finding{{
		Severity: "minor", File: "meson.build", Issue: "The source might be missing from the build target.",
		Fix: "Verify the source list and include directories.",
	}}}
	changed, err := ectx.NormalizeContract(config.RoleReviewer, "verdict:native", verdict)
	if err != nil || !changed || !verdict.Approved() || len(verdict.Findings) != 0 {
		t.Fatalf("speculative finding survived: changed=%v err=%v verdict=%+v", changed, err, verdict)
	}
}

func TestReviewerMustAccountForEveryAcceptanceProbe(t *testing.T) {
	ectx := &tools.ExecContext{TaskAcceptanceProbes: []string{"./app --help", "! ./app --save"}}
	attachReviewContractValidator(ectx)
	verdict := &agent.Verdict{Verdict: "approve", Findings: []agent.Finding{}, AcceptanceEvidence: []agent.AcceptanceEvidence{
		{Slice: 1, Status: "pass", Evidence: "stdout contains Usage and process exits 0"},
	}}
	if _, err := ectx.NormalizeContract(config.RoleReviewer, "verdict", verdict); err == nil || !strings.Contains(err.Error(), "exactly 2 entries") {
		t.Fatalf("partial acceptance evidence was accepted: %v", err)
	}
	verdict.AcceptanceEvidence = append(verdict.AcceptanceEvidence, agent.AcceptanceEvidence{Slice: 2, Status: "fail", Evidence: "process exits 0 without a save path"})
	if _, err := ectx.NormalizeContract(config.RoleReviewer, "verdict", verdict); err == nil || !strings.Contains(err.Error(), "approval requires") {
		t.Fatalf("approval with a failed slice was accepted: %v", err)
	}
	verdict.Verdict = "request-changes"
	if _, err := ectx.NormalizeContract(config.RoleReviewer, "verdict", verdict); err != nil {
		t.Fatalf("dissent with complete probe evidence was rejected: %v", err)
	}
}

func TestGLibNormalizerDropsInvalidRemedyButKeepsValidDissent(t *testing.T) {
	ectx := &tools.ExecContext{ActiveCapabilities: []string{"glib-async"}}
	attachReviewContractValidator(ectx)
	verdict := &agent.Verdict{Verdict: "request-changes", Findings: []agent.Finding{
		{Severity: "major", File: "worker.c", Issue: "idle source may fail", Fix: "Check the return value of g_idle_add; if it returns 0, complete the task with an error."},
		{Severity: "major", File: "worker.c", Issue: "serialization blocks the main context", Fix: "Move PNG serialization to the worker."},
	}}
	changed, err := ectx.NormalizeContract(config.RoleReviewer, "verdict:native", verdict)
	if err != nil || !changed {
		t.Fatalf("normalize = %v, %v", changed, err)
	}
	if verdict.Approved() || len(verdict.Findings) != 1 || !strings.Contains(verdict.Findings[0].Issue, "serialization") {
		t.Fatalf("valid dissent was not preserved: %+v", verdict)
	}
}

func TestHarnessProfileIsResolvedPersistedAndEmittedOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[package]\nname='fixture'\nversion='0.1.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{ID: "r-profile", ProjectID: "fixture", Stage: "build", Mode: "pair", TaskID: "T-004"}
	writer, err := runlog.NewWriter(root, run)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	rs := &runState{run: run, writer: writer}
	project := config.DefaultProject("fixture", "Fixture")
	project.Verify.Mode = "custom"
	project.Verify.Custom = "cargo test"

	first, err := ensureHarnessProfile(rs, root, project, "cc -fsyntax-only capture.c", []string{"./capture --help"}, []string{"src/base.c"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ensureHarnessProfile(rs, root, project, "this changed but a resume must not re-resolve", []string{"false"}, []string{"src/changed.c"})
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !strings.Contains(first, "rust (Cargo.toml)") || !strings.Contains(first, "cc -fsyntax-only capture.c") ||
		!strings.Contains(first, "./capture --help") || !strings.Contains(first, "src/base.c") || strings.Contains(first, "src/changed.c") {
		t.Fatalf("capsule was not stable or complete:\n%s", first)
	}
	state, err := os.ReadFile(filepath.Join(root, ".ducklab", "runs", run.ID, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), `"harness_profile"`) || !strings.Contains(string(state), `"c-native"`) {
		t.Fatalf("profile not persisted:\n%s", state)
	}
	events, err := os.ReadFile(filepath.Join(root, ".ducklab", "runs", run.ID, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(events), `"type":"capabilities_resolved"`); got != 1 {
		t.Fatalf("capability event count = %d, want 1:\n%s", got, events)
	}
}

func TestHarnessCapsuleCarriesResolvedStackRules(t *testing.T) {
	profile := &runlog.HarnessProfile{ReviewRules: []runlog.HarnessReviewRule{{
		Capability: "x11-image", ID: "pixel-layout", Guidance: "Honor bytes_per_line.",
	}}}
	got := harnessCapsule(profile)
	if !strings.Contains(got, "Stack invariant (x11-image/pixel-layout): Honor bytes_per_line.") {
		t.Fatalf("capsule lacks stack rule:\n%s", got)
	}
}
