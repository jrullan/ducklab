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
	// Path is optional; empty means ~/Ducklab/<name-slug>.
	Path string `json:"path,omitempty"`
	// Brief is what to build. Empty starts the intake as an interview.
	Brief string `json:"brief"`
	// Refs are reference documents or images (B-457).
	Refs []string `json:"refs,omitempty"`
	// Preset sets up a kind of project (B-459): "web-page", "web-app", or
	// empty for none.
	Preset string `json:"preset,omitempty"`
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

// DefaultProjectsDir is where a project goes when the person names no folder.
func DefaultProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Ducklab"), nil
}

// ProjectStart creates the project (git included) and starts its intake.
func (s *Service) ProjectStart(ctx context.Context, req ProjectStartRequest) (*ProjectStartResult, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("a project name is required")
	}
	var preset ProjectPreset
	if req.Preset != "" {
		p, ok := presetByID(req.Preset)
		if !ok {
			return nil, fmt.Errorf("unknown preset %q", req.Preset)
		}
		preset = p
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		base, err := DefaultProjectsDir()
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
	if entries, err := os.ReadDir(path); err == nil && len(entries) > 0 {
		if _, statErr := os.Stat(filepath.Join(path, ".ducklab", "project.toml")); statErr != nil {
			return nil, fmt.Errorf("%s already contains files; choose an empty or new folder, or open it as an existing project from Settings → Projects", path)
		}
	}
	project, err := s.ProjectInit(ctx, InitRequest{
		Path: path, Name: name, GitInit: true, GitName: req.GitName, GitEmail: req.GitEmail,
	})
	if err != nil {
		return nil, err
	}
	out := &ProjectStartResult{Project: project}
	refs := append([]string(nil), req.Refs...)
	if preset.ID != "" {
		ref, err := applyPreset(project.Path, project.ID, req.Brief, preset)
		if err != nil {
			return nil, fmt.Errorf("preset %s: %w", preset.ID, err)
		}
		refs = append(refs, ref)
	}
	run, err := s.StageStart(ctx, project.ID, StageRequest{
		Stage: "intake", From: strings.TrimSpace(req.Brief), Refs: refs,
	})
	if err != nil {
		out.IntakeError = err.Error()
		return out, nil
	}
	out.RunID = run.ID
	return out, nil
}
