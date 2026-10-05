package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

// B-501 reproduced in shape: TI-36X T-005's test-first pair spent both of its
// rounds, the gate went red, and the reviewer's LAST verdict still requested
// changes (two majors open). The stage passed on the gate alone and nothing on
// the record, the gate event or the card said the reviewer still objected.
const b501Objection = `{"verdict":"request-changes","findings":[` +
	`{"severity":"major","file":"tests/lcd-flow.test.mjs","line":283,"issue":"13 tests pin T-003 factorial/percent/square semantics","fix":"assert only the LCD flow"},` +
	`{"severity":"major","file":"tests/lcd-flow.test.mjs","line":104,"issue":"nothing asserts the cursor/delete actions are inert outside entry state","fix":"add an inert-outside-entry assertion"},` +
	`{"severity":"minor","file":"tests/lcd-flow.test.mjs","line":529,"issue":"angleMode assigned directly","fix":"dispatch the mode action"}]}`

const b501Approval = `{"verdict":"approve","findings":[]}`

// startPairTestFirst runs a real pair test-first over a fake provider: the
// implementer writes a failing test on its first call, the reviewer answers
// every review with lastVerdict. The gate is green before and red after.
func startPairTestFirst(t *testing.T, lastVerdict string, thenBuild bool) (*Service, string, *runState) {
	t.Helper()
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
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
	fake := s.providers["fake"].(*provider.Fake)
	var implementerCalls atomic.Int32
	fake.ScriptFunc = func(req provider.ChatRequest, call int) *provider.ChatResponse {
		for _, m := range req.Messages {
			if m.Role == "system" && strings.Contains(m.Content, "You are the reviewer") {
				return &provider.ChatResponse{Choices: []provider.Choice{{
					Message: provider.Message{Content: lastVerdict}, FinishReason: provider.FinishStop,
				}}}
			}
		}
		var message provider.Message
		if implementerCalls.Add(1) == 1 {
			tc := provider.ToolCall{ID: "call-write", Type: "function"}
			tc.Function.Name = "fs_write"
			tc.Function.Arguments = `{"path":"regression_test.go","content":"package fixture\n\nimport \"testing\"\n\nfunc TestRegression(t *testing.T) { t.Fatal(\"missing\") }\n"}`
			message.ToolCalls = []provider.ToolCall{tc}
			return &provider.ChatResponse{Choices: []provider.Choice{{Message: message, FinishReason: provider.FinishToolCalls}}}
		}
		message.Content = "The failing regression test is written."
		return &provider.ChatResponse{Choices: []provider.Choice{{Message: message, FinishReason: provider.FinishStop}}}
	}

	req := TestFirstRequest{TaskID: "T-001", Mode: "pair", Ducklings: []string{"pato-uno", "pato-dos"}}
	if thenBuild {
		req.ThenBuild = true
		req.Build = RunRequest{Mode: "solo"}
	}
	run, err := s.TestStart(context.Background(), projectID, req)
	if err != nil {
		t.Fatal(err)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	select {
	case <-rs.done:
	case <-time.After(30 * time.Second):
		t.Fatal("the test-first run did not reach its gate")
	}
	t.Cleanup(func() {
		// A chained build may still be running; let every run settle before
		// the temp dirs go.
		s.runsMu.RLock()
		all := make([]*runState, 0, len(s.runs))
		for _, r := range s.runs {
			all = append(all, r)
		}
		s.runsMu.RUnlock()
		for _, r := range all {
			if snap := r.snapshotRun(); snap.Status == "running" || snap.Status == "queued" {
				_ = s.RunAbort(context.Background(), snap.ID)
			}
			if r.done != nil {
				select {
				case <-r.done:
				case <-time.After(15 * time.Second):
				}
			}
		}
	})
	return s, projectID, rs
}

func lastEvent(t *testing.T, rs *runState, kind string) map[string]interface{} {
	t.Helper()
	events, err := runlog.ReadEvents(rs.runDir)
	if err != nil {
		t.Fatal(err)
	}
	var found map[string]interface{}
	for _, e := range events {
		if e.Type == kind {
			found = e.Data
		}
	}
	return found
}

func TestATestFirstEndingOnObjectionsSaysSoEverywhere(t *testing.T) {
	_, _, rs := startPairTestFirst(t, b501Objection, false)
	run := rs.snapshotRun()
	if run.Verdict != "PASSED" {
		t.Fatalf("verdict = %q — the gate went green to red; the word stays the gate's fact", run.Verdict)
	}
	if run.Status != "paused" || run.PendingKind != "gate" {
		t.Fatalf("state = %s/%s, want paused at the gate", run.Status, run.PendingKind)
	}
	rounds := 0
	events, _ := runlog.ReadEvents(rs.runDir)
	for _, e := range events {
		if e.Type == "round_gate" {
			rounds++
		}
	}
	if rounds != 2 {
		t.Fatalf("round gates = %d, want the script's 2 rounds spent", rounds)
	}

	// The verdict detail.
	verdict := lastEvent(t, rs, "verdict")
	detail, _ := verdict["detail"].(string)
	if !strings.Contains(detail, "reviewer still requests changes") ||
		!strings.Contains(detail, "13 tests pin T-003") || !strings.Contains(detail, "inert outside entry state") ||
		!strings.Contains(detail, "2 blocking finding(s) (3 in all)") || !strings.Contains(detail, "build's oracle") {
		t.Fatalf("verdict detail hides the objection: %q", detail)
	}
	if strings.Contains(detail, "angleMode") {
		t.Errorf("a minor finding was worded as blocking: %q", detail)
	}
	// The durable record.
	if run.ReviewEvidence == nil || run.ReviewEvidence.Status != "dissent" ||
		run.ReviewEvidence.Verdict != "request-changes" || run.ReviewEvidence.Findings != 3 {
		t.Fatalf("review evidence = %+v, want dissent / request-changes / 3", run.ReviewEvidence)
	}
	if lastEvent(t, rs, "reviewer_dissent") == nil {
		t.Error("no reviewer_dissent event on the record")
	}
	// The pending decision and the gate event carry the same objection.
	for name, data := range map[string]map[string]interface{}{
		"pending_data": run.PendingData, "human_needed": lastEvent(t, rs, "human_needed"),
	} {
		if data == nil {
			t.Fatalf("%s missing", name)
		}
		if data["dissent"] != "request-changes" {
			t.Errorf("%s dissent = %v", name, data["dissent"])
		}
		blocking, ok := data["dissent_findings"].([]interface{})
		if !ok {
			if typed, typedOK := data["dissent_findings"].([]map[string]interface{}); typedOK {
				for _, f := range typed {
					blocking = append(blocking, f)
				}
				ok = true
			}
		}
		if !ok || len(blocking) != 2 {
			t.Errorf("%s dissent_findings = %#v, want the 2 majors", name, data["dissent_findings"])
		}
		if d, _ := data["detail"].(string); !strings.Contains(d, "reviewer still requests changes") {
			t.Errorf("%s detail = %q", name, d)
		}
	}
	// A person may still decide to lock it in: Accept stays offered.
	offersAccept := false
	for _, action := range runNext(run) {
		offersAccept = offersAccept || action == "accept"
	}
	if !offersAccept {
		t.Errorf("next = %v — the objection must inform the decision, not remove it", runNext(run))
	}
}

func TestAnApprovedTestFirstGateIsUnchanged(t *testing.T) {
	_, _, rs := startPairTestFirst(t, b501Approval, false)
	run := rs.snapshotRun()
	if run.Verdict != "PASSED" || run.Status != "paused" || run.PendingKind != "gate" {
		t.Fatalf("state = %s %s/%s", run.Verdict, run.Status, run.PendingKind)
	}
	if _, has := run.PendingData["dissent"]; has {
		t.Errorf("an approved test carries a dissent: %#v", run.PendingData)
	}
	if d, _ := run.PendingData["detail"].(string); strings.Contains(d, "reviewer") {
		t.Errorf("an approved test's detail mentions the reviewer: %q", d)
	}
	if run.ReviewEvidence != nil {
		t.Errorf("review evidence = %+v, want none (unchanged)", run.ReviewEvidence)
	}
	if lastEvent(t, rs, "reviewer_dissent") != nil {
		t.Error("an approved test emitted reviewer_dissent")
	}
}

// The unattended chain — the autopilot launches every test chained — must not
// commit an oracle the reviewer still objects to.
func TestTheTddChainHoldsATestTheReviewerStillObjectsTo(t *testing.T) {
	s, projectID, rs := startPairTestFirst(t, b501Objection, true)
	run := rs.snapshotRun()
	if run.Accepted {
		t.Fatalf("the chain committed a test with open objections: %s", run.Resolution)
	}
	if run.Status != "paused" || run.PendingKind != "gate" || run.PendingData["chain_held"] != true {
		t.Fatalf("state = %s/%s %#v, want held at the gate", run.Status, run.PendingKind, run.PendingData)
	}
	if gate := lastEvent(t, rs, "human_needed"); gate == nil || gate["chain_held"] != true || gate["dissent"] != "request-changes" {
		t.Fatalf("human_needed = %#v", gate)
	}
	runs, _ := s.RunList(context.Background(), RunFilter{ProjectID: projectID})
	for _, r := range runs {
		if r.Stage == "build" {
			t.Fatalf("a build started over a held test: %s", r.ID)
		}
	}
	if run.ChainBuild == nil {
		t.Error("the chain promise left the record; a person's accept could not continue it")
	}
}

func TestTheTddChainStillCommitsAnApprovedTest(t *testing.T) {
	_, _, rs := startPairTestFirst(t, b501Approval, true)
	run := rs.snapshotRun()
	if !run.Accepted || !strings.Contains(run.Resolution, "auto:tdd") {
		t.Fatalf("approved chained test was not committed by the chain: %s %s %s", run.Status, run.Resolution, run.Failure)
	}
}

// The floor under the executors: an unattended actor cannot accept a gate
// that carries a standing dissent, whichever path brought it there; a person
// can.
func TestAnUnattendedAcceptRefusesStandingDissent(t *testing.T) {
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
		[]byte("## M-001 — Core\n\n### T-003 — Do a thing\n\nDo it.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{
		ID: "r-tf", ProjectID: p.ID, TaskID: "T-003", Stage: "test",
		Status: "paused", Verdict: "PASSED", PendingKind: "gate",
		StartedAt: "2026-10-04T21:27:15Z",
		// No chain on this record: a person's accept would start the build
		// asynchronously, and this test is about the accept door alone.
		PendingData: map[string]interface{}{"kind": "test_first", "dissent": "request-changes"},
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())
	for _, actor := range []string{"auto:tdd", "auto:yolo"} {
		_, err := s.RunAcceptAs(context.Background(), "r-tf", "unattended", actor)
		if err == nil || !strings.Contains(err.Error(), "reviewer still requests changes") {
			t.Fatalf("%s accept = %v, want refused on the standing dissent", actor, err)
		}
	}
	got, _ := s.RunGet(context.Background(), "r-tf")
	if got.Run.Accepted {
		t.Fatal("an unattended actor accepted over the dissent")
	}
	if _, err := s.RunAcceptAs(context.Background(), "r-tf", "read them; accepting", "human"); err != nil &&
		strings.Contains(err.Error(), "reviewer still requests changes") {
		t.Fatalf("a person's accept was refused on the dissent: %v", err)
	}
}

// The LAST verdict decides: round 1's objections answered by a round-2
// approval are not standing, and a run with no reviewer has nothing open.
func TestLastOpenReviewReadsOnlyTheFinalVerdict(t *testing.T) {
	msg := func(verdict string, findings ...map[string]interface{}) *runlog.Event {
		raw := make([]interface{}, 0, len(findings))
		for _, f := range findings {
			raw = append(raw, f)
		}
		return &runlog.Event{Type: "message", Data: map[string]interface{}{"verdict": verdict, "findings": raw}}
	}
	major := map[string]interface{}{"severity": "major", "issue": "pins another task", "line": float64(7)}
	minor := map[string]interface{}{"severity": "minor", "issue": "naming"}
	if _, open := lastOpenReview([]*runlog.Event{msg("request-changes", major), msg("approve")}); open {
		t.Error("an answered objection still reads as open")
	}
	if _, open := lastOpenReview([]*runlog.Event{{Type: "message", Data: map[string]interface{}{"content": "solo"}}}); open {
		t.Error("a run with no reviewer invented an objection")
	}
	review, open := lastOpenReview([]*runlog.Event{msg("approve"), msg("request_changes", major, minor)})
	if !open || review.Verdict != "request_changes" || review.Total != 2 || len(review.Blocking) != 1 ||
		review.Blocking[0]["line"] != 7 {
		t.Fatalf("review = %+v open=%v", review, open)
	}
}
