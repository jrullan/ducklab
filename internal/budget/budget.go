// Package budget handles budget accounting and enforcement for ducklab runs.
package budget

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Budget is a set of caps for a run.
type Budget struct {
	MaxUSD        float64 `json:"max_usd"`
	MaxTokens     int64   `json:"max_tokens"`
	MaxWallclockS int     `json:"max_wallclock_s"`
	MaxTurns      int     `json:"max_turns"`
}

// Spend tracks what has been spent in a run.
type Spend struct {
	USD        float64
	Tokens     int64
	Turns      int
	WallclockS float64
	StartTime  time.Time
	mu         sync.Mutex
}

// NewSpend creates a new spend tracker.
func NewSpend() *Spend {
	return &Spend{
		StartTime: time.Now(),
	}
}

// AddUSD adds to the USD spend.
func (s *Spend) AddUSD(usd float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.USD += usd
}

// AddTokens adds to the token spend.
func (s *Spend) AddTokens(tokens int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Tokens += tokens
}

// AddTurn adds a turn.
func (s *Spend) AddTurn() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Turns++
}

// UpdateWallclock updates the wallclock time.
func (s *Spend) UpdateWallclock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.WallclockS = time.Since(s.StartTime).Seconds()
}

// RestoreWallclock carries active execution time across a pause/resume. The
// Neocapture plan resumed with its tokens and turns intact but a fresh clock,
// turning a 30-minute cap into another 30 minutes.
func (s *Spend) RestoreWallclock(seconds float64) {
	if seconds < 0 {
		seconds = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.WallclockS = seconds
	s.StartTime = time.Now().Add(-time.Duration(seconds * float64(time.Second)))
}

// Snapshot returns a copy of the current spend.
func (s *Spend) Snapshot() Spend {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Spend{
		USD:        s.USD,
		Tokens:     s.Tokens,
		Turns:      s.Turns,
		WallclockS: s.WallclockS,
		StartTime:  s.StartTime,
	}
}

// Exceeded checks if any budget cap is exceeded.
func (b *Budget) Exceeded(spend *Spend) (string, bool) {
	s := spend.Snapshot()
	if b.MaxUSD > 0 && s.USD >= b.MaxUSD {
		return fmt.Sprintf("budget exceeded: $%.4f >= $%.4f", s.USD, b.MaxUSD), true
	}
	if b.MaxTokens > 0 && s.Tokens >= b.MaxTokens {
		return fmt.Sprintf("token budget exceeded: %d >= %d", s.Tokens, b.MaxTokens), true
	}
	if b.MaxWallclockS > 0 && s.WallclockS >= float64(b.MaxWallclockS) {
		return fmt.Sprintf("wallclock budget exceeded: %.0fs >= %ds", s.WallclockS, b.MaxWallclockS), true
	}
	if b.MaxTurns > 0 && s.Turns >= b.MaxTurns {
		return fmt.Sprintf("turn budget exceeded: %d >= %d", s.Turns, b.MaxTurns), true
	}
	return "", false
}

// WouldExceed checks if a proposed call would exceed the budget.
// Uses worst-case estimate: prompt_tokens_estimate * input + max_tokens * output.
func (b *Budget) WouldExceed(spend *Spend, estimatedPromptTokens int, maxOutputTokens int, inputPerMTok, outputPerMTok float64) (string, bool) {
	s := spend.Snapshot()

	// Worst case: all estimated prompt tokens + all possible output tokens
	worstCaseUSD := float64(estimatedPromptTokens)/1e6*inputPerMTok + float64(maxOutputTokens)/1e6*outputPerMTok
	if b.MaxUSD > 0 && s.USD+worstCaseUSD > b.MaxUSD {
		return fmt.Sprintf("budget would be exceeded: $%.4f + $%.4f worst case > $%.4f",
			s.USD, worstCaseUSD, b.MaxUSD), true
	}
	if b.MaxTokens > 0 && s.Tokens+int64(estimatedPromptTokens+maxOutputTokens) > b.MaxTokens {
		return fmt.Sprintf("token budget would be exceeded: %d + %d > %d",
			s.Tokens, estimatedPromptTokens+maxOutputTokens, b.MaxTokens), true
	}
	if b.MaxTurns > 0 && s.Turns+1 > b.MaxTurns {
		return fmt.Sprintf("turn budget would be exceeded: %d + 1 > %d",
			s.Turns, b.MaxTurns), true
	}
	return "", false
}

// Remaining returns the remaining budget in each dimension.
func (b *Budget) Remaining(spend *Spend) Budget {
	s := spend.Snapshot()
	return Budget{
		MaxUSD:        maxFloat(0, b.MaxUSD-s.USD),
		MaxTokens:     max(0, b.MaxTokens-s.Tokens),
		MaxWallclockS: int(maxFloat(0, float64(b.MaxWallclockS)-s.WallclockS)),
		MaxTurns:      int(max(0, int64(b.MaxTurns)-int64(s.Turns))),
	}
}

// PercentUsed returns the percentage of budget used (0-100).
func (b *Budget) PercentUsed(spend *Spend) map[string]float64 {
	s := spend.Snapshot()
	result := make(map[string]float64)
	if b.MaxUSD > 0 {
		result["usd"] = s.USD / b.MaxUSD * 100
	}
	if b.MaxTokens > 0 {
		result["tokens"] = float64(s.Tokens) / float64(b.MaxTokens) * 100
	}
	if b.MaxWallclockS > 0 {
		result["wallclock"] = s.WallclockS / float64(b.MaxWallclockS) * 100
	}
	if b.MaxTurns > 0 {
		result["turns"] = float64(s.Turns) / float64(b.MaxTurns) * 100
	}
	return result
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// Tracker tracks budget across a run.
type Tracker struct {
	Budget *Budget
	Spend  *Spend
	// mu guards Budget: the agent loop reads the caps before every model
	// call, and a human may lift one WHILE the run is going — the whole
	// point of lifting is that the run is alive to save.
	mu sync.Mutex
}

// NewTracker creates a new budget tracker.
func NewTracker(b *Budget) *Tracker {
	return &Tracker{
		Budget: b,
		Spend:  NewSpend(),
	}
}

// Check checks if the budget is exceeded.
func (t *Tracker) Check() (string, bool) {
	// Refresh the clock before judging it.
	//
	// UpdateWallclock existed and nothing called it, so Spend.WallclockS stayed
	// at zero and Exceeded compared 0 >= MaxWallclockS — always false. The
	// wallclock budget has never stopped anything. A run that sat ten minutes
	// on its first turn should have been cut off by it and was not.
	//
	// Check runs before every model call, which is the only place a run can be
	// stopped anyway.
	t.Spend.UpdateWallclock()
	t.mu.Lock()
	b := *t.Budget
	t.mu.Unlock()
	return b.Exceeded(t.Spend)
}

// CheckWallclock refreshes and checks only the active-time ceiling. It is used
// after a provider response: token/cost ceilings deliberately stop before the
// next call so a tool action already returned by the model is not discarded.
func (t *Tracker) CheckWallclock() (string, bool) {
	t.Spend.UpdateWallclock()
	t.mu.Lock()
	maxWallclock := t.Budget.MaxWallclockS
	t.mu.Unlock()
	if maxWallclock > 0 {
		spent := t.Spend.Snapshot().WallclockS
		if spent >= float64(maxWallclock) {
			return fmt.Sprintf("wallclock budget exceeded: %.0fs >= %ds", spent, maxWallclock), true
		}
	}
	return "", false
}

// Context returns a context that expires when the run's remaining active
// wall-clock budget does. Checking only between model calls let a long local
// generation cross the cap and continue until an unrelated validator failed.
func (t *Tracker) Context(parent context.Context) (context.Context, context.CancelFunc) {
	t.Spend.UpdateWallclock()
	t.mu.Lock()
	maxWallclock := t.Budget.MaxWallclockS
	t.mu.Unlock()
	if maxWallclock <= 0 {
		return context.WithCancel(parent)
	}
	remainingSeconds := float64(maxWallclock) - t.Spend.Snapshot().WallclockS
	remaining := time.Duration(remainingSeconds * float64(time.Second))
	if remaining <= 0 {
		remaining = time.Nanosecond
	}
	return context.WithTimeout(parent, remaining)
}

// WouldExceed checks if a proposed call would exceed the budget.
func (t *Tracker) WouldExceed(estimatedPromptTokens, maxOutputTokens int, inputPerMTok, outputPerMTok float64) (string, bool) {
	t.mu.Lock()
	b := *t.Budget
	t.mu.Unlock()
	return b.WouldExceed(t.Spend, estimatedPromptTokens, maxOutputTokens, inputPerMTok, outputPerMTok)
}

// Lift removes ONE cap from a live run's budget, returning what it was.
// Zero already means "no cap" to Exceeded and WouldExceed, so lifting is
// setting the cap to zero — one-way for the run's remaining life, and the
// caller records who did it and when. The other caps keep guarding: lifting
// tokens leaves the dollar ceiling standing, which is the reason lifting is
// per-cap instead of a single kill switch.
func (t *Tracker) Lift(kind string) (was float64, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch kind {
	case "tokens":
		was, t.Budget.MaxTokens = float64(t.Budget.MaxTokens), 0
	case "usd":
		was, t.Budget.MaxUSD = t.Budget.MaxUSD, 0
	case "turns":
		was, t.Budget.MaxTurns = float64(t.Budget.MaxTurns), 0
	case "wallclock":
		was, t.Budget.MaxWallclockS = float64(t.Budget.MaxWallclockS), 0
	default:
		return 0, fmt.Errorf("invalid kind %q: no budget cap named %q — one of tokens, usd, turns, wallclock", kind, kind)
	}
	return was, nil
}

// Record records a completed model call.
func (t *Tracker) Record(promptTokens, completionTokens int, costUSD float64) {
	t.Spend.AddTokens(int64(promptTokens + completionTokens))
	t.Spend.AddUSD(costUSD)
}

// RecordTurn records a completed turn.
func (t *Tracker) RecordTurn() {
	t.Spend.AddTurn()
}
