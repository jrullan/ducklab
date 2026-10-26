package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/bug"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/store"
)

// B-523's evidence: calculator run r-20261026-183029-kxck, event seq 51, the
// triage of B-014 as the triager returned it. Its deliverables promise a
// pointer-event test; neither portion owns a test path or carries a test
// slice, so promote refuses it.
const b014Triage = `{"severity":"high","duplicate_of":"","component":"canvas interaction",
 "reason":"The canvas interaction block in index.html uses mouse-only listeners, lacks touch-action:none, and scripts/check.sh forbids touch/pointer gesture handlers.",
 "reproducible":true,
 "suspected_files":["index.html","scripts/check.sh"],
 "task_title":"Migrate canvas mouse listeners to Pointer Events, add touch-action:none, and remove the check.sh guard",
 "test_strategy":"test-first",
 "test_reason":"Pointer events can be dispatched in a headless/jsdom test to assert drag, pan, lock toggle, and pinch-to-zoom behave as mouse does; touch-action:none is a verifiable DOM attribute",
 "deliverables":[
  "Pointer-down/move/up listeners replace mousedown/mousemove/mouseup on the canvas and window, preserving vertex drag, lock toggle, and camera pan behavior",
  "canvas has touch-action: none (CSS or attribute) so touch drags do not scroll the page and pinch does not trigger browser zoom",
  "check.sh scope-boundary validation no longer rejects touch/pointer gesture handlers",
  "A test simulates pointerdown/pointermove/pointerup on the canvas and asserts vertex drag and camera pan work identically to mouse"],
 "proposal":[
  {"title":"Migrate canvas interaction to Pointer Events and add touch-action:none",
   "acceptance":["canvas has touch-action: none","pointerdown/pointermove/pointerup handlers implement drag, lock toggle, and pan with the same precedence as mouse"],
   "owns":["index.html"]},
  {"title":"Remove check.sh guard forbidding touch/pointer handlers",
   "acceptance":["check.sh no longer contains the 'no touch or pointer gesture handlers' scope check","check.sh passes after pointer handlers are added to index.html"],
   "owns":["scripts/check.sh"]}]}`

// b014WithRoom is the same triage with one slice fewer on the canvas portion:
// the engine can then give it the test work itself.
var b014WithRoom = strings.Replace(b014Triage,
	`"acceptance":["canvas has touch-action: none","pointerdown/pointermove/pointerup handlers implement drag, lock toggle, and pan with the same precedence as mouse"]`,
	`"acceptance":["pointerdown/pointermove/pointerup handlers implement drag, lock toggle, and pan with the same precedence as mouse"]`, 1)

// b014Revised is what a triager answers when told the rule: the canvas
// portion takes a test path.
var b014Revised = strings.Replace(b014Triage, `"owns":["index.html"]`, `"owns":["index.html","tests/pointer.test.mjs"]`, 1)

const repairMarker = "## Promote would refuse your proposal"

// triagerScript answers every triager turn: first with first, and a repair
// turn with repair. It counts the repair turns it was asked for.
func triagerScript(t *testing.T, s *Service, first, repair string) *atomic.Int32 {
	t.Helper()
	var repairs atomic.Int32
	fake := s.providers["fake"].(*provider.Fake)
	fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
		isTriager, isRepair := false, false
		for _, m := range req.Messages {
			if m.Role == "system" && strings.Contains(m.Content, "You are the triager") {
				isTriager = true
			}
			if m.Role == "user" && strings.Contains(m.Content, repairMarker) {
				isRepair = true
			}
		}
		if !isTriager {
			return nil
		}
		answer := first
		if isRepair {
			repairs.Add(1)
			answer = repair
		}
		return &provider.ChatResponse{
			Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Content: answer}, FinishReason: provider.FinishStop}},
			Usage:   provider.Usage{PromptTokens: 70, CompletionTokens: 40},
		}
	}
	return &repairs
}

// calculatorProject is B-014's tree: a page and the check script.
func calculatorProject(t *testing.T, s *Service) (string, string) {
	t.Helper()
	id, root := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	for _, name := range []string{"index.html", "scripts/check.sh"} {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "touch devices cannot drag vertices", Severity: "normal"}); err != nil {
		t.Fatal(err)
	}
	return id, root
}

func triageToGate(t *testing.T, s *Service, projectID string) *runlog.Run {
	t.Helper()
	run, err := s.BugTriage(context.Background(), projectID, "B-001")
	if err != nil {
		t.Fatal(err)
	}
	cleanupStartedRun(t, s, run.ID)
	_, _ = s.waitForRun(context.Background(), run.ID)
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return detail.Run
}

func gateProposal(t *testing.T, run *runlog.Run) map[string]interface{} {
	t.Helper()
	proposals := triageProposalList(run.PendingData["proposals"])
	if len(proposals) != 1 {
		t.Fatalf("gate carries %d proposals, want 1: %#v", len(proposals), run.PendingData)
	}
	return proposals[0]
}

func triageEventsOf(t *testing.T, root, runID, kind string) []map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".ducklab", "runs", runID, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e struct {
			Type string                 `json:"type"`
			Data map[string]interface{} `json:"data"`
		}
		if json.Unmarshal([]byte(line), &e) == nil && e.Type == kind {
			out = append(out, e.Data)
		}
	}
	return out
}

// The B-014 shape, with a triager that cannot fix it, reaches the gate saying
// what promote will refuse and how to fix it; accepting it is allowed (the
// classification is sound) but loudly flagged, and promote's own refusal now
// names the way out.
func TestTriageFlagsAnUnpromotableSplitAtTheGate(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, root := calculatorProject(t, s)
	repairs := triagerScript(t, s, b014Triage, b014Triage)

	run := triageToGate(t, s, id)
	if run.Status != "paused" || run.PendingKind != "gate" {
		t.Fatalf("triage is %s/%s, want paused at its gate", run.Status, run.PendingKind)
	}
	if repairs.Load() != 1 {
		t.Errorf("triager was given %d repair turns, want exactly 1", repairs.Load())
	}
	p := gateProposal(t, run)
	blocker, _ := p["promote_blocker"].(string)
	if !strings.Contains(blocker, "no portion claims it") {
		t.Fatalf("gate proposal promote_blocker = %q, want promote's own refusal", blocker)
	}
	fix, _ := p["promote_fix"].(string)
	for _, want := range []string{"Regression test covers", "bug_edit", "Edit the proposal"} {
		if !strings.Contains(fix, want) {
			t.Errorf("promote_fix lacks %q:\n%s", want, fix)
		}
	}
	blockers := triageProposalList(run.PendingData["promote_blockers"])
	if len(blockers) != 1 || blockers[0]["bug"] != "B-001" {
		t.Fatalf("pending_data.promote_blockers = %#v", run.PendingData["promote_blockers"])
	}
	if detail, _ := run.PendingData["detail"].(string); !strings.Contains(detail, "cannot be promoted as written") {
		t.Errorf("gate detail = %q", detail)
	}
	repaired := triageEventsOf(t, root, run.ID, "triage_repair")
	if len(repaired) != 1 || repaired[0]["outcome"] != "unrepaired" {
		t.Errorf("triage_repair events = %#v, want one unrepaired", repaired)
	}

	res, err := s.RunAccept(context.Background(), run.ID, "")
	if err != nil {
		t.Fatalf("a person may accept the classification: %v", err)
	}
	if !strings.Contains(res.Warning, "cannot be promoted as written") || !strings.Contains(res.Warning, "no portion claims it") {
		t.Errorf("accept warning = %q, want the flag", res.Warning)
	}
	applied := triageEventsOf(t, root, run.ID, "triage_applied")
	if len(applied) != 1 || applied[0]["promote_blockers"] == nil {
		t.Errorf("triage_applied = %#v, want promote_blockers", applied)
	}
	_, err = s.BugPromote(context.Background(), id, "B-001", "human")
	if err == nil || !strings.Contains(err.Error(), "no portion claims it") || !strings.Contains(err.Error(), "bug_edit") {
		t.Fatalf("promote = %v, want the same refusal naming the edit", err)
	}
}

// The triager is told promote's refusal and given one turn; its corrected
// split is what reaches the gate, and it promotes.
func TestTriageGivesTheTriagerOneTurnToRepairARefusedSplit(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, root := calculatorProject(t, s)
	repairs := triagerScript(t, s, b014Triage, b014Revised)

	run := triageToGate(t, s, id)
	if repairs.Load() != 1 {
		t.Fatalf("repair turns = %d, want 1", repairs.Load())
	}
	p := gateProposal(t, run)
	if b, _ := p["promote_blocker"].(string); b != "" {
		t.Fatalf("revised split still flagged: %s", b)
	}
	if note, _ := p["proposal_repair"].(string); !strings.Contains(note, "triager revised") {
		t.Errorf("proposal_repair = %q", note)
	}
	if run.PendingData["promote_blockers"] != nil {
		t.Errorf("gate lists blockers for a repaired split: %#v", run.PendingData["promote_blockers"])
	}
	if got := triageEventsOf(t, root, run.ID, "triage_repair"); len(got) != 1 || got[0]["outcome"] != "triager" {
		t.Errorf("triage_repair = %#v", got)
	}
	res, err := s.RunAccept(context.Background(), run.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Warning != "" {
		t.Errorf("clean accept warned: %s", res.Warning)
	}
	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatalf("repaired split did not promote: %v", err)
	}
	if tasks, _ := out["tasks"].([]string); len(tasks) != 2 {
		t.Errorf("promote made %v, want the two portions", out)
	}
}

// When only one portion owns the behavior under test and it has room for a
// slice, the engine repairs the split itself — no extra model turn.
func TestTriageRepairsAnUnambiguousSplitWithoutAModelTurn(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, root := calculatorProject(t, s)
	repairs := triagerScript(t, s, b014WithRoom, b014WithRoom)

	run := triageToGate(t, s, id)
	if repairs.Load() != 0 {
		t.Errorf("triager given %d repair turns for a split the engine could repair", repairs.Load())
	}
	p := gateProposal(t, run)
	if b, _ := p["promote_blocker"].(string); b != "" {
		t.Fatalf("repairable split flagged: %s", b)
	}
	if note, _ := p["proposal_repair"].(string); !strings.Contains(note, "repaired by the engine") || !strings.Contains(note, "portion 1") {
		t.Errorf("proposal_repair = %q", note)
	}
	raw, _ := json.Marshal(p["proposal"])
	var portions []agent.SplitProposal
	if err := json.Unmarshal(raw, &portions); err != nil || len(portions) != 2 {
		t.Fatalf("repaired proposal = %s", raw)
	}
	if len(portions[0].Acceptance) != 2 || !strings.HasPrefix(portions[0].Acceptance[1], "Regression test covers") {
		t.Errorf("canvas portion acceptance = %v, want the regression slice", portions[0].Acceptance)
	}
	if len(portions[1].Acceptance) != 2 {
		t.Errorf("check.sh portion changed: %v", portions[1].Acceptance)
	}
	if got := triageEventsOf(t, root, run.ID, "triage_repair"); len(got) != 1 || got[0]["outcome"] != "engine" {
		t.Errorf("triage_repair = %#v", got)
	}
	if _, err := s.RunAccept(context.Background(), run.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugPromote(context.Background(), id, "B-001", "human"); err != nil {
		t.Fatalf("engine-repaired split did not promote: %v", err)
	}
}

// Unattended triage never applies a split promote would refuse: the run
// waits for a person, saying why.
func TestAutoTriageDeclinesAnUnpromotableSplit(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := calculatorProject(t, s)
	if err := s.ProjectAutonomySet(id, "auto"); err != nil {
		t.Fatal(err)
	}
	triagerScript(t, s, b014Triage, b014Triage)

	run := triageToGate(t, s, id)
	if run.Status != "paused" || run.PendingKind != "gate" || run.Accepted {
		t.Fatalf("auto triage applied an unpromotable split: %s/%s accepted=%v", run.Status, run.PendingKind, run.Accepted)
	}
	if detail, _ := run.PendingData["detail"].(string); !strings.Contains(detail, "auto-apply declined") {
		t.Errorf("detail = %q", detail)
	}
	if _, err := s.RunAcceptAs(context.Background(), run.ID, "", "auto-triage"); err == nil {
		t.Error("an automatic actor accepted a flagged triage")
	}
}

// Closer is a judgement: when two portions both talk about the behavior under
// test, the engine does not pick one.
func TestEngineRepairRefusesToChooseBetweenSimilarPortions(t *testing.T) {
	root := t.TempDir()
	rec := &store.Bug{
		TestStrategy: "test-first",
		Deliverables: "A test drags a vertex on the canvas with pointer events",
	}
	p := map[string]interface{}{"proposal": []agent.SplitProposal{
		{Title: "Canvas vertex drag", Acceptance: []string{"pointer drag moves the vertex"}, Owns: []string{"a.js"}},
		{Title: "Canvas vertex highlight", Acceptance: []string{"pointer hover marks the vertex"}, Owns: []string{"b.js"}},
	}}
	if _, _, ok := repairUnclaimedTestWork(root, rec, p, &unclaimedTestWorkError{msg: "x"}); ok {
		t.Fatal("engine chose between two portions that both own the tested behavior")
	}
	// And it only repairs the one refusal it understands.
	if _, _, ok := repairUnclaimedTestWork(root, rec, p, errString("split portions share a lane")); ok {
		t.Fatal("engine repaired a refusal it does not understand")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// The edit that unsticks a refused promote says whether it worked.
func TestBugEditReportsWhetherPromoteWillTakeTheSplit(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := calculatorProject(t, s)
	var triage map[string]interface{}
	if err := json.Unmarshal([]byte(b014Triage), &triage); err != nil {
		t.Fatal(err)
	}
	triage["bug"] = "B-001"
	if _, err := s.ApplyTriage(context.Background(), id, []map[string]interface{}{triage}); err != nil {
		t.Fatal(err)
	}
	still := []bug.Portion{
		{Title: "Canvas", Acceptance: []string{"canvas has touch-action: none"}, Owns: []string{"index.html"}},
		{Title: "Guard", Acceptance: []string{"check.sh allows pointer handlers"}, Owns: []string{"scripts/check.sh"}},
	}
	out, err := s.BugEdit(context.Background(), id, "B-001", BugEditRequest{Proposal: &still})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.PromoteBlocker, "no portion claims it") {
		t.Errorf("edit left the split unpromotable and said nothing: %q", out.PromoteBlocker)
	}
	fixed := []bug.Portion{
		{Title: "Canvas", Acceptance: []string{"canvas has touch-action: none", "Regression test covers pointer drag"}, Owns: []string{"index.html"}},
		{Title: "Guard", Acceptance: []string{"check.sh allows pointer handlers"}, Owns: []string{"scripts/check.sh"}},
	}
	out, err = s.BugEdit(context.Background(), id, "B-001", BugEditRequest{Proposal: &fixed})
	if err != nil {
		t.Fatal(err)
	}
	if out.PromoteBlocker != "" {
		t.Errorf("promotable split still flagged: %s", out.PromoteBlocker)
	}
	if _, err := s.BugPromote(context.Background(), id, "B-001", "human"); err != nil {
		t.Fatalf("edited split did not promote: %v", err)
	}
}
