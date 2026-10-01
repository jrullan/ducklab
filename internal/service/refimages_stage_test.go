package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/vcs"
)

// B-457 end to end: an intake whose references include an image shows it to
// a seeing architect and names it in the prompt; a blind architect gets the
// name and the instruction not to invent visual detail, never the bytes.
func TestIntakeImageReferenceReachesASeeingArchitectAndIsNamedForABlindOne(t *testing.T) {
	for _, seeing := range []bool{true, false} {
		s := serviceWithDucklings(t, "pato-uno", "pato-dos")
		d := s.cfg.Ducklings[config.DucklingID("pato-uno")]
		v := seeing
		d.Caps.Vision = &v
		s.cfg.Ducklings[config.DucklingID("pato-uno")] = d
		projectID, projectRoot := projectWithDocs(t, s, map[artifact.Kind]string{})
		img := filepath.Join(t.TempDir(), "ti36x.png")
		writePNG(t, img, 30, 60)

		fake := s.providers["fake"].(*provider.Fake)
		images, named, blindNote := 0, false, false
		fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
			for _, m := range req.Messages {
				// Only the stage's own messages: the engine's vision probe
				// sends a 1x1 PNG of its own to the same provider.
				if strings.Contains(m.Content, "pixel perfect calculator") {
					images += len(m.Images)
				}
				named = named || strings.Contains(m.Content, "REF-IMG-1: ti36x.png")
				blindNote = blindNote || strings.Contains(m.Content, "cannot see images")
			}
			return &provider.ChatResponse{Choices: []provider.Choice{{
				Message:      provider.Message{Role: "assistant", Content: "## REQ-001 — Matches REF-IMG-1\n\n**Priority:** must\n\nThe face matches REF-IMG-1 at 1x.\n"},
				FinishReason: provider.FinishStop,
			}}}
		}
		run, err := s.StageStart(context.Background(), projectID, StageRequest{
			Stage: "intake", Mode: "solo", From: "A pixel perfect calculator.", Refs: []string{img},
			Ducklings: []string{"pato-uno"},
		})
		if err != nil {
			t.Fatal(err)
		}
		s.runsMu.RLock()
		rs := s.runs[run.ID]
		s.runsMu.RUnlock()
		<-rs.done
		detail, _ := s.RunGet(context.Background(), run.ID)
		if detail.Run.Status == "failed" {
			t.Fatalf("seeing=%v: run failed: %s", seeing, detail.Run.Failure)
		}
		if !named {
			t.Errorf("seeing=%v: the prompt never named REF-IMG-1", seeing)
		}
		if seeing && images == 0 {
			t.Errorf("a seeing architect was not shown the image")
		}
		if !seeing && (images != 0 || !blindNote) {
			t.Errorf("blind architect: images=%d note=%v", images, blindNote)
		}
		if _, err := artifact.LoadProposed(projectRoot, artifact.KindRequirements); err != nil {
			t.Errorf("seeing=%v: no proposal: %v", seeing, err)
		}
	}
}

// B-457: the image a requirement cites as REF-IMG-n lands with the document,
// or the citation breaks on the next clone.
func TestAcceptingTheStageCommitsItsReferenceImages(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	projectID, projectRoot := projectWithDocs(t, s, map[artifact.Kind]string{})
	git := vcs.New(projectRoot)
	if !git.HasGit() {
		if err := git.Init(); err != nil {
			t.Fatal(err)
		}
	}
	_ = git.AddAll()
	_, _ = git.Commit("fixture")
	img := filepath.Join(t.TempDir(), "ti36x.png")
	writePNG(t, img, 30, 60)
	fake := s.providers["fake"].(*provider.Fake)
	fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
		return &provider.ChatResponse{Choices: []provider.Choice{{
			Message:      provider.Message{Role: "assistant", Content: "## REQ-001 — Matches REF-IMG-1\n\n**Priority:** must\n\nThe face matches REF-IMG-1 at 1x.\n"},
			FinishReason: provider.FinishStop,
		}}}
	}
	run, err := s.StageStart(context.Background(), projectID, StageRequest{
		Stage: "intake", Mode: "solo", From: "A pixel perfect calculator.", Refs: []string{img}, Ducklings: []string{"pato-uno"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	<-rs.done
	if _, err := s.RunAccept(context.Background(), run.ID, ""); err != nil {
		t.Fatal(err)
	}
	files := git.LsFiles()
	want := ".ducklab/refs/" + run.ID + "/img-1.png"
	for _, f := range files {
		if strings.HasSuffix(filepath.ToSlash(f), want) {
			return
		}
	}
	t.Fatalf("%s was not committed with the requirements; tracked: %v", want, files)
}
