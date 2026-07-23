package project

import (
	"strings"
	"testing"
)

func TestAttemptsRoundtripAndMatch(t *testing.T) {
	repo := t.TempDir()
	AddAttempt(repo, Attempt{Goal: "Fix the fib bug", Mode: "plan", Reason: "reviewer rejected",
		Detail: "off-by-one still present", Diff: "- return b\n+ return b"})

	// same goal, different casing/whitespace still matches
	got := AttemptsFor(repo, "  fix   the FIB bug ")
	if len(got) != 1 || got[0].Reason != "reviewer rejected" {
		t.Fatalf("attempt not matched: %+v", got)
	}
	// a different goal does not match
	if len(AttemptsFor(repo, "add a footer")) != 0 {
		t.Error("unrelated goal should not match")
	}
}

func TestAttemptsContextAndClear(t *testing.T) {
	repo := t.TempDir()
	AddAttempt(repo, Attempt{Goal: "g", Mode: "driver", Reason: "tests red", Detail: "assert failed"})
	ctx := AttemptsContext(repo, "g")
	if !strings.Contains(ctx, "already FAILED") || !strings.Contains(ctx, "tests red") {
		t.Errorf("context missing failure info: %q", ctx)
	}

	// clearing on success drops the memory for that goal
	if err := ClearAttempts(repo, "g"); err != nil {
		t.Fatal(err)
	}
	if Count(repo, "g") != 0 {
		t.Error("attempts not cleared")
	}
	if AttemptsContext(repo, "g") != "" {
		t.Error("context should be empty after clear")
	}
}

func TestClearAttemptsKeepsOthers(t *testing.T) {
	repo := t.TempDir()
	AddAttempt(repo, Attempt{Goal: "keep me", Mode: "solo", Reason: "x"})
	AddAttempt(repo, Attempt{Goal: "drop me", Mode: "solo", Reason: "y"})
	ClearAttempts(repo, "drop me")
	if Count(repo, "keep me") != 1 {
		t.Error("clearing one goal must not drop others")
	}
	if Count(repo, "drop me") != 0 {
		t.Error("target goal should be cleared")
	}
}
