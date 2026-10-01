package service

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/runlog"
)

// paintPNG writes a w×h PNG filled with bg, with the rectangle box in fg.
func paintPNG(t *testing.T, path string, w, h int, bg, fg color.RGBA, box image.Rectangle) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := bg
			if (image.Point{X: x, Y: y}).In(box) {
				c = fg
			}
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

var (
	white = color.RGBA{255, 255, 255, 255}
	black = color.RGBA{0, 0, 0, 255}
	nearW = color.RGBA{250, 250, 250, 255}
)

// visualFixture is a run writer with one capture, and a project root.
func visualFixture(t *testing.T, paint func(path string)) (*runlog.Writer, string) {
	t.Helper()
	root := t.TempDir()
	w, err := runlog.NewWriter(t.TempDir(), &runlog.Run{ID: "visual-run", ProjectID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	tmp := filepath.Join(t.TempDir(), "scene-01.png")
	paint(tmp)
	data, _ := os.ReadFile(tmp)
	if err := w.WriteCapture("scene-01.png", data); err != nil {
		t.Fatal(err)
	}
	return w, root
}

func TestTheVisualGatePassesAMatchAndFailsAMismatchWithADiff(t *testing.T) {
	w, root := visualFixture(t, func(p string) {
		paintPNG(t, p, 100, 100, white, black, image.Rect(10, 10, 30, 30)) // 4% black
	})
	same := filepath.Join(root, "same.png")
	paintPNG(t, same, 100, 100, nearW, black, image.Rect(10, 10, 30, 30)) // near-white is under the threshold
	other := filepath.Join(root, "other.png")
	paintPNG(t, other, 100, 100, white, black, image.Rect(60, 60, 80, 80))

	gate := runVisualGate(root, config.RenderContract{
		Enforcement: "required",
		Compare: []config.RenderCompare{
			{Capture: "scene-01.png", Reference: "same.png"},
		},
	}, w, []string{"scene-01.png"})
	if !gate.Passed || gate.Results[0].Mismatch != 0 {
		t.Fatalf("a matching capture failed: %+v", gate.Results)
	}

	gate = runVisualGate(root, config.RenderContract{
		Compare: []config.RenderCompare{{Capture: "scene-01.png", Reference: "other.png"}},
	}, w, []string{"scene-01.png"})
	r := gate.Results[0]
	if gate.Passed || r.Passed || gate.Enforcement != "diagnostic" {
		t.Fatalf("a different capture passed: %+v", gate)
	}
	// Two 20x20 boxes in different places: 800 of 10000 pixels differ.
	if r.Mismatch < 0.079 || r.Mismatch > 0.081 {
		t.Fatalf("mismatch = %v, want 0.08", r.Mismatch)
	}
	for _, name := range []string{r.ReferenceCapture, r.DiffCapture} {
		if _, err := os.Stat(filepath.Join(w.RunDir(), "captures", name)); err != nil || name == "" {
			t.Fatalf("comparison image %q not stored: %v", name, err)
		}
	}
	if !strings.Contains(visualGateSummary(gate), "8.0% of pixels (allowed 2.0%)") {
		t.Fatalf("summary = %q", visualGateSummary(gate))
	}

	// The tolerance is the person's: 10% allowed lets the same pair pass.
	tol := 0.1
	gate = runVisualGate(root, config.RenderContract{
		Compare: []config.RenderCompare{{Capture: "scene-01.png", Reference: "other.png", Tolerance: &tol}},
	}, w, []string{"scene-01.png"})
	if !gate.Passed {
		t.Fatalf("8%% failed a 10%% tolerance: %+v", gate.Results)
	}
}

// A reference of another size is scaled to the capture and the result says
// from what size, so the person knows exactly what was compared.
func TestTheVisualGateScalesAReferenceOfAnotherSizeAndSaysSo(t *testing.T) {
	w, root := visualFixture(t, func(p string) {
		paintPNG(t, p, 100, 100, white, black, image.Rect(0, 0, 50, 100))
	})
	ref := filepath.Join(root, "half.png")
	paintPNG(t, ref, 40, 40, white, black, image.Rect(0, 0, 20, 40))
	gate := runVisualGate(root, config.RenderContract{
		Compare: []config.RenderCompare{{Capture: "scene-01.png", Reference: "half.png"}},
	}, w, []string{"scene-01.png"})
	r := gate.Results[0]
	if r.ScaledFrom != "40x40" || r.Width != 100 || !r.Passed {
		t.Fatalf("result = %+v", r)
	}
}

// The reference is named by the id the requirements cite.
func TestTheVisualGateResolvesAReferenceImageId(t *testing.T) {
	w, root := visualFixture(t, func(p string) {
		paintPNG(t, p, 20, 20, white, black, image.Rect(0, 0, 5, 5))
	})
	src := filepath.Join(t.TempDir(), "ti36x.png")
	paintPNG(t, src, 20, 20, white, black, image.Rect(0, 0, 5, 5))
	_, recs, err := loadRefImages(root, []string{src})
	if err != nil {
		t.Fatal(err)
	}
	gate := runVisualGate(root, config.RenderContract{
		Compare: []config.RenderCompare{{Capture: "scene-01.png", Reference: recs[0].ID}},
	}, w, []string{"scene-01.png"})
	if !gate.Passed {
		t.Fatalf("%s did not resolve: %+v", recs[0].ID, gate.Results)
	}
	gate = runVisualGate(root, config.RenderContract{
		Compare: []config.RenderCompare{{Capture: "scene-01.png", Reference: "REF-IMG-00000000"}},
	}, w, []string{"scene-01.png"})
	if gate.Passed || !strings.Contains(gate.Results[0].Error, "not stored in this project") {
		t.Fatalf("an unknown id = %+v", gate.Results)
	}
}

// A compare that cannot run fails with the reason; silently skipping it is
// the gap the gate exists to close.
func TestTheVisualGateFailsACaptureThatWasNotProduced(t *testing.T) {
	w, root := visualFixture(t, func(p string) { paintPNG(t, p, 10, 10, white, black, image.Rect(0, 0, 1, 1)) })
	paintPNG(t, filepath.Join(root, "ref.png"), 10, 10, white, black, image.Rect(0, 0, 1, 1))
	gate := runVisualGate(root, config.RenderContract{
		Compare: []config.RenderCompare{{Capture: "scene-02.png", Reference: "ref.png"}},
	}, w, []string{"scene-01.png"})
	if gate.Passed || !strings.Contains(gate.Results[0].Error, "produced no scene-02.png (it produced scene-01.png)") {
		t.Fatalf("result = %+v", gate.Results)
	}
	gate = runVisualGate(root, config.RenderContract{
		Compare: []config.RenderCompare{{Capture: "scene-01.png", Reference: "ref.png"}},
	}, w, nil)
	if gate.Passed || !strings.Contains(gate.Results[0].Error, "no captures were produced") {
		t.Fatalf("result = %+v", gate.Results)
	}
}

func TestRenderCompareConfigIsValidated(t *testing.T) {
	bad := -0.5
	for name, mutate := range map[string]func(*config.Project){
		"enforcement": func(p *config.Project) { p.Render.Enforcement = "advisory-ish" },
		"capture":     func(p *config.Project) { p.Render.Compare = []config.RenderCompare{{Capture: "a/b.png", Reference: "r.png"}} },
		"reference":   func(p *config.Project) { p.Render.Compare = []config.RenderCompare{{Capture: "a.png", Reference: "../r.png"}} },
		"tolerance": func(p *config.Project) {
			p.Render.Compare = []config.RenderCompare{{Capture: "a.png", Reference: "r.png", Tolerance: &bad}}
		},
	} {
		p := config.DefaultProject("demo", "Demo")
		mutate(p)
		if err := p.Validate("project.toml"); err == nil {
			t.Errorf("%s: invalid compare accepted", name)
		}
	}
}

// End to end through a build run: a required visual mismatch fails the run
// like a red test; a diagnostic one leaves the verdict and says why.
func TestARequiredVisualMismatchFailsTheBuildAndADiagnosticOneIsACaveat(t *testing.T) {
	for _, enforcement := range []string{"required", "diagnostic"} {
		t.Run(enforcement, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno")
			id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
			for _, args := range [][]string{
				{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
				{"add", "-A"}, {"commit", "-q", "-m", "seed", "--allow-empty"},
			} {
				cmd := exec.Command("git", args...)
				cmd.Dir = dir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			shot := filepath.Join(t.TempDir(), "shot.png")
			paintPNG(t, shot, 50, 50, white, black, image.Rect(0, 0, 25, 50))
			paintPNG(t, filepath.Join(dir, "ref.png"), 50, 50, black, white, image.Rect(0, 0, 25, 50))
			tomlPath := filepath.Join(dir, ".ducklab", "project.toml")
			cfg, err := config.LoadProject(tomlPath)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Render = config.RenderContract{
				Command:     `mkdir -p "$DUCKLAB_RENDER_OUTPUT" && cp '` + shot + `' "$DUCKLAB_RENDER_OUTPUT/scene-01.png"`,
				Artifacts:   ".ducklab-render-captures/*.png",
				Enforcement: enforcement,
				Compare:     []config.RenderCompare{{Capture: "scene-01.png", Reference: "ref.png"}},
			}
			if err := config.SaveProject(tomlPath, cfg); err != nil {
				t.Fatal(err)
			}
			native := true
			for did, duck := range s.cfg.Ducklings {
				duck.Caps.NativeTools = &native
				s.cfg.Ducklings[did] = duck
			}
			fake := s.providers["fake"].(*provider.Fake)
			fake.ScriptFunc = func(req provider.ChatRequest, call int) *provider.ChatResponse {
				var message provider.Message
				finish := provider.FinishStop
				if call == 1 {
					tc := provider.ToolCall{ID: "call-1", Type: "function"}
					tc.Function.Name, tc.Function.Arguments = "fs_write", `{"path":"index.html","content":"<p>calc</p>\n"}`
					message.ToolCalls = []provider.ToolCall{tc}
					finish = provider.FinishToolCalls
				} else {
					message.Content = "The page is written."
				}
				return &provider.ChatResponse{Choices: []provider.Choice{{Message: message, FinishReason: finish}}}
			}
			r, err := s.RunStart(context.Background(), id, RunRequest{TaskID: "T-001", Mode: "solo"})
			if err != nil {
				t.Fatal(err)
			}
			// A diagnostic caveat pauses for the person's decision, as any
			// passed-with-caveat run does; the record is what matters here.
			if _, err := s.waitForRun(context.Background(), r.ID); err != nil && !strings.Contains(err.Error(), "waiting for a human") {
				t.Fatal(err)
			}
			detail, err := s.RunGet(context.Background(), r.ID)
			if err != nil {
				t.Fatal(err)
			}
			run := detail.Run
			if run.Visual == nil || run.Visual.Passed || len(run.Visual.Results) != 1 {
				ev, _ := os.ReadFile(filepath.Join(dir, ".ducklab", "runs", run.ID, "events.jsonl"))
				t.Fatalf("visual gate = %+v (status %s verdict %s captures %v)\n%s", run.Visual, run.Status, run.Verdict, run.Captures, ev)
			}
			if enforcement == "required" {
				if run.Verdict != "FAILED" || !strings.Contains(run.Failure, "visual check failed") {
					t.Fatalf("verdict = %s, failure = %q", run.Verdict, run.Failure)
				}
			} else {
				if run.Verdict == "FAILED" || !strings.Contains(run.Warning, "visual check failed") {
					t.Fatalf("verdict = %s, warning = %q", run.Verdict, run.Warning)
				}
			}
		})
	}
}

// Review of #123: two comparisons of one capture keep their own evidence.
func TestEachComparisonKeepsItsOwnEvidence(t *testing.T) {
	w, root := visualFixture(t, func(p string) {
		paintPNG(t, p, 40, 40, white, black, image.Rect(0, 0, 20, 40))
	})
	paintPNG(t, filepath.Join(root, "left.png"), 40, 40, white, black, image.Rect(0, 0, 20, 40))
	paintPNG(t, filepath.Join(root, "right.png"), 40, 40, black, white, image.Rect(0, 0, 20, 40))
	gate := runVisualGate(root, config.RenderContract{Compare: []config.RenderCompare{
		{Capture: "scene-01.png", Reference: "left.png"},
		{Capture: "scene-01.png", Reference: "right.png"},
	}}, w, []string{"scene-01.png"})
	a, b := gate.Results[0], gate.Results[1]
	if a.Mismatch != 0 || b.Mismatch != 1 {
		t.Fatalf("mismatches = %v, %v", a.Mismatch, b.Mismatch)
	}
	if a.ReferenceCapture == b.ReferenceCapture || a.DiffCapture == b.DiffCapture {
		t.Fatalf("comparisons share evidence: %+v / %+v", a, b)
	}
	// The first comparison's diff is still its own: nothing in red.
	img, err := decodeImageFile(filepath.Join(w.RunDir(), "captures", a.DiffCapture))
	if err != nil {
		t.Fatal(err)
	}
	if r, g, _, _ := img.At(5, 5).RGBA(); r>>8 == 230 && g == 0 {
		t.Fatal("the first comparison's diff shows the second one's mismatch")
	}
}
