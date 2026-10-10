package service

import (
	"context"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/strategy"
	"github.com/jrullan/ducklab/internal/vcs"
)

// B-505..B-508: the TI-36X T-008 build r-20261005-012549-uvns, replayed
// through RunStart with fake seats. luna implements and can see; glm52
// reviews blind; glm53flash advises blind. Slice 1 cites the photo a
// [[render.compare]] measures; slice 2 does not.

const visualLoopPlan = `## M-01 — Face

### T-001 — Visual replica composition

**Implements:** SPEC-001

**Acceptance slices:**
- At a 730 × 1500 viewport the calculator renders matching REF-IMG-6c63e390's layout: enclosure, solar panel, d-pad and key grid in physical positions.
- Rendered width equals min(730px, vw − gutter) and the height derives from the fixed ratio.
`

type visualLoop struct {
	mode     string
	rounds   int
	required bool
	// match makes the capture identical to the reference (the check passes).
	match bool
	// report is the implementer's closing report on its n-th turn (1-based).
	report func(n int) string
	// verdict is the reviewer's reply on its n-th turn.
	verdict func(n int) string
	// priorFailure records a FAILED run on the task whose visual check
	// measured other code (B-508).
	priorFailure bool
	// plan replaces visualLoopPlan (B-516: a task that inherits the photo).
	plan string
	// tolerance replaces the comparison's default tolerance when non-zero.
	tolerance float64
}

type visualLoopRequest struct {
	role   string // implementer, reviewer, advisor
	first  bool   // the turn's first model call
	prompt string
	images []string
}

func runVisualLoop(t *testing.T, o visualLoop) ([]visualLoopRequest, []*runlog.Event) {
	t.Helper()
	s := serviceWithDucklings(t, "luna", "glm52", "glm53flash")
	setVision(s, "luna", true)
	setVision(s, "glm52", false)
	setVision(s, "glm53flash", false)
	plan := visualLoopPlan
	if o.plan != "" {
		plan = o.plan
	}
	projectID, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: plan, artifact.KindSpec: visionSpecDoc, artifact.KindRequirements: visionReqDoc,
	})
	dark := color.RGBA{R: 40, G: 40, B: 40, A: 255}
	storeRefImage(t, dir, visionRefFile, solidPNG(t, 8, 16, dark))
	shotColor := color.Color(color.White)
	if o.match {
		shotColor = dark
	}
	shot := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(shot, solidPNG(t, 8, 16, shotColor), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".ducklab", "project.toml")
	cfg, err := config.LoadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Render = config.RenderContract{
		Command:   `mkdir -p "$DUCKLAB_RENDER_OUTPUT" && cp '` + shot + `' "$DUCKLAB_RENDER_OUTPUT/calculator.png"`,
		Artifacts: ".ducklab-render-captures/*.png",
		Compare:   []config.RenderCompare{{Capture: "calculator.png", Reference: visionRefID}},
	}
	if o.required {
		cfg.Render.Enforcement = "required"
	}
	if o.tolerance > 0 {
		tol := o.tolerance
		cfg.Render.Compare[0].Tolerance = &tol
	}
	if err := writeProjectTOML(path, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<p>calc</p>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vcs.New(dir).Init(); err != nil {
		t.Fatal(err)
	}
	if o.priorFailure {
		prior := &runlog.Run{
			ID: "r-20261005-004552-4tpn", ProjectID: projectID, TaskID: "T-001", Mode: "pair",
			Status: "failed", Verdict: "FAILED", StartedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
			Visual: &runlog.VisualGate{Enforcement: "diagnostic", Results: []runlog.VisualCompare{{
				Capture: "calculator.png", Reference: visionRefID, Mismatch: 0.445, Tolerance: 0.02,
			}}},
		}
		w, err := runlog.NewWriter(dir, prior)
		if err != nil {
			t.Fatal(err)
		}
		w.WriteVerify("# fail 0\nproduct smoke: run.smoke met expectation\nvisual check failed: calculator.png differs from " + visionRefID + " in 44.5% of pixels (allowed 2.0%)")
		w.Close()
		if err := s.RecoverRuns(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	var reqs []visualLoopRequest
	implTurns, reviews := 0, 0
	reply := func(content string) *provider.ChatResponse {
		return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Content: content}, FinishReason: provider.FinishStop}}}
	}
	fake := s.providers["fake"].(*provider.Fake)
	fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
		mu.Lock()
		defer mu.Unlock()
		role, toolResult := "implementer", false
		var user *provider.Message
		for i, m := range req.Messages {
			switch {
			case m.Role == "system" && strings.Contains(m.Content, "You are the reviewer"):
				role = "reviewer"
			case m.Role == "system" && strings.Contains(m.Content, "You are the advisor"):
				role = "advisor"
			case m.Role == "tool" || (m.Role == "user" && strings.HasPrefix(m.Content, "Tool result for ")):
				toolResult = true
			case m.Role == "user" && user == nil:
				user = &req.Messages[i]
			}
		}
		r := visualLoopRequest{role: role, first: !toolResult}
		if user != nil {
			r.prompt, r.images = user.Content, user.Images
		}
		reqs = append(reqs, r)
		switch role {
		case "reviewer":
			reviews++
			return reply(o.verdict(reviews))
		case "advisor":
			return reply(`{"action":"note","note":"Finish slice 2's scaling rule before anything else."}`)
		}
		if !toolResult {
			implTurns++
			return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: "assistant",
				ToolCalls: []provider.ToolCall{fakeToolCall("fs_write", fmt.Sprintf(`{"path":"index.html","content":"<p>calc %d</p>"}`, implTurns))}},
				FinishReason: provider.FinishToolCalls}}}
		}
		return reply("Updated the composition.\n\n" + o.report(implTurns))
	}
	run, err := s.RunStart(context.Background(), projectID, RunRequest{TaskID: "T-001", Mode: o.mode, Rounds: o.rounds,
		Seats: map[string]string{"implementer": "luna", "reviewer": "glm52", "advisor": "glm53flash"}})
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
		t.Fatal("the build never finished")
	}
	events, err := runlog.ReadEvents(rs.runDir)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]visualLoopRequest(nil), reqs...), events
}

func firstCalls(reqs []visualLoopRequest, role string) []visualLoopRequest {
	var out []visualLoopRequest
	for _, r := range reqs {
		if r.role == role && r.first {
			out = append(out, r)
		}
	}
	return out
}

const (
	visualPartialOnly = `{"deliverables":[{"id":1,"status":"partial","note":"pixel similarity was not verified"},{"id":2,"status":"done"}]}`
	bothPartial       = `{"deliverables":[{"id":1,"status":"partial","note":"pixel similarity was not verified"},{"id":2,"status":"partial","note":"short viewports overflow"}]}`
	visualDone        = `{"deliverables":[{"id":1,"status":"done"},{"id":2,"status":"done"}]}`
	approveVerdict    = `{"verdict":"approve","findings":[]}`
)

func requestChangesThenApprove(after int) func(int) string {
	return func(n int) string {
		if n < after {
			return `{"verdict":"request-changes","findings":[{"severity":"major","file":"index.html","issue":"the d-pad sits left of the reference's position","fix":"move it right"}]}`
		}
		return approveVerdict
	}
}

// The incident's own shape: three implementer turns, each an honest
// "partial" on the measured slice only. #163 rendered none of them, the
// self-report counted as distress three times, and the run ended in a
// stuck_deliverable escalation. Now: every turn starts from a render of its
// tree, the measured slice is neither distress nor a stuck item, and nothing
// consults the advisor.
func TestB505B506AnHonestPartialOnTheMeasuredSliceIsNotDistress(t *testing.T) {
	reqs, events := runVisualLoop(t, visualLoop{mode: "pair", rounds: 3,
		report:  func(int) string { return visualPartialOnly },
		verdict: requestChangesThenApprove(3)})
	impl := firstCalls(reqs, "implementer")
	if len(impl) != 3 {
		t.Fatalf("implementer turns = %d, want one per round", len(impl))
	}
	for i, r := range impl {
		if len(r.images) != 3 || !strings.Contains(r.prompt, "## Visual check of the candidate") ||
			!strings.Contains(r.prompt, "calculator.png against "+visionRefID+": 100.0% of pixels differ (allowed 2.0%)") {
			t.Errorf("implementer turn %d: %d images, no current figure:\n%s", i+1, len(r.images), r.prompt)
		}
		if !strings.Contains(r.prompt, "## Visual check — measured by the harness") || !strings.Contains(r.prompt, "Slice 1 cites a reference image") ||
			!strings.Contains(r.prompt, "verify_run does NOT run it") {
			t.Errorf("implementer turn %d is not told slice 1 is the harness's:\n%s", i+1, r.prompt)
		}
		if strings.Contains(r.prompt, "predates the tree") {
			t.Errorf("implementer turn %d was shown an outdated figure", i+1)
		}
	}
	if n := len(eventsOf(events, "advisor_consult")); n != 0 {
		t.Errorf("advisor consulted %d times on a partial the harness measures", n)
	}
	for _, e := range eventsOf(events, "escalation_suggestion") {
		if strings.Contains(stringSliceJoin(e.Data["thresholds_fired"]), "stuck_deliverable") {
			t.Errorf("stuck_deliverable escalation from the measured slice alone: %v", e.Data)
		}
	}
	reports := eventsOf(events, "deliverables_report")
	if len(reports) != 3 {
		t.Fatalf("deliverables_report = %d", len(reports))
	}
	for _, e := range reports {
		// "missing" is what the service's escalation counts; "undelivered"
		// stays the implementer's own word, for the record.
		if missing, _ := e.Data["missing"].([]interface{}); len(missing) != 0 {
			t.Errorf("missing = %v, want the measured slice out of it", missing)
		}
		if und, _ := e.Data["undelivered"].([]interface{}); len(und) != 1 {
			t.Errorf("undelivered = %v, want the self-report kept on the record", und)
		}
		if v, _ := e.Data["visual"].([]interface{}); len(v) != 1 || v[0] != float64(1) {
			t.Errorf("visual = %v, want slice 1 recorded as measured", e.Data["visual"])
		}
	}
	// Every implementer turn after the first follows a change; each review
	// is rendered because the turn before it changed the tree, so the next
	// round's implementer finds that render current.
	var phases []string
	for _, e := range eventsOf(events, "visual_feedback") {
		if e.Data["ok"] != true {
			t.Errorf("a render failed: %v", e.Data)
		}
		phases = append(phases, stringValueAny(e.Data["phase"]))
	}
	want := "before implementer turn,before review,before review,before review"
	if strings.Join(phases, ",") != want {
		t.Errorf("render phases = %v, want %s", phases, want)
	}
	// The diagnostic mismatch did not turn the final approval into
	// request-changes.
	if gaps := eventsOf(events, "deliverables_gap"); len(gaps) != 0 {
		t.Errorf("deliverables_gap = %v on a diagnostic mismatch", gaps[0].Data)
	}
}

// A non-visual partial is still distress, exactly as before; the advisor
// consulted on it is told the comparison contract and the latest figure, is
// told it cannot see the images, and is not asked about the measured slice.
// The advisor's retry starts from a render of the tree its consult judged.
func TestB505B507TheAdvisorAndItsRetryWorkFromTheMeasurement(t *testing.T) {
	reqs, events := runVisualLoop(t, visualLoop{mode: "pair", rounds: 1,
		report: func(n int) string {
			if n == 1 {
				return bothPartial
			}
			return visualPartialOnly
		},
		verdict: func(int) string { return approveVerdict }})
	consults := eventsOf(events, "advisor_consult")
	if len(consults) != 1 {
		t.Fatalf("advisor_consult = %d, want one for slice 2", len(consults))
	}
	signals, _ := consults[0].Data["signals"].(map[string]interface{})
	if und, _ := signals["undelivered"].([]interface{}); len(und) != 1 || und[0] != float64(2) {
		t.Errorf("distress undelivered = %v, want [2]: the measured slice is not distress", signals["undelivered"])
	}
	if n := len(eventsOf(events, "advisor_retry")); n != 1 {
		t.Fatalf("advisor_retry = %d", n)
	}
	adv := firstCalls(reqs, "advisor")
	if len(adv) != 1 {
		t.Fatalf("advisor requests = %d", len(adv))
	}
	p := adv[0].prompt
	for _, want := range []string{
		"### The visual comparison (owned by the harness)",
		"capture `calculator.png` against " + visionRefID + ": at most 2.0% of its pixels may differ",
		"before every implementer, advisor and reviewer turn that follows a change to the tree",
		"verify_run does NOT run it",
		"Mode: diagnostic. This figure is NOT an acceptance criterion",
		"calculator.png against " + visionRefID + ": 100.0% of pixels differ (allowed 2.0%)",
		"Slice 1 is measured by this comparison",
		"The implementer itself reports [2] undelivered",
		"this seat cannot see images",
		"Do not search for them, read them or guess where they are",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("advisor prompt lacks %q:\n%s", want, p)
		}
	}
	if len(adv[0].images) != 0 {
		t.Errorf("a blind advisor was sent %d images", len(adv[0].images))
	}
	if strings.Contains(p, "pixel oracle") || strings.Contains(p, "reports [1 2] undelivered") {
		t.Errorf("advisor prompt carries a false claim:\n%s", p)
	}
	impl := firstCalls(reqs, "implementer")
	if len(impl) != 2 {
		t.Fatalf("implementer turns = %d, want the turn and its advisor retry", len(impl))
	}
	retry := impl[1]
	if len(retry.images) != 3 || !strings.Contains(retry.prompt, "(rendered before advisor consult, round 1)") {
		t.Errorf("the advisor retry was not shown the render of its tree: %d images\n%s", len(retry.images), retry.prompt)
	}
	var phases []string
	for _, e := range eventsOf(events, "visual_feedback") {
		phases = append(phases, stringValueAny(e.Data["phase"]))
	}
	if got := strings.Join(phases, ","); got != "before implementer turn,before advisor consult,before review" {
		t.Errorf("render phases = %s", got)
	}
	// The advisor's turn records what it was (not) shown.
	var advisorImages bool
	for _, e := range eventsOf(events, "turn_images") {
		if e.Data["role"] == "advisor" {
			advisorImages = e.Data["can_see"] == false
		}
	}
	if !advisorImages {
		t.Errorf("no turn_images records the blind advisor")
	}
}

// B-508: the failed run before this one measured other code. Its figure is
// labelled with its run until this run renders its own tree; from then on
// every prompt carries this run's figure instead.
func TestB508ACarriedVisualResultIsLabelledAndThenReplaced(t *testing.T) {
	s := serviceWithDucklings(t, "luna")
	projectID, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: visualLoopPlan})
	prior := &runlog.Run{
		ID: "r-20261005-004552-4tpn", ProjectID: projectID, TaskID: "T-001", Mode: "pair",
		Status: "failed", Verdict: "FAILED", StartedAt: time.Now().UTC().Format(time.RFC3339),
		Visual: &runlog.VisualGate{Enforcement: "diagnostic", Results: []runlog.VisualCompare{{
			Capture: "calculator.png", Reference: visionRefID, Mismatch: 0.445, Tolerance: 0.02,
		}}},
	}
	w, err := runlog.NewWriter(dir, prior)
	if err != nil {
		t.Fatal(err)
	}
	// The gate tail the prompt quotes is six lines: here the visual line fell
	// off it, and the run's own record still carries the figure.
	w.WriteVerify("# pass 41\n# fail 0\n# cancelled 0\n# skipped 0\n# todo 0\n# duration_ms 47.4\nproduct smoke: run.smoke met expectation")
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	prompt := s.buildTaskPrompt(context.Background(), projectID, dir, "T-001")
	label := "Visual result carried from run r-20261005-004552-4tpn — it measured that run's code, not this run's tree: visual check failed: calculator.png differs from " + visionRefID + " in 44.5% of pixels (allowed 2.0%)"
	if !strings.Contains(prompt, label) {
		t.Fatalf("the carried figure is not labelled:\n%s", prompt)
	}
	if strings.Count(prompt, "44.5%") != 1 {
		t.Errorf("the carried figure appears unlabelled too:\n%s", prompt)
	}
	superseded := artifact.SupersedeCarriedVisual(prompt)
	if strings.Contains(superseded, "44.5%") || !strings.Contains(superseded, "Visual result carried from run r-20261005-004552-4tpn — superseded") {
		t.Errorf("the carried figure survived this run's own render:\n%s", superseded)
	}
}

// Through the run: before the first render the implementer reads the labelled
// figure; every turn after the run's first render reads its own.
func TestB508TheRunsOwnRenderReplacesTheCarriedFigure(t *testing.T) {
	reqs, _ := runVisualLoop(t, visualLoop{mode: "pair", rounds: 1, priorFailure: true,
		report: func(int) string { return visualDone }, verdict: func(int) string { return approveVerdict }})
	if len(reqs) == 0 {
		t.Fatal("no requests")
	}
	carried := 0
	for _, r := range reqs {
		if strings.Contains(r.prompt, "Visual result carried from run r-20261005-004552-4tpn — superseded") {
			carried++
		}
	}
	if carried < 2 {
		t.Fatalf("the prior run's line reached %d prompts, want the implementer's and the reviewer's", carried)
	}
	for i, r := range reqs {
		if r.prompt == "" {
			continue
		}
		if strings.Contains(r.prompt, "44.5%") {
			t.Errorf("request %d (%s) still carries the other run's figure:\n%s", i, r.role, r.prompt)
		}
		if strings.Contains(r.prompt, "Approaches already tried") && !strings.Contains(r.prompt, "r-20261005-004552-4tpn — superseded") {
			t.Errorf("request %d (%s) lost the carried line's run id", i, r.role)
		}
	}
}

// The reviewer-approval conversion, one test per cell: the measured slice
// (passed / over tolerance under a diagnostic check / over tolerance under a
// required check) × the other slice (done / partial).
func reviewerMatrix(t *testing.T, match, required, otherDone bool) (*runlog.Event, []visualLoopRequest) {
	t.Helper()
	report := bothPartial
	if otherDone {
		report = visualPartialOnly
	}
	reqs, events := runVisualLoop(t, visualLoop{mode: "pair", rounds: 1, match: match, required: required,
		report: func(int) string { return report }, verdict: func(int) string { return approveVerdict }})
	gaps := eventsOf(events, "deliverables_gap")
	if len(gaps) > 1 {
		t.Fatalf("deliverables_gap = %d", len(gaps))
	}
	if len(gaps) == 0 {
		return nil, reqs
	}
	return gaps[0], reqs
}

func reviewerPrompt(t *testing.T, reqs []visualLoopRequest) string {
	t.Helper()
	rev := firstCalls(reqs, "reviewer")
	if len(rev) == 0 {
		t.Fatal("no reviewer request")
	}
	return rev[len(rev)-1].prompt
}

func gapIDs(e *runlog.Event, key string) string {
	if e == nil {
		return ""
	}
	v, _ := e.Data[key].([]interface{})
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = fmt.Sprint(x)
	}
	return strings.Join(parts, ",")
}

func TestB506MatrixVisualPassedOtherDone(t *testing.T) {
	gap, reqs := reviewerMatrix(t, true, false, true)
	if gap != nil {
		t.Errorf("a passed measurement and a done slice were converted: %v", gap.Data)
	}
	if p := reviewerPrompt(t, reqs); !strings.Contains(p, "- slice 1: passed — calculator.png against "+visionRefID+": 0.0% of pixels differ") {
		t.Errorf("reviewer not told slice 1 passed:\n%s", p)
	}
}

func TestB506MatrixVisualPassedOtherPartial(t *testing.T) {
	gap, _ := reviewerMatrix(t, true, false, false)
	if gap == nil || gapIDs(gap, "undelivered") != "2" || gapIDs(gap, "visual") != "" {
		t.Errorf("gap = %v, want slice 2 only", gap)
	}
}

func TestB506MatrixVisualDiagnosticFailedOtherDone(t *testing.T) {
	gap, reqs := reviewerMatrix(t, false, false, true)
	if gap != nil {
		t.Errorf("a diagnostic mismatch blocked the approval: %v", gap.Data)
	}
	p := reviewerPrompt(t, reqs)
	if !strings.Contains(p, "- slice 1: over tolerance — calculator.png against "+visionRefID+": 100.0% of pixels differ (allowed 2.0%). The check is diagnostic") {
		t.Errorf("the figure was not surfaced to the reviewer:\n%s", p)
	}
	if !strings.Contains(p, `{"id":1,"status":"measured_by_harness"}`) {
		t.Errorf("the reviewer read the self-report as the slice's status:\n%s", p)
	}
}

func TestB506MatrixVisualDiagnosticFailedOtherPartial(t *testing.T) {
	gap, _ := reviewerMatrix(t, false, false, false)
	if gap == nil || gapIDs(gap, "undelivered") != "2" || gapIDs(gap, "visual") != "" {
		t.Errorf("gap = %v, want slice 2 only", gap)
	}
}

func TestB506MatrixVisualRequiredFailedOtherDone(t *testing.T) {
	gap, reqs := reviewerMatrix(t, false, true, true)
	if gap == nil || gapIDs(gap, "undelivered") != "" || gapIDs(gap, "visual") != "1" {
		t.Fatalf("gap = %v, want slice 1 from the required check", gap)
	}
	figures, _ := gap.Data["visual_figures"].(map[string]interface{})
	if !strings.Contains(fmt.Sprint(figures["1"]), "100.0% of pixels differ (allowed 2.0%)") {
		t.Errorf("the finding lacks the figure: %v", figures)
	}
	if p := reviewerPrompt(t, reqs); !strings.Contains(p, "- slice 1: FAILED") || !strings.Contains(p, "The check is required") {
		t.Errorf("reviewer not told the required check failed:\n%s", p)
	}
}

func TestB506MatrixVisualRequiredFailedOtherPartial(t *testing.T) {
	gap, _ := reviewerMatrix(t, false, true, false)
	if gap == nil || gapIDs(gap, "undelivered") != "2" || gapIDs(gap, "visual") != "1" {
		t.Errorf("gap = %v, want slice 2 from the report and slice 1 from the check", gap)
	}
}

// B-507 in test-first: the advisor consulted on a distressed test writer
// (three red verify_runs) is shown the reference the writer works from.
func TestB507TheTestFirstAdvisorIsShownTheReference(t *testing.T) {
	s := serviceWithDucklings(t, "luna", "glm53flash")
	setVision(s, "luna", true)
	setVision(s, "glm53flash", true)
	projectID, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: planDoc, artifact.KindSpec: visionSpecDoc, artifact.KindRequirements: visionReqDoc,
	})
	ref := storeRefImage(t, dir, visionRefFile, solidPNG(t, 8, 16, color.Black))
	if _, err := s.ProjectUpdate(context.Background(), projectID, map[string]string{"verify.mode": "tests", "verify.tests": "false"}); err != nil {
		t.Fatal(err)
	}
	if err := vcs.New(dir).Init(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var advisor *provider.Message
	fake := s.providers["fake"].(*provider.Fake)
	fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
		mu.Lock()
		defer mu.Unlock()
		results := 0
		for _, m := range req.Messages {
			if m.Role == "system" && strings.Contains(m.Content, "You are the advisor") {
				for j := range req.Messages {
					if req.Messages[j].Role == "user" && advisor == nil {
						advisor = &req.Messages[j]
					}
				}
				return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Content: `{"action":"none"}`}, FinishReason: provider.FinishStop}}}
			}
			if m.Role == "system" && strings.Contains(m.Content, "You are the reviewer") {
				return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Content: `{"verdict":"approve","findings":[]}`}, FinishReason: provider.FinishStop}}}
			}
			if m.Role == "tool" || (m.Role == "user" && strings.HasPrefix(m.Content, "Tool result for ")) {
				results++
			}
		}
		if results < 3 {
			return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: "assistant",
				ToolCalls: []provider.ToolCall{fakeToolCall("verify_run", "{}")}}, FinishReason: provider.FinishToolCalls}}}
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Content: "The failing test is written."}, FinishReason: provider.FinishStop}}}
	}
	run, err := s.TestStart(context.Background(), projectID, TestFirstRequest{TaskID: "T-001",
		Seats: map[string]string{"implementer": "luna", "advisor": "glm53flash"}})
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
	mu.Lock()
	defer mu.Unlock()
	if advisor == nil {
		t.Fatal("the advisor was never consulted")
	}
	if len(advisor.Images) != 1 || advisor.Images[0] != ref || !strings.Contains(advisor.Content, visionRefID) {
		t.Errorf("the test-first advisor was not shown the reference: %d images", len(advisor.Images))
	}
}

// B-507: a seeing advisor is shown the reference and the latest capture and
// diff, within the same per-turn bounds as every seat, and the record says
// so; #163 left the advisor out.
func TestB507ASeeingAdvisorSeesTheReferenceAndTheCapture(t *testing.T) {
	s := serviceWithDucklings(t, "luna", "glm53flash")
	setVision(s, "luna", true)
	setVision(s, "glm53flash", true)
	projectID, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: visualLoopPlan, artifact.KindSpec: visionSpecDoc, artifact.KindRequirements: visionReqDoc,
	})
	storeRefImage(t, dir, visionRefFile, solidPNG(t, 8, 16, color.RGBA{R: 40, G: 40, B: 40, A: 255}))
	writer, err := runlog.NewWriter(dir, &runlog.Run{ID: "r-b507-advisor", ProjectID: projectID, Stage: "build"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Close() })
	roster := map[config.Role]config.DucklingID{config.RoleImplementer: "luna", config.RoleAdvisor: "glm53flash"}
	var recorded []map[string]interface{}
	v := s.newTaskVision(context.Background(), projectID, "T-001", []string{dir}, roster,
		[]config.Role{config.RoleImplementer, config.RoleAdvisor},
		func(kind string, data map[string]interface{}) {
			if kind == "turn_images" {
				recorded = append(recorded, data)
			}
		})
	contract := config.RenderContract{Compare: []config.RenderCompare{{Capture: "calculator.png", Reference: visionRefID}}}
	v.withFeedback(writer.RunDir(), func() (string, error) { return "tree", nil }, func(context.Context) (*runlog.VisualGate, []string, error) {
		if err := writer.WriteCapture("calculator.png", solidPNG(t, 8, 16, color.White)); err != nil {
			return nil, nil, err
		}
		return runVisualGate(dir, contract, writer, []string{"calculator.png"}), []string{"calculator.png"}, nil
	})
	var seen []seenTurn
	run := v.wrap(recordingRunner(&seen, func(*strategy.Turn, int) *agent.Outcome { return &agent.Outcome{Text: `{"action":"none"}`} }), roster)
	if _, err := run(context.Background(), &strategy.Turn{Role: config.RoleAdvisor}, "glm53flash", "Rubber-duck consult", nil, strategy.TurnContext{Round: 1}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || len(seen[0].images) != 3 {
		t.Fatalf("seeing advisor images = %v, want the reference, the capture and the diff", seen)
	}
	if !strings.Contains(seen[0].prompt, "they are attached to this message, with the latest capture") ||
		!strings.Contains(seen[0].prompt, "100.0% of pixels differ") {
		t.Errorf("seeing advisor prompt:\n%s", seen[0].prompt)
	}
	if len(recorded) != 1 || recorded[0]["role"] != "advisor" || recorded[0]["can_see"] != true {
		t.Errorf("turn_images = %v", recorded)
	}
}
