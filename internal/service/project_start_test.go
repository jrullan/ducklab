package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/vcs"
)

// B-463: a machine with no git identity used to fail the very first action
// with git's raw "Author identity unknown". Now it is a question, asked
// before anything is created, and the answer lands in the repository only.
func TestProjectInitAsksForAGitIdentityAndSetsItForThatRepositoryOnly(t *testing.T) {
	orig := gitIdentityKnown
	gitIdentityKnown = func(*vcs.Git) bool { return false }
	defer func() { gitIdentityKnown = orig }()

	s := serviceWithDucklings(t, "pato-uno")
	dir := filepath.Join(t.TempDir(), "nested", "calc")
	_, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "calc", GitInit: true})
	if !errors.Is(err, ErrGitIdentityRequired) {
		t.Fatalf("err = %v, want ErrGitIdentityRequired", err)
	}
	// Review of #121: nothing at all, not even the empty folders.
	if _, statErr := os.Stat(filepath.Dir(dir)); !os.IsNotExist(statErr) {
		t.Fatal("a refused init left the folders it created")
	}

	if _, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "calc", GitInit: true, GitName: "Ada Lovelace", GitEmail: "ada@example.com"}); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", dir, "config", "--local", "user.email").Output()
	if err != nil || strings.TrimSpace(string(out)) != "ada@example.com" {
		t.Fatalf("repository identity = %q (%v)", out, err)
	}
	if sha, err := vcs.New(dir).HeadSHA(); err != nil || sha == "" {
		t.Fatalf("no root commit: %v", err)
	}
}

// B-456: one verb from an idea to a drafting run, with a default folder.
func TestProjectStartCreatesInTheDefaultFolderAndStartsTheIntake(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	res, err := s.ProjectStart(context.Background(), ProjectStartRequest{
		Name: "TI-36X Pro", Brief: "A pixel perfect replica of the TI-36X Pro as a local web page.",
		GitName: "Ada", GitEmail: "ada@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Project == nil || res.Project.Path != filepath.Join(home, "Ducklab", "ti-36x-pro") {
		t.Fatalf("project = %+v", res.Project)
	}
	if res.RunID == "" {
		t.Fatalf("no intake run started: %s", res.IntakeError)
	}
	s.runsMu.RLock()
	rs := s.runs[res.RunID]
	s.runsMu.RUnlock()
	if rs == nil || rs.run.Stage != "intake" {
		t.Fatalf("run = %+v", rs)
	}
	<-rs.done
}

func TestProjectStartRefusesAFolderWithSomeoneElsesFiles(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "thesis.tex"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := s.ProjectStart(context.Background(), ProjectStartRequest{Name: "calc", Path: dir})
	if err == nil || !strings.Contains(err.Error(), "already contains files") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".ducklab")); !os.IsNotExist(statErr) {
		t.Fatal("refusal created a project anyway")
	}
}

// B-459: a local web page is previewable from the first run, and every later
// prompt knows what is being delivered.
func TestProjectStartWithTheWebPagePresetSetsUpServingAndConventions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	res, err := s.ProjectStart(context.Background(), ProjectStartRequest{
		Name: "calc", Brief: "A calculator.", Preset: "web-page", GitName: "Ada", GitEmail: "ada@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	root := res.Project.Path
	cfg, err := config.LoadProject(filepath.Join(root, ".ducklab", "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.Run.Command, "python3 -m http.server ") || !strings.HasPrefix(cfg.Run.URL, "http://127.0.0.1:") ||
		cfg.Run.Health != cfg.Run.URL || cfg.Run.Smoke != "test -f index.html" || cfg.Run.SmokeExpect != "exit" {
		t.Fatalf("run = %+v", cfg.Run)
	}
	mem, err := artifact.LoadMemory(root)
	if err != nil || !strings.Contains(mem.Conventions, "self-contained `index.html`") || !strings.Contains(mem.Conventions, "node --test tests/") {
		t.Fatalf("memory conventions = %q (%v)", mem.Conventions, err)
	}
	if res.RunID == "" {
		t.Fatalf("no intake: %s", res.IntakeError)
	}
	s.runsMu.RLock()
	rs := s.runs[res.RunID]
	s.runsMu.RUnlock()
	<-rs.done
	brief, _ := os.ReadFile(filepath.Join(root, ".ducklab", "preset.md"))
	if !strings.Contains(string(brief), "These are settled") {
		t.Fatalf("preset reference = %q", brief)
	}
	found := false
	for _, f := range rs.refFiles() {
		found = found || strings.HasSuffix(f, filepath.Join(".ducklab", "preset.md"))
	}
	if !found {
		t.Fatalf("the intake did not receive the preset as a reference: %v", rs.refFiles())
	}
}

func TestProjectStartRefusesAnUnknownPresetBeforeCreatingAnything(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	s := serviceWithDucklings(t, "pato-uno")
	if _, err := s.ProjectStart(context.Background(), ProjectStartRequest{Name: "calc", Preset: "rocket"}); err == nil {
		t.Fatal("unknown preset accepted")
	}
	if _, err := os.Stat(filepath.Join(home, "Ducklab", "calc")); !os.IsNotExist(err) {
		t.Fatal("an unknown preset still created the project")
	}
}

// Jose, reviewing B-456: the default folder is the person's preference, with
// ~/Ducklab only as the starting point.
func TestTheProjectsFolderIsAPreference(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	s.configPath = filepath.Join(t.TempDir(), "config.toml")
	if v := s.ProjectDefaults(); v.ProjectsDir != "" || v.Effective != filepath.Join(home, "Ducklab") {
		t.Fatalf("starting point = %+v", v)
	}
	if err := s.ProjectDefaultsSet(ProjectDefaultsView{ProjectsDir: "relative/dir"}); err == nil {
		t.Fatal("a relative folder was accepted")
	}
	if err := s.ProjectDefaultsSet(ProjectDefaultsView{ProjectsDir: "~/code"}); err != nil {
		t.Fatal(err)
	}
	if v := s.ProjectDefaults(); v.Effective != filepath.Join(home, "code") {
		t.Fatalf("~ not expanded once at set time: %+v", v)
	}
	res, err := s.ProjectStart(context.Background(), ProjectStartRequest{Name: "calc", GitName: "Ada", GitEmail: "ada@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Project.Path != filepath.Join(home, "code", "calc") {
		t.Fatalf("project went to %s", res.Project.Path)
	}
	if res.RunID != "" {
		s.runsMu.RLock()
		rs := s.runs[res.RunID]
		s.runsMu.RUnlock()
		<-rs.done
	}
	if err := s.ProjectDefaultsSet(ProjectDefaultsView{}); err != nil {
		t.Fatal(err)
	}
	if v := s.ProjectDefaults(); v.ProjectsDir != "" || v.Effective != filepath.Join(home, "Ducklab") {
		t.Fatalf("empty did not restore the starting point: %+v", v)
	}
}

// Review of #121: "Create" must not reopen an existing project and start a
// second intake on it under a new name.
func TestProjectStartRefusesAnExistingDucklabProject(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	dir := t.TempDir()
	if _, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "original", GitInit: true, GitName: "Ada", GitEmail: "ada@example.com"}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.RunList(context.Background(), RunFilter{})
	_, err := s.ProjectStart(context.Background(), ProjectStartRequest{Name: "replacement", Path: dir, Brief: "something else"})
	if err == nil || !strings.Contains(err.Error(), "already a Ducklab project") {
		t.Fatalf("err = %v", err)
	}
	after, _ := s.RunList(context.Background(), RunFilter{})
	if len(after) != len(before) {
		t.Fatalf("a refused start launched %d run(s)", len(after)-len(before))
	}
}
