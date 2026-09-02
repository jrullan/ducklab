package strategy

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
)

func TestNativeDiffRequiresAResourceConcurrencyAndRepresentationSweep(t *testing.T) {
	var reviewerPrompt string
	var reviewerContract string
	params := &ExecuteParams{
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, prompt string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			if turn.Role == config.RoleReviewer {
				reviewerPrompt = prompt
				reviewerContract = turn.Contract
				return verdictOutcome("approve"), nil
			}
			return &agent.Outcome{Text: "implemented"}, nil
		},
		Roster: map[config.Role]config.DucklingID{config.RoleImplementer: "impl", config.RoleReviewer: "review"},
		Diff: func() (string, error) {
			return "diff --git a/src/capture.c b/src/capture.c\n--- /dev/null\n+++ b/src/capture.c\n+void capture(void) {}\n", nil
		},
		Gate: func(context.Context) (string, string, error) { return "green", "", nil },
	}
	if _, err := ExecuteScript(context.Background(), PairScript(), params); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Native-code review sweep", "every early error/cleanup path", "signals its waiter", "allocator-family", "thread lifetime", "masks", "byte order", "alpha", "invalid handle", "callback user_data", "retained past the public call", "documented results match", "comments do not claim"} {
		if !strings.Contains(reviewerPrompt, want) {
			t.Errorf("native reviewer prompt lacks %q:\n%s", want, reviewerPrompt)
		}
	}
	if reviewerContract != "verdict:native" {
		t.Fatalf("native reviewer contract = %q, want verdict:native", reviewerContract)
	}
}

func TestNonNativeDiffDoesNotReceiveTheNativeSweep(t *testing.T) {
	if nativeCodeDiff("diff --git a/app.go b/app.go\n+package app\n") {
		t.Fatal("Go diff was classified as native C/C++")
	}
}
