package budget

import (
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
