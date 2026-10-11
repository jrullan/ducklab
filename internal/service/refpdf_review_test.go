package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/vcs"
)

// runIntakeWithRefs runs one intake to its gate with a scripted architect.
func runIntakeWithRefs(t *testing.T, s *Service, projectID string, refs []string) *RunDetail {
	t.Helper()
	fake := s.providers["fake"].(*provider.Fake)
	fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
		return &provider.ChatResponse{Choices: []provider.Choice{{
			Message:      provider.Message{Role: "assistant", Content: "## REQ-001 — Mode key\n\n**Priority:** must\n\nPressing 2nd then MODE quits.\n"},
			FinishReason: provider.FinishStop,
		}}}
	}
	run, err := s.StageStart(context.Background(), projectID, StageRequest{
		Stage: "intake", Mode: "solo", From: "A calculator.", Refs: backgroundRefs(refs...), Ducklings: []string{"pato-uno"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	<-rs.done
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.Status == "failed" {
		t.Fatalf("run failed: %s", detail.Run.Failure)
	}
	return detail
}

// Review of #173: the PDF extraction (and the digest cache beside it) was
// written to <project>/.ducklab/refs-cache, which no .gitignore excludes —
// untracked plaintext of a private manual, one `git add -A` from a commit and
// a dirty tree for every clean-tree guard. An inline PDF intake must leave
// nothing in the project for git to see, and the extraction must land in the
// person's cache directory instead.
func TestInlinePDFIntakeLeavesTheProjectTreeClean(t *testing.T) {
	pdf := pdfFixture(t)
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	projectID, projectRoot := projectWithDocs(t, s, map[artifact.Kind]string{})
	git := vcs.New(projectRoot)
	if !git.HasGit() {
		if err := git.Init(); err != nil {
			t.Fatal(err)
		}
	}
	// The ignore entries ProjectInit manages for run records; refs-cache was
	// never among them, which is the point.
	if err := vcs.EnsureGitignore(projectRoot, []string{".ducklab/runs/", ".ducklab/ducklab.db", ".ducklab/lock", ".ducklab/app.log"}); err != nil {
		t.Fatal(err)
	}
	_ = git.AddAll()
	_, _ = git.Commit("fixture")
	before := porcelain(t, projectRoot)

	runIntakeWithRefs(t, s, projectID, []string{pdf})

	after := porcelain(t, projectRoot)
	for _, line := range after {
		if !contains(before, line) && !strings.Contains(line, ".ducklab/docs/") {
			t.Errorf("the intake left %q in the project tree", line)
		}
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".ducklab", "refs-cache")); err == nil {
		t.Error(".ducklab/refs-cache was created inside the project")
	}
	entries, _ := os.ReadDir(refCacheDir())
	var cached bool
	for _, e := range entries {
		cached = cached || strings.Contains(e.Name(), "pdftext")
	}
	if !cached {
		t.Errorf("the extraction is not in the person's cache dir %s: %v", refCacheDir(), entries)
	}
}

func porcelain(t *testing.T, dir string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain", "--untracked-files=all").Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// Review of #173: with max_files=1, a readable reference followed by a binary
// one drops the binary from the prompt — yet it entered
// references_loaded.unreadable and the warning said "the model saw only a
// notice" for a file the model never saw. It is reported as dropped, and
// only what the prompt carried is warned about.
func TestCappedInlineReferencesWarnOnlyAboutIncludedFiles(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	projectID, projectRoot := projectWithDocs(t, s, map[artifact.Kind]string{})
	cfg := config.DefaultProject(projectID, "proj")
	cfg.References.MaxFiles = 1
	if err := writeProjectTOML(filepath.Join(projectRoot, ".ducklab", "project.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	readable := filepath.Join(dir, "a.md")
	os.WriteFile(readable, []byte("# Keys\n2nd then MODE quits.\n"), 0o644)
	docx := docxFixture(t)
	binary := filepath.Join(dir, "b.docx")
	data, _ := os.ReadFile(docx)
	os.WriteFile(binary, data, 0o644)

	detail := runIntakeWithRefs(t, s, projectID, []string{readable, binary})

	loaded := eventsOfType(detail.Events, "references_loaded")
	if len(loaded) != 1 {
		t.Fatalf("references_loaded = %+v", loaded)
	}
	if u, ok := loaded[0].Data["unreadable"].([]interface{}); ok && len(u) > 0 {
		t.Errorf("a capped-out file was reported as seen-as-notice: %v", u)
	}
	dropped, _ := loaded[0].Data["dropped"].([]interface{})
	if len(dropped) != 1 || dropped[0] != binary {
		t.Errorf("dropped = %v, want [%s]", dropped, binary)
	}
	for _, e := range eventsOfType(detail.Events, "warning") {
		if e.Data["kind"] == "unreadable_reference" {
			t.Errorf("warned about a reference the model never saw: %v", e.Data["detail"])
		}
	}
	if strings.Contains(detail.Run.Warning, "b.docx") {
		t.Errorf("run warning names the dropped file: %q", detail.Run.Warning)
	}
}

// The same corpus uncapped still warns: the filter keeps what was included.
func TestUncappedBinaryReferenceStillWarns(t *testing.T) {
	got := includedUnreadable(
		[]refUnreadable{{Path: "/r/b.docx"}, {Path: "/r/c.zip"}},
		[]refFile{{Path: "/r/a.md"}, {Path: "/r/b.docx", Unreadable: "b.docx: unreadable"}},
	)
	if len(got) != 1 || got[0].Path != "/r/b.docx" {
		t.Errorf("includedUnreadable = %+v", got)
	}
}
