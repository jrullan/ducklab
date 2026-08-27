package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/release"
)

// A release tag is the public version boundary, so every project-owned package
// manifest recorded with that tag must report the same version. In particular,
// npm's lockfile has both top-level and root-package metadata that must move
// without rewriting dependency constraints.
func TestReleaseCutSynchronizesFrontendManifestVersionsInTaggedCommit(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	projectID, dir := projectWithDocs(t, s, nil)

	frontend := filepath.Join(dir, "frontend")
	if err := os.MkdirAll(frontend, 0o755); err != nil {
		t.Fatal(err)
	}
	packageJSON := []byte(`{
  "name": "example-frontend",
  "version": "0.9.0",
  "dependencies": {"react": "^18.3.1"},
  "devDependencies": {"vite": "^5.4.11"}
}
`)
	lockJSON := []byte(`{
  "name": "example-frontend",
  "version": "0.9.0",
  "lockfileVersion": 3,
  "packages": {
    "": {
      "name": "example-frontend",
      "version": "0.9.0",
      "dependencies": {"react": "^18.3.1"},
      "devDependencies": {"vite": "^5.4.11"}
    }
  }
}
`)
	if err := os.WriteFile(filepath.Join(frontend, "package.json"), packageJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(frontend, "package-lock.json"), lockJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	git := gitProject(t, dir)

	version := release.Version{Major: 0, Minor: 9, Patch: 1}
	draft := release.Path(dir, version) + ".proposed"
	if err := os.MkdirAll(filepath.Dir(draft), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draft, []byte("# Release v0.9.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cut, err := s.ReleaseCut(context.Background(), projectID, version.String())
	if err != nil {
		t.Fatalf("release cut: %v", err)
	}
	commit, ok := cut["commit"].(string)
	if !ok || commit == "" {
		t.Fatalf("release cut commit = %#v, want commit SHA", cut["commit"])
	}
	if head, err := git.HeadSHA(); err != nil || head != commit {
		t.Fatalf("release cut commit = %q, HEAD = %q, err = %v", commit, head, err)
	}
	if !git.HasTag(version.String()) {
		t.Fatalf("release tag %s was not created", version)
	}

	var manifest struct {
		Version         string            `json:"version"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	readJSON(t, filepath.Join(frontend, "package.json"), &manifest)
	if manifest.Version != "0.9.1" {
		t.Errorf("frontend/package.json version = %q, want 0.9.1", manifest.Version)
	}
	if !reflect.DeepEqual(manifest.Dependencies, map[string]string{"react": "^18.3.1"}) || !reflect.DeepEqual(manifest.DevDependencies, map[string]string{"vite": "^5.4.11"}) {
		t.Errorf("package.json dependency versions changed: dependencies=%v devDependencies=%v", manifest.Dependencies, manifest.DevDependencies)
	}

	var lock struct {
		Version  string `json:"version"`
		Packages map[string]struct {
			Version         string            `json:"version"`
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		} `json:"packages"`
	}
	readJSON(t, filepath.Join(frontend, "package-lock.json"), &lock)
	if lock.Version != "0.9.1" || lock.Packages[""].Version != "0.9.1" {
		t.Errorf("frontend/package-lock.json versions = top-level %q, root package %q; want 0.9.1", lock.Version, lock.Packages[""].Version)
	}
	if root := lock.Packages[""]; !reflect.DeepEqual(root.Dependencies, map[string]string{"react": "^18.3.1"}) || !reflect.DeepEqual(root.DevDependencies, map[string]string{"vite": "^5.4.11"}) {
		t.Errorf("package-lock dependency versions changed: dependencies=%v devDependencies=%v", root.Dependencies, root.DevDependencies)
	}

	diff, err := git.ShowCommit(commit)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"frontend/package.json", "frontend/package-lock.json"} {
		if !strings.Contains(diff, path) {
			t.Errorf("release commit does not contain %s:\n%s", path, diff)
		}
	}
}

func readJSON(t *testing.T, path string, value any) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, value); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}
