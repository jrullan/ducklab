package bug

import "testing"

// The rail had a case for open and a case for triaged, and nothing else. A
// report that reached in_progress — which is where promoting one puts it — sat
// with no button on it at all, and there was no way to move it by hand. The
// advice "just move it manually" described something that did not exist.
//
// Reported by the engine so a client cannot invent a move the loop forbids, and
// cannot miss one either.
func TestEveryStatusReportsItsLegalMoves(t *testing.T) {
	// Every state a bug can be in must either offer a move or be an ending.
	endings := map[Status]bool{Closed: true}
	for _, st := range []Status{Open, Triaged, Duplicate, WontFix, InProgress, Fixed, Verified, Closed} {
		next := NextFrom(st)
		if len(next) == 0 && !endings[st] {
			t.Errorf("%s offers no move and is not an ending: a bug there is stuck", st)
		}
		for _, to := range next {
			if !CanMove(st, to) {
				t.Errorf("%s offers %s, which CanMove refuses", st, to)
			}
		}
	}
}

// The state promoting a bug puts it in is the one that was stuck.
func TestInProgressCanBeMovedByHand(t *testing.T) {
	next := NextFrom(InProgress)
	if len(next) == 0 {
		t.Fatal("in_progress offers nothing")
	}
	var fixed bool
	for _, s := range next {
		if s == Fixed {
			fixed = true
		}
	}
	if !fixed {
		t.Errorf("in_progress cannot reach fixed: %v", next)
	}
}

// The caller must not be able to edit the loop's rules through what it is given.
func TestTheMoveListIsCopied(t *testing.T) {
	got := NextFrom(Triaged)
	if len(got) == 0 {
		t.Fatal("no moves from triaged")
	}
	got[0] = "tampered"
	if again := NextFrom(Triaged); again[0] == "tampered" {
		t.Error("the transition table was mutated through a returned slice")
	}
}
