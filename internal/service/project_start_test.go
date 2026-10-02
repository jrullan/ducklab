package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
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

// B-467: a fresh machine may still default git init to master, while Ducklab's
// project contract defaults to main. The repository is born on the contract's
// branch, independent of the machine preference.
func TestProjectInitUsesTheConfiguredBaseAsInitialBranch(t *testing.T) {
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[init]\n\tdefaultBranch = master\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	s := serviceWithDucklings(t, "pato-uno")
	dir := filepath.Join(t.TempDir(), "calc")
	if _, err := s.ProjectInit(context.Background(), InitRequest{
		Path: dir, Name: "calc", GitInit: true, GitName: "Ada", GitEmail: "ada@example.com",
	}); err != nil {
		t.Fatal(err)
	}
	branch, err := vcs.New(dir).CurrentBranch()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadProject(filepath.Join(dir, ".ducklab", "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if branch != cfg.Git.BaseBranch || branch != "main" {
		t.Fatalf("branch = %q, configured base = %q", branch, cfg.Git.BaseBranch)
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
	orig := presetPython
	presetPython = func() (string, error) { return "python3", nil }
	defer func() { presetPython = orig }()
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
		cfg.Run.Health != cfg.Run.URL || !strings.Contains(cfg.Run.Smoke, "os.path.isfile('index.html')") || cfg.Run.SmokeExpect != "exit" {
		t.Fatalf("run = %+v", cfg.Run)
	}
	// Review of #122: the setup is versioned at once, so a clone keeps it.
	tracked := map[string]bool{}
	for _, f := range vcs.New(root).LsFiles() {
		tracked[filepath.ToSlash(f)] = true
	}
	for _, want := range []string{".ducklab/project.toml", ".ducklab/preset.md", ".gitignore"} {
		if !tracked[want] {
			t.Errorf("%s is not versioned after start; tracked: %v", want, tracked)
		}
	}
	memRel, _ := filepath.Rel(root, artifact.Path(root, artifact.KindProject))
	if !tracked[filepath.ToSlash(memRel)] {
		t.Errorf("project memory %s is not versioned", memRel)
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

// Review of #122: a preset that cannot run on this machine is refused at the
// choice, with the fix, before anything is created.
func TestAPresetWithoutPythonIsRefusedBeforeCreating(t *testing.T) {
	orig := presetPython
	presetPython = func() (string, error) {
		return "", fmt.Errorf("no python3 or python was found on PATH; install Python 3 or choose \"Something else\"")
	}
	defer func() { presetPython = orig }()
	home := t.TempDir()
	t.Setenv("HOME", home)
	s := serviceWithDucklings(t, "pato-uno")
	_, err := s.ProjectStart(context.Background(), ProjectStartRequest{Name: "calc", Preset: "web-page", GitName: "Ada", GitEmail: "ada@example.com"})
	if err == nil || !strings.Contains(err.Error(), "install Python 3") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "Ducklab", "calc")); !os.IsNotExist(statErr) {
		t.Fatal("a refused preset still created the project")
	}
}

// When only `python` exists (the usual Windows install), the preset uses it.
func TestThePresetUsesWhicheverPythonExists(t *testing.T) {
	orig := presetPython
	presetPython = func() (string, error) { return "python", nil }
	defer func() { presetPython = orig }()
	home := t.TempDir()
	t.Setenv("HOME", home)
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	res, err := s.ProjectStart(context.Background(), ProjectStartRequest{Name: "calc", Preset: "web-app", GitName: "Ada", GitEmail: "ada@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.LoadProject(filepath.Join(res.Project.Path, ".ducklab", "project.toml"))
	if !strings.HasPrefix(cfg.Run.Command, "python -m http.server ") || !strings.HasPrefix(cfg.Run.Smoke, "python -c ") {
		t.Fatalf("run = %+v", cfg.Run)
	}
	if res.RunID != "" {
		s.runsMu.RLock()
		rs := s.runs[res.RunID]
		s.runsMu.RUnlock()
		<-rs.done
	}
}

// Review of #121: a started project is a Ducklab project in git from the first
// moment (project.toml and housekeeping committed at creation), and accepting
// the requirements lands the person's own brief (intent.md) with them, so a
// clone carries the whole project and its origin.
func TestAStartedProjectIsVersionedAndTheAcceptedIntakeLandsTheBrief(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	fake := s.providers["fake"].(*provider.Fake)
	fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
		content := "## REQ-001 — Adds numbers\n\n**Priority:** must\n\nIt adds two numbers.\n"
		for _, m := range req.Messages {
			if m.Role == "system" && strings.Contains(m.Content, "You are the reviewer") {
				content = `{"verdict":"approve","findings":[]}`
			}
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{
			Message:      provider.Message{Role: "assistant", Content: content},
			FinishReason: provider.FinishStop,
		}}}
	}
	res, err := s.ProjectStart(context.Background(), ProjectStartRequest{
		Name: "calc", Brief: "A calculator that adds.", GitName: "Ada", GitEmail: "ada@example.com",
	})
	if err != nil || res.RunID == "" {
		t.Fatalf("start: %v %s", err, res.IntakeError)
	}
	git := vcs.New(res.Project.Path)
	tracked := func() map[string]bool {
		m := map[string]bool{}
		for _, f := range git.LsFiles() {
			m[filepath.ToSlash(f)] = true
		}
		return m
	}
	before := tracked()
	for _, f := range []string{".ducklab/project.toml", ".gitignore"} {
		if !before[f] {
			t.Errorf("%s not committed at creation; tracked: %v", f, before)
		}
	}
	s.runsMu.RLock()
	rs := s.runs[res.RunID]
	s.runsMu.RUnlock()
	<-rs.done
	if rs.run.Status == "failed" {
		t.Fatal("the intake run failed; the fixture no longer speaks the stage contracts")
	}
	if _, err := s.RunAccept(context.Background(), res.RunID, ""); err != nil {
		t.Fatal(err)
	}
	after := tracked()
	intent, _ := filepath.Rel(res.Project.Path, artifact.Path(res.Project.Path, artifact.KindIntent))
	req, _ := filepath.Rel(res.Project.Path, artifact.Path(res.Project.Path, artifact.KindRequirements))
	for _, f := range []string{filepath.ToSlash(intent), filepath.ToSlash(req)} {
		if !after[f] {
			t.Errorf("%s not committed by the accept; tracked: %v", f, after)
		}
	}
}

// Second review of #122: two projects created before either runs must not be
// given the same port; the choice skips ports registered projects claim.
func TestPresetPortsSkipPortsOtherProjectsClaim(t *testing.T) {
	first := presetPort("calc", nil)
	if got := presetPort("calc", map[int]bool{first: true}); got == first {
		t.Fatalf("presetPort returned the claimed port %d", got)
	}

	orig := presetPython
	presetPython = func() (string, error) { return "python3", nil }
	defer func() { presetPython = orig }()
	t.Setenv("HOME", t.TempDir())
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	ports := map[int]string{}
	for _, name := range []string{"calc", "notes", "clock"} {
		res, err := s.ProjectStart(context.Background(), ProjectStartRequest{
			Name: name, Preset: "web-page", GitName: "Ada", GitEmail: "ada@example.com",
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.RunID != "" {
			s.runsMu.RLock()
			rs := s.runs[res.RunID]
			s.runsMu.RUnlock()
			<-rs.done
		}
		claimed := s.claimedRunPorts(res.Project.ID)
		for p, other := range ports {
			if !claimed[p] {
				t.Errorf("%s's port %d is not seen as claimed", other, p)
			}
		}
		mine := s.claimedRunPorts("")
		for p := range mine {
			if !claimed[p] {
				if prev, dup := ports[p]; dup {
					t.Fatalf("%s and %s share port %d", prev, name, p)
				}
				ports[p] = name
			}
		}
	}
	if len(ports) != 3 {
		t.Fatalf("ports = %v, want three distinct", ports)
	}
}

// Second review of #122: an executable called python is not enough; it must
// be a Python 3 that has http.server.
func TestPresetPythonRefusesAPythonThatIsNotPython3(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stubs")
	}
	bin := t.TempDir()
	for _, name := range []string{"python3", "python"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\necho 'Python 2.7.18' >&2\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	_, err := presetPython()
	if err == nil || !strings.Contains(err.Error(), "not a Python 3") {
		t.Fatalf("err = %v", err)
	}
}
