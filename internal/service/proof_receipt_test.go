package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/runlog"
)

// An acceptance receipt is evidence for someone who has the repository but not
// the engine that accepted it. It must carry only the stated stable facts and
// the proof command must reject both fabricated facts and incomplete evidence.
func TestAcceptedRunWritesReceiptThatProofVerifyCanCheck(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	projectID, dir := projectWithDocs(t, s, nil)
	git := gitProject(t, dir)
	if _, err := s.ProjectUpdate(context.Background(), projectID, map[string]string{
		"verify.mode":  "tests",
		"verify.tests": "true",
	}); err != nil {
		t.Fatal(err)
	}

	base := mustHead(t, git)
	snapshot, err := git.SnapshotTree()
	if err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{
		ID:               "r-proof-receipt",
		ProjectID:        projectID,
		TaskID:           "T-109",
		Stage:            "build",
		Status:           "paused",
		Verdict:          "PASSED",
		PendingKind:      "gate",
		TreeSnapshot:     snapshot,
		TreeSnapshotHead: base,
		StartedAt:        time.Now().UTC().Format(time.RFC3339Nano),
	}
	writer, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	writer.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("accepted proof change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunAcceptAs(context.Background(), run.ID, "proof evidence", "auditor"); err != nil {
		t.Fatalf("accept: %v", err)
	}

	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(dir, ".ducklab", "runs", run.ID, "receipt.json")
	receiptBytes, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatalf("accepted run did not write its standalone receipt at %s: %v", receiptPath, err)
	}
	assertReceiptFacts(t, receiptBytes, dir, base, detail.Run)

	binary := filepath.Join(t.TempDir(), "ducklab")
	if out, err := exec.Command("go", "build", "-o", binary, "github.com/jrullan/ducklab/cmd/ducklab").CombinedOutput(); err != nil {
		t.Fatalf("build ducklab: %v\n%s", err, out)
	}
	proofVerify(t, binary, dir, receiptPath, 0)

	var tampered map[string]any
	if err := json.Unmarshal(receiptBytes, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered["exit_code"] = float64(1)
	writeReceiptJSON(t, receiptPath, tampered)
	proofVerify(t, binary, dir, receiptPath, 1)

	if err := json.Unmarshal(receiptBytes, &tampered); err != nil {
		t.Fatal(err)
	}
	delete(tampered, "base_sha")
	writeReceiptJSON(t, receiptPath, tampered)
	proofVerify(t, binary, dir, receiptPath, 1)
}

type acceptanceReceipt struct {
	BaseSHA             string  `json:"base_sha"`
	HeadSHA             string  `json:"head_sha"`
	DiffSHA256          string  `json:"diff_sha256"`
	GateCommand         string  `json:"gate_command"`
	ExitCode            int     `json:"exit_code"`
	DurationS           float64 `json:"duration_s"`
	ReproductionVerdict string  `json:"reproduction_verdict"`
	AcceptedBy          string  `json:"accepted_by"`
	AcceptedAt          string  `json:"accepted_at"`
}

func assertReceiptFacts(t *testing.T, raw []byte, repo, base string, run *runlog.Run) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("receipt is not JSON: %v", err)
	}
	wantFields := []string{"base_sha", "head_sha", "diff_sha256", "gate_command", "exit_code", "duration_s", "reproduction_verdict", "accepted_by", "accepted_at"}
	if len(fields) != len(wantFields) {
		t.Fatalf("receipt fields = %v, want exactly %v", receiptFieldNames(fields), wantFields)
	}
	for _, name := range wantFields {
		if _, ok := fields[name]; !ok {
			t.Errorf("receipt missing required field %q", name)
		}
	}
	var receipt acceptanceReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.BaseSHA != base || receipt.HeadSHA != run.CommitSHA {
		t.Errorf("receipt shas = base %q head %q, want base %q head %q", receipt.BaseSHA, receipt.HeadSHA, base, run.CommitSHA)
	}
	diff := exec.Command("git", "-c", "color.ui=false", "-c", "diff.external=", "diff", "--no-ext-diff", "--binary", base, run.CommitSHA)
	diff.Dir = repo
	if out, err := diff.Output(); err != nil {
		t.Fatalf("derive binary diff: %v", err)
	} else if want := sha256Hex(out); receipt.DiffSHA256 != want {
		t.Errorf("diff_sha256 = %q, want SHA-256 of git diff --binary %q", receipt.DiffSHA256, want)
	}
	if run.GateReproduced == nil {
		t.Fatal("accepted run has no recorded clean-checkout reproduction")
	}
	if receipt.GateCommand != run.GateReproduced.Command || receipt.ExitCode != run.GateReproduced.ExitCode || receipt.DurationS != run.GateReproduced.Duration {
		t.Errorf("receipt gate facts = command %q exit %d duration %v, want %+v", receipt.GateCommand, receipt.ExitCode, receipt.DurationS, run.GateReproduced)
	}
	if receipt.ReproductionVerdict != "green" {
		t.Errorf("reproduction_verdict = %q, want green", receipt.ReproductionVerdict)
	}
	if receipt.AcceptedBy != "auditor" {
		t.Errorf("accepted_by = %q, want auditor", receipt.AcceptedBy)
	}
	if receipt.AcceptedAt != run.EndedAt {
		t.Errorf("accepted_at = %q, want acceptance time %q", receipt.AcceptedAt, run.EndedAt)
	}
	if _, err := time.Parse(time.RFC3339Nano, receipt.AcceptedAt); err != nil {
		t.Errorf("accepted_at = %q is not an RFC3339 timestamp: %v", receipt.AcceptedAt, err)
	}
}

func receiptFieldNames(fields map[string]json.RawMessage) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	return names
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeReceiptJSON(t *testing.T, path string, receipt map[string]any) {
	t.Helper()
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func proofVerify(t *testing.T, binary, repo, receipt string, wantCode int) {
	t.Helper()
	cmd := exec.Command(binary, "proof", "verify", receipt)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); exitCode(err) != wantCode {
		t.Fatalf("ducklab proof verify exit = %d, want %d; err = %v\n%s", exitCode(err), wantCode, err, out)
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if exited, ok := err.(*exec.ExitError); ok {
		return exited.ExitCode()
	}
	return -1
}
