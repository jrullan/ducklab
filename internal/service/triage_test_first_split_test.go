package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/store"
)

// B-539's evidence: TI-36X B-006 as the triager split it. Test-first, with the
// regression test in a portion of its own; promoted as T-008 (app.js) and
// T-009 (test/e2e.py), the honest red test was refused as out of T-008's lane.
const b006Triage = `{"severity":"normal","duplicate_of":"","component":"keypad dispatch",
 "reason":"The SECOND lookup lacks a dot entry even though the key's secondary legend is a comma separator.",
 "reproducible":true,
 "suspected_files":["app.js","test/e2e.py"],
 "task_title":"Map 2nd + dot to the comma separator",
 "test_strategy":"test-first",
 "test_reason":"Click 2nd then the dot keypad key and assert a comma separator is inserted in the expression.",
 "deliverables":["The SECOND mapping translates the dot keypad action to the existing comma action","The browser regression suite covers 2nd + dot comma insertion"],
 "proposal":[
  {"title":"Map shifted dot to comma",
   "acceptance":["2nd + dot dispatches the existing comma action.","The resulting expression displays a comma separator and exits shifted mode."],
   "owns":["app.js"]},
  {"title":"Cover shifted-dot comma entry",
   "acceptance":["An end-to-end regression clicks 2nd then dot and asserts comma insertion."],
   "owns":["test/e2e.py","package.json"]}]}`

// calculatorWebProject is TI-36X's tree: a page script, a keypad table, a
// browser suite under test/, and the package.json that registers it.
func calculatorWebProject(t *testing.T, s *Service) (string, string) {
	t.Helper()
	id, root := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	files := map[string]string{
		"app.js":       "x\n",
		"keys.js":      "x\n",
		"test/e2e.py":  "x\n",
		"package.json": `{"name":"calc","scripts":{"test":"python3 test/e2e.py"}}` + "\n",
		"Makefile":     "test:\n\tpython3 test/e2e.py\n",
		"README.md":    "x\n",
	}
	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "2nd + . does not insert a comma", Severity: "normal"}); err != nil {
		t.Fatal(err)
	}
	return id, root
}

func b006Portions(t *testing.T) []agent.SplitProposal {
	t.Helper()
	var triage struct {
		Proposal []agent.SplitProposal `json:"proposal"`
	}
	if err := json.Unmarshal([]byte(b006Triage), &triage); err != nil {
		t.Fatal(err)
	}
	return triage.Proposal
}

func TestATestFirstSplitWithATestsOnlyPortionIsRefusedAtPromotion(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	_, root := calculatorWebProject(t, s)
	rec := &store.Bug{TestStrategy: "test-first", SuspectedFiles: "app.js\ntest/e2e.py"}

	_, err := preparePromotionPortions(root, rec, b006Portions(t))
	var split *testFirstSplitError
	if !errors.As(err, &split) {
		t.Fatalf("B-006's split promoted (err = %v); test-first could run in neither task", err)
	}
	for _, want := range []string{`portion 2 "Cover shifted-dot comma entry"`, `portion 1 "Map shifted dot to comma"`, "merge the portions"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not say %q: %v", want, err)
		}
	}
}

// The test portion's companions are not the code the test exercises, whether
// or not a stack registers them: a package.json with no test script (the gate
// lives in project.toml), a Makefile, a README.
func TestATestsOnlyPortionIsRecognizedWhateverRunsTheSuite(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	_, root := calculatorWebProject(t, s)
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"calc"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &store.Bug{TestStrategy: "test-first", SuspectedFiles: "app.js\ntest/e2e.py"}
	for _, companions := range [][]string{
		{"test/e2e.py", "package.json"},
		{"test/e2e.py", "Makefile"},
		{"test/e2e.py", "package.json", "README.md"},
	} {
		portions := b006Portions(t)
		portions[1].Owns = companions
		var split *testFirstSplitError
		if _, err := preparePromotionPortions(root, rec, portions); !errors.As(err, &split) {
			t.Errorf("test portion owning %v promoted beside the code (err = %v)", companions, err)
		}
	}
}

// A code portion that says how it will be proven still gets the merge, not the
// unrepairable "requires tests but owns no test path".
func TestACodePortionThatMentionsItsTestStillGetsTheMergeableRefusal(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	_, root := calculatorWebProject(t, s)
	portions := b006Portions(t)
	portions[0].Acceptance[1] = "Proven by a failing regression test first."
	rec := &store.Bug{TestStrategy: "test-first", SuspectedFiles: "app.js\ntest/e2e.py"}
	var split *testFirstSplitError
	if _, err := preparePromotionPortions(root, rec, portions); !errors.As(err, &split) {
		t.Fatalf("refusal = %v, want the test-first split refusal the engine can repair", err)
	}
}

// Only test-first writes a test before the fix; the other shapes stay legal.
func TestSplitsThatTestFirstCanRunStillPromote(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	_, root := calculatorWebProject(t, s)

	buildOnly := &store.Bug{TestStrategy: "build-only", SuspectedFiles: "app.js\ntest/e2e.py"}
	if _, err := preparePromotionPortions(root, buildOnly, b006Portions(t)); err != nil {
		t.Errorf("build-only split with a separate test portion refused: %v", err)
	}

	testWithCode := []agent.SplitProposal{
		{Title: "Map shifted dot to comma", Acceptance: []string{"2nd + dot inserts a comma"}, Owns: []string{"app.js", "test/e2e.py", "package.json"}},
		{Title: "Relabel the dot key", Acceptance: []string{"the dot key shows a comma legend"}, Owns: []string{"keys.js"}},
	}
	testFirst := &store.Bug{TestStrategy: "test-first", SuspectedFiles: "app.js\ntest/e2e.py"}
	if _, err := preparePromotionPortions(root, testFirst, testWithCode); err != nil {
		t.Errorf("test-first split whose test sits with its code refused: %v", err)
	}
}

func TestEngineMergesATestsOnlyPortionIntoTheCodeItExercises(t *testing.T) {
	problem := &testFirstSplitError{msg: "x", testOnly: []int{1}, code: []int{0}}
	p := map[string]interface{}{"proposal": b006Portions(t)}

	repaired, note, ok := repairTestFirstSplit(p, problem)
	if !ok {
		t.Fatal("engine did not merge the plain two-portion case")
	}
	portions := repaired["proposal"].([]agent.SplitProposal)
	if len(portions) != 1 {
		t.Fatalf("repaired proposal has %d portions, want 1: %#v", len(portions), portions)
	}
	got := portions[0]
	if got.Title != "Map shifted dot to comma" || !slices.Equal(got.Owns, []string{"app.js", "test/e2e.py", "package.json"}) {
		t.Errorf("merged portion = %#v", got)
	}
	if len(got.Acceptance) != 2 || got.Acceptance[0] != "2nd + dot dispatches the existing comma action." {
		t.Errorf("merged acceptance = %v, want the code portion's two slices", got.Acceptance)
	}
	if !strings.Contains(note, "portion 2") || !strings.Contains(note, "portion 1") {
		t.Errorf("note = %q", note)
	}

	// With room, the test slice joins the code slices.
	roomy := b006Portions(t)
	roomy[0].Acceptance = roomy[0].Acceptance[:1]
	repaired, _, ok = repairTestFirstSplit(map[string]interface{}{"proposal": roomy}, problem)
	if !ok {
		t.Fatal("merge with room refused")
	}
	if acc := repaired["proposal"].([]agent.SplitProposal)[0].Acceptance; len(acc) != 2 || !strings.Contains(acc[1], "regression") {
		t.Errorf("merged acceptance with room = %v, want the test slice second", acc)
	}

	// Two code portions the test could belong to is a judgement.
	if _, _, ok := repairTestFirstSplit(p, &testFirstSplitError{msg: "x", testOnly: []int{1}, code: []int{0, 2}}); ok {
		t.Error("engine chose between two code portions")
	}
	if _, _, ok := repairTestFirstSplit(p, errString("split portions share a lane")); ok {
		t.Error("engine repaired a refusal it does not understand")
	}
}

// End to end: B-006's triage reaches the gate already merged, without a
// triager turn, and promotes to one task whose lane holds the test.
func TestTriageRepairsATestFirstSplitAndPromotesOneTaskOwningTheTest(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, root := calculatorWebProject(t, s)
	repairs := triagerScript(t, s, b006Triage, b006Triage)

	run := triageToGate(t, s, id)
	if repairs.Load() != 0 {
		t.Errorf("triager given %d repair turns for a split the engine could repair", repairs.Load())
	}
	p := gateProposal(t, run)
	if b, _ := p["promote_blocker"].(string); b != "" {
		t.Fatalf("repairable split flagged: %s", b)
	}
	if note, _ := p["proposal_repair"].(string); !strings.Contains(note, "merged test-only portion") {
		t.Errorf("proposal_repair = %q", note)
	}
	if got := triageEventsOf(t, root, run.ID, "triage_repair"); len(got) != 1 || got[0]["outcome"] != "engine" {
		t.Errorf("triage_repair = %#v", got)
	}
	if _, err := s.RunAccept(context.Background(), run.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugPromote(context.Background(), id, "B-001", "human"); err != nil {
		t.Fatalf("merged split did not promote: %v", err)
	}
	plan, err := artifact.Load(root, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	var lanes [][]string
	for _, milestone := range plan.Sections {
		for _, task := range milestone.Children {
			if strings.Contains(task.Body, "B-001") {
				lanes = append(lanes, task.Owns)
			}
		}
	}
	if len(lanes) != 1 || !slices.Contains(lanes[0], "app.js") || !slices.Contains(lanes[0], "test/e2e.py") {
		t.Errorf("B-001 promoted to lanes %v, want one task owning app.js and test/e2e.py", lanes)
	}
}
