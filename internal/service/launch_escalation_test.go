package service

import (
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/runlog"
)

func TestLaunchEscalationWarnsAfterTwoTaskStageFailures(t *testing.T) {
	s := newTestService(t)
	dir := t.TempDir()
	current := &runlog.Run{ID: "r-current", ProjectID: "p", TaskID: "T-132", Stage: "build", Roster: map[string]string{"implementer": "luna"}}
	w, err := runlog.NewWriter(dir, current)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	rs := &runState{run: current, writer: w}
	s.runs = map[string]*runState{
		"r-first":  {run: &runlog.Run{ID: "r-first", ProjectID: "p", TaskID: "T-132", Stage: "build", Verdict: "FAILED"}},
		"r-second": {run: &runlog.Run{ID: "r-second", ProjectID: "p", TaskID: "T-132", Stage: "build", Verdict: "ABORTED"}},
		current.ID: rs,
	}

	s.emitLaunchEscalation(rs)
	events, err := runlog.ReadEvents(w.RunDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "escalation_suggestion" && event.Data["point"] == "launch" {
			return
		}
	}
	t.Fatal("two prior failed/aborted runs did not emit a launch escalation")
}

func TestWallclockEscalationTriggersWithHistory(t *testing.T) {
	s := newTestService(t)
	dir := t.TempDir()
	started := time.Now().Add(-130 * time.Second)
	current := &runlog.Run{ID: "r-current", ProjectID: "p", Mode: "solo", Status: "running", StartedAt: started.UTC().Format(time.RFC3339), ActiveSince: started.UTC().Format(time.RFC3339Nano)}
	w, err := runlog.NewWriter(dir, current)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	rs := &runState{run: current, writer: w, runDir: w.RunDir()}
	s.runs = map[string]*runState{current.ID: rs}
	for i := 0; i < 5; i++ {
		s.runs["history-"+string(rune('a'+i))] = &runState{run: &runlog.Run{
			ID: "history", ProjectID: "p", Mode: "solo", ActiveWallclockMs: 60_000,
			EndedAt: time.Now().UTC().Format(time.RFC3339),
		}}
	}

	s.checkWallclockEscalation(rs)
	events, err := runlog.ReadEvents(w.RunDir())
	if err != nil {
		t.Fatal(err)
	}
	var suggestion, requested, human bool
	for _, event := range events {
		suggestion = suggestion || event.Type == "escalation_suggestion"
		requested = requested || event.Type == "pause_requested"
		human = human || event.Type == "human_needed"
	}
	// The card is filed and the pause is REQUESTED; the run keeps its turn in
	// flight. Cancelling at once threw away a 110 s reviewer turn.
	if !suggestion || !requested || human || current.Status != "running" {
		t.Fatalf("escalation must request a pause, not cancel mid-turn: suggestion=%v requested=%v human=%v status=%s", suggestion, requested, human, current.Status)
	}
	// The turn ends: now the pause lands with its card.
	s.pauseAtSafePoint(rs)
	events, err = runlog.ReadEvents(w.RunDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		human = human || event.Type == "human_needed"
	}
	if !human || current.Status != "paused" || current.PendingKind != "history_duration" {
		t.Fatalf("the pause did not land at the safe point: human=%v status=%s pending=%s", human, current.Status, current.PendingKind)
	}
	// Idempotent: a second turn end does nothing.
	s.pauseAtSafePoint(rs)
}

func TestWallclockEscalationExcludesQueuedAndPausedTime(t *testing.T) {
	s := newTestService(t)
	dir := t.TempDir()
	current := &runlog.Run{ID: "r-current", ProjectID: "p", Mode: "solo", Status: "running", StartedAt: time.Now().Add(-10 * time.Hour).UTC().Format(time.RFC3339), ActiveWallclockMs: 30_000, ActiveSince: time.Now().UTC().Format(time.RFC3339Nano)}
	w, err := runlog.NewWriter(dir, current)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	rs := &runState{run: current, writer: w, runDir: w.RunDir()}
	s.runs = map[string]*runState{current.ID: rs}
	for i := 0; i < 5; i++ {
		s.runs["history-"+string(rune('a'+i))] = &runState{run: &runlog.Run{ID: "history", ProjectID: "p", Mode: "solo", ActiveWallclockMs: 60_000, EndedAt: time.Now().UTC().Format(time.RFC3339)}}
	}

	s.checkWallclockEscalation(rs)
	if current.Status != "running" {
		t.Fatalf("queued or paused time triggered escalation: %s", current.Status)
	}
}

func TestWallclockEscalationIsSilentWithoutEnoughHistory(t *testing.T) {
	s := newTestService(t)
	dir := t.TempDir()
	current := &runlog.Run{ID: "r-current", ProjectID: "p", Mode: "solo", Status: "running", StartedAt: time.Now().Add(-10 * time.Hour).UTC().Format(time.RFC3339), ActiveSince: time.Now().UTC().Format(time.RFC3339Nano)}
	w, err := runlog.NewWriter(dir, current)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	rs := &runState{run: current, writer: w, runDir: w.RunDir()}
	s.runs = map[string]*runState{current.ID: rs}
	for i := 0; i < 4; i++ {
		s.runs["history-"+string(rune('a'+i))] = &runState{run: &runlog.Run{
			ID: "history", ProjectID: "p", Mode: "solo", ActiveWallclockMs: 60_000,
			EndedAt: time.Now().UTC().Format(time.RFC3339),
		}}
	}

	s.checkWallclockEscalation(rs)
	events, err := runlog.ReadEvents(w.RunDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "escalation_suggestion" || event.Type == "human_needed" {
			t.Fatalf("insufficient history emitted %s", event.Type)
		}
	}
}

func TestWallclockEscalationMultiplierIsConfigurable(t *testing.T) {
	s := newTestService(t)
	s.cfg.Defaults.Budget.WallclockEscalationMultiplier = 3
	dir := t.TempDir()
	activeSince := time.Now().Add(-130 * time.Second).UTC().Format(time.RFC3339Nano)
	current := &runlog.Run{ID: "r-current", ProjectID: "p", Mode: "solo", Status: "running", StartedAt: time.Now().Add(-130 * time.Second).UTC().Format(time.RFC3339), ActiveSince: activeSince}
	w, err := runlog.NewWriter(dir, current)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	rs := &runState{run: current, writer: w, runDir: w.RunDir()}
	s.runs = map[string]*runState{current.ID: rs}
	for i := 0; i < 5; i++ {
		s.runs["history-"+string(rune('a'+i))] = &runState{run: &runlog.Run{
			ID: "history", ProjectID: "p", Mode: "solo", ActiveWallclockMs: 60_000,
			EndedAt: time.Now().UTC().Format(time.RFC3339),
		}}
	}

	s.checkWallclockEscalation(rs)
	events, err := runlog.ReadEvents(w.RunDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "escalation_suggestion" {
			t.Fatal("3x threshold fired at only about 2x history")
		}
	}
	if current.Status != "running" {
		t.Fatalf("configurable threshold paused run at %s", current.Status)
	}
}

func TestLaunchEscalationDoesNotWarnAfterOneFailure(t *testing.T) {
	s := newTestService(t)
	dir := t.TempDir()
	current := &runlog.Run{ID: "r-current", ProjectID: "p", TaskID: "T-132", Stage: "build", Roster: map[string]string{"implementer": "luna"}}
	w, err := runlog.NewWriter(dir, current)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	rs := &runState{run: current, writer: w}
	s.runs = map[string]*runState{
		"r-first":  {run: &runlog.Run{ID: "r-first", ProjectID: "p", TaskID: "T-132", Stage: "build", Verdict: "FAILED"}},
		current.ID: rs,
	}

	s.emitLaunchEscalation(rs)
	events, err := runlog.ReadEvents(w.RunDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "escalation_suggestion" {
			t.Fatal("one prior failure emitted a launch escalation")
		}
	}
}
