package service

import (
	"context"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

// B-460 part 2: the visual gate is configured from what the project already
// knows (its reference images, its last captures), not from typed ids.
func TestTheVisualCheckIsConfiguredFromTheProjectsOwnImagesAndCaptures(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: t.TempDir(), Name: "calc", GitInit: true, GitName: "Ada", GitEmail: "a@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// A run with captures is what names the capture a comparison can hold.
	run := &runlog.Run{ID: "r-shots", ProjectID: p.ID, Stage: "build", TaskID: "T-001", Status: "done", Verdict: "PASSED",
		Captures: []string{"scene-01.png", "scene-02.png"}, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	w, _ := runlog.NewWriter(p.Path, run)
	w.Close()
	s.RecoverRuns(ctx)

	view, err := s.VisualCheck(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Configured || view.Enforcement != "diagnostic" || strings.Join(view.RecentCaptures, ",") != "scene-01.png,scene-02.png" || view.RecentRunID != "r-shots" {
		t.Fatalf("fresh view = %+v", view)
	}

	// An image chosen from disk becomes a reference with a citable id.
	src := filepath.Join(t.TempDir(), "ti36x.png")
	paintPNG(t, src, 30, 60, white, black, image.Rect(0, 0, 10, 10))
	ref, err := s.ReferenceImport(ctx, p.ID, ReferenceImportRequest{Path: src})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ref.ID, "REF-IMG-") || ref.Width != 30 || ref.Height != 60 {
		t.Fatalf("imported = %+v", ref)
	}
	if data, mediaType, err := s.ReferenceImage(ctx, p.ID, ref.ID); err != nil || mediaType != "image/png" || len(data) == 0 {
		t.Fatalf("reference image = %d bytes %q %v", len(data), mediaType, err)
	}

	// A comparison with nothing to capture it is refused, with the fix.
	cmp := []config.RenderCompare{{Capture: "scene-01.png", Reference: ref.ID}}
	if _, err := s.VisualCheckSet(ctx, p.ID, VisualCheckSetRequest{Compare: cmp}); err == nil || !strings.Contains(err.Error(), "needs a capture command") {
		t.Fatalf("no command: %v", err)
	}
	// An id the project does not hold is refused before it is saved.
	if _, err := s.VisualCheckSet(ctx, p.ID, VisualCheckSetRequest{Command: "node shot.mjs", Compare: []config.RenderCompare{{Capture: "scene-01.png", Reference: "REF-IMG-00000000"}}}); err == nil {
		t.Fatal("an unknown reference was accepted")
	}

	view, err = s.VisualCheckSet(ctx, p.ID, VisualCheckSetRequest{Command: "node shot.mjs", Enforcement: "required", Compare: cmp})
	if err != nil {
		t.Fatal(err)
	}
	if !view.Configured || view.Enforcement != "required" || view.Artifacts != defaultRenderArtifacts || len(view.Compare) != 1 || len(view.References) != 1 {
		t.Fatalf("set view = %+v", view)
	}
	cfg, err := config.LoadProject(filepath.Join(p.Path, ".ducklab", "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.RenderConfigured || cfg.Render.Command != "node shot.mjs" || cfg.Render.Compare[0].Reference != ref.ID {
		t.Fatalf("saved render = %+v", cfg.Render)
	}
	// Review of #124: the settings and the reference are versioned, so a
	// worktree made from the branch (as a build run's is) holds both.
	git := vcs.New(p.Path)
	refRel := strings.TrimPrefix(ref.Stored, "./")
	for _, f := range []string{".ducklab/project.toml", refRel} {
		if !git.PathIsCommitted(f) {
			t.Errorf("%s is not committed after saving the visual check", f)
		}
	}
	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := exec.Command("git", "-C", p.Path, "worktree", "add", "-q", "--detach", wt, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("worktree: %v\n%s", err, out)
	}
	if _, err := resolveRenderReference(wt, ref.ID); err != nil {
		t.Fatalf("a worktree from HEAD cannot find the reference: %v", err)
	}
	audit, _ := os.ReadFile(filepath.Join(p.Path, ".ducklab", "config-audit.jsonl"))
	if !strings.Contains(string(audit), `"source":"visual_check"`) {
		t.Fatalf("no audit receipt: %s", audit)
	}
}
