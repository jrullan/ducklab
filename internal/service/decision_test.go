package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

// decisionEvents returns a run's recorded "decision" events.
func decisionEvents(t *testing.T, s *Service, runID string) []map[string]interface{} {
	t.Helper()
	events, err := runlog.ReadEvents(s.RunDir(runID))
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]interface{}
	for _, e := range events {
		if e.Type == "decision" {
			out = append(out, e.Data)
		}
	}
	return out
}

// recoveredRun persists run under dir and loads it the way the engine does.
func recoveredRun(t *testing.T, s *Service, dir string, run *runlog.Run) {
	t.Helper()
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func wantDecision(t *testing.T, s *Service, runID, action, actor, reason string) {
	t.Helper()
	got := decisionEvents(t, s, runID)
	if len(got) != 1 || got[0]["action"] != action || got[0]["actor"] != actor || got[0]["reason"] != reason {
		t.Fatalf("decision events = %v, want one %s by %s with reason %q", got, action, actor, reason)
	}
	// The record is an attempt until it is closed (second review of #177).
	if out := outcomeEvents(t, s, runID); len(out) != 1 || out[0]["action"] != action || out[0]["outcome"] != "taken" {
		t.Fatalf("decision outcomes = %v, want one %s taken", out, action)
	}
}

func outcomeEvents(t *testing.T, s *Service, runID string) []map[string]interface{} {
	t.Helper()
	events, err := runlog.ReadEvents(s.RunDir(runID))
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]interface{}
	for _, e := range events {
		if e.Type == "decision_outcome" {
			out = append(out, e.Data)
		}
	}
	return out
}

// Second review of #177: the words are recorded before the door acts, and
// the door can still refuse after that — accept's own work (here the triage
// it applies), reject's and abort's tree restore. The record then read as an
// accept that never happened. Each recorded attempt is closed: refused, with
// the error, and the run is still where it was.
func TestARecordedDecisionThatFailsIsClosedAsRefused(t *testing.T) {
	cases := []struct {
		door   string
		setup  func(t *testing.T, s *Service) string
		decide func(s *Service, runID string) error
	}{
		{"accept", func(t *testing.T, s *Service) string {
			id, dir := projectWithConfig(t, s, "p")
			gitProject(t, dir)
			// Its worktree is gone: every guard before the record passes,
			// and landing it — after the words are recorded — fails.
			gone := filepath.Join(t.TempDir(), "removed-worktree")
			recoveredRun(t, s, dir, &runlog.Run{ID: "r-late", ProjectID: id, TaskID: "T-001", Stage: "build", Status: "paused",
				PendingKind: "gate", Verdict: "PASSED", StartedAt: time.Now().UTC().Format(time.RFC3339),
				WorktreePath: gone, ExecutionRoot: gone, GateRoot: gone, Branch: "ducklab/T-001-build", BaseSHA: "0123456789abcdef0123456789abcdef01234567"})
			return "r-late"
		}, func(s *Service, id string) error {
			_, err := s.RunAcceptAs(context.Background(), id, "no jsdom", "mcp:opencode")
			return err
		}},
		{"reject", restoreFailingRun, func(s *Service, id string) error {
			return s.RunRejectAs(context.Background(), id, "not a bug", "mcp:opencode")
		}},
		{"abort", restoreFailingRun, func(s *Service, id string) error {
			return s.RunAbortAs(context.Background(), id, "withdrawn", "mcp:opencode")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.door, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno")
			runID := tc.setup(t, s)
			err := tc.decide(s, runID)
			if err == nil {
				t.Fatalf("%s succeeded; the fixture must make it fail after the words are recorded", tc.door)
			}
			if got := decisionEvents(t, s, runID); len(got) != 1 || got[0]["action"] != tc.door {
				t.Fatalf("decision events = %v, want the recorded %s attempt", got, tc.door)
			}
			out := outcomeEvents(t, s, runID)
			if len(out) != 1 || out[0]["action"] != tc.door || out[0]["outcome"] != "refused" || out[0]["error"] != err.Error() {
				t.Fatalf("decision outcomes = %v, want one %s refused with %q", out, tc.door, err)
			}
			if tc.door == "accept" {
				detail, _ := s.RunGet(context.Background(), runID)
				if detail.Run.Accepted || detail.Run.Status != "paused" || detail.Run.PendingKind != "gate" {
					t.Fatalf("the refused accept moved the run: %+v", detail.Run)
				}
			}
		})
	}
}

// restoreFailingRun is a paused build in a git project whose recorded tree
// snapshot does not exist, so restoring it — after a reject's or abort's
// words are recorded — fails.
func restoreFailingRun(t *testing.T, s *Service) string {
	t.Helper()
	id, dir := projectWithConfig(t, s, "p")
	gitProject(t, dir)
	recoveredRun(t, s, dir, &runlog.Run{ID: "r-late", ProjectID: id, TaskID: "T-001", Stage: "build", Status: "paused",
		PendingKind: "gate", Verdict: "PASSED", StartedAt: time.Now().UTC().Format(time.RFC3339),
		TreeSnapshot: "0123456789abcdef0123456789abcdef01234567"})
	return "r-late"
}

// B-524: an MCP operator accepted triage r-20261026-183029-kxck with the
// person's adjustment ("no jsdom") in its reason. The triage accept recorded
// only "accepted by mcp:opencode": the words reached no event, no commit and
// no screen. An accept's words are now its recorded decision on every path —
// the ones that commit (where they also ride the commit body) and the ones
// that do not.
func TestAnAcceptsReasonIsRecordedOnEveryAcceptPath(t *testing.T) {
	const reason = "Proposal is right. Person's adjustment: no jsdom."
	cases := []struct {
		name  string
		setup func(t *testing.T, s *Service) string
	}{
		{"triage gate", func(t *testing.T, s *Service) string {
			id := projectWithBugs(t, s, BugRequest{Title: "canvas ignores touch"})
			entry, _ := s.registry.Get(id)
			recoveredRun(t, s, entry.Path, &runlog.Run{ID: "r-triage", ProjectID: id, Stage: "triage", Status: "paused",
				PendingKind: "gate", Subject: "B-001", StartedAt: time.Now().UTC().Format(time.RFC3339),
				PendingData: map[string]interface{}{"proposals": []interface{}{map[string]interface{}{
					"bug": "B-001", "severity": "high", "reproducible": true, "reason": "actionable",
					"proposal": []interface{}{map[string]interface{}{"title": "Pointer events", "acceptance": []interface{}{"pointerdown drags"}, "owns": []interface{}{"index.html"}}},
				}}}})
			return "r-triage"
		}},
		{"build gate committed from its worktree", func(t *testing.T, s *Service) string {
			id, dir := projectWithDocs(t, s, nil)
			gitProject(t, dir)
			run, _ := pausedWorktreeRun(t, s, id, dir, "r-build")
			if err := os.WriteFile(filepath.Join(run.WorktreePath, "fix.txt"), []byte("fixed\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return "r-build"
		}},
		{"test gate without git", func(t *testing.T, s *Service) string {
			id, dir := projectWithConfig(t, s, "nogit")
			recoveredRun(t, s, dir, &runlog.Run{ID: "r-test", ProjectID: id, TaskID: "T-001", Stage: "test", Status: "paused",
				Verdict: "UNVERIFIED", PendingKind: "gate", StartedAt: time.Now().UTC().Format(time.RFC3339)})
			return "r-test"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno")
			runID := tc.setup(t, s)
			if _, err := s.RunAcceptAs(context.Background(), runID, reason, "mcp:opencode"); err != nil {
				t.Fatalf("accept: %v", err)
			}
			wantDecision(t, s, runID, "accept", "mcp:opencode", reason)

			// A repeated accept decided nothing; its words are not recorded,
			// and the reply says so rather than returning as if they were.
			again, err := s.RunAcceptAs(context.Background(), runID, "a second thought", "mcp:opencode")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(again.Info, "its message was not recorded") {
				t.Errorf("a repeated accept's info = %q, want it to say its words were not recorded", again.Info)
			}
			if got := decisionEvents(t, s, runID); len(got) != 1 {
				t.Errorf("a repeated accept recorded a decision: %v", got)
			}
		})
	}
}

// An accept with no words records no decision event: the record is for what
// was said, not a second copy of the resolution.
func TestAnAcceptWithoutWordsRecordsNoDecision(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithConfig(t, s, "nogit")
	recoveredRun(t, s, dir, &runlog.Run{ID: "r-quiet", ProjectID: id, TaskID: "T-001", Stage: "test", Status: "paused",
		Verdict: "UNVERIFIED", PendingKind: "gate", StartedAt: time.Now().UTC().Format(time.RFC3339)})
	if _, err := s.RunAccept(context.Background(), "r-quiet", ""); err != nil {
		t.Fatal(err)
	}
	if got := decisionEvents(t, s, "r-quiet"); len(got) != 0 {
		t.Fatalf("decision events = %v, want none", got)
	}
}

// Abort's reason was required by the MCP decide tool and then dropped: the
// engine's abort took no words at all. It is now the recorded decision, at
// a gate and at a distress pause alike.
func TestAnAbortsReasonIsRecorded(t *testing.T) {
	for _, pending := range []string{"gate", "budget", "error"} {
		t.Run(pending, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno")
			id, dir := projectWithConfig(t, s, "p")
			recoveredRun(t, s, dir, &runlog.Run{ID: "r-abort", ProjectID: id, Stage: "triage", Status: "paused",
				PendingKind: pending, StartedAt: time.Now().UTC().Format(time.RFC3339)})
			if err := s.RunAbortAs(context.Background(), "r-abort", "the person withdrew the bug", "mcp:elena"); err != nil {
				t.Fatal(err)
			}
			wantDecision(t, s, "r-abort", "abort", "mcp:elena", "the person withdrew the bug")
		})
	}
}

// A resume at a gate does nothing, so words sent with it — a note for the run
// or a reason for the record — would be dropped. Both are refused, naming the
// doors that do take words.
func TestAResumeAtAGateRefusesItsWords(t *testing.T) {
	cases := []struct{ name, note, reason string }{
		{"note", "build without jsdom", ""},
		{"reason", "", "the person adjusted it"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno")
			id, dir := projectWithConfig(t, s, "p")
			recoveredRun(t, s, dir, &runlog.Run{ID: "r-gate", ProjectID: id, Stage: "triage", Status: "paused",
				PendingKind: "gate", StartedAt: time.Now().UTC().Format(time.RFC3339)})
			_, err := s.RunResumeDecided(context.Background(), "r-gate", tc.note, tc.reason, "mcp:elena")
			if err == nil || !strings.Contains(err.Error(), "request changes to send your words") {
				t.Fatalf("resume at a gate with words = %v, want a refusal naming request changes", err)
			}
			if got := decisionEvents(t, s, "r-gate"); len(got) != 0 {
				t.Fatalf("a refused resume recorded a decision: %v", got)
			}
		})
	}
}

// A resumed build records its reason as the decision; the note rides the run
// (B-493) and stays on the checkpoint, where the desktop shows it.
func TestAResumesReasonIsRecordedBesideItsNote(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(artifact.Path(dir, artifact.KindPlan)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path(dir, artifact.KindPlan),
		[]byte("## M-001 — Core\n\n### T-001 — Do it\n\nDo.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	recoveredRun(t, s, dir, &runlog.Run{ID: "r-res", ProjectID: p.ID, TaskID: "T-001", Stage: "build", Mode: "solo",
		Status: "paused", PendingKind: "error", StartedAt: time.Now().UTC().Format(time.RFC3339)})
	if _, err := s.RunResumeDecided(context.Background(), "r-res", "use fs_write", "the fix is known", "mcp:elena"); err != nil {
		t.Fatal(err)
	}
	s.runsMu.RLock()
	rs := s.runs["r-res"]
	s.runsMu.RUnlock()
	select {
	case <-rs.done:
	case <-time.After(20 * time.Second):
		t.Fatal("the resumed run never finished")
	}
	wantDecision(t, s, "r-res", "resume", "mcp:elena", "the fix is known")
	events, _ := runlog.ReadEvents(s.RunDir("r-res"))
	noted := false
	for _, e := range events {
		if e.Type == "checkpoint" && e.Data["note"] == "use fs_write" {
			noted = true
		}
	}
	if !noted {
		t.Error("the resume note is not on the checkpoint")
	}
}

// A promote note is read only for a reopened bug. On any other bug it reached
// neither the task nor the audit; it is refused, and nothing is promoted.
func TestAPromoteNoteOnABugThatWasNotReopenedIsRefused(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id := projectWithBugs(t, s, BugRequest{Title: "canvas ignores touch"})
	entry, _ := s.registry.Get(id)
	if err := os.WriteFile(filepath.Join(entry.Path, "index.html"), []byte("<canvas></canvas>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyTriage(context.Background(), id, []map[string]interface{}{{
		"bug": "B-001",
		"proposal": []map[string]interface{}{{
			"title": "pointer events", "acceptance": []string{"pointerdown drags"}, "owns": []string{"index.html"},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	_, err := s.BugPromoteWithNote(context.Background(), id, "B-001", "mcp:opencode", "no jsdom")
	if err == nil || !strings.Contains(err.Error(), "bug_edit") || !strings.Contains(err.Error(), "read only for a reopened bug") {
		t.Fatalf("promote note on a fresh bug = %v, want a refusal naming bug_edit", err)
	}
	bugs, _ := s.BugList(context.Background(), id, false)
	if len(bugs) != 1 || bugs[0].TaskID != "" {
		t.Fatalf("a refused promote created a task: %+v", bugs)
	}
	if _, err := s.BugPromoteWithNote(context.Background(), id, "B-001", "mcp:opencode", ""); err != nil {
		t.Fatalf("the same promote without a note: %v", err)
	}
}

// Review of #177: the decision record was written after the decision, with
// its error discarded — a run log that stopped accepting writes let accept,
// abort and resume go through while the words vanished again, B-524 itself.
// Every door now records first and refuses when the record fails: nothing is
// decided, and the error says why.
func TestADecisionWhoseReasonCannotBeRecordedIsNotTaken(t *testing.T) {
	failing := func(t *testing.T) {
		t.Helper()
		saved := appendDecisionEvent
		appendDecisionEvent = func(*runlog.Writer, map[string]interface{}) error {
			return fmt.Errorf("disk full")
		}
		t.Cleanup(func() { appendDecisionEvent = saved })
	}
	gateRun := func(t *testing.T, s *Service, stage, pending string) (string, string) {
		t.Helper()
		id := projectWithBugs(t, s, BugRequest{Title: "canvas ignores touch"})
		entry, _ := s.registry.Get(id)
		recoveredRun(t, s, entry.Path, &runlog.Run{ID: "r-fail", ProjectID: id, TaskID: "T-001", Stage: stage, Mode: "solo",
			Status: "paused", PendingKind: pending, Verdict: "UNVERIFIED", Subject: "B-001",
			StartedAt: time.Now().UTC().Format(time.RFC3339),
			PendingData: map[string]interface{}{"proposals": []interface{}{map[string]interface{}{
				"bug": "B-001", "severity": "high", "reproducible": true, "reason": "actionable",
			}}}})
		return id, "r-fail"
	}
	cases := []struct {
		door, stage, pending string
		decide               func(s *Service, runID string) error
	}{
		{"accept", "triage", "gate", func(s *Service, id string) error {
			_, err := s.RunAcceptAs(context.Background(), id, "no jsdom", "mcp:opencode")
			return err
		}},
		{"accept", "test", "gate", func(s *Service, id string) error {
			_, err := s.RunAcceptAs(context.Background(), id, "no jsdom", "mcp:opencode")
			return err
		}},
		{"reject", "triage", "gate", func(s *Service, id string) error {
			return s.RunRejectAs(context.Background(), id, "not a bug", "mcp:opencode")
		}},
		{"abort", "triage", "gate", func(s *Service, id string) error {
			return s.RunAbortAs(context.Background(), id, "withdrawn", "mcp:opencode")
		}},
		{"abort", "build", "budget", func(s *Service, id string) error {
			return s.RunAbortAs(context.Background(), id, "withdrawn", "mcp:opencode")
		}},
		{"resume", "build", "error", func(s *Service, id string) error {
			_, err := s.RunResumeDecided(context.Background(), id, "", "the fix is known", "mcp:opencode")
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.door+"/"+tc.stage+"/"+tc.pending, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno")
			projectID, runID := gateRun(t, s, tc.stage, tc.pending)
			failing(t)
			err := tc.decide(s, runID)
			if err == nil || !strings.Contains(err.Error(), "its reason could not be recorded") || !strings.Contains(err.Error(), "nothing was decided") {
				t.Fatalf("%s with an unrecordable reason = %v, want a refusal", tc.door, err)
			}
			detail, gErr := s.RunGet(context.Background(), runID)
			if gErr != nil {
				t.Fatal(gErr)
			}
			if detail.Run.Status != "paused" || detail.Run.PendingKind != tc.pending || detail.Run.Accepted {
				t.Fatalf("the refused %s changed the run: %s/%s accepted=%v", tc.door, detail.Run.Status, detail.Run.PendingKind, detail.Run.Accepted)
			}
			if tc.stage == "triage" {
				bugs, _ := s.BugList(context.Background(), projectID, false)
				if len(bugs) != 1 || bugs[0].Status != "open" {
					t.Fatalf("the refused %s applied the triage: %+v", tc.door, bugs)
				}
			}
		})
	}
}

// Review of #177: the MCP folded its actor into reject's reason and the
// engine recorded no actor, so the desktop credited an operator's reject to
// the person. The actor is a field on both records; the reason is only the
// reason.
func TestARejectNamesItsActorApartFromItsReason(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithConfig(t, s, "p")
	recoveredRun(t, s, dir, &runlog.Run{ID: "r-rej", ProjectID: id, Stage: "triage", Status: "paused",
		PendingKind: "gate", StartedAt: time.Now().UTC().Format(time.RFC3339)})
	if err := s.RunRejectAs(context.Background(), "r-rej", "not a bug", "mcp:codex"); err != nil {
		t.Fatal(err)
	}
	wantDecision(t, s, "r-rej", "reject", "mcp:codex", "not a bug")
	events, _ := runlog.ReadEvents(s.RunDir("r-rej"))
	for _, e := range events {
		if e.Type == "human" && e.Data["action"] == "reject" {
			if e.Data["actor"] != "mcp:codex" || e.Data["reason"] != "not a bug" {
				t.Fatalf("reject's human event = %v, want actor mcp:codex and the bare reason", e.Data)
			}
			return
		}
	}
	t.Fatal("no reject event")
}
