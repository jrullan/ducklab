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
