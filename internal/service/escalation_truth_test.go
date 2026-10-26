package service

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

// B-522 (calculator r-20261026-171819-zzgs): a triage crossed its history
// threshold, the card said "The run paused", and the next bug's turn started
// a second later. These tests pin the whole class: every run kind the check
// fires on either lands the pause at a real safe point or says, on the card,
// that it continues — and the card offers only what fits the run.

// seedTriageHistory files five finished triage runs for the project, seated by
// the given triager and accepted, so they are both the wallclock history and
// that triager's scorecard evidence for the triager role. A strong
// implementer with build evidence is filed too: it must never be offered to
// replace a triager.
func seedTriageHistory(s *Service, projectID, triager string) {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	ended := time.Now().UTC().Format(time.RFC3339)
	for i := 0; i < 5; i++ {
		id := "r-history-triage-" + string(rune('a'+i))
		s.runs[id] = &runState{run: &runlog.Run{
			ID: id, ProjectID: projectID, Stage: "triage", Mode: "solo",
			ActiveWallclockMs: 60_000, EndedAt: ended, Verdict: "UNVERIFIED", Accepted: true,
			Roster: map[string]string{"triager": triager},
			Spend:  map[string]runlog.DucklingSpend{triager: {Calls: 1}},
		}}
		bid := "r-history-build-" + string(rune('a'+i))
		s.runs[bid] = &runState{run: &runlog.Run{
			ID: bid, ProjectID: "elsewhere", Stage: "build", Mode: "solo", TaskID: "T-001",
			ActiveWallclockMs: 60_000, EndedAt: ended, Verdict: "PASSED",
			Roster: map[string]string{"implementer": "pato-tres"},
			Spend:  map[string]runlog.DucklingSpend{"pato-tres": {Calls: 1}},
		}}
	}
}

// overThreshold backdates a prepared run's active clock past the five-minute
// floor, as eleven minutes of real work would.
func overThreshold(rs *runState) {
	rs.wmu.Lock()
	rs.run.ActiveSince = time.Now().Add(-11 * time.Minute).UTC().Format(time.RFC3339Nano)
	rs.wmu.Unlock()
}

func runEvents522(t *testing.T, s *Service, runID string) []*runlog.Event {
	t.Helper()
	events, err := runlog.ReadEvents(s.RunDir(runID))
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func triagedBugs(events []*runlog.Event) []string {
	var out []string
	for _, e := range events {
		if e.Type == "turn_start" {
			if id, _ := e.Data["bug"].(string); id != "" {
				out = append(out, id)
			}
		}
	}
	return out
}

func lastOfType(events []*runlog.Event, kind string) *runlog.Event {
	var out *runlog.Event
	for _, e := range events {
		if e.Type == kind {
			out = e
		}
	}
	return out
}

func strs(v interface{}) []string {
	var out []string
	switch x := v.(type) {
	case []string:
		return x
	case []interface{}:
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func waitForStatus(t *testing.T, s *Service, runID, status, pending string) *runlog.Run {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		detail, err := s.RunGet(context.Background(), runID)
		if err == nil && detail.Run.Status == status && (pending == "" || detail.Run.PendingKind == pending) {
			return detail.Run
		}
		time.Sleep(20 * time.Millisecond)
	}
	detail, _ := s.RunGet(context.Background(), runID)
	t.Fatalf("run %s never reached %s/%s: %+v", runID, status, pending, detail.Run)
	return nil
}

func TestTriageCrossingHistoryPausesAfterTheCurrentBugAndResumesTheRest(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	ctx := context.Background()
	for _, title := range []string{"vertex drag never starts", "edge label overlaps", "angle input loses focus"} {
		if _, err := s.BugAdd(ctx, id, BugRequest{Title: title, Severity: "normal"}); err != nil {
			t.Fatal(err)
		}
	}
	seedTriageHistory(s, id, "pato-dos")
	l, err := s.prepareTriage(ctx, id, BugTriageRequest{Ducklings: []string{"pato-uno"}})
	if err != nil {
		t.Fatal(err)
	}
	runID := l.rs.run.ID
	cleanupStartedRun(t, s, runID)
	overThreshold(l.rs)
	s.executeTriage(l.ctx, l.rs, l.root, l.todo, l.all, nil)

	// The pause landed after the first bug's turn: one bug triaged, the run
	// paused with its card, and nothing else started.
	run := l.rs.snapshotRun()
	if run.Status != "paused" || run.PendingKind != "history_duration" {
		t.Fatalf("triage did not pause at its safe point: status=%s pending=%s", run.Status, run.PendingKind)
	}
	events := runEvents522(t, s, runID)
	if got := triagedBugs(events); len(got) != 1 {
		t.Fatalf("triage kept working past its requested pause: bugs started %v", got)
	}
	card := lastOfType(events, "escalation_suggestion")
	if card == nil {
		t.Fatal("no escalation card")
	}
	if card.Data["role"] != "triager" || card.Data["seat"] != "pato-uno" || card.Data["pause"] != "requested" {
		t.Fatalf("card does not describe the triager or the pause: %+v", card.Data)
	}
	actions := strs(card.Data["actions"])
	if !slices.Equal(actions, []string{"relaunch_with_another_seat", "continue_as-is", "abort"}) {
		t.Fatalf("triage card actions = %v", actions)
	}
	diagnoses, _ := card.Data["diagnoses"].(map[string]interface{})
	if _, ok := diagnoses["task_brief_quality"]; ok {
		t.Fatalf("a task-less run diagnosed a task brief: %+v", diagnoses)
	}
	// The stronger seat is a TRIAGER with triage evidence — never the
	// implementer with the better build record.
	cand, _ := card.Data["candidate"].(map[string]interface{})
	if cand["id"] != "pato-dos" {
		t.Fatalf("candidate = %+v, want the triager with triage evidence", card.Data["candidate"])
	}
	detail, err := s.RunGet(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.Escalation["state"] != "paused" || !slices.Contains(detail.Run.Next, "resume") {
		t.Fatalf("run_get does not say the run paused and can resume: escalation=%v next=%v", detail.Run.Escalation, detail.Run.Next)
	}

	// Resume continues with the two bugs not yet triaged, never the first.
	if _, err := s.RunResume(ctx, runID); err != nil {
		t.Fatal(err)
	}
	run = waitForStatus(t, s, runID, "paused", "gate")
	events = runEvents522(t, s, runID)
	started := triagedBugs(events)
	if len(started) != 3 || started[0] == started[1] || started[1] == started[2] || started[0] == started[2] {
		t.Fatalf("resume redid or skipped bugs: started %v", started)
	}
	proposals, _ := run.PendingData["proposals"].([]map[string]interface{})
	if proposals == nil {
		raw, _ := run.PendingData["proposals"].([]interface{})
		if len(raw) != 3 {
			t.Fatalf("the gate holds %d proposals, want all 3 (the first bug's from before the pause): %+v", len(raw), run.PendingData)
		}
	} else if len(proposals) != 3 {
		t.Fatalf("the gate holds %d proposals, want all 3 (the first bug's from before the pause)", len(proposals))
	}
	if run.Roster["triager"] != "pato-uno" {
		t.Fatalf("resume lost the seat the run was launched with: %v", run.Roster)
	}
}

func TestTriageThresholdOnTheLastBugGoesToItsGateAndSaysSo(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	ctx := context.Background()
	if _, err := s.BugAdd(ctx, id, BugRequest{Title: "vertex drag never starts", Severity: "normal"}); err != nil {
		t.Fatal(err)
	}
	seedTriageHistory(s, id, "pato-uno")
	l, err := s.prepareTriage(ctx, id, BugTriageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	cleanupStartedRun(t, s, l.rs.run.ID)
	overThreshold(l.rs)
	s.executeTriage(l.ctx, l.rs, l.root, l.todo, l.all, nil)
	run := l.rs.snapshotRun()
	if run.Status != "paused" || run.PendingKind != "gate" {
		t.Fatalf("a triage with nothing left should reach its gate: status=%s pending=%s", run.Status, run.PendingKind)
	}
	if l.rs.pauseAfterTurn.Load() {
		t.Fatal("a pause request outlived the run's last safe point")
	}
	events := runEvents522(t, s, l.rs.run.ID)
	requested, deferred := lastOfType(events, "pause_requested"), lastOfType(events, "pause_deferred")
	if requested == nil || deferred == nil || deferred.Seq < requested.Seq {
		t.Fatalf("the record must say the requested pause was withdrawn: requested=%v deferred=%v", requested, deferred)
	}
}

// Every run kind the history check can fire on, and what it must do.
func TestWallclockEscalationIsTruthfulForEveryRunKind(t *testing.T) {
	for _, tc := range []struct {
		stage, task string
		pausable    bool
		wantActions []string
		taskDiag    bool
	}{
		{"build", "T-001", true, []string{"relaunch_with_stronger_seat", "improve_task_body", "continue_as-is"}, true},
		{"test", "T-001", false, []string{"relaunch_with_stronger_seat", "improve_task_body", "continue_as-is"}, true},
		{"triage", "", true, []string{"relaunch_with_another_seat", "continue_as-is", "abort"}, false},
		{"intake", "", false, []string{"continue_as-is", "abort"}, false},
		{"spec", "", false, []string{"continue_as-is", "abort"}, false},
		{"plan", "", false, []string{"continue_as-is", "abort"}, false},
		{"release", "", false, []string{"continue_as-is", "abort"}, false},
		{"review", "T-001", false, []string{"continue_as-is", "abort"}, false},
		{"chat", "", false, []string{"continue_as-is", "abort"}, false},
		{"pr_body", "", false, []string{"continue_as-is", "abort"}, false},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			s := newTestService(t)
			started := time.Now().Add(-11 * time.Minute)
			current := &runlog.Run{ID: "r-current", ProjectID: "p", Stage: tc.stage, TaskID: tc.task, Mode: "solo", Status: "running",
				StartedAt: started.UTC().Format(time.RFC3339), ActiveSince: started.UTC().Format(time.RFC3339Nano)}
			w, err := runlog.NewWriter(t.TempDir(), current)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			rs := &runState{run: current, writer: w, runDir: w.RunDir()}
			s.runs = map[string]*runState{current.ID: rs}
			for i := 0; i < 5; i++ {
				s.runs["history-"+string(rune('a'+i))] = &runState{run: &runlog.Run{
					ID: "history", ProjectID: "p", Stage: tc.stage, Mode: "solo", ActiveWallclockMs: 60_000,
					EndedAt: time.Now().UTC().Format(time.RFC3339),
				}}
			}
			s.checkWallclockEscalation(rs)
			events, err := runlog.ReadEvents(w.RunDir())
			if err != nil {
				t.Fatal(err)
			}
			card := lastOfType(events, "escalation_suggestion")
			if card == nil {
				t.Fatal("no card")
			}
			requested, deferred := lastOfType(events, "pause_requested") != nil, lastOfType(events, "pause_deferred") != nil
			if tc.pausable {
				if !requested || deferred || !rs.pauseAfterTurn.Load() || card.Data["pause"] != "requested" {
					t.Fatalf("a pausable run must request its pause: requested=%v deferred=%v armed=%v card=%v", requested, deferred, rs.pauseAfterTurn.Load(), card.Data["pause"])
				}
			} else {
				detail, _ := card.Data["pause_detail"].(string)
				if requested || !deferred || rs.pauseAfterTurn.Load() || card.Data["pause"] != "deferred" || detail == "" {
					t.Fatalf("a run with no safe point must say it continues: requested=%v deferred=%v armed=%v card=%v", requested, deferred, rs.pauseAfterTurn.Load(), card.Data)
				}
				if view := escalationView(current, events); view["state"] != "continuing" {
					t.Fatalf("run_get escalation state = %v, want continuing", view["state"])
				}
			}
			if got := strs(card.Data["actions"]); !slices.Equal(got, tc.wantActions) {
				t.Fatalf("actions = %v, want %v", got, tc.wantActions)
			}
			diagnoses, _ := card.Data["diagnoses"].(map[string]interface{})
			if _, ok := diagnoses["task_brief_quality"]; ok != tc.taskDiag {
				t.Fatalf("task brief diagnosis present=%v, want %v: %v", ok, tc.taskDiag, diagnoses)
			}
			if card.Data["role"] != escalationRole(current) {
				t.Fatalf("role = %v", card.Data["role"])
			}
		})
	}
}

// A requested pause that has not landed yet is reported as pending, not as a
// pause: the card said "paused" while the triage ran on.
func TestEscalationViewNeverCallsARunningRunPaused(t *testing.T) {
	events := []*runlog.Event{
		{Seq: 3, Type: "escalation_suggestion", Data: map[string]interface{}{"point": "wallclock_history", "role": "triager"}},
		{Seq: 4, Type: "pause_requested", Data: map[string]interface{}{"detail": "pausing when the current bug's turn ends"}},
	}
	run := &runlog.Run{Stage: "triage", Status: "running"}
	if got := escalationView(run, events)["state"]; got != "pausing" {
		t.Fatalf("state = %v, want pausing", got)
	}
	run = &runlog.Run{Stage: "triage", Status: "paused", PendingKind: "history_duration"}
	if got := escalationView(run, events)["state"]; got != "paused" {
		t.Fatalf("state = %v, want paused", got)
	}
}

func TestTriageRelaunchRetriagesTheSameBugsWithThePickedTriager(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	ctx := context.Background()
	var ids []string
	for _, title := range []string{"vertex drag never starts", "edge label overlaps", "angle input loses focus"} {
		b, err := s.BugAdd(ctx, id, BugRequest{Title: title, Severity: "normal"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, b.ID)
	}
	seedTriageHistory(s, id, "pato-dos")
	l, err := s.prepareTriage(ctx, id, BugTriageRequest{BugIDs: ids[:2], Ducklings: []string{"pato-uno"}})
	if err != nil {
		t.Fatal(err)
	}
	first := l.rs.run.ID
	cleanupStartedRun(t, s, first)
	overThreshold(l.rs)
	s.executeTriage(l.ctx, l.rs, l.root, l.todo, l.all, nil)
	if st := l.rs.snapshotRun(); st.Status != "paused" || st.PendingKind != "history_duration" {
		t.Fatalf("setup: first triage did not pause: %s/%s", st.Status, st.PendingKind)
	}

	// A seat that does not exist is refused before anything is ended.
	if _, err := s.BugTriageWith(ctx, id, BugTriageRequest{RelaunchOf: first, Ducklings: []string{"nobody"}}); err == nil {
		t.Fatal("relaunch with an unknown triager was accepted")
	}
	if st := l.rs.snapshotRun(); st.Status != "paused" {
		t.Fatalf("a refused relaunch ended the run it would replace: %s", st.Status)
	}
	// Nor is one with no bug left to triage.
	if _, err := s.BugTriageWith(ctx, id, BugTriageRequest{RelaunchOf: first, BugIDs: []string{"B-999"}, Ducklings: []string{"pato-dos"}}); err == nil {
		t.Fatal("relaunch with nothing to triage was accepted")
	}
	if st := l.rs.snapshotRun(); st.Status != "paused" {
		t.Fatalf("a relaunch with nothing to triage ended the run it would replace: %s", st.Status)
	}

	again, err := s.BugTriageWith(ctx, id, BugTriageRequest{RelaunchOf: first, Ducklings: []string{"pato-dos"}})
	if err != nil {
		t.Fatal(err)
	}
	cleanupStartedRun(t, s, again.ID)
	waitForStatus(t, s, again.ID, "paused", "gate")
	if st := l.rs.snapshotRun(); st.Status != "failed" || st.Verdict != "ABORTED" {
		t.Fatalf("the replaced triage was left %s/%s", st.Status, st.Verdict)
	}
	events := runEvents522(t, s, again.ID)
	if got := triagedBugs(events); !slices.Equal(got, ids[:2]) {
		t.Fatalf("relaunch triaged %v, want the same bugs %v", got, ids[:2])
	}
	for _, e := range events {
		if e.Type == "turn_start" && e.Data["duckling"] != "pato-dos" {
			t.Fatalf("relaunch seated %v, want the picked triager", e.Data["duckling"])
		}
	}
	if start := lastOfType(events, "run_start"); start == nil || start.Data["relaunch_of"] != first {
		t.Fatalf("relaunch does not record what it replaced: %+v", start)
	}
}

// A failed or distressed triage asked for implementer candidates against an
// empty implementer seat and offered to edit a task it did not have.
func TestDecisionEscalationOnATriageIsAboutTheTriager(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos", "pato-tres")
	current := &runlog.Run{ID: "r-current", ProjectID: "p", Stage: "triage", Mode: "solo", Status: "running",
		Roster: map[string]string{"triager": "pato-uno"}}
	w, err := runlog.NewWriter(t.TempDir(), current)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	rs := &runState{run: current, writer: w, runDir: w.RunDir()}
	s.runs = map[string]*runState{current.ID: rs}
	seedTriageHistory(s, "p", "pato-dos")
	w.AppendEvent("turn_start", map[string]interface{}{"role": "triager", "bug": "B-001"})
	w.AppendEvent("distress_evidence", map[string]interface{}{"kind": "unanswered_death", "role": "triager"})
	s.emitEscalationAtDecision(rs, "failed_run")
	card := lastOfType(runEvents522(t, s, current.ID), "escalation_suggestion")
	if card == nil {
		t.Fatal("no card for a triage with a stronger triager on record")
	}
	cand, _ := card.Data["candidate"].(map[string]interface{})
	if card.Data["role"] != "triager" || cand["id"] != "pato-dos" {
		t.Fatalf("decision card is not about the triager: role=%v candidate=%v", card.Data["role"], card.Data["candidate"])
	}
	if slices.Contains(strs(card.Data["actions"]), "improve_task_body") {
		t.Fatalf("task action on a task-less run: %v", card.Data["actions"])
	}
	diagnoses, _ := card.Data["diagnoses"].(map[string]interface{})
	if _, ok := diagnoses["task_brief_quality"]; ok {
		t.Fatalf("task diagnosis on a task-less run: %v", diagnoses)
	}
}
