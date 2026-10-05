package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
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

// B-504 fixtures: the TI-36X T-008 shape. The task never names the image
// itself; SPEC-001 implements REQ-001, and REQ-001 cites the photo.
const (
	visionRefID   = "REF-IMG-6c63e390"
	visionRefFile = "6c63e3905320.png"
	visionReqDoc  = "## REQ-001 — Replica appearance\n\nAppearance shall match **REF-IMG-6c63e390** at its native scale.\n"
	visionSpecDoc = "## SPEC-001 — Replica composition\n\n**Implements:** REQ-001\n\nCompose the device as the photo shows.\n"
)

func solidPNG(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func storeRefImage(t *testing.T, root, name string, data []byte) string {
	t.Helper()
	dir := filepath.Join(root, ".ducklab", "refs", "images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
}

func setVision(s *Service, id string, see bool) {
	native := true
	s.cfgMu.Lock()
	duck := s.cfg.Ducklings[config.DucklingID(id)]
	duck.Caps.Vision = &see
	duck.Caps.NativeTools = &native
	s.cfg.Ducklings[config.DucklingID(id)] = duck
	s.cfgMu.Unlock()
}

// visionPairBuild runs the real build of T-001 (pair, or the mode given) through the fake provider
// and returns every request and the run's events.
func visionPairBuild(t *testing.T, mode string, reviewerSees, render bool) ([]provider.ChatRequest, []*runlog.Event, string) {
	t.Helper()
	s := serviceWithDucklings(t, "luna", "glm52")
	setVision(s, "luna", true)
	setVision(s, "glm52", reviewerSees)
	projectID, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: planDoc, artifact.KindSpec: visionSpecDoc, artifact.KindRequirements: visionReqDoc,
	})
	ref := storeRefImage(t, dir, visionRefFile, solidPNG(t, 8, 16, color.RGBA{R: 40, G: 40, B: 40, A: 255}))
	if render {
		// The project's own capture command, as TI-36X's: it writes a PNG
		// into DUCKLAB_RENDER_OUTPUT. This one is all white — every pixel
		// differs from the dark reference.
		shot := filepath.Join(t.TempDir(), "shot.png")
		if err := os.WriteFile(shot, solidPNG(t, 8, 16, color.White), 0o644); err != nil {
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
		if err := writeProjectTOML(path, cfg); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<p>calc</p>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vcs.New(dir).Init(); err != nil {
		t.Fatal(err)
	}
	fake := s.providers["fake"].(*provider.Fake)
	fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
		for _, m := range req.Messages {
			if m.Role == "system" && strings.Contains(m.Content, "You are the reviewer") {
				return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Content: `{"verdict":"approve","findings":[]}`}, FinishReason: provider.FinishStop}}}
			}
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Content: `Built it. {"deliverables":[{"id":1,"status":"done"}]}`}, FinishReason: provider.FinishStop}}}
	}
	run, err := s.RunStart(context.Background(), projectID, RunRequest{TaskID: "T-001", Mode: mode,
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
		t.Fatal("the build never finished")
	}
	events, err := runlog.ReadEvents(rs.runDir)
	if err != nil {
		t.Fatal(err)
	}
	return fake.Requests(), events, ref
}

// requestOf returns the first user message of the first request of a role.
func requestOf(reqs []provider.ChatRequest, reviewer bool) *provider.Message {
	for _, req := range reqs {
		isReviewer := false
		for _, m := range req.Messages {
			isReviewer = isReviewer || (m.Role == "system" && strings.Contains(m.Content, "You are the reviewer"))
		}
		if isReviewer != reviewer {
			continue
		}
		for i := range req.Messages {
			if req.Messages[i].Role == "user" && strings.Contains(req.Messages[i].Content, "T-001") {
				return &req.Messages[i]
			}
		}
	}
	return nil
}

func eventsOf(events []*runlog.Event, kind string) []*runlog.Event {
	var out []*runlog.Event
	for _, e := range events {
		if e.Type == kind {
			out = append(out, e)
		}
	}
	return out
}

// TI-36X T-008: a task citing REF-IMG (here through SPEC → REQ) with a seeing
// implementer and a seeing reviewer. Both requests carry the stored image.
func TestB504APairBuildShowsTheCitedReferenceToSeeingSeats(t *testing.T) {
	reqs, events, ref := visionPairBuild(t, "pair", true, false)
	for _, reviewer := range []bool{false, true} {
		msg := requestOf(reqs, reviewer)
		if msg == nil {
			t.Fatalf("no request for reviewer=%v among %d", reviewer, len(reqs))
		}
		if len(msg.Images) != 1 || msg.Images[0] != ref {
			t.Errorf("reviewer=%v request images = %d, want the stored reference", reviewer, len(msg.Images))
		}
		if !strings.Contains(msg.Content, "## Reference images") || !strings.Contains(msg.Content, visionRefID) ||
			!strings.Contains(msg.Content, "attached to this message") {
			t.Errorf("reviewer=%v prompt does not name the attached reference:\n%s", reviewer, msg.Content)
		}
	}
	turns := eventsOf(events, "turn_images")
	if len(turns) < 2 {
		t.Fatalf("turn_images events = %d, want one per implementer and reviewer turn", len(turns))
	}
	for _, e := range turns {
		images, _ := e.Data["images"].([]interface{})
		if e.Data["can_see"] != true || len(images) != 1 {
			t.Errorf("turn_images = %v, want a seeing seat shown one image", e.Data)
		}
	}
	if refs := eventsOf(events, "reference_images"); len(refs) != 1 || !strings.Contains(stringSliceJoin(refs[0].Data["cited"]), visionRefID) {
		t.Errorf("reference_images = %v", refs)
	}
}

// The real T-008 loop with the project's visual check: the seeing reviewer
// judges a render of the candidate it reviews — capture and diff beside the
// photo — where r-20261005-004552-4tpn's reviewer approved a 44.5% mismatch
// it never saw. The final gate still renders under the run's own names.
func TestB504ASeeingReviewerJudgesARenderOfTheCandidate(t *testing.T) {
	reqs, events, ref := visionPairBuild(t, "pair", true, true)
	rev := requestOf(reqs, true)
	if rev == nil {
		t.Fatalf("no reviewer request among %d", len(reqs))
	}
	if len(rev.Images) != 3 || rev.Images[0] != ref {
		t.Fatalf("reviewer images = %d, want the reference, the capture and the diff", len(rev.Images))
	}
	if !strings.Contains(rev.Content, "- calculator.png against "+visionRefID+": 100.0% of pixels differ") {
		t.Errorf("reviewer prompt lacks the mismatch figure:\n%s", rev.Content)
	}
	fb := eventsOf(events, "visual_feedback")
	if len(fb) != 1 || fb[0].Data["ok"] != true || fb[0].Data["phase"] != "before review" {
		t.Errorf("visual_feedback = %v, want one render before the review", fb)
	}
	var final bool
	for _, e := range eventsOf(events, "visual_compare") {
		results, _ := e.Data["results"].([]interface{})
		if len(results) == 1 {
			r, _ := results[0].(map[string]interface{})
			final = r["capture"] == "calculator.png" && r["diff_capture"] == "visual-01-diff-calculator.png"
		}
	}
	if !final {
		t.Errorf("the final gate's comparison changed: %v", eventsOf(events, "visual_compare"))
	}
}

// Solo has no reviewer to render before: each round gate renders the tree
// the implementer just changed, and the next round's implementer starts from
// it. (This fixture's solo runs three rounds: its gate never goes green.)
func TestB504ASoloBuildRendersAtTheRoundGate(t *testing.T) {
	reqs, events, ref := visionPairBuild(t, "solo", true, true)
	rounds := len(eventsOf(events, "round_gate"))
	fb := eventsOf(events, "visual_feedback")
	if rounds < 2 || len(fb) != rounds {
		t.Fatalf("visual_feedback = %d over %d rounds, want one render per round gate", len(fb), rounds)
	}
	for _, e := range fb {
		shown, _ := e.Data["shown"].([]interface{})
		if e.Data["ok"] != true || e.Data["phase"] != "after round gate" || len(shown) != 2 {
			t.Errorf("visual_feedback = %v, want the capture and its diff after the round gate", e.Data)
		}
	}
	var withFeedback int
	for _, req := range reqs {
		for _, m := range req.Messages {
			if m.Role == "user" && strings.Contains(m.Content, "## Visual check of the candidate (rendered after round gate, round 1)") {
				if len(m.Images) == 3 && m.Images[0] == ref {
					withFeedback++
				}
			}
		}
	}
	if withFeedback == 0 {
		t.Error("round 2's implementer was not shown round 1's render")
	}
}

// A blind reviewer is told it cannot see the reference and the record says
// so; it is never silently handed nothing.
func TestB504ABlindSeatIsToldAndRecordedNotSilentlyDropped(t *testing.T) {
	reqs, events, ref := visionPairBuild(t, "pair", false, false)
	impl, rev := requestOf(reqs, false), requestOf(reqs, true)
	if impl == nil || rev == nil {
		t.Fatalf("missing requests among %d", len(reqs))
	}
	if len(impl.Images) != 1 || impl.Images[0] != ref {
		t.Errorf("the seeing implementer lost its image: %d", len(impl.Images))
	}
	if len(rev.Images) != 0 {
		t.Errorf("a blind reviewer was sent %d image(s)", len(rev.Images))
	}
	if !strings.Contains(rev.Content, "this seat cannot see images") || !strings.Contains(rev.Content, visionRefID) {
		t.Errorf("the blind reviewer was not told:\n%s", rev.Content)
	}
	var blind bool
	for _, e := range eventsOf(events, "turn_images") {
		if e.Data["role"] == "reviewer" {
			shown, _ := e.Data["images"].([]interface{})
			blind = e.Data["can_see"] == false && len(shown) == 0 && strings.Contains(stringSliceJoin(e.Data["notes"]), "cannot see")
		}
	}
	if !blind {
		t.Errorf("no turn_images event records the blind reviewer: %v", eventsOf(events, "turn_images"))
	}
}

func stringSliceJoin(v interface{}) string {
	return strings.Join(stringSliceAny(v), "\n")
}

// visionFixture is a taskVision over the real chain, without a run.
func visionFixture(t *testing.T, implSees, revSees bool) (*Service, *taskVision, string, *[]map[string]interface{}) {
	t.Helper()
	s := serviceWithDucklings(t, "luna", "glm52")
	setVision(s, "luna", implSees)
	setVision(s, "glm52", revSees)
	projectID, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: planDoc, artifact.KindSpec: visionSpecDoc, artifact.KindRequirements: visionReqDoc,
	})
	storeRefImage(t, dir, visionRefFile, solidPNG(t, 8, 16, color.RGBA{R: 40, G: 40, B: 40, A: 255}))
	var events []map[string]interface{}
	roster := map[config.Role]config.DucklingID{config.RoleImplementer: "luna", config.RoleReviewer: "glm52"}
	v := s.newTaskVision(context.Background(), projectID, "T-001", []string{dir}, roster,
		[]config.Role{config.RoleImplementer, config.RoleReviewer},
		func(kind string, data map[string]interface{}) {
			events = append(events, map[string]interface{}{"kind": kind, "data": data})
		})
	return s, v, dir, &events
}

type seenTurn struct {
	role   config.Role
	round  int
	images []string
	prompt string
}

func recordingRunner(seen *[]seenTurn, outcome func(t *strategy.Turn, n int) *agent.Outcome) strategy.TurnRunner {
	n := 0
	return func(_ context.Context, t *strategy.Turn, _ config.DucklingID, prompt string, _ []string, tc strategy.TurnContext) (*agent.Outcome, error) {
		n++
		*seen = append(*seen, seenTurn{role: t.Role, round: tc.Round, images: t.Images, prompt: prompt})
		return outcome(t, n), nil
	}
}

func approve() *agent.Outcome {
	return &agent.Outcome{Text: `{"verdict":"approve"}`, Parsed: &agent.Verdict{Verdict: "approve"}}
}

// #146 and #155: a deliverables-report retry is a new implementer
// conversation and a resumed reviewer replays only its own turn. Both go
// through the runner, so both carry the reference.
func TestB504RetriedAndResumedTurnsCarryTheReference(t *testing.T) {
	_, v, _, _ := visionFixture(t, true, true)
	roster := map[config.Role]config.DucklingID{config.RoleImplementer: "luna", config.RoleReviewer: "glm52"}
	var seen []seenTurn
	params := &strategy.ExecuteParams{
		Prompt:       "Implement T-001",
		Deliverables: []string{"Match the photo"},
		Roster:       roster,
		Runner: v.wrap(recordingRunner(&seen, func(t *strategy.Turn, n int) *agent.Outcome {
			if t.Role == config.RoleReviewer {
				return approve()
			}
			if n == 1 {
				return &agent.Outcome{Text: "still working"}
			}
			return &agent.Outcome{Text: `done {"deliverables":[{"id":1,"status":"done"}]}`}
		}), roster),
		Diff: func() (string, error) { return "diff", nil },
		Gate: func(context.Context) (string, string, error) { return "green", "", nil },
	}
	if _, err := strategy.ExecuteScript(context.Background(), strategy.PairScript(), params); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 || seen[0].role != config.RoleImplementer || seen[1].role != config.RoleImplementer {
		t.Fatalf("turns = %+v, want implementer, its report retry, reviewer", seen)
	}
	for i, turn := range seen {
		if len(turn.images) != 1 || !strings.HasPrefix(turn.images[0], "data:image/png;base64,") {
			t.Errorf("turn %d (%s) images = %d, want the reference", i, turn.role, len(turn.images))
		}
	}

	seen = nil
	params.ResumeFrom = &strategy.ResumeTurn{Round: 1, Index: 1, Role: config.RoleReviewer, Notes: "partial"}
	if _, err := strategy.ExecuteScript(context.Background(), strategy.PairScript(), params); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0].role != config.RoleReviewer || len(seen[0].images) != 1 {
		t.Fatalf("resumed turns = %+v, want the reviewer shown the reference", seen)
	}
	if !strings.Contains(seen[0].prompt, "Resume checkpoint") || !strings.Contains(seen[0].prompt, visionRefID) {
		t.Errorf("resumed reviewer prompt lacks the checkpoint or the reference section")
	}
}

// The test-first writer turns "matches the photo" into assertions, and the
// standalone review judges the accepted diff: both seats see the reference.
func TestB504TestFirstAndReviewSeatsSeeTheReference(t *testing.T) {
	s := serviceWithDucklings(t, "luna")
	setVision(s, "luna", true)
	projectID, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: planDoc, artifact.KindSpec: visionSpecDoc, artifact.KindRequirements: visionReqDoc,
	})
	ref := storeRefImage(t, dir, visionRefFile, solidPNG(t, 8, 16, color.Black))
	if _, err := s.ProjectUpdate(context.Background(), projectID, map[string]string{"verify.mode": "tests", "verify.tests": "true"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<p>calc</p>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vcs.New(dir).Init(); err != nil {
		t.Fatal(err)
	}
	fake := s.providers["fake"].(*provider.Fake)
	fake.ScriptFunc = func(provider.ChatRequest, int) *provider.ChatResponse {
		return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Content: `{"verdict":"approve","findings":[]}`}, FinishReason: provider.FinishStop}}}
	}
	run, err := s.TestStart(context.Background(), projectID, TestFirstRequest{TaskID: "T-001", Duckling: "luna"})
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
	writerReq := requestOf(fake.Requests(), false)
	if writerReq == nil || len(writerReq.Images) != 1 || writerReq.Images[0] != ref {
		t.Fatalf("the test writer was not shown the reference: %+v", writerReq)
	}

	before := len(fake.Requests())
	review := &runlog.Run{ID: "r-b504-review", ProjectID: projectID, Stage: "review", Mode: "solo", TaskID: "T-001"}
	writer, err := runlog.NewWriter(dir, review)
	if err != nil {
		t.Fatal(err)
	}
	rrs := &runState{run: review, writer: writer, runDir: writer.RunDir(), projectPath: dir, done: make(chan struct{})}
	s.executeReview(context.Background(), rrs, dir, ReviewRequest{TaskID: "T-001"}, "--- a/index.html\n+++ b/index.html\n+<div>calc</div>\n", "")
	reviewerReq := requestOf(fake.Requests()[before:], true)
	if reviewerReq == nil || len(reviewerReq.Images) != 1 || reviewerReq.Images[0] != ref {
		t.Fatalf("the standalone reviewer was not shown the reference: %+v", reviewerReq)
	}
}

// A task citing nothing is untouched: no images, no prompt section, no
// event — the wrapper is the inner runner itself.
func TestB504ATaskCitingNoReferenceIsUntouched(t *testing.T) {
	s := serviceWithDucklings(t, "luna")
	setVision(s, "luna", true)
	projectID, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc, artifact.KindSpec: specDoc})
	var events int
	v := s.newTaskVision(context.Background(), projectID, "T-001", []string{dir}, nil,
		[]config.Role{config.RoleImplementer}, func(string, map[string]interface{}) { events++ })
	// Even in a project with a visual check: a logic task is not rendered
	// for between rounds.
	v = v.withFeedback(t.TempDir(), func(context.Context) (*runlog.VisualGate, []string, error) {
		t.Fatal("rendered for a task citing nothing")
		return nil, nil, nil
	})
	if !v.empty() || events != 0 {
		t.Fatalf("a task citing nothing armed vision (events %d)", events)
	}
}

// Bounds: at most four references per turn (the rest named in a note), and
// an oversized reference is downscaled to fit, never sent whole.
func TestB504ReferencesAreBoundedInCountAndSize(t *testing.T) {
	s := serviceWithDucklings(t, "luna")
	setVision(s, "luna", true)
	ids := []string{"aaaaaaaa", "bbbbbbbb", "cccccccc", "dddddddd", "eeeeeeee"}
	body := "Match"
	for _, id := range ids {
		body += " REF-IMG-" + id
	}
	plan := "## M-01 — Face\n\n### T-001 — Replica\n\n" + body + ".\n"
	projectID, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: plan})
	storeRefImage(t, dir, ids[0]+"0000.png", solidPNG(t, 3000, 200, color.RGBA{R: 200, A: 255}))
	for _, id := range ids[1:] {
		storeRefImage(t, dir, id+"0000.png", solidPNG(t, 4, 4, color.Black))
	}
	roster := map[config.Role]config.DucklingID{config.RoleImplementer: "luna"}
	v := s.newTaskVision(context.Background(), projectID, "T-001", []string{dir}, roster,
		[]config.Role{config.RoleImplementer}, nil)
	urls, section, shown, notes := v.forTurn(config.RoleImplementer, true)
	if len(urls) != maxTurnRefImages || len(shown) != maxTurnRefImages {
		t.Fatalf("attached %d references, want %d", len(urls), maxTurnRefImages)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "REF-IMG-eeeeeeee was not attached") ||
		!strings.Contains(section, "REF-IMG-eeeeeeee was not attached") {
		t.Errorf("the fifth reference was dropped without a note: %v", notes)
	}
	big := shown[0]
	if big.ScaledFrom != "3000x200" || big.Width != maxTurnImageSide || big.Height != 200*maxTurnImageSide/3000 {
		t.Errorf("oversized reference = %+v, want downscaled to %d wide", big, maxTurnImageSide)
	}
	if !strings.Contains(section, "downscaled from 3000x200") {
		t.Errorf("the prompt does not say the reference was downscaled")
	}
}

// The per-turn total (6 MB) and count (6) bound what references and feedback
// add up to; what does not fit is named, not silently lost.
func TestB504ATurnsImagesAreBoundedInTotal(t *testing.T) {
	ref := func(id string, n int) turnImage {
		return turnImage{ID: id, Kind: "reference", File: "r.png", Bytes: n, url: "data:image/png;base64," + id}
	}
	v := &taskVision{
		cited: []string{"REF-IMG-aaaaaaaa", "REF-IMG-bbbbbbbb", "REF-IMG-cccccccc", "REF-IMG-dddddddd"},
		refs: []turnImage{ref("REF-IMG-aaaaaaaa", 2<<20), ref("REF-IMG-bbbbbbbb", 2<<20),
			ref("REF-IMG-cccccccc", 2<<20), ref("REF-IMG-dddddddd", 2<<20)},
	}
	urls, section, _, notes := v.forTurn(config.RoleImplementer, true)
	if len(urls) != 3 || !strings.Contains(strings.Join(notes, "\n"), "REF-IMG-dddddddd (reference) was not attached: the turn's images would exceed 6 MB") {
		t.Errorf("total bound: %d attached, notes %v", len(urls), notes)
	}
	if !strings.Contains(section, "REF-IMG-dddddddd: stored at r.png — not attached") {
		t.Errorf("the prompt does not say which reference is missing:\n%s", section)
	}

	small := func(id, kind string) turnImage { return turnImage{ID: id, Kind: kind, Bytes: 10, url: "data:," + id} }
	v = &taskVision{
		cited: []string{"REF-IMG-aaaaaaaa", "REF-IMG-bbbbbbbb", "REF-IMG-cccccccc", "REF-IMG-dddddddd"},
		refs: []turnImage{small("REF-IMG-aaaaaaaa", "reference"), small("REF-IMG-bbbbbbbb", "reference"),
			small("REF-IMG-cccccccc", "reference"), small("REF-IMG-dddddddd", "reference")},
		feedback: &visualFeedback{Round: 1, Phase: "before review", images: []turnImage{
			small("a.png", "capture"), small("a.png vs REF-IMG-aaaaaaaa", "diff"), small("b.png", "capture")}},
	}
	urls, _, _, notes = v.forTurn(config.RoleReviewer, true)
	if len(urls) != maxTurnImages || !strings.Contains(strings.Join(notes, "\n"), "b.png (capture) was not attached: at most 6 images per turn") {
		t.Errorf("count bound: %d attached, notes %v", len(urls), notes)
	}
}

// After a round whose gate rendered the candidate, the next round's seeing
// implementer gets the capture and its diff with the mismatch figure; the
// seeing reviewer after it gets a fresh render of the tree it judges.
func TestB504TheNextRoundSeesTheCaptureAndItsDiff(t *testing.T) {
	_, v, dir, events := visionFixture(t, true, true)
	writer, err := runlog.NewWriter(dir, &runlog.Run{ID: "r-b504-feedback", ProjectID: "p", Stage: "build"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Close() })
	contract := config.RenderContract{Compare: []config.RenderCompare{{Capture: "calculator.png", Reference: visionRefID}}}
	renders := 0
	v.withFeedback(writer.RunDir(), func(context.Context) (*runlog.VisualGate, []string, error) {
		renders++
		// A capture that is half right: the mismatch is a real figure.
		capture := image.NewRGBA(image.Rect(0, 0, 8, 16))
		for y := 0; y < 16; y++ {
			for x := 0; x < 8; x++ {
				c := color.RGBA{R: 40, G: 40, B: 40, A: 255}
				if y < 8 {
					c = color.RGBA{R: 255, G: 255, B: 255, A: 255}
				}
				capture.Set(x, y, c)
			}
		}
		data, _ := encodePNG(capture)
		if err := writer.WriteCapture("calculator.png", data); err != nil {
			return nil, nil, err
		}
		return runVisualGate(dir, contract, writer, []string{"calculator.png"}), []string{"calculator.png"}, nil
	})
	roster := map[config.Role]config.DucklingID{config.RoleImplementer: "luna", config.RoleReviewer: "glm52"}
	var seen []seenTurn
	round := 0
	params := &strategy.ExecuteParams{
		Prompt: "Implement T-001",
		Roster: roster,
		Runner: v.wrap(recordingRunner(&seen, func(t *strategy.Turn, _ int) *agent.Outcome {
			if t.Role == config.RoleReviewer {
				if round++; round == 1 {
					return &agent.Outcome{Text: `{"verdict":"request-changes"}`, Parsed: &agent.Verdict{Verdict: "request-changes",
						Findings: []agent.Finding{{Severity: "major", Issue: "the d-pad is on the wrong side"}}}}
				}
				return approve()
			}
			return &agent.Outcome{Text: "Built."}
		}), roster),
		Diff: func() (string, error) { return "diff", nil },
		Gate: v.wrapGate(func(context.Context) (string, string, error) { return "green", "", nil }),
	}
	if _, err := strategy.ExecuteScript(context.Background(), strategy.PairScript(), params); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 4 {
		t.Fatalf("turns = %d, want two rounds of implementer and reviewer", len(seen))
	}
	// Round 1's implementer has nothing rendered yet: the reference only.
	if len(seen[0].images) != 1 || strings.Contains(seen[0].prompt, "## Visual check") {
		t.Errorf("round 1 implementer: %d images", len(seen[0].images))
	}
	// Every reviewer judges a fresh render, and round 2's implementer starts
	// from the render taken before round 1's review (the reviewer changed
	// nothing): reference, capture, diff — and the figure in words.
	for i := 1; i < 4; i++ {
		turn := seen[i]
		if len(turn.images) != 3 {
			t.Errorf("turn %d (%s, round %d): %d images, want reference, capture and diff", i, turn.role, turn.round, len(turn.images))
		}
		if !strings.Contains(turn.prompt, "## Visual check of the candidate") ||
			!strings.Contains(turn.prompt, "- calculator.png against "+visionRefID+": 50.0% of pixels differ") {
			t.Errorf("turn %d (%s) prompt lacks the mismatch figure:\n%s", i, turn.role, turn.prompt)
		}
		if strings.Contains(turn.prompt, "predates the implementer's latest turn") {
			t.Errorf("turn %d (%s) was shown a stale render", i, turn.role)
		}
	}
	// One render per reviewer turn; the round gates had nothing new to show.
	if renders != 2 {
		t.Errorf("renders = %d, want one before each review", renders)
	}
	var feedback int
	for _, e := range *events {
		if e["kind"] == "visual_feedback" {
			feedback++
		}
	}
	if feedback != 2 {
		t.Errorf("visual_feedback events = %d", feedback)
	}
	// The run's own capture names stay the final gate's: feedback evidence
	// lives under its own names.
	if _, err := os.Stat(filepath.Join(writer.RunDir(), "captures", "calculator.png")); !os.IsNotExist(err) {
		t.Errorf("feedback left the final gate's capture name in place: %v", err)
	}
	if _, err := os.Stat(filepath.Join(writer.RunDir(), "captures", "feedback-02-visual-01-diff-calculator.png")); err != nil {
		t.Errorf("the second render's diff is not kept: %v", err)
	}
}

// Solo has no reviewer to render before: the round gate renders after the
// implementer changed the tree, so the next round's implementer sees it. A
// blind seat in the same run gets the figure in words and no images.
func TestB504TheRoundGateRendersForTheNextImplementer(t *testing.T) {
	_, v, dir, _ := visionFixture(t, true, false)
	writer, err := runlog.NewWriter(dir, &runlog.Run{ID: "r-b504-solo", ProjectID: "p", Stage: "build"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Close() })
	// On the run's record, as the build wires it: the resume reads it back.
	v.emit = func(kind string, data map[string]interface{}) { writer.AppendEvent(kind, data) }
	contract := config.RenderContract{Compare: []config.RenderCompare{{Capture: "calculator.png", Reference: visionRefID}}}
	v.withFeedback(writer.RunDir(), func(context.Context) (*runlog.VisualGate, []string, error) {
		if err := writer.WriteCapture("calculator.png", solidPNG(t, 8, 16, color.White)); err != nil {
			return nil, nil, err
		}
		return runVisualGate(dir, contract, writer, []string{"calculator.png"}), []string{"calculator.png"}, nil
	})
	gate := v.wrapGate(func(context.Context) (string, string, error) { return "red", "", nil })
	var seen []seenTurn
	run := v.wrap(recordingRunner(&seen, func(*strategy.Turn, int) *agent.Outcome { return &agent.Outcome{Text: "Built."} }), nil)
	impl := &strategy.Turn{Role: config.RoleImplementer}
	if _, err := run(context.Background(), impl, "luna", "Implement T-001", nil, strategy.TurnContext{Round: 1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := gate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := run(context.Background(), impl, "luna", "Implement T-001", nil, strategy.TurnContext{Round: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := run(context.Background(), &strategy.Turn{Role: config.RoleReviewer}, "glm52", "Review T-001", nil, strategy.TurnContext{Round: 2, Index: 1}); err != nil {
		t.Fatal(err)
	}
	if len(seen[1].images) != 3 || !strings.Contains(seen[1].prompt, "100.0% of pixels differ") {
		t.Errorf("round 2 implementer: %d images, prompt:\n%s", len(seen[1].images), seen[1].prompt)
	}
	blind := seen[2]
	if len(blind.images) != 0 || !strings.Contains(blind.prompt, "100.0% of pixels differ") || !strings.Contains(blind.prompt, "cannot see images") {
		t.Errorf("blind reviewer: %d images, prompt:\n%s", len(blind.images), blind.prompt)
	}
	// The blind reviewer was not worth a render, and its stale render says so.
	if !strings.Contains(blind.prompt, "predates the implementer's latest turn") {
		t.Errorf("the blind reviewer was not told the render predates the latest turn")
	}

	// A resumed run restores the latest feedback from the record.
	again := (&taskVision{svc: v.svc, roles: v.roles, cited: v.cited, refs: v.refs}).withFeedback(writer.RunDir(),
		func(context.Context) (*runlog.VisualGate, []string, error) { return nil, nil, nil })
	urls, section, _, _ := again.forTurn(config.RoleImplementer, true)
	if len(urls) != 3 || !strings.Contains(section, "100.0% of pixels differ") {
		t.Errorf("resumed feedback: %d images, section:\n%s", len(urls), section)
	}
}
