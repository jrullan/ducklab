package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Starting a project from an idea (B-456).
//
// The greenfield path used to be two disconnected acts: Settings → Projects →
// Project management asked for an absolute folder and git before anything
// about the product, and the brief lived three screens away under Documents →
// Intent → Add intention. ProjectStart is the one verb behind the guided flow:
// what to build, a name, an optional location, references, then the project
// exists and its first drafting run is already started. The desktop and an MCP
// operator call the same thing, so neither has to learn the order of six
// endpoints.

// ProjectStartRequest is everything the first screen asks for.
type ProjectStartRequest struct {
	// Name is required: it names the project and, without Path, its folder.
	Name string `json:"name"`
	// Path is optional; empty means <defaults.projects_dir>/<name-slug>
	// (~/Ducklab unless the person chose another folder).
	Path string `json:"path,omitempty"`
	// Brief is what to build. Empty starts the intake as an interview.
	Brief string `json:"brief"`
	// Refs are reference documents or images (B-457).
	Refs []string `json:"refs,omitempty"`
	// GitName and GitEmail answer git_identity_required (B-463).
	GitName  string `json:"git_name,omitempty"`
	GitEmail string `json:"git_email,omitempty"`
}

// ProjectStartResult is the project and its first run.
type ProjectStartResult struct {
	Project *Project `json:"project"`
	// RunID is the intake run drafting the requirements; empty when it could
	// not start, with IntakeError saying why (the project exists either way:
	// a missing model must not undo the folder the person just named).
	RunID       string `json:"run_id,omitempty"`
	IntakeError string `json:"intake_error,omitempty"`
}

// DefaultProjectsDir is the built-in starting point: ~/Ducklab.
func DefaultProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Ducklab"), nil
}

// projectsDir is the person's preference (defaults.projects_dir), else the
// built-in starting point.
func (s *Service) projectsDir() (string, error) {
	s.cfgMu.RLock()
	dir := strings.TrimSpace(s.cfg.Defaults.ProjectsDir)
	s.cfgMu.RUnlock()
	if dir != "" {
		return dir, nil
	}
	return DefaultProjectsDir()
}

// ProjectDefaultsView is the preference behind the start flow's folder.
type ProjectDefaultsView struct {
	// ProjectsDir is the stored preference; empty means the built-in default.
	ProjectsDir string `json:"projects_dir"`
	// Effective is the folder new projects actually go under.
	Effective string `json:"effective"`
}

func (s *Service) ProjectDefaults() ProjectDefaultsView {
	s.cfgMu.RLock()
	stored := s.cfg.Defaults.ProjectsDir
	s.cfgMu.RUnlock()
	effective, _ := s.projectsDir()
	return ProjectDefaultsView{ProjectsDir: stored, Effective: effective}
}

// ProjectDefaultsSet stores the folder; empty restores the built-in default.
// A leading ~/ is expanded here, once: the engine never interprets ~ later.
func (s *Service) ProjectDefaultsSet(v ProjectDefaultsView) error {
	if err := s.canWriteConfig(); err != nil {
		return err
	}
	dir := strings.TrimSpace(v.ProjectsDir)
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(dir, "~"), "/"))
	}
	if dir != "" && !filepath.IsAbs(dir) {
		return fmt.Errorf("projects_dir must be an absolute folder (or start with ~/); got %q", v.ProjectsDir)
	}
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	previous := s.cfg.Defaults.ProjectsDir
	s.cfg.Defaults.ProjectsDir = filepath.Clean(dir)
	if dir == "" {
		s.cfg.Defaults.ProjectsDir = ""
	}
	if err := s.saveConfig(); err != nil {
		s.cfg.Defaults.ProjectsDir = previous
		return err
	}
	return nil
}

// ProjectStart creates the project (git included) and starts its intake.
func (s *Service) ProjectStart(ctx context.Context, req ProjectStartRequest) (*ProjectStartResult, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("a project name is required")
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		base, err := s.projectsDir()
		if err != nil {
			return nil, err
		}
		slug := slugify(name)
		if slug == "" {
			return nil, fmt.Errorf("the name %q leaves no usable folder name; choose a folder", name)
		}
		path = filepath.Join(base, slug)
	}
	// A greenfield start must not adopt somebody's existing work by accident:
	// an existing non-empty folder that is not already a Ducklab project is
	// refused with the path, so the person chooses deliberately.
	// "Create" never mutates a previous project (review of #121: an existing
	// Ducklab project at the path was opened and given a second intake under
	// the new name).
	if _, err := os.Stat(filepath.Join(path, ".ducklab", "project.toml")); err == nil {
		return nil, fmt.Errorf("%s is already a Ducklab project; open it from Settings → Projects instead of creating it again", path)
	}
	if entries, err := os.ReadDir(path); err == nil && len(entries) > 0 {
		return nil, fmt.Errorf("%s already contains files; choose an empty or new folder, or open it as an existing project from Settings → Projects", path)
	}
	project, err := s.ProjectInit(ctx, InitRequest{
		Path: path, Name: name, GitInit: true, GitName: req.GitName, GitEmail: req.GitEmail,
	})
	if err != nil {
		return nil, err
	}
	out := &ProjectStartResult{Project: project}
	run, err := s.StageStart(ctx, project.ID, StageRequest{
		Stage: "intake", From: strings.TrimSpace(req.Brief), Refs: req.Refs,
	})
	if err != nil {
		out.IntakeError = err.Error()
		return out, nil
	}
	out.RunID = run.ID
	return out, nil
}

// mkdirAllTracked creates path and returns the directories it created, deepest
// last, so a refusal can remove exactly those.
func mkdirAllTracked(path string) ([]string, error) {
	var missing []string
	for dir := filepath.Clean(path); ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		missing = append([]string{dir}, missing...)
		if parent := filepath.Dir(dir); parent == dir {
			break
		}
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, err
	}
	return missing, nil
}

// removeCreatedDirs removes directories made by mkdirAllTracked, deepest
// first; os.Remove refuses a non-empty directory, so nothing else is lost.
func removeCreatedDirs(dirs []string) {
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i])
	}
}
