package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/strategy"
	"github.com/jrullan/ducklab/internal/vcs"
)

// B-516: TI-36X T-009, r-20261010-011504-4oml, replayed through RunStart.
// T-009 ("Interactive controls with pressed-state feedback") cites
// REF-IMG-6c63e390 in neither its body nor its slices; the photo reaches it
// through SPEC-001 → REQ-001. The comparison is diagnostic at 30%. The
// reviewer filed "INV-2: … within the 30% pixel-difference allowance" — an
// invariant in no project document — as a major, and the run FAILED on its
// dissent.

const t009Plan = `## M-01 — Face

### T-001 — Interactive controls with pressed-state feedback

**Implements:** SPEC-001

**Acceptance slices:**
- Every key in the grid and the d-pad is a ` + "`<button>`" + ` with an accessible name matching its primary legend.
- Activating a key shows a measurable depression, shadow change, and color response via ` + "`:active`" + `.
`

// The reviewer's three kinds of blocking finding, as JSON verdicts.
func b516Verdict(major map[string]interface{}) string {
	minor := map[string]interface{}{"severity": "minor", "file": "index.html", "line": 112,
		"issue": "The scripted pressed class is added on every keydown, including repeats.", "fix": "Gate it on the activating keydown."}
	out, _ := json.Marshal(map[string]interface{}{"verdict": "request-changes", "findings": []interface{}{major, minor}})
	return string(out)
}

var (
	// 4oml's own finding, with the figure quoted and no contract field.
	b516FigureMajor = map[string]interface{}{"severity": "major", "file": "index.html", "line": 27,
		"issue":     "The rendered device still differs on 32.4% of pixels against REF-IMG-6c63e390 (allowed 30.0%).",
		"fix":       "Reconcile the title, solar panel, LCD and key grid until the whole-device pixel difference is at or below 30%.",
		"invariant": "INV-2: the rendered device matches REF-IMG-6c63e390 within the 30% pixel-difference allowance."}
	// A reviewer that follows the contract: the field, no figure.
	b516FieldMajor = map[string]interface{}{"severity": "major", "file": "*", "visual_check": true,
		"issue":     "The solar panel and the title block sit higher than in the reference.",
		"fix":       "Lower them to the reference's positions.",
		"invariant": "The device looks like the photo."}
	// A real defect that happens to quote the allowance's number.
	b516PercentMajor = map[string]interface{}{"severity": "major", "file": "logic.mjs", "line": 812,
		"issue":     "The % key divides twice: 30% of 50 shows 0.15 instead of 15.",
		"fix":       "Divide by 100 once in the percent action.",
		"invariant": "Each key's activation dispatches exactly the named action."}
)

type b516Run struct {
	reqs   []visualLoopRequest
	events []*runlog.Event
}

func runB516(t *testing.T, plan string, required bool, major map[string]interface{}) b516Run {
	t.Helper()
	reqs, events := runVisualLoop(t, visualLoop{mode: "pair", rounds: 1, plan: plan, required: required, tolerance: 0.30,
		report: func(int) string { return visualDone }, verdict: func(int) string { return b516Verdict(major) }})
	return b516Run{reqs, events}
}

func (r b516Run) dissent() bool { return len(eventsOf(r.events, "reviewer_dissent")) > 0 }

func (r b516Run) observations() []*runlog.Event { return eventsOf(r.events, "visual_observation") }

// The incident itself: the inheriting task is shown no figure, no capture and
// no diff, and nothing is rendered for it — and the reviewer's invented
// invariant, should it come anyway, does not fail the run.
func TestB516TheInheritingTaskIsShownNoFigureAndItsReviewerCannotBlockOnOne(t *testing.T) {
	r := runB516(t, t009Plan, false, b516FigureMajor)
	if n := len(eventsOf(r.events, "visual_feedback")); n != 0 {
		t.Errorf("visual_feedback = %d: a task inheriting the photo paid for renders", n)
	}
	for i, q := range r.reqs {
		for _, leak := range []string{"Visual check of the candidate", "of pixels differ", "Latest measurement", "Harness-measured slices"} {
			if strings.Contains(q.prompt, leak) {
				t.Errorf("request %d (%s) carries %q:\n%s", i, q.role, leak, q.prompt)
			}
		}
		if q.role == "implementer" && len(q.images) > 1 {
			t.Errorf("request %d: %d images, want the reference only", i, len(q.images))
		}
	}
	// The reference is still context, said as such.
	impl := firstCalls(r.reqs, "implementer")
	if len(impl) == 0 || !strings.Contains(impl[0].prompt, "## Reference images") ||
		!strings.Contains(impl[0].prompt, "reach this task only through the specification it implements") ||
		!strings.Contains(impl[0].prompt, "not an acceptance criterion of this task") || len(impl[0].images) != 1 {
		t.Errorf("the inheriting implementer is not shown the reference as context:\n%+v", impl)
	}
	if strings.Contains(impl[0].prompt, "They are the visual authority") {
		t.Errorf("the inherited reference is still called this task's authority")
	}
	if r.dissent() {
		t.Errorf("the figure-based major was dissent: the run failed as 4oml did")
	}
	obs := r.observations()
	if len(obs) != 1 || obs[0].Data["effective_verdict"] != "approve" {
		t.Fatalf("visual_observation = %v", obs)
	}
	for _, m := range eventsOf(r.events, "message") {
		if m.Data["role"] == "reviewer" && (m.Data["verdict"] != "approve" || strings.Contains(fmt.Sprint(m.Data["findings"]), "INV-2")) {
			t.Errorf("the reviewer's recorded verdict = %v %v", m.Data["verdict"], m.Data["findings"])
		}
	}
}

// The owning task is shown the figure, with its mode — and its reviewer's
// figure-based major is an observation; the verdict passes.
func TestB516TheOwningTaskSeesTheModeAndTheGuardDemotesTheFigure(t *testing.T) {
	r := runB516(t, visualLoopPlan, false, b516FigureMajor)
	if len(eventsOf(r.events, "visual_feedback")) == 0 {
		t.Fatal("the owning task was not rendered for")
	}
	mode := strategy.VisualModeStatement(false)
	for i, q := range r.reqs {
		if q.first && strings.Contains(q.prompt, "of pixels differ") && !strings.Contains(q.prompt, mode) {
			t.Errorf("request %d (%s) shows the figure without its mode:\n%s", i, q.role, q.prompt)
		}
	}
	rev := reviewerPrompt(t, r.reqs)
	if !strings.Contains(rev, strategy.VisualFindingRule(false)) {
		t.Errorf("the reviewer is not given the visual_check field:\n%s", rev)
	}
	if r.dissent() || len(r.observations()) != 1 {
		t.Errorf("dissent=%v observations=%d, want the figure demoted and no dissent", r.dissent(), len(r.observations()))
	}
}

// The matrix: mode × the task owns/inherits the reference × the finding's
// kind. Only a figure-based finding under a diagnostic check stops blocking.
func TestB516Matrix(t *testing.T) {
	kinds := []struct {
		name   string
		major  map[string]interface{}
		visual bool
	}{
		{"figure quoted", b516FigureMajor, true},
		{"visual_check field", b516FieldMajor, true},
		{"non-visual, quotes 30%", b516PercentMajor, false},
	}
	tasks := []struct {
		name string
		plan string
	}{{"owns", visualLoopPlan}, {"inherits", t009Plan}}
	for _, required := range []bool{false, true} {
		for _, task := range tasks {
			for _, kind := range kinds {
				name := fmt.Sprintf("required=%v/%s/%s", required, task.name, kind.name)
				t.Run(name, func(t *testing.T) {
					r := runB516(t, task.plan, required, kind.major)
					demoted := !required && kind.visual
					if r.dissent() == demoted {
						t.Errorf("dissent = %v, want %v", r.dissent(), !demoted)
					}
					if got := len(r.observations()); (got == 1) != demoted {
						t.Errorf("visual_observation = %d, want demoted=%v", got, demoted)
					}
					rendered := len(eventsOf(r.events, "visual_feedback")) > 0
					if rendered != (task.name == "owns") {
						t.Errorf("rendered = %v for a task that %s the reference", rendered, task.name)
					}
					// Every prompt showing the figure states THIS run's mode.
					for i, q := range r.reqs {
						if !strings.Contains(q.prompt, "of pixels differ") {
							continue
						}
						if !strings.Contains(q.prompt, strategy.VisualModeStatement(required)) || strings.Contains(q.prompt, strategy.VisualModeStatement(!required)) {
							t.Errorf("request %d (%s) states the wrong mode:\n%s", i, q.role, q.prompt)
						}
					}
				})
			}
		}
	}
}

// A failed run's figure reaches the next prompt only for a task that owns
// the check, and always with the mode.
func TestB516TheCarriedFigureFollowsTheSameRule(t *testing.T) {
	carried := func(plan string, enforcement ...string) string {
		s := serviceWithDucklings(t, "luna")
		projectID, dir := projectWithDocs(t, s, map[artifact.Kind]string{
			artifact.KindPlan: plan, artifact.KindSpec: visionSpecDoc, artifact.KindRequirements: visionReqDoc,
		})
		prior := &runlog.Run{
			ID: "r-20261010-011504-4oml", ProjectID: projectID, TaskID: "T-001", Mode: "pair",
			Status: "failed", Verdict: "FAILED", StartedAt: time.Now().UTC().Format(time.RFC3339),
			Visual: &runlog.VisualGate{Enforcement: "diagnostic", Results: []runlog.VisualCompare{{
				Capture: "calculator.png", Reference: visionRefID, Mismatch: 0.324, Tolerance: 0.30,
			}}},
		}
		if len(enforcement) > 0 {
			prior.Visual.Enforcement = enforcement[0]
		}
		w, err := runlog.NewWriter(dir, prior)
		if err != nil {
			t.Fatal(err)
		}
		w.WriteVerify("# fail 0\nvisual check failed: calculator.png differs from " + visionRefID + " in 32.4% of pixels (allowed 30.0%)")
		w.Close()
		if err := s.RecoverRuns(context.Background()); err != nil {
			t.Fatal(err)
		}
		return s.buildTaskPrompt(context.Background(), projectID, dir, "T-001")
	}
	if p := carried(t009Plan); !strings.Contains(p, "r-20261010-011504-4oml") || strings.Contains(p, "32.4%") || strings.Contains(p, "Visual result carried") {
		t.Errorf("the inheriting task was handed the failed run's figure:\n%s", p)
	}
	p := carried(visualLoopPlan)
	if !strings.Contains(p, "in 32.4% of pixels (allowed 30.0%) "+strategy.VisualModeStatement(false)) {
		t.Errorf("the owning task's carried figure lacks its mode:\n%s", p)
	}
	// With no comparison configured any more, the mode is the one that run
	// recorded.
	if p := carried(visualLoopPlan, "required"); !strings.Contains(p, "(allowed 30.0%) "+strategy.VisualModeStatement(true)) {
		t.Errorf("the carried figure does not state the recorded required mode:\n%s", p)
	}
}

// A task with no slice list is held to its body: a body naming the photo
// owns the check; the SPEC chain alone never does.
func TestB516ATaskWithoutSlicesIsHeldToItsBody(t *testing.T) {
	s := serviceWithDucklings(t, "luna")
	contract := config.RenderContract{Compare: []config.RenderCompare{{Capture: "calculator.png", Reference: visionRefID}}}
	for _, c := range []struct {
		plan string
		want bool
	}{{visionOwnPlanDoc, true}, {planDoc, false}, {t009Plan, false}, {visualLoopPlan, true}} {
		projectID, _ := projectWithDocs(t, s, map[artifact.Kind]string{
			artifact.KindPlan: c.plan, artifact.KindSpec: visionSpecDoc, artifact.KindRequirements: visionReqDoc,
		})
		if got := s.taskOwnsVisualCheck(context.Background(), projectID, "T-001", contract); got != c.want {
			t.Errorf("plan %q: owns = %v, want %v", strings.SplitN(c.plan, "\n", 4)[2], got, c.want)
		}
	}
}

// The runner's section — every role, retry and resume goes through it —
// states the mode with the figure, and asks only the reviewer for the field.
// A task with one own and one inherited reference says which is which.
func TestB516TheRunnersFigureSectionStatesTheMode(t *testing.T) {
	for _, required := range []bool{false, true} {
		v := &taskVision{
			cited: []string{visionRefID, "REF-IMG-aaaaaaaa"}, own: map[string]bool{strings.ToLower(visionRefID): true},
			required: required,
			feedback: &visualFeedback{Round: 1, Phase: "before review", Results: []runlog.VisualCompare{{
				Capture: "calculator.png", Reference: visionRefID, Mismatch: 0.324, Tolerance: 0.30,
			}}},
		}
		for _, role := range []config.Role{config.RoleImplementer, config.RoleReviewer, config.RoleAdvisor, config.RoleJudge} {
			_, section, _, _ := v.forTurn(role, false, blindUndeclared)
			if !strings.Contains(section, "32.4% of pixels differ") || !strings.Contains(section, strategy.VisualModeStatement(required)) {
				t.Errorf("required=%v %s: the figure is shown without its mode:\n%s", required, role, section)
			}
			if rule := strategy.VisualFindingRule(required); strings.Contains(section, rule) != (role == config.RoleReviewer) {
				t.Errorf("required=%v %s: finding rule present=%v", required, role, strings.Contains(section, rule))
			}
			if !strings.Contains(section, "- REF-IMG-aaaaaaaa (reaches this task through its specification") ||
				strings.Contains(section, "- "+visionRefID+" (reaches") {
				t.Errorf("%s: own and inherited references are not told apart:\n%s", role, section)
			}
		}
	}
}

// Test-first carries the guard too: its prompt can quote a failed run's
// figure, and its reviewer cannot block on it under a diagnostic check.
func TestB516TheTestFirstReviewerCannotBlockOnTheFigure(t *testing.T) {
	s := serviceWithDucklings(t, "luna", "glm52")
	projectID, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: visualLoopPlan, artifact.KindSpec: visionSpecDoc, artifact.KindRequirements: visionReqDoc,
	})
	if _, err := s.ProjectUpdate(context.Background(), projectID, map[string]string{"verify.mode": "tests", "verify.tests": "false"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".ducklab", "project.toml")
	cfg, err := config.LoadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	tol := 0.30
	cfg.Render = config.RenderContract{Command: "true", Compare: []config.RenderCompare{{Capture: "calculator.png", Reference: visionRefID, Tolerance: &tol}}}
	if err := writeProjectTOML(path, cfg); err != nil {
		t.Fatal(err)
	}
	if err := vcs.New(dir).Init(); err != nil {
		t.Fatal(err)
	}
	fake := s.providers["fake"].(*provider.Fake)
	fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
		content := "The failing test is written."
		for _, m := range req.Messages {
			if m.Role == "system" && strings.Contains(m.Content, "You are the reviewer") {
				content = b516Verdict(b516FigureMajor)
			}
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Content: content}, FinishReason: provider.FinishStop}}}
	}
	run, err := s.TestStart(context.Background(), projectID, TestFirstRequest{TaskID: "T-001", Mode: "pair",
		Seats: map[string]string{"implementer": "luna", "reviewer": "glm52"}})
	if err != nil {
		t.Fatal(err)
	}
	cleanupStartedRun(t, s, run.ID)
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	select {
	case <-rs.done:
	case <-time.After(30 * time.Second):
		t.Fatal("the test-first run never finished")
	}
	events, err := runlog.ReadEvents(rs.runDir)
	if err != nil {
		t.Fatal(err)
	}
	if obs := eventsOf(events, "visual_observation"); len(obs) == 0 {
		t.Errorf("the test-first reviewer's figure-based major was not set aside")
	}
}
