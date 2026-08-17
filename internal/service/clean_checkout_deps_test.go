package service

import (
	"os"
	"path/filepath"
	"testing"
)

// A clean checkout must be able to run the frontend half of the gate without
// re-installing the world: the live project's node_modules links in wherever
// the commit carries the matching package.json (B-048's lesson, accept-side).
func TestCleanCheckoutLinksInstalledDeps(t *testing.T) {
	root, checkout := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(root, "frontend", "node_modules", ".bin"), 0o755)
	os.MkdirAll(filepath.Join(checkout, "frontend"), 0o755)
	os.WriteFile(filepath.Join(checkout, "frontend", "package.json"), []byte("{}"), 0o644)

	linkInstalledDeps(root, checkout)

	link := filepath.Join(checkout, "frontend", "node_modules")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("frontend/node_modules is not linked into the checkout: %v", err)
	}
	if target != filepath.Join(root, "frontend", "node_modules") {
		t.Errorf("link points at %s", target)
	}
	// No package.json at the checkout root, so no root-level link.
	if _, err := os.Lstat(filepath.Join(checkout, "node_modules")); err == nil {
		t.Error("root node_modules linked without a package.json to justify it")
	}
}

// A Python project's gate says .venv/bin/pytest — a relative path into a
// gitignored virtualenv the clean checkout does not carry. Same custody rule
// as node_modules: installed dependencies are the tools of the trade, not
// engine state, and the checkout borrows them from the live tree.
func TestCleanCheckoutLinksVirtualenv(t *testing.T) {
	root, checkout := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(root, ".venv", "bin"), 0o755)
	os.WriteFile(filepath.Join(checkout, "requirements.txt"), []byte("pytest\n"), 0o644)

	linkInstalledDeps(root, checkout)

	target, err := os.Readlink(filepath.Join(checkout, ".venv"))
	if err != nil {
		t.Fatalf(".venv is not linked into the checkout: %v", err)
	}
	if target != filepath.Join(root, ".venv") {
		t.Errorf("link points at %s", target)
	}
}

// A project of loose .py files with only a pytest.ini at root is still a
// Python project — excercise-tracker's exact layout, which the original
// marker list missed and stranded a yolo accept.
func TestCleanCheckoutLinksVenvForPytestIniOnlyProjects(t *testing.T) {
	root, checkout := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(root, ".venv", "bin"), 0o755)
	os.WriteFile(filepath.Join(checkout, "pytest.ini"), []byte("[pytest]\n"), 0o644)

	linkInstalledDeps(root, checkout)

	if _, err := os.Readlink(filepath.Join(checkout, ".venv")); err != nil {
		t.Fatalf(".venv is not linked for a pytest.ini-marked checkout: %v", err)
	}
}

// A checkout with no Python markers gets no .venv link even when the live
// tree has one.
func TestCleanCheckoutSkipsVenvWithoutPythonMarkers(t *testing.T) {
	root, checkout := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(root, ".venv", "bin"), 0o755)

	linkInstalledDeps(root, checkout)

	if _, err := os.Lstat(filepath.Join(checkout, ".venv")); err == nil {
		t.Error(".venv linked without a Python marker to justify it")
	}
}
