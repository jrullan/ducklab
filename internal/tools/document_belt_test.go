package tools

import (
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

// A document council has no gate to run: verify_run and the diff tools are
// not in its belt at all, not merely discouraged (ten verify_run calls on a
// spec draft, benchmark run 3).
func TestTheDocumentToolbeltHasNoGateAndNoDiff(t *testing.T) {
	reg := NewRegistry()
	for _, tool := range []Tool{&FSRead{}, &FSSearch{}, &ArtifactRead{}, &VerifyRun{}, &GitDiff{}} {
		reg.Register(tool)
	}
	belt, err := reg.NarrowToolbelt(config.RoleReviewer, "document")
	if err != nil {
		t.Fatal(err)
	}
	has := map[string]bool{}
	for _, n := range belt {
		has[n] = true
	}
	if has["verify_run"] || has["git_diff"] {
		t.Fatalf("document belt still carries gate/diff tools: %v", belt)
	}
	if !has["fs_read"] || !has["artifact_read"] {
		t.Fatalf("document belt lost its reads: %v", belt)
	}
}
