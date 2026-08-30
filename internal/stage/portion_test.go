package stage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// A plan built for a small implementer is portioned for it: one primary
// deliverable, at most three criteria per task. Tasks of 6-11 criteria went
// to a local 35B seat (Neocapture, 2026-08-29).
func TestAPlanForASmallSeatIsPortioned(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(artifact.DocsDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path(root, artifact.KindSpec), []byte("## SPEC-001 — Shell\n\n**Implements:** REQ-001\n\nGTK4 shell.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	small, err := BuildPrompt(root, Plan, "", &artifact.Document{}, "", false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(small, "Portion the tasks for a small implementer") || !strings.Contains(small, "AT MOST THREE acceptance criteria") {
		t.Fatalf("the small-seat plan brief is not portioned:\n%s", small)
	}
	big, err := BuildPrompt(root, Plan, "", &artifact.Document{}, "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(big, "Portion the tasks") {
		t.Fatal("a plan for a full seat was portioned")
	}
	_ = filepath.Join
}
