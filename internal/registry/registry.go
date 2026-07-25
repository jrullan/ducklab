// Package registry manages the global project registry.
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/xplat"
)

// ProjectEntry is a registered project.
type ProjectEntry struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	Name       string `json:"name"`
	LastOpened string `json:"last_opened"`
	Missing    bool   `json:"missing,omitempty"`
}

// Registry manages the global project registry.
type Registry struct {
	path     string
	Projects []ProjectEntry `json:"projects"`
}

// Load loads the registry from disk.
func Load() (*Registry, error) {
	dataDir, err := xplat.DataDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "projects.json")
	r := &Registry{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, r); err != nil {
		return nil, err
	}
	return r, nil
}

// Save saves the registry to disk.
func (r *Registry) Save() error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return xplat.AtomicWrite(r.path, data, 0o644)
}

// Register registers a project. Returns the project ID (with deduplication).
func (r *Registry) Register(path, name string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	id := slugify(filepath.Base(absPath))
	// Check for existing
	for _, p := range r.Projects {
		if p.Path == absPath {
			// Update last opened
			for i := range r.Projects {
				if r.Projects[i].Path == absPath {
					r.Projects[i].LastOpened = time.Now().UTC().Format(time.RFC3339)
					r.Projects[i].Missing = false
				}
			}
			r.Save()
			return p.ID, nil
		}
	}
	// Deduplicate ID
	baseID := id
	counter := 2
	for r.idExists(id) {
		id = fmt.Sprintf("%s-%d", baseID, counter)
		counter++
	}
	r.Projects = append(r.Projects, ProjectEntry{
		ID:         id,
		Path:       absPath,
		Name:       name,
		LastOpened: time.Now().UTC().Format(time.RFC3339),
	})
	if err := r.Save(); err != nil {
		return "", err
	}
	return id, nil
}

// Get returns a project by ID.
func (r *Registry) Get(id string) (*ProjectEntry, error) {
	for i, p := range r.Projects {
		if p.ID == id {
			return &r.Projects[i], nil
		}
	}
	return nil, fmt.Errorf("project %q not found", id)
}

// GetByPath returns a project by path.
func (r *Registry) GetByPath(path string) (*ProjectEntry, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	for i, p := range r.Projects {
		if p.Path == absPath {
			return &r.Projects[i], nil
		}
	}
	return nil, fmt.Errorf("project at %q not found", path)
}

// List returns all projects, most recent first.
func (r *Registry) List() []ProjectEntry {
	// Sort by last opened descending
	sorted := make([]ProjectEntry, len(r.Projects))
	copy(sorted, r.Projects)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].LastOpened > sorted[i].LastOpened {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return sorted
}

// Unregister removes a project from the registry. Never deletes files.
func (r *Registry) Unregister(id string) error {
	for i, p := range r.Projects {
		if p.ID == id {
			r.Projects = append(r.Projects[:i], r.Projects[i+1:]...)
			return r.Save()
		}
	}
	return fmt.Errorf("project %q not found", id)
}

// MarkMissing marks projects whose paths no longer exist.
func (r *Registry) MarkMissing() {
	for i := range r.Projects {
		ducklabDir := filepath.Join(r.Projects[i].Path, ".ducklab")
		if _, err := os.Stat(ducklabDir); os.IsNotExist(err) {
			r.Projects[i].Missing = true
		} else {
			r.Projects[i].Missing = false
		}
	}
	r.Save()
}

func (r *Registry) idExists(id string) bool {
	for _, p := range r.Projects {
		if p.ID == id {
			return true
		}
	}
	return false
}

var nonAlphanum = regexp.MustCompile(`[^a-z0-9-]+`)
var multiDash = regexp.MustCompile(`-+`)

func slugify(s string) string {
	s = strings.ToLower(s)
	s = nonAlphanum.ReplaceAllString(s, "-")
	s = multiDash.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "project"
	}
	if len(s) > 32 {
		s = s[:32]
	}
	return s
}
