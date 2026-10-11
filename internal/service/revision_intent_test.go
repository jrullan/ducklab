package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

// B-521 fixtures: a project whose intake runs on the fake provider and pauses
// at its gate with a requirements proposal, plus one reference document.
func revisionChainProject(t *testing.T) (*Service, string, string, string) {
	t.Helper()
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	projectID, root := projectWithDocs(t, s, map[artifact.Kind]string{})
	git := vcs.New(root)
	if !git.HasGit() {
		if err := git.Init(); err != nil {
			t.Fatal(err)
		}
	}
	_ = git.AddAll()
	_, _ = git.Commit("fixture")
	// The project preset rides every intake (B-474). It made every TI-36X
	// revision look as if it named references of its own, so the parent's
	// guidebook was never inherited.
	if err := os.WriteFile(filepath.Join(root, ".ducklab", "preset.md"), []byte("Delivery: one static web page.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	guide := filepath.Join(t.TempDir(), "guidebook.md")
	if err := os.WriteFile(guide, []byte("# Guidebook\n\nThe calculator converts real numbers to polar form.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := s.providers["fake"].(*provider.Fake)
	var drafts atomic.Int64
	fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
		// Each draft differs, so every revision changes something.
		n := drafts.Add(1)
		content := fmt.Sprintf("## REQ-001 — Polar conversion\n\n**Priority:** must\n\nReal numbers convert to polar form (draft %d).\n", n)
		for _, m := range req.Messages {
			// A revision first triages which sections change.
			if strings.Contains(m.Content, "NEW: <title> for a section to ADD") {
				content = fmt.Sprintf("NEW: Polar detail %d", n)
			}
			if m.Role == "system" && strings.Contains(m.Content, "You are the reviewer") {
				content = `{"verdict":"approve","findings":[]}`
			}
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{
			Message:      provider.Message{Role: "assistant", Content: content},
			FinishReason: provider.FinishStop,
		}}}
	}
	return s, projectID, root, guide
}

func startAndSettle(t *testing.T, s *Service, projectID string, req StageRequest) *runlog.Run {
	t.Helper()
	run, err := s.StageStart(context.Background(), projectID, req)
	if err != nil {
		t.Fatal(err)
	}
	cleanupStartedRun(t, s, run.ID)
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	select {
	case <-rs.done:
	case <-time.After(60 * time.Second):
		t.Fatalf("run %s never settled", run.ID)
	}
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.Status != "paused" || detail.Run.PendingKind != "gate" {
		t.Fatalf("run %s is %s/%s (failure: %s), want paused at its gate", run.ID, detail.Run.Status, detail.Run.PendingKind, detail.Run.Failure)
	}
	return detail.Run
}

func persistedStageRequest(t *testing.T, root, runID string) StageRequest {
	t.Helper()
	req, ok := loadStageRequest(filepath.Join(root, ".ducklab", "runs", runID))
	if !ok {
		t.Fatalf("run %s has no persisted stage request", runID)
	}
	return req
}

// TI-36X (tpfn → eett → mrrq → 6kyd): three chained revisions of one intake
// proposal. The journal keeps ONE intention with three revisions — never
// INT-002..004 made of reviewer findings — and every revision runs the same
// job: the guidebook, the mode and the rounds the person chose.
func intakeChainOfThree(t *testing.T) (*Service, string, string, string, []*runlog.Run) {
	t.Helper()
	s, projectID, root, guide := revisionChainProject(t)
	first := startAndSettle(t, s, projectID, StageRequest{
		Stage: "intake", Mode: "solo", Rounds: 2, Refs: []string{guide},
		From: "Fill the gap between the implemented and the intended calculator.",
	})
	// Revision 1: only the reviewer's findings, forwarded.
	r1 := startAndSettle(t, s, projectID, StageRequest{
		Stage: "intake", RevisesRun: first.ID,
		ReviseFindings: "- [major] REQ-001 invents an error for real-number polar conversion.",
	})
	// Revision 2: the person's own note, and the findings again plus a new one.
	r2 := startAndSettle(t, s, projectID, StageRequest{
		Stage: "intake", RevisesRun: r1.ID, Revise: "Keep the wording close to the guidebook.",
		ReviseFindings: "- [major] REQ-001 invents an error for real-number polar conversion.\n- [critical] The fraction paragraph was deleted.",
	})
	// Revision 3: no RevisesRun — the proposal on the table names the parent.
	r3 := startAndSettle(t, s, projectID, StageRequest{Stage: "intake", Revise: "Restore the Ans sentence."})
	return s, projectID, root, guide, []*runlog.Run{first, r1, r2, r3}
}

func TestIntakeRevisionChainStaysUnderOneIntentAndInheritsTheJob(t *testing.T) {
	s, _, root, guide, runs := intakeChainOfThree(t)
	first, r1, r2, r3 := runs[0], runs[1], runs[2], runs[3]

	intent, err := artifact.Load(root, artifact.KindIntent)
	if err != nil {
		t.Fatal(err)
	}
	if len(intent.Sections) != 1 {
		var ids []string
		for _, sec := range intent.Sections {
			ids = append(ids, sec.ID+" "+sec.Title)
		}
		t.Fatalf("intent entries = %v, want one intention with three revisions", ids)
	}
	sec := intent.Sections[0]
	if sec.ID != "INT-001" || sec.Field("run") != first.ID || !strings.Contains(sec.Title, "Fill the gap") {
		t.Fatalf("intent = %s %q run %s", sec.ID, sec.Title, sec.Field("run"))
	}
	if got := sec.Field("revisions"); got != strings.Join([]string{r1.ID, r2.ID, r3.ID}, ", ") {
		t.Fatalf("Revisions = %q", got)
	}
	if got := sec.Field("outcome"); got != "pending" {
		t.Fatalf("outcome = %q, want pending: superseding a draft in the chain does not close the request", got)
	}
	for n, run := range []*runlog.Run{r1, r2, r3} {
		if !strings.Contains(sec.Body, "### Revision "+string(rune('1'+n))+" — "+run.ID) {
			t.Fatalf("revision %d (%s) is missing from the journal:\n%s", n+1, run.ID, sec.Body)
		}
	}
	// Provenance: findings are labelled, the person's note is the person's.
	rev1 := sec.Body[strings.Index(sec.Body, "### Revision 1"):strings.Index(sec.Body, "### Revision 2")]
	if strings.Contains(rev1, "#### The person's note") || !strings.Contains(rev1, "#### Reviewer findings carried over\n\n- [major] REQ-001 invents") {
		t.Fatalf("revision 1 attributes findings wrongly:\n%s", rev1)
	}
	rev2 := sec.Body[strings.Index(sec.Body, "### Revision 2"):strings.Index(sec.Body, "### Revision 3")]
	if !strings.Contains(rev2, "#### The person's note\n\nKeep the wording close to the guidebook.") {
		t.Fatalf("revision 2 lost the person's note:\n%s", rev2)
	}
	// No snowball: the finding already forwarded is not recorded again.
	if strings.Contains(rev2, "invents an error") || !strings.Contains(rev2, "[critical] The fraction paragraph was deleted.") {
		t.Fatalf("revision 2 re-recorded an earlier finding or lost the new one:\n%s", rev2)
	}

	// The run records name the chain.
	for i, run := range []*runlog.Run{r1, r2, r3} {
		detail, _ := s.RunGet(context.Background(), run.ID)
		parent := runs[i].ID
		if detail.Run.Intent != "INT-001" || detail.Run.RevisesRun != parent || detail.Run.Revision != i+1 {
			t.Fatalf("revision %d record: intent %q revises %q number %d", i+1, detail.Run.Intent, detail.Run.RevisesRun, detail.Run.Revision)
		}
	}

	// The job carries over: references (wholesale), brief, mode, rounds.
	for _, run := range []*runlog.Run{r1, r2, r3} {
		req := persistedStageRequest(t, root, run.ID)
		if !slices.Contains(req.Refs, guide) || !slices.Contains(req.Refs, filepath.Join(root, ".ducklab", "preset.md")) {
			t.Fatalf("%s refs = %v, want the guidebook inherited beside the preset", run.ID, req.Refs)
		}
		if req.Mode != "solo" || req.Rounds != 2 {
			t.Fatalf("%s mode/rounds = %q/%d, want solo/2 inherited", run.ID, req.Mode, req.Rounds)
		}
		if !strings.Contains(req.From, "Fill the gap") {
			t.Fatalf("%s brief = %q, want the parent's brief", run.ID, req.From)
		}
		if req.ReviseFindings != "" {
			t.Fatalf("%s kept revise_findings unfolded: %q", run.ID, req.ReviseFindings)
		}
	}
	// The architect still reads every earlier narrowing, findings labelled.
	last := persistedStageRequest(t, root, r3.ID)
	for _, want := range []string{revisionFindingsLabel, "invents an error", "Keep the wording", "fraction paragraph", "Restore the Ans sentence."} {
		if !strings.Contains(last.Revise, want) {
			t.Fatalf("accumulated revision lacks %q:\n%s", want, last.Revise)
		}
	}
	if strings.Count(last.Revise, "invents an error") != 1 {
		t.Fatalf("a forwarded finding snowballed:\n%s", last.Revise)
	}
}

func TestAcceptingTheLastRevisionClosesTheOriginalIntent(t *testing.T) {
	s, projectID, root, _, runs := intakeChainOfThree(t)
	if _, err := s.RunAccept(context.Background(), runs[3].ID, ""); err != nil {
		t.Fatal(err)
	}
	intent, _ := artifact.Load(root, artifact.KindIntent)
	if len(intent.Sections) != 1 {
		t.Fatalf("intent entries = %d, want 1", len(intent.Sections))
	}
	sec := intent.Sections[0]
	if sec.Field("outcome") != "accepted" || !strings.HasPrefix(sec.Field("requirements"), "REQ-001") {
		t.Fatalf("intent after accept: outcome %q requirements %q", sec.Field("outcome"), sec.Field("requirements"))
	}
	reqs, _ := artifact.Load(root, artifact.KindRequirements)
	if len(reqs.Sections) == 0 {
		t.Fatal("no requirements were accepted")
	}
	// Every requirement the chain wrote originates from the person's intent,
	// never from a revision (the TI-36X proposal cited INT-001, INT-005).
	for _, req := range reqs.Sections {
		if req.Field("originates from") != "INT-001" {
			t.Fatalf("%s originates from %q, want INT-001", req.ID, req.Field("originates from"))
		}
	}
	// Traceability still validates against the journal.
	trace, err := s.TraceCheck(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range trace.Errors {
		if strings.Contains(e.String(), "INT-") {
			t.Fatalf("intent trace error: %v", e)
		}
	}
}

func TestRejectingTheLastRevisionClosesTheIntentAsNotAccepted(t *testing.T) {
	s, _, root, _, runs := intakeChainOfThree(t)
	// Rejecting an older draft says nothing while a later one is open.
	if err := artifact.ResolveIntent(root, runs[1].ID, "not accepted", nil); err != nil {
		t.Fatal(err)
	}
	intent, _ := artifact.Load(root, artifact.KindIntent)
	if got := intent.Sections[0].Field("outcome"); got != "pending" {
		t.Fatalf("an older revision closed the intent: %q", got)
	}
	if err := s.RunReject(context.Background(), runs[3].ID, "not what I asked"); err != nil {
		t.Fatal(err)
	}
	intent, _ = artifact.Load(root, artifact.KindIntent)
	if len(intent.Sections) != 1 || intent.Sections[0].Field("outcome") != "not accepted" {
		t.Fatalf("intent after reject: %d entries, outcome %q", len(intent.Sections), intent.Sections[0].Field("outcome"))
	}
}

// The whole class: spec and plan revisions inherit the parent's references,
// mode and rounds exactly as intake does, unless the person overrides them.
// Reference entries are inherited as recorded, not rebuilt from paths.
func TestDocumentRevisionsInheritRefsModeAndRounds(t *testing.T) {
	for _, stageName := range []string{"intake", "spec", "plan"} {
		t.Run(stageName, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno")
			projectID, root := projectWithDocs(t, s, map[artifact.Kind]string{})
			parentID := "r-parent-" + stageName
			run := &runlog.Run{ID: parentID, ProjectID: projectID, Stage: stageName, Mode: "council",
				Status: "paused", PendingKind: "gate", Verdict: "UNVERIFIED", StartedAt: time.Now().UTC().Format(time.RFC3339)}
			w, err := runlog.NewWriter(root, run)
			if err != nil {
				t.Fatal(err)
			}
			w.Close()
			parentRefs := []string{"/refs/guidebook.pdf", "/refs/preset.md"}
			writeStageRequest(filepath.Join(root, ".ducklab", "runs", parentID), StageRequest{
				Stage: stageName, Mode: "sectioned", Rounds: 3, Refs: parentRefs, From: "the brief",
			})
			kind := artifact.KindRequirements
			switch stageName {
			case "spec":
				kind = artifact.KindSpec
			case "plan":
				kind = artifact.KindPlan
			}
			if err := artifact.WriteProposal(root, kind, &artifact.Document{Sections: []artifact.Section{{ID: kind.Prefix() + "-001", Title: "X", Body: "x"}}}, parentID, nil); err != nil {
				t.Fatal(err)
			}
			if err := s.RecoverRuns(context.Background()); err != nil {
				t.Fatal(err)
			}

			req := StageRequest{Stage: stageName, Revise: "tighten it"}
			plan, err := s.prepareRevision(root, projectID, &req)
			if err != nil {
				t.Fatal(err)
			}
			if plan.proposalRun != parentID || req.Revision == nil || req.Revision.Parent != parentID {
				t.Fatalf("parent = %q / %+v", plan.proposalRun, req.Revision)
			}
			if !slices.Equal(req.Refs, parentRefs) || req.Mode != "sectioned" || req.Rounds != 3 || req.From != "the brief" {
				t.Fatalf("inherited refs %v mode %q rounds %d from %q", req.Refs, req.Mode, req.Rounds, req.From)
			}

			// The person's own choices win.
			override := StageRequest{Stage: stageName, Revise: "again", RevisesRun: parentID, Refs: []string{"/refs/other.md"}, Mode: "solo", Rounds: 1}
			if _, err := s.prepareRevision(root, projectID, &override); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(override.Refs, []string{"/refs/other.md"}) || override.Mode != "solo" || override.Rounds != 1 {
				t.Fatalf("override lost: refs %v mode %q rounds %d", override.Refs, override.Mode, override.Rounds)
			}

			// A parent launched with no mode ran in its recorded one.
			writeStageRequest(filepath.Join(root, ".ducklab", "runs", parentID), StageRequest{Stage: stageName})
			blank := StageRequest{Stage: stageName, Revise: "x"}
			if _, err := s.prepareRevision(root, projectID, &blank); err != nil {
				t.Fatal(err)
			}
			if blank.Mode != "council" {
				t.Fatalf("mode = %q, want the parent's recorded council", blank.Mode)
			}

			// A run of another stage is not a parent.
			wrong := StageRequest{Stage: stageName, Revise: "x", RevisesRun: "r-nope"}
			if _, err := s.prepareRevision(root, projectID, &wrong); err == nil {
				t.Fatal("an unknown revises_run was accepted")
			}
		})
	}
}

// A revision request persists its own record, never a client-sent one, and
// the request JSON keeps the shape older readers parse.
func TestRevisionRecordIsEngineOwned(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	projectID, root := projectWithDocs(t, s, map[artifact.Kind]string{})
	req := StageRequest{Stage: "intake", From: "fresh", Revision: &StageRevision{Intent: "INT-999"}}
	if _, err := s.prepareRevision(root, projectID, &req); err != nil {
		t.Fatal(err)
	}
	if req.Revision != nil {
		t.Fatalf("a fresh brief kept a client-sent revision: %+v", req.Revision)
	}
	data, _ := json.Marshal(StageRequest{Stage: "intake", Revise: "x"})
	if strings.Contains(string(data), "revision\"") || strings.Contains(string(data), "revise_findings") {
		t.Fatalf("empty revision fields leak into old-shape requests: %s", data)
	}
}
