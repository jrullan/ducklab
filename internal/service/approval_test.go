package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

// laneRefusedRun is B-530's situation: a build whose accept needs a lane
// widening only a person may approve, refused once so the engine offers it.
func laneRefusedRun(t *testing.T, s *Service, runID string) (*runlog.Run, *vcs.Git, string, string) {
	t.Helper()
	plan := `## M-001 — Parallel work

### T-001 — Leaf implementation

**Produces:** file:owned.txt

### T-002 — Shared integration

**Produces:** file:shared.txt
`
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: plan})
	git := gitProject(t, dir)
	base := mustHead(t, git)
	run, _ := pausedWorktreeRun(t, s, id, dir, runID)
	for path, body := range map[string]string{"owned.txt": "mine\n", "shared.txt": "not mine\n"} {
		if err := os.WriteFile(filepath.Join(run.WorktreePath, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.RunAccept(context.Background(), run.ID, ""); err == nil || !strings.Contains(err.Error(), "outside T-001") {
		t.Fatalf("accept error = %v, want the lane refusal that offers a widening", err)
	}
	return run, git, base, dir
}

func approvalEvents(t *testing.T, s *Service, runID, typ string) []runlog.Event {
	t.Helper()
	detail, err := s.RunGet(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	var out []runlog.Event
	for _, e := range detail.Events {
		if e.Type == typ {
			out = append(out, *e)
		}
	}
	return out
}

func approvalOf(t *testing.T, s *Service, runID, reqID string) runlog.ApprovalRequest {
	t.Helper()
	detail, err := s.RunGet(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range detail.Run.ApprovalRequests {
		if r.ID == reqID {
			return r
		}
	}
	t.Fatalf("run %s has no approval request %s: %+v", runID, reqID, detail.Run.ApprovalRequests)
	return runlog.ApprovalRequest{}
}

// B-530: refused as an operator, an agent with the person's agreement files
// an approval request; the person confirms it; the decision lands as the
// person's and the record carries both actors.
func TestAConfirmedApprovalRequestLandsWithBothActors(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	run, git, base, dir := laneRefusedRun(t, s, "r-approval-landed")

	// The operator's own attempt is refused and recorded with its name.
	if _, err := s.RunAcceptAsWithOptions(context.Background(), run.ID, "", "mcp:opencode", AcceptOptions{
		LaneWidening: []string{"shared.txt"},
	}); err == nil || !strings.Contains(err.Error(), "direct human approval") || !strings.Contains(err.Error(), "approval_request") {
		t.Fatalf("operator lane widening = %v, want the human-only refusal naming the approval door", err)
	}

	req, err := s.ApprovalRequestFile(context.Background(), run.ID, ApprovalRequestInput{
		Action: "accept", Reason: "Jose said Ok in chat",
	}, "mcp:opencode")
	if err != nil {
		t.Fatalf("file approval request: %v", err)
	}
	if req.ID != "ar-1" || req.Status != ApprovalPending || req.RequestedBy != "mcp:opencode" ||
		!slices.Equal(req.LaneWidening, []string{"shared.txt"}) || req.PendingKind != "gate" {
		t.Fatalf("filed request = %+v; want pending, by the operator, carrying the offered lane", req)
	}
	if got := approvalEvents(t, s, run.ID, "approval_requested"); len(got) != 1 {
		t.Fatalf("approval_requested events = %d, want the doorbell once", len(got))
	}
	if mustHead(t, git) != base {
		t.Fatal("filing a request landed something")
	}

	// An agent cannot approve, its own request or any other.
	if _, err := s.ApprovalRequestApprove(context.Background(), run.ID, req.ID, "mcp:opencode"); err == nil {
		t.Fatal("an agent approved an approval request")
	}
	if _, err := s.ApprovalRequestApprove(context.Background(), run.ID, req.ID, "agent"); err == nil {
		t.Fatal("the agent credential's actor approved an approval request")
	}

	out, err := s.ApprovalRequestApprove(context.Background(), run.ID, req.ID, "human")
	if err != nil {
		t.Fatalf("person approves: %v", err)
	}
	if out.Accept == nil || out.Accept.CommitSHA == "" || mustHead(t, git) != out.Accept.CommitSHA {
		t.Fatalf("approved accept did not land: %+v head=%s", out.Accept, mustHead(t, git))
	}
	final := approvalOf(t, s, run.ID, req.ID)
	if final.Status != ApprovalLanded || final.RequestedBy != "mcp:opencode" || final.DecidedBy != "human" ||
		!strings.Contains(final.Outcome, out.Accept.CommitSHA) {
		t.Fatalf("recorded request = %+v; want landed, requested by the operator, decided by the person", final)
	}
	granted := approvalEvents(t, s, run.ID, "approval_granted")
	if len(granted) != 1 || granted[0].Data["requested_by"] != "mcp:opencode" || granted[0].Data["approved_by"] != "human" {
		t.Fatalf("approval_granted = %+v; want both actors", granted)
	}
	widened := approvalEvents(t, s, run.ID, "lane_widened")
	if len(widened) != 1 || widened[0].Data["actor"] != "human" {
		t.Fatalf("lane_widened = %+v; want the person's decision", widened)
	}
	plan, err := artifact.Load(dir, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	if task := plan.Section("T-001"); task == nil || !slices.Contains(task.Owns, "shared.txt") {
		t.Fatalf("approved lane was not recorded on T-001: %+v", task)
	}
	if _, err := s.ApprovalRequestApprove(context.Background(), run.ID, req.ID, "human"); err == nil {
		t.Fatal("a decided request was decided twice")
	}
}

// Declined, nothing lands, the run keeps waiting, and the request cannot be
// approved afterwards.
func TestADeclinedApprovalRequestDoesNotLand(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	run, git, base, dir := laneRefusedRun(t, s, "r-approval-declined")
	req, err := s.ApprovalRequestFile(context.Background(), run.ID, ApprovalRequestInput{
		Action: "accept", Reason: "the person agreed", LaneWidening: []string{"shared.txt"},
	}, "mcp:opencode")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApprovalRequestDecline(context.Background(), run.ID, req.ID, "", "mcp:opencode"); err == nil {
		t.Fatal("an agent declined on the person's behalf")
	}
	declined, err := s.ApprovalRequestDecline(context.Background(), run.ID, req.ID, "not that lane", "human")
	if err != nil {
		t.Fatalf("decline: %v", err)
	}
	if declined.Status != ApprovalDeclined || declined.DeclineReason != "not that lane" || declined.DecidedBy != "human" {
		t.Fatalf("declined = %+v", declined)
	}
	if _, err := s.ApprovalRequestApprove(context.Background(), run.ID, req.ID, "human"); err == nil {
		t.Fatal("a declined request was approved")
	}
	if mustHead(t, git) != base {
		t.Fatal("a declined request landed")
	}
	plan, err := artifact.Load(dir, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	if task := plan.Section("T-001"); task == nil || slices.Contains(task.Owns, "shared.txt") {
		t.Fatalf("a declined request widened the lane: %+v", task)
	}
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.Status != "paused" || detail.Run.PendingKind != "gate" {
		t.Fatalf("run after decline = %s/%s, want still waiting at its gate", detail.Run.Status, detail.Run.PendingKind)
	}
	if got := approvalEvents(t, s, run.ID, "approval_declined"); len(got) != 1 || got[0].Data["requested_by"] != "mcp:opencode" {
		t.Fatalf("approval_declined = %+v", got)
	}
}

// A request names the exact decision it was filed against. The person asked
// "widen this lane" must not see it applied to whatever the run waits on now,
// nor to a path the decision never offered.
func TestAnApprovalRequestIsBoundToItsDecision(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	run, _, _, _ := laneRefusedRun(t, s, "r-approval-bound")
	if _, err := s.ApprovalRequestFile(context.Background(), run.ID, ApprovalRequestInput{
		Action: "accept", Reason: "x", LaneWidening: []string{"elsewhere.txt"},
	}, "mcp:opencode"); err == nil || !strings.Contains(err.Error(), "not offered") {
		t.Fatalf("unoffered lane = %v, want refused at filing", err)
	}
	if _, err := s.ApprovalRequestFile(context.Background(), run.ID, ApprovalRequestInput{Action: "accept"}, "mcp:opencode"); err == nil {
		t.Fatal("a request without a reason was filed")
	}
	if _, err := s.ApprovalRequestFile(context.Background(), run.ID, ApprovalRequestInput{Action: "accept", Reason: "x"}, "human"); err == nil {
		t.Fatal("the person filed a request to themselves")
	}
	req, err := s.ApprovalRequestFile(context.Background(), run.ID, ApprovalRequestInput{Action: "accept", Reason: "x"}, "agent")
	if err != nil {
		t.Fatal(err)
	}
	// The run moves on to another decision before the person looks.
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	rs.wmu.Lock()
	rs.run.PendingSince = "2099-01-01T00:00:00Z"
	rs.wmu.Unlock()
	if _, err := s.ApprovalRequestApprove(context.Background(), run.ID, req.ID, "human"); err == nil || !strings.Contains(err.Error(), "no longer pending") {
		t.Fatalf("approve after the decision moved = %v, want refused", err)
	}
	if got := approvalOf(t, s, run.ID, req.ID); got.Status != ApprovalSuperseded {
		t.Fatalf("status = %s, want superseded", got.Status)
	}
}

// B-530's refusal must be on the record with the actor the engine derived,
// not the one the body claimed: manual verification is the person's claim
// too, and an agent's is refused and recorded.
func TestAnAgentCannotConfirmManualVerification(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	gitProject(t, dir)
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-manual-verify")
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	rs.wmu.Lock()
	rs.run.PendingData = map[string]interface{}{"human_verification": []map[string]interface{}{{"id": 1, "text": "the dialog opens"}}}
	rs.wmu.Unlock()
	_, err := s.RunAcceptAsWithOptions(context.Background(), run.ID, "", "agent", AcceptOptions{HumanVerified: []int{1}})
	if err == nil || !strings.Contains(err.Error(), "confirmed by a person") {
		t.Fatalf("agent manual verification = %v, want refused", err)
	}
	if got := approvalEvents(t, s, run.ID, "human_verification_confirmed"); len(got) != 0 {
		t.Fatalf("an agent's claim was recorded as a confirmation: %+v", got)
	}
	refused := approvalEvents(t, s, run.ID, "accept_refused")
	if len(refused) != 1 || refused[0].Data["actor"] != "agent" {
		t.Fatalf("accept_refused = %+v, want one, by the agent", refused)
	}
}
