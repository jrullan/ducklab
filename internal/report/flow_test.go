package report

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/bug"
	"github.com/jrullan/ducklab/internal/runlog"
)

var flowT0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func fev(min float64, typ string, data map[string]interface{}) *runlog.Event {
	at := flowT0.Add(time.Duration(min * float64(time.Minute)))
	return &runlog.Event{TS: at.Format(time.RFC3339Nano), Type: typ, Data: data}
}

// B-556: one build run — a 5-minute turn, a 2-minute gate, then a gate pause
// the owner answers 30 minutes later. A pre-reproduction while it waits does
// not end the wait.
func TestFlowSplitsARunIntoModelGateAndOwnerWait(t *testing.T) {
	run := FlowRun{
		Run: &runlog.Run{ID: "r-1", Stage: "build", TaskID: "T-1", BugID: "B-1", Accepted: true, Verdict: "PASSED",
			StartedAt: "2026-11-01T12:00:00Z", Roster: map[string]string{"implementer": "terra"},
			Spend: map[string]runlog.DucklingSpend{"terra": {CostUSD: 0.2}}, ActiveWallclockMs: 7 * 60000},
		Events: []*runlog.Event{
			fev(0, "run_start", nil),
			fev(0, "turn_start", nil),
			fev(5, "turn_end", nil),
			fev(5, "gate_started", nil),
			fev(7, "round_gate", nil),
			fev(7, "human_needed", map[string]interface{}{"kind": "gate"}),
			fev(10, "pre_reproduction", nil),
			fev(37, "accept_started", nil),
			fev(38, "run_end", nil),
		},
	}
	f := BuildFlow([]FlowRun{run}, nil)
	if f.Model != 5*time.Minute || f.Gate != 2*time.Minute || f.OwnerWait != 30*time.Minute || f.Wall != 38*time.Minute {
		t.Fatalf("model=%s gate=%s wait=%s wall=%s", f.Model, f.Gate, f.OwnerWait, f.Wall)
	}
	if len(f.Waits) != 1 || f.Waits[0].Kind != "gate" || f.Waits[0].Count != 1 || f.Waits[0].Total != 30*time.Minute {
		t.Fatalf("waits = %+v", f.Waits)
	}
	if f.Tasks.Built != 1 || f.Tasks.Accepted != 1 || f.Tasks.FirstPass != 1 {
		t.Fatalf("tasks = %+v", f.Tasks)
	}
	if len(f.Implementers) != 1 || f.Implementers[0].Duckling != "terra" || f.Implementers[0].Passed != 1 ||
		f.Implementers[0].MedianCost != 0.2 || f.Implementers[0].MedianActive != 7*time.Minute {
		t.Fatalf("implementers = %+v", f.Implementers)
	}
}

// A second pause before the owner answers the first closes the first; a
// turn or gate the log never closed adds nothing.
func TestFlowCountsOnlyClosedIntervals(t *testing.T) {
	run := FlowRun{Run: &runlog.Run{ID: "r-2", Stage: "build", TaskID: "T-2"}, Events: []*runlog.Event{
		fev(0, "human_needed", map[string]interface{}{"kind": "budget"}),
		fev(4, "human_needed", map[string]interface{}{"kind": "gate"}),
		fev(10, "decision", nil),
		fev(10, "turn_start", nil),
		fev(12, "gate_started", nil),
	}}
	f := BuildFlow([]FlowRun{run}, nil)
	if f.OwnerWait != 10*time.Minute || f.Model != 0 || f.Gate != 0 {
		t.Fatalf("wait=%s model=%s gate=%s", f.OwnerWait, f.Model, f.Gate)
	}
	kinds := map[string]time.Duration{}
	for _, w := range f.Waits {
		kinds[w.Kind] = w.Total
	}
	if kinds["budget"] != 4*time.Minute || kinds["gate"] != 6*time.Minute {
		t.Fatalf("waits = %+v", f.Waits)
	}
}

// A bug's lead time runs from its report to its first move to fixed; owner
// actions are its human-signed moves before fixed plus the pauses of every
// run that worked on it — the triage batch by its bug_ids, the test run
// through the task the bug was promoted to, the build by its bug_id.
func TestFlowBugLeadTimeAndOwnerActions(t *testing.T) {
	b := bug.Bug{ID: "B-7", TaskID: "T-7", CreatedAt: "2026-11-01T12:00:00Z", History: []bug.AuditEntry{
		{TS: "2026-11-01T12:10:00Z", To: "triaged", Actor: "engine"},
		{TS: "2026-11-01T12:20:00Z", To: "in_progress", Actor: "human"},
		{TS: "2026-11-01T14:00:00Z", To: "fixed", Actor: "engine"},
		{TS: "2026-11-01T15:00:00Z", To: "verified", Actor: "human"},
	}}
	open := bug.Bug{ID: "B-8", CreatedAt: "2026-11-01T12:00:00Z"}
	pause := func(id string, r *runlog.Run, extra ...*runlog.Event) FlowRun {
		r.ID = id
		return FlowRun{Run: r, Events: append(extra, fev(1, "human_needed", map[string]interface{}{"kind": "gate"}), fev(2, "accept_started", nil))}
	}
	runs := []FlowRun{
		pause("r-tri", &runlog.Run{Stage: "triage"}, fev(0, "run_start", map[string]interface{}{"bug_ids": []interface{}{"B-7", "B-9"}})),
		pause("r-test", &runlog.Run{Stage: "test", TaskID: "T-7"}),
		pause("r-build", &runlog.Run{Stage: "build", TaskID: "T-7", BugID: "B-7"}),
	}
	f := BuildFlow(runs, []bug.Bug{b, open})
	if f.Bugs.Reported != 2 || f.Bugs.Fixed != 1 || f.Bugs.LeadMedian != 2*time.Hour {
		t.Fatalf("bugs = %+v", f.Bugs)
	}
	// One signed move before fixed (the verify after it is not counted) plus
	// one pause in each of its three runs.
	if f.Bugs.OwnerActionsMedian != 4 || f.Bugs.RunsMedian != 3 {
		t.Fatalf("owner actions = %v, runs = %v", f.Bugs.OwnerActionsMedian, f.Bugs.RunsMedian)
	}
}

// First pass counts a task whose first build was accepted; a task accepted
// on a later build is accepted but not first-pass.
func TestFlowFirstPassIsTheFirstBuild(t *testing.T) {
	b := func(id, task, started string, accepted bool) FlowRun {
		return FlowRun{Run: &runlog.Run{ID: id, Stage: "build", TaskID: task, StartedAt: started, Accepted: accepted}}
	}
	f := BuildFlow([]FlowRun{
		b("r-a2", "T-A", "2026-11-01T13:00:00Z", true),
		b("r-a1", "T-A", "2026-11-01T12:00:00Z", false),
		b("r-b1", "T-B", "2026-11-01T12:00:00Z", true),
	}, nil)
	if f.Tasks.Built != 2 || f.Tasks.Accepted != 2 || f.Tasks.FirstPass != 1 {
		t.Fatalf("tasks = %+v", f.Tasks)
	}
}

func TestFlowPercentile(t *testing.T) {
	var v []time.Duration
	for i := 1; i <= 10; i++ {
		v = append(v, time.Duration(i)*time.Minute)
	}
	if got := p90Dur(v); got != 9*time.Minute {
		t.Errorf("p90 of 1..10 = %s, want 9m", got)
	}
	if got := p90Dur(v[:1]); got != time.Minute {
		t.Errorf("p90 of one = %s", got)
	}
}

func TestRenderFlowNamesWhatItMeasured(t *testing.T) {
	run := FlowRun{Run: &runlog.Run{ID: "r-1", Stage: "build", TaskID: "T-1"}, Events: []*runlog.Event{
		fev(0, "turn_start", nil), fev(6, "turn_end", nil),
		fev(6, "human_needed", map[string]interface{}{"kind": "gate"}), fev(60, "accept_started", nil),
	}}
	out := RenderFlow(BuildFlow([]FlowRun{run}, nil))
	for _, want := range []string{"# Flow report", "no model was consulted", "| Waiting for the owner | 0.9h | 90% |", "| gate | 1 |"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// The longest single waits are named, longest first, so an abandoned pause
// cannot pass for a slow owner.
func TestFlowNamesTheLongestWaits(t *testing.T) {
	var runs []FlowRun
	for i, mins := range []float64{3, 600, 1, 45, 2, 30} {
		runs = append(runs, FlowRun{Run: &runlog.Run{ID: fmt.Sprintf("r-%d", i), Stage: "build", TaskID: fmt.Sprintf("T-%d", i)}, Events: []*runlog.Event{
			fev(0, "human_needed", map[string]interface{}{"kind": "gate"}), fev(mins, "accept_started", nil),
		}})
	}
	f := BuildFlow(runs, nil)
	if len(f.Longest) != longestShown || f.Longest[0].RunID != "r-1" || f.Longest[0].Duration != 10*time.Hour || f.Longest[1].RunID != "r-3" {
		t.Fatalf("longest = %+v", f.Longest)
	}
	if out := RenderFlow(f); !strings.Contains(out, "| r-1 | T-1 | gate | 10h00m |") {
		t.Errorf("the longest wait is not named:\n%s", out)
	}
}

// Review of B-556: a safe-point pause cancels in-flight turns, which still
// write turn_start/turn_end right after the pause. That is not the owner
// answering; the owner's resume 20 minutes later is.
func TestACancelledTurnAfterAPauseDoesNotEndTheWait(t *testing.T) {
	run := FlowRun{Run: &runlog.Run{ID: "r-h", Stage: "build", TaskID: "T-h", Status: "done"}, Events: []*runlog.Event{
		fev(0, "human_needed", map[string]interface{}{"kind": "history_duration"}),
		fev(0, "turn_start", nil),
		fev(0, "turn_end", nil),
		fev(20, "decision", nil),
	}}
	f := BuildFlow([]FlowRun{run}, nil)
	if f.OwnerWait != 20*time.Minute {
		t.Fatalf("wait = %s, want the 20 min until the owner resumed", f.OwnerWait)
	}
}

// Review of B-556: a failed accept-time reproduction writes a fresh pause and
// then accept_refused; the refusal must not close it at 0 s, and a refusal
// with no new pause puts the run back to waiting.
func TestARefusedAcceptKeepsTheRunWaiting(t *testing.T) {
	repaused := FlowRun{Run: &runlog.Run{ID: "r-r", Stage: "build", TaskID: "T-r", Status: "done"}, Events: []*runlog.Event{
		fev(0, "human_needed", map[string]interface{}{"kind": "gate"}),
		fev(1, "accept_started", nil),
		fev(1, "gate_started", map[string]interface{}{"phase": "accept"}),
		fev(3, "gate_reproduced", nil),
		fev(3, "human_needed", map[string]interface{}{"kind": "gate"}),
		fev(3, "accept_refused", nil),
		fev(8, "accept_started", nil),
	}}
	silent := FlowRun{Run: &runlog.Run{ID: "r-s", Stage: "build", TaskID: "T-s", Status: "done"}, Events: []*runlog.Event{
		fev(0, "human_needed", map[string]interface{}{"kind": "gate"}),
		fev(1, "accept_started", nil),
		fev(2, "accept_settled", map[string]interface{}{"outcome": "refused"}),
		fev(12, "accept_started", nil),
	}}
	f := BuildFlow([]FlowRun{repaused}, nil)
	if f.OwnerWait != 6*time.Minute || f.Waits[0].Count != 2 {
		t.Fatalf("re-paused: wait = %s over %d pauses, want 1 + 5 min over 2", f.OwnerWait, f.Waits[0].Count)
	}
	for _, w := range f.Longest {
		if w.Duration == 0 {
			t.Errorf("a phantom 0 s wait: %+v", w)
		}
	}
	f = BuildFlow([]FlowRun{silent}, nil)
	if f.OwnerWait != 11*time.Minute {
		t.Fatalf("silent refusal: wait = %s, want 1 + 10 min", f.OwnerWait)
	}
}

// Resumes that write only a checkpoint, chat replies and old-style accepts
// (gate_started phase accept) all end a wait.
func TestTheOtherWaysTheOwnerAnswers(t *testing.T) {
	for name, closer := range map[string]*runlog.Event{
		"resume checkpoint": fev(4, "checkpoint", map[string]interface{}{"reason": "resume"}),
		"chat reply":        fev(4, "message", map[string]interface{}{"role": "human"}),
		"old accept":        fev(4, "gate_started", map[string]interface{}{"phase": "accept"}),
		"budget lift":       fev(4, "budget_lifted", nil),
		"answer":            fev(4, "human", map[string]interface{}{"action": "answer"}),
	} {
		run := FlowRun{Run: &runlog.Run{ID: "r", Stage: "build", Status: "done"}, Events: []*runlog.Event{
			fev(0, "human_needed", map[string]interface{}{"kind": "question"}), closer,
		}}
		if f := BuildFlow([]FlowRun{run}, nil); f.OwnerWait != 4*time.Minute {
			t.Errorf("%s: wait = %s", name, f.OwnerWait)
		}
	}
	// Engine checkpoints and model messages do not.
	run := FlowRun{Run: &runlog.Run{ID: "r", Stage: "build", Status: "done"}, Events: []*runlog.Event{
		fev(0, "human_needed", map[string]interface{}{"kind": "question"}),
		fev(1, "checkpoint", map[string]interface{}{"reason": "round"}),
		fev(2, "message", map[string]interface{}{"role": "implementer"}),
		fev(9, "decision", nil),
	}}
	if f := BuildFlow([]FlowRun{run}, nil); f.OwnerWait != 9*time.Minute {
		t.Errorf("engine events ended the wait: %s", f.OwnerWait)
	}
}

// A run still waiting is named with when it started waiting, and kept out
// of the totals; a finished run's unclosed wait is neither.
func TestARunWaitingNowIsNamedNotTimed(t *testing.T) {
	open := FlowRun{Run: &runlog.Run{ID: "r-open", TaskID: "T-9", Stage: "build", Status: "paused"}, Events: []*runlog.Event{
		fev(0, "turn_start", nil), fev(1, "turn_end", nil),
		fev(1, "human_needed", map[string]interface{}{"kind": "budget"}),
	}}
	f := BuildFlow([]FlowRun{open}, nil)
	if f.OwnerWait != 0 || len(f.Open) != 1 || f.Open[0].RunID != "r-open" || f.Open[0].Kind != "budget" || f.Open[0].Since != "2026-11-01T12:01:00Z" {
		t.Fatalf("wait = %s, open = %+v", f.OwnerWait, f.Open)
	}
	if out := RenderFlow(f); !strings.Contains(out, "| r-open | T-9 | budget | 2026-11-01T12:01:00Z |") {
		t.Errorf("the waiting run is not named:\n%s", out)
	}
	open.Run.Status = "done"
	if f := BuildFlow([]FlowRun{open}, nil); len(f.Open) != 0 {
		t.Errorf("a finished run is listed as waiting: %+v", f.Open)
	}
}

// The owner signs through their agent too (mcp:<client>); the engine and the
// autopilot are not the owner. Runs after the first fix are not its cost.
func TestOwnerActionsCountTheOwnersAgentAndStopAtTheFirstFix(t *testing.T) {
	b := bug.Bug{ID: "B-1", CreatedAt: "2026-11-01T12:00:00Z", History: []bug.AuditEntry{
		{TS: "2026-11-01T12:01:00Z", To: "triaged", Actor: "engine"},
		{TS: "2026-11-01T12:02:00Z", To: "in_progress", Actor: "mcp:opencode"},
		{TS: "2026-11-01T12:03:00Z", To: "in_progress", Actor: "autopilot"},
		{TS: "2026-11-01T13:00:00Z", To: "fixed", Actor: "engine"},
	}}
	pause := func(id, started string) FlowRun {
		return FlowRun{Run: &runlog.Run{ID: id, Stage: "build", BugID: "B-1", StartedAt: started, Status: "done"}, Events: []*runlog.Event{
			fev(0, "human_needed", map[string]interface{}{"kind": "gate"}), fev(1, "accept_started", nil),
		}}
	}
	f := BuildFlow([]FlowRun{pause("r-1", "2026-11-01T12:10:00Z"), pause("r-2", "2026-11-02T09:00:00Z")}, []bug.Bug{b})
	if f.Bugs.OwnerActionsMedian != 2 || f.Bugs.RunsMedian != 1 {
		t.Fatalf("owner actions = %v (want mcp move + one pause), runs = %v (want the one before the fix)", f.Bugs.OwnerActionsMedian, f.Bugs.RunsMedian)
	}
}
