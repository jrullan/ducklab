package bug

import (
	"strings"
	"testing"
)

func TestTheHappyPathIsWalkable(t *testing.T) {
	s := Open
	for _, next := range []Status{Triaged, InProgress, Fixed, Verified, Closed} {
		got, err := Move(s, next)
		if err != nil {
			t.Fatalf("%s -> %s: %v", s, next, err)
		}
		s = got
	}
}

// A bug that turns out not to be fixed is reopened as its own record, so the
// history of what was claimed and when survives. Rewinding a status erases the
// claim.
func TestNothingReturnsToOpen(t *testing.T) {
	for _, from := range []Status{Triaged, InProgress, Fixed, Verified, Closed, Duplicate, WontFix} {
		if CanMove(from, Open) {
			t.Errorf("%s was allowed back to open", from)
		}
	}
}

// Closing is what verified is for. A fix nobody re-ran the gate on is a fix
// nobody checked.
func TestAFixCannotCloseItself(t *testing.T) {
	if CanMove(Fixed, Closed) {
		t.Error("a fixed bug closed without being verified")
	}
	if !CanMove(Fixed, Verified) || !CanMove(Verified, Closed) {
		t.Error("the verified route is not walkable")
	}
}

// The caller is usually a person typing a command; refusing without saying
// what is allowed turns a mistake into a guessing game.
func TestARefusalSaysWhatIsAllowed(t *testing.T) {
	_, err := Move(Fixed, Open)
	if err == nil {
		t.Fatal("fixed -> open was allowed")
	}
	for _, want := range []string{"verified", "in_progress"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not offer %q: %v", want, err)
		}
	}
}

func TestAClosedBugIsFinished(t *testing.T) {
	_, err := Move(Closed, InProgress)
	if err == nil {
		t.Fatal("a closed bug was reopened")
	}
	if !strings.Contains(err.Error(), "finished") {
		t.Errorf("the refusal is unclear: %v", err)
	}
}

func TestMovingNowhereIsNotAnError(t *testing.T) {
	if got, err := Move(Triaged, Triaged); err != nil || got != Triaged {
		t.Errorf("re-stating a status failed: %v", err)
	}
}

// Worst first, oldest first within a severity: a critical reported on Monday
// must not be buried by one reported today.
func TestSortByUrgency(t *testing.T) {
	bugs := []Bug{
		{ID: "B-1", Severity: Low, CreatedAt: "2026-01-01"},
		{ID: "B-2", Severity: Critical, CreatedAt: "2026-03-01"},
		{ID: "B-3", Severity: Critical, CreatedAt: "2026-01-01"},
		{ID: "B-4", Severity: "spicy", CreatedAt: "2026-01-01"},
		{ID: "B-5", Severity: Normal, CreatedAt: "2026-01-01"},
	}
	SortByUrgency(bugs)
	got := []string{bugs[0].ID, bugs[1].ID, bugs[2].ID, bugs[3].ID, bugs[4].ID}
	want := []string{"B-3", "B-2", "B-5", "B-1", "B-4"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v (an unknown severity must sort last)", got, want)
		}
	}
}

func TestIsOpenIsAboutNeedingAttention(t *testing.T) {
	for _, s := range []Status{Open, Triaged, InProgress, Fixed} {
		if !(Bug{Status: s}).IsOpen() {
			t.Errorf("%s should still need attention", s)
		}
	}
	for _, s := range []Status{Verified, Closed, Duplicate, WontFix} {
		if (Bug{Status: s}).IsOpen() {
			t.Errorf("%s should not be waiting on anyone", s)
		}
	}
}
