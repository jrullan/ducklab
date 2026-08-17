package service

import (
	"strings"
	"testing"
)

// The acceptance checkout demands the polarity the stage promised: a build
// must reproduce green, a test-first must reproduce an honest red — B-056
// made every TDD accept impossible by demanding green from a commit whose
// whole point is red.
func TestCheckoutPolarityFollowsTheStage(t *testing.T) {
	// The judgment layer only — exercised through the same helpers
	// verifyAcceptedCommit composes: green result vs stage expectations.
	greenOut := "ok  \tgithub.com/x/internal/service\t1.0s"
	redAssert := "--- FAIL: TestNewBehavior (0.01s)\n    x_test.go:10: want 202, got 404\nFAIL\nFAIL\tgithub.com/x/internal/service\t1.0s\nFAIL"
	redCompile := "# github.com/x/internal/service\nx_test.go:5:2: undefined: Nope\nFAIL\tgithub.com/x/internal/service [build failed]\nFAIL"

	if compileFailure(redAssert) {
		t.Error("assertion red judged as compile failure")
	}
	if !compileFailure(redCompile) {
		t.Error("compile red not recognized")
	}
	if strings.Contains(greenOut, "FAIL") {
		t.Error("fixture sanity")
	}
}
