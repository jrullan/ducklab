package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

// B-500's own timeline, minute for minute: TI-36X T-005 worked 27 min, waited
// 33 min on a question, worked 57 min after the answer and paused at its gate.
// The record said 27.5 min. Each transition is the real helper every status
// write now goes through.
func TestActiveWallclockAccumulatesEveryWorkingSegment(t *testing.T) {
	t0 := time.Date(2026, 10, 4, 21, 27, 15, 0, time.UTC)
	at := func(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }
	run := &runlog.Run{ID: "r-b500"}

	setRunStatus(run, "running", at(0))
	setRunStatus(run, "paused", at(27)) // the question
	if got := time.Duration(run.ActiveWallclockMs) * time.Millisecond; got != 27*time.Minute {
		t.Fatalf("after the question pause active = %v, want 27m", got)
	}
	// The 33 minutes the person took to answer are not work, whoever asks.
	if got := activeWallclock(run, at(59)); got != 27*time.Minute {
		t.Fatalf("while waiting on the answer active reads %v, want 27m", got)
	}
	setRunStatus(run, "running", at(60)) // the answer resumes it
	if got := activeWallclock(run, at(90)); got != 57*time.Minute {
		t.Fatalf("thirty minutes into the second segment active reads %v, want 57m", got)
	}
	setRunStatus(run, "paused", at(117)) // the gate
	if got := time.Duration(run.ActiveWallclockMs) * time.Millisecond; got != 84*time.Minute {
		t.Fatalf("at the gate active = %v, want 27m + 57m = 84m", got)
	}
	if run.ActiveSince != "" {
		t.Fatalf("the gate left the segment open since %s", run.ActiveSince)
	}
	setRunStatus(run, "done", at(135)) // rejected 18 minutes later
	if got := time.Duration(run.ActiveWallclockMs) * time.Millisecond; got != 84*time.Minute {
		t.Fatalf("after the decision active = %v — the gate wait was counted as work", got)
	}
}

// The old settle read the open segment through activeWallclock, which counts
// it only while Status is "running": a path that wrote "paused" first and
// settled second recorded nothing for the segment.
func TestSettleCountsTheOpenSegmentWhateverTheStatusSays(t *testing.T) {
	t0 := time.Date(2026, 10, 4, 22, 27, 20, 0, time.UTC)
	run := &runlog.Run{Status: "paused", ActiveWallclockMs: 60_000, ActiveSince: t0.Format(time.RFC3339Nano)}
	settleActiveWallclock(run, t0.Add(10*time.Minute))
	if run.ActiveWallclockMs != 60_000+10*60_000 {
		t.Fatalf("active = %dms, want the 10-minute segment added to 60000", run.ActiveWallclockMs)
	}
	if run.ActiveSince != "" {
		t.Fatal("settle left the segment open")
	}
}

// Double start and double settle are both reachable: RunResume opens the
// clock and then the queue's start opens it again; the escalation monitor
// starts it on its own goroutine and settles it on exit, after the run's own
// pause already did. None of these may count anything twice, and a start on a
// run that is not running must not turn a pause into work.
func TestActiveWallclockNeverCountsTwice(t *testing.T) {
	t0 := time.Date(2026, 10, 4, 21, 0, 0, 0, time.UTC)
	run := &runlog.Run{}
	setRunStatus(run, "running", t0)
	setRunStatus(run, "running", t0.Add(5*time.Minute)) // the queue starting a resumed run
	startActiveWallclock(run, t0.Add(6*time.Minute))    // the monitor
	setRunStatus(run, "paused", t0.Add(10*time.Minute))
	settleActiveWallclock(run, t0.Add(40*time.Minute)) // the monitor's deferred settle
	setRunStatus(run, "done", t0.Add(50*time.Minute))
	if run.ActiveWallclockMs != (10 * time.Minute).Milliseconds() {
		t.Fatalf("active = %v, want 10m", time.Duration(run.ActiveWallclockMs)*time.Millisecond)
	}

	paused := &runlog.Run{Status: "paused"}
	startActiveWallclock(paused, t0)
	if paused.ActiveSince != "" {
		t.Fatal("a start on a paused run opened a segment; its wait would be counted at the next settle")
	}
}

// openSegment plants a running run with 5 settled minutes and 10 more open.
func openSegment(t *testing.T, s *Service, dir, projectID, id string) *runState {
	t.Helper()
	rs := insertRunningRun(t, s, dir, projectID, id)
	rs.run.ActiveWallclockMs = (5 * time.Minute).Milliseconds()
	rs.run.ActiveSince = time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339Nano)
	if _, err := s.ensureWriter(rs); err != nil {
		t.Fatal(err)
	}
	return rs
}

func requireSettled(t *testing.T, rs *runState, why string) {
	t.Helper()
	got := rs.snapshotRun()
	if got.ActiveSince != "" {
		t.Errorf("%s: status %s with the segment still open", why, got.Status)
	}
	if ms := time.Duration(got.ActiveWallclockMs) * time.Millisecond; ms < 15*time.Minute || ms > 16*time.Minute {
		t.Errorf("%s: active = %v, want 5m settled + the 10m segment", why, ms)
	}
	onDisk, err := runlog.ReadState(rs.runDir)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.ActiveSince != "" || onDisk.ActiveWallclockMs != got.ActiveWallclockMs {
		t.Errorf("%s: state.json has active=%dms since=%q, memory has %dms — the pause wrote the unsettled clock",
			why, onDisk.ActiveWallclockMs, onDisk.ActiveSince, got.ActiveWallclockMs)
	}
}

// Every way a working run stops settles the segment it was in, in memory and
// on disk, so a run paused at any of them reads its true active time.
func TestEveryPauseSettlesTheActiveClock(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	cases := []struct {
		name  string
		pause func(rs *runState)
	}{
		{"budget", func(rs *runState) { s.failRun(rs, agent.ErrBudgetExceeded) }},
		{"provider", func(rs *runState) { s.failRun(rs, provider.ErrProviderUnavailable) }},
		{"failure", func(rs *runState) { s.failRun(rs, errors.New("boom")) }},
		{"history_duration", func(rs *runState) {
			rs.pausePending = map[string]interface{}{"detail": "25m so far"}
			rs.pauseAfterTurn.Store(true)
			s.pauseAtSafePoint(rs)
		}},
		{"engine_restart_request", func(rs *runState) {
			if err := s.RequestRestart(context.Background(), "test"); err != nil {
				t.Fatal(err)
			}
		}},
		{"queued", func(rs *runState) {
			// A resumed run that finds the engine full waits its turn; the
			// wait is not work.
			s.queue.mu.Lock()
			s.queue.running = 1 << 20
			s.queue.mu.Unlock()
			s.queue.submit(s, &queued{rs: rs, ctx: context.Background(), exec: func(context.Context) {}})
		}},
		// Last: shutdown leaves the service shutting down.
		{"engine_shutdown", func(rs *runState) {
			if err := s.PauseAllRuns(context.Background()); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rs := openSegment(t, s, dir, id, "r-"+tc.name)
			tc.pause(rs)
			if rs.snapshotRun().Status == "running" {
				t.Fatalf("the %s path did not stop the run", tc.name)
			}
			requireSettled(t, rs, tc.name)
			s.queue.mu.Lock()
			s.queue.running = 0
			s.queue.waiting = nil
			s.queue.mu.Unlock()
			s.runsMu.Lock()
			delete(s.runs, rs.run.ID)
			s.runsMu.Unlock()
		})
	}
}

// An engine that died mid-segment leaves state.json saying "running" with the
// segment open. Recovery closes it at the run's last event: the downtime
// until recovery is not work, and dropping the segment loses all of it.
func TestRecoveredOrphanSettlesAtItsLastEvent(t *testing.T) {
	t0 := time.Date(2026, 10, 4, 22, 27, 20, 0, time.UTC)
	clock := t0.Add(3 * time.Hour)
	s := newClockedService(t, func() time.Time { return clock }, time.Minute)
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	run := &runlog.Run{
		ID: "r-orphan", ProjectID: id, TaskID: "T-001", Stage: "test", Mode: "solo",
		Status: "running", StartedAt: t0.Format(time.RFC3339),
		ActiveWallclockMs: (27 * time.Minute).Milliseconds(), ActiveSince: t0.Format(time.RFC3339Nano),
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	runDir := runlog.RunDirFor(dir, run.ID)
	events := `{"seq":1,"ts":"` + t0.Format(time.RFC3339Nano) + `","type":"checkpoint","run_id":"r-orphan","data":{"reason":"resume"}}` + "\n" +
		`{"seq":2,"ts":"` + t0.Add(57*time.Minute).Format(time.RFC3339Nano) + `","type":"reply_call","run_id":"r-orphan","data":{}}` + "\n"
	if err := os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	s.RecoverRuns(context.Background())

	got, err := runlog.ReadState(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "paused" || got.PendingKind != "engine_restart" {
		t.Fatalf("state = %s/%s, want paused/engine_restart", got.Status, got.PendingKind)
	}
	if d := time.Duration(got.ActiveWallclockMs) * time.Millisecond; d != 84*time.Minute || got.ActiveSince != "" {
		t.Fatalf("active = %v since %q, want 27m + 57m to the last event and closed", d, got.ActiveSince)
	}
}

// The real sequence end to end: a test-first works, asks, waits while the
// person reads the question, is answered, works again and pauses at its gate.
// Active time is both working segments and neither the wait nor the gate; the
// budget clock agrees; and the ceiling the person lifted while it waited is
// still lifted after the answer (the resumed test-first rebuilt its tracker
// from defaults, so the budget card restarted at zero and the lift was lost).
func TestAnsweredTestFirstCountsBothSegmentsAtItsGate(t *testing.T) {
	const seg = time.Second
	const wait = 1500 * time.Millisecond
	s := serviceWithDucklings(t, "pato-uno")
	native := true
	for id, duck := range s.cfg.Ducklings {
		duck.Caps.NativeTools = &native
		s.cfg.Ducklings[id] = duck
	}
	projectID, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if err := vcs.New(dir).Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectUpdate(context.Background(), projectID, map[string]string{
		"verify.mode": "tests", "verify.tests": "test ! -f regression_test.go",
	}); err != nil {
		t.Fatal(err)
	}
	toolCall := func(name, args string) provider.ToolCall {
		call := provider.ToolCall{ID: "call-" + name, Type: "function"}
		call.Function.Name, call.Function.Arguments = name, args
		return call
	}
	var answered atomic.Bool
	var step sync.Mutex
	worker := 0
	fake := s.providers["fake"].(*provider.Fake)
	fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
		if len(req.Messages) > 0 && strings.HasPrefix(req.Messages[0].Content, "You are the advisor duckling in ducklab.") {
			return &provider.ChatResponse{Choices: []provider.Choice{{
				Message: provider.Message{Content: "Either is fine."}, FinishReason: provider.FinishStop,
			}}}
		}
		step.Lock()
		worker++
		n := worker
		step.Unlock()
		var message provider.Message
		switch {
		case !answered.Load() && n == 1:
			time.Sleep(seg)
			message.ToolCalls = []provider.ToolCall{toolCall("fs_read", `{"path":"go.mod"}`)}
		case !answered.Load():
			message.ToolCalls = []provider.ToolCall{toolCall("ask_human", `{"question":"Cover the legacy format too?"}`)}
		case !strings.Contains(lastToolResults(req), "regression_test.go"):
			time.Sleep(seg)
			message.ToolCalls = []provider.ToolCall{toolCall("fs_write", `{"path":"regression_test.go","content":"package fixture\n\nimport \"testing\"\n\nfunc TestRegression(t *testing.T) { t.Fatal(\"missing\") }\n"}`)}
		default:
			message.Content = "The failing regression test is complete."
		}
		finish := provider.FinishStop
		if len(message.ToolCalls) > 0 {
			finish = provider.FinishToolCalls
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{Message: message, FinishReason: finish}}}
	}

	started := time.Now()
	run, err := s.TestStart(context.Background(), projectID, TestFirstRequest{TaskID: "T-001", Duckling: "pato-uno"})
	if err != nil {
		t.Fatal(err)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	select {
	case <-rs.done:
	case <-time.After(30 * time.Second):
		t.Fatal("test writer did not pause on its question")
	}
	paused := rs.snapshotRun()
	if paused.Status != "paused" || paused.PendingKind != "question" {
		t.Fatalf("state = %s/%s, want paused/question", paused.Status, paused.PendingKind)
	}
	seg1 := time.Duration(paused.ActiveWallclockMs) * time.Millisecond
	if seg1 < seg {
		t.Fatalf("first segment = %v, want at least the %v the writer worked", seg1, seg)
	}
	if _, err := s.RunBudgetLift(context.Background(), run.ID, "tokens"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(wait) // the person reading the question
	questionID, _ := paused.PendingData["question_id"].(string)
	answered.Store(true)
	if err := s.RunAnswer(context.Background(), run.ID, questionID, "yes"); err != nil {
		t.Fatal(err)
	}
	s.runsMu.RLock()
	rs = s.runs[run.ID]
	s.runsMu.RUnlock()
	select {
	case <-rs.done:
	case <-time.After(30 * time.Second):
		t.Fatal("the answered test writer never reached its gate")
	}
	total := time.Since(started)
	gate := rs.snapshotRun()
	if gate.Status != "paused" || gate.PendingKind != "gate" {
		t.Fatalf("state = %s/%s (%s), want paused/gate", gate.Status, gate.PendingKind, gate.Failure)
	}
	active := time.Duration(gate.ActiveWallclockMs) * time.Millisecond
	if gate.ActiveSince != "" {
		t.Errorf("the gate pause left the second segment open since %s", gate.ActiveSince)
	}
	if active < seg1+seg {
		t.Errorf("active at the gate = %v, want the first segment (%v) plus the second (at least %v)", active, seg1, seg)
	}
	if active > total-wait+100*time.Millisecond {
		t.Errorf("active at the gate = %v of %v elapsed — the %v wait was counted as work", active, total, wait)
	}
	onDisk, err := runlog.ReadState(rs.runDir)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.ActiveWallclockMs != gate.ActiveWallclockMs {
		t.Errorf("state.json active = %dms, memory %dms", onDisk.ActiveWallclockMs, gate.ActiveWallclockMs)
	}
	if budgetClock := time.Duration(gate.Budget.WallclockS * float64(time.Second)); budgetClock < 2*seg {
		t.Errorf("budget clock at the gate = %v — the answer restarted it (want both segments, at least %v)", budgetClock, 2*seg)
	}
	if gate.Budget.Limit.Tokens != 0 {
		t.Errorf("token ceiling = %d after the answer; the person lifted it while the run waited", gate.Budget.Limit.Tokens)
	}
	if gate.Budget.Turns < paused.Budget.Turns {
		t.Errorf("turns = %d after the answer, %d before — the ledger restarted", gate.Budget.Turns, paused.Budget.Turns)
	}
	if err := s.RunReject(context.Background(), run.ID, ""); err != nil {
		t.Fatal(err)
	}
	if ended := rs.snapshotRun(); ended.ActiveWallclockMs != gate.ActiveWallclockMs {
		t.Errorf("rejecting at the gate moved active from %dms to %dms", gate.ActiveWallclockMs, ended.ActiveWallclockMs)
	}
}

// Every resume reopens the clock and keeps what the run had already worked:
// Resume and Resume-with-note (#155), on both stages that resume from the
// record, and whatever the run then ends on settles the segment it opened.
func TestEveryResumeOpensTheClockOverTheSettledTime(t *testing.T) {
	const worked = int64(27 * 60_000)
	for _, stage := range []string{"build", "test"} {
		for _, note := range []string{"", ti36xResumeNote} {
			name := stage + "/plain"
			if note != "" {
				name = stage + "/note"
			}
			t.Run(name, func(t *testing.T) {
				s := serviceWithDucklings(t, "pato-uno")
				pausedRunWithNote(t, s, stage, "error")
				const id = "r-20261003-145618-bbdk"
				s.runsMu.RLock()
				rs := s.runs[id]
				s.runsMu.RUnlock()
				rs.wmu.Lock()
				rs.run.ActiveWallclockMs = worked
				rs.wmu.Unlock()

				got, err := s.RunResumeWithNote(context.Background(), id, note, "")
				if err != nil {
					t.Fatal(err)
				}
				if got.Status == "running" && got.ActiveSince == "" {
					t.Error("the resumed run is running with no open segment; its work would never be counted")
				}
				if got.ActiveWallclockMs != worked {
					t.Errorf("resume changed the settled time: %dms, want %dms", got.ActiveWallclockMs, worked)
				}
				rs = waitResumed(t, s, id)
				end := rs.snapshotRun()
				if end.Status == "running" {
					t.Fatalf("the resumed run is still running after its goroutine returned")
				}
				if end.ActiveSince != "" {
					t.Errorf("the run stopped (%s/%s) with its resumed segment open", end.Status, end.PendingKind)
				}
				if end.ActiveWallclockMs <= worked {
					t.Errorf("active = %dms after resumed work, want more than the %dms before it", end.ActiveWallclockMs, worked)
				}
			})
		}
	}
}

func lastToolResults(req provider.ChatRequest) string {
	var b strings.Builder
	for _, m := range req.Messages {
		if m.Role == "tool" {
			b.WriteString(m.Content)
		}
		for _, call := range m.ToolCalls {
			b.WriteString(call.Function.Arguments)
		}
	}
	return b.String()
}

// The whole class, structurally: a status written around setRunStatus is a
// transition that neither settles nor starts the clock. B-500's gate pause was
// one bare assignment among fifty. A run born "running" in its literal is the
// same hole at birth: review, release, triage and the PR scribe start outside
// the queue, so nothing else would ever open their clock.
func TestRunStatusIsOnlyWrittenThroughSetRunStatus(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	bare := regexp.MustCompile(`\brun\.Status(\s*,[^=\n]*)?\s*=[^=]|\bStatus:\s*"running"`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		inHelper := false
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "func ") {
				inHelper = strings.HasPrefix(line, "func setRunStatus(")
			}
			if bare.MatchString(line) && !inHelper {
				t.Errorf("%s:%d writes a run's status around setRunStatus: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}
