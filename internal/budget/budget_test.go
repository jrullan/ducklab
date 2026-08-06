package budget

import (
	"strings"
	"testing"
	"time"
)

func TestBudgetExceeded(t *testing.T) {
	b := &Budget{MaxUSD: 1.0, MaxTokens: 1000, MaxTurns: 5, MaxWallclockS: 60}
	s := NewSpend()

	// Not exceeded
	if msg, ok := b.Exceeded(s); ok {
		t.Errorf("should not be exceeded: %s", msg)
	}

	// USD exceeded
	s.AddUSD(1.5)
	if msg, ok := b.Exceeded(s); !ok {
		t.Error("should be exceeded (USD)")
	} else {
		_ = msg
	}
}

func TestBudgetWouldExceed(t *testing.T) {
	b := &Budget{MaxUSD: 1.0}
	s := NewSpend()
	s.AddUSD(0.9)

	// Would exceed
	if msg, ok := b.WouldExceed(s, 1000000, 1000000, 0.20, 0.60); !ok {
		t.Error("should would-exceed")
	} else {
		_ = msg
	}

	// Would not exceed
	s2 := NewSpend()
	s2.AddUSD(0.1)
	if msg, ok := b.WouldExceed(s2, 1000, 1000, 0.20, 0.60); ok {
		t.Errorf("should not would-exceed: %s", msg)
	}
}

func TestBudgetPercentUsed(t *testing.T) {
	b := &Budget{MaxUSD: 2.0, MaxTokens: 1000}
	s := NewSpend()
	s.AddUSD(1.0)
	s.AddTokens(500)

	pct := b.PercentUsed(s)
	if pct["usd"] != 50.0 {
		t.Errorf("usd pct = %f, want 50.0", pct["usd"])
	}
	if pct["tokens"] != 50.0 {
		t.Errorf("tokens pct = %f, want 50.0", pct["tokens"])
	}
}

func TestSpendSnapshot(t *testing.T) {
	s := NewSpend()
	s.AddUSD(0.5)
	s.AddTokens(100)
	s.AddTurn()

	snap := s.Snapshot()
	if snap.USD != 0.5 {
		t.Errorf("USD = %f, want 0.5", snap.USD)
	}
	if snap.Tokens != 100 {
		t.Errorf("Tokens = %d, want 100", snap.Tokens)
	}
	if snap.Turns != 1 {
		t.Errorf("Turns = %d, want 1", snap.Turns)
	}
}

func TestTracker(t *testing.T) {
	b := &Budget{MaxUSD: 1.0, MaxTokens: 1000}
	tracker := NewTracker(b)

	tracker.Record(500, 300, 0.1)
	tracker.RecordTurn()

	if tracker.Spend.Snapshot().Tokens != 800 {
		t.Errorf("Tokens = %d, want 800", tracker.Spend.Snapshot().Tokens)
	}
	if tracker.Spend.Snapshot().Turns != 1 {
		t.Errorf("Turns = %d, want 1", tracker.Spend.Snapshot().Turns)
	}

	if msg, ok := tracker.Check(); ok {
		t.Errorf("should not be exceeded: %s", msg)
	}
}

func TestWallclock(t *testing.T) {
	s := NewSpend()
	time.Sleep(10 * time.Millisecond)
	s.UpdateWallclock()
	if s.Snapshot().WallclockS <= 0 {
		t.Error("wallclock should be positive")
	}
}

// UpdateWallclock existed and nothing called it, so WallclockS stayed at zero
// and Exceeded compared 0 >= MaxWallclockS — always false. The wallclock
// budget has never stopped anything: a run that sat ten minutes on its first
// turn should have been cut off by it and was not.
func TestWallclockBudgetActuallyStopsARun(t *testing.T) {
	tracker := NewTracker(&Budget{MaxWallclockS: 1})
	// Pretend the run started two seconds ago.
	tracker.Spend.StartTime = time.Now().Add(-2 * time.Second)

	msg, exceeded := tracker.Check()
	if !exceeded {
		t.Fatal("a run two seconds into a one-second budget was allowed to continue")
	}
	if !strings.Contains(msg, "wallclock") {
		t.Errorf("message = %q, should name the budget that stopped it", msg)
	}
}

// A run inside its budget must not be stopped, and the clock must be reported
// rather than left at zero.
func TestWallclockIsMeasuredNotAssumed(t *testing.T) {
	tracker := NewTracker(&Budget{MaxWallclockS: 3600})
	tracker.Spend.StartTime = time.Now().Add(-5 * time.Second)

	if _, exceeded := tracker.Check(); exceeded {
		t.Fatal("a run well inside its budget was stopped")
	}
	if got := tracker.Spend.Snapshot().WallclockS; got < 4 {
		t.Errorf("wallclock = %.1fs, want about 5: it is not being measured", got)
	}
}

// Lifting is per-cap on purpose: the person removes the ceiling that is
// binding, and the others keep guarding — lifting tokens leaves the dollar
// cap standing. Zero already means "no cap" to Exceeded, so a lifted cap is
// a zero cap, recorded by the caller.
func TestLiftRemovesOneCapAndKeepsTheRest(t *testing.T) {
	tr := NewTracker(&Budget{MaxUSD: 5, MaxTokens: 100, MaxTurns: 10})
	tr.Spend.AddTokens(150)
	if _, exceeded := tr.Check(); !exceeded {
		t.Fatal("150 of 100 tokens did not exceed")
	}
	was, err := tr.Lift("tokens")
	if err != nil || was != 100 {
		t.Fatalf("Lift = %v, %v; want 100, nil", was, err)
	}
	if msg, exceeded := tr.Check(); exceeded {
		t.Fatalf("the lifted cap still binds: %s", msg)
	}
	tr.Spend.AddUSD(9)
	if _, exceeded := tr.Check(); !exceeded {
		t.Fatal("lifting tokens disarmed the dollar cap too")
	}
	if _, err := tr.Lift("vibes"); err == nil {
		t.Fatal("an unknown cap name was accepted")
	}
}
