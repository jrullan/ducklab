package strategy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/tools"
)

// Round 2's reviewer is told what moved since its own round-1 review and
// what it already read — and still nothing of the implementer's.
func TestReviewerRemembersItsOwnPreviousRound(t *testing.T) {
	rec := &recorder{}
	diffs := []string{
		"diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-x\n+y\n" +
			"diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-p\n+q\n",
		"diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-x\n+z\n" +
			"diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-p\n+q\n",
	}
	call := 0
	params := pairParams(rec, "red",
		editsOutcome("first attempt, secretly I think the spec is wrong"),
		&agent.Outcome{
			Parsed: &agent.Verdict{Verdict: "request-changes", Findings: []agent.Finding{{Severity: "major", File: "a.go", Line: 1, Issue: "y is wrong", Fix: "make it z"}}},
			ToolCalls: []agent.ToolCallRecord{
				{Name: "fs_read", Args: json.RawMessage(`{"path":"conftest.py","start":1,"end":240}`), Result: &tools.Result{Content: "ok"}},
				{Name: "fs_search", Args: json.RawMessage(`{"pattern":"def create_app"}`), Result: &tools.Result{Content: "ok"}},
				{Name: "fs_read", Args: json.RawMessage(`{"path":"missing.py"}`), Result: &tools.Result{IsError: true, Content: "no such file"}},
			},
		},
		editsOutcome("second attempt"),
		verdictOutcome("approve"),
	)
	params.Diff = func() (string, error) {
		// Round 1 sees diffs[0] (both reviewer prompt and remember); round 2 sees diffs[1].
		d := diffs[0]
		if call >= 2 {
			d = diffs[1]
		}
		call++
		return d, nil
	}
	params.Gate = func(context.Context) (string, string, error) {
		if call >= 2 {
			return "green", "", nil
		}
		return "red", "", nil
	}
	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	var reviewerPrompts []string
	for i, role := range rec.roles {
		if role == config.RoleReviewer {
			reviewerPrompts = append(reviewerPrompts, rec.prompts[i])
		}
	}
	if len(reviewerPrompts) != 2 {
		t.Fatalf("expected two reviewer rounds, got %d: %v", len(reviewerPrompts), rec.roles)
	}
	if strings.Contains(reviewerPrompts[0], "Since your last review") {
		t.Error("round 1 must not claim a previous review")
	}
	p2 := reviewerPrompts[1]
	for _, want := range []string{"Since your last review", "Hunks changed since your review: a.go", "Unchanged since your review: b.go", "fs_read conftest.py:1-240", "fs_search def create_app", "y is wrong"} {
		if !strings.Contains(p2, want) {
			t.Errorf("round-2 reviewer prompt lacks %q:\n%s", want, p2)
		}
	}
	if strings.Contains(p2, "missing.py") {
		t.Error("a failed read is not something it already read")
	}
	if strings.Contains(p2, "secretly I think") {
		t.Error("the implementer's words leaked into the reviewer's memory")
	}
}
