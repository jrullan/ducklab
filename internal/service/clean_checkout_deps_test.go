package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

	linkInstalledDeps(root, checkout, nil)

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

	linkInstalledDeps(root, checkout, nil)

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

	linkInstalledDeps(root, checkout, nil)

	if _, err := os.Readlink(filepath.Join(checkout, ".venv")); err != nil {
		t.Fatalf(".venv is not linked for a pytest.ini-marked checkout: %v", err)
	}
}

// A checkout with no Python markers gets no .venv link even when the live
// tree has one.
func TestCleanCheckoutSkipsVenvWithoutPythonMarkers(t *testing.T) {
	root, checkout := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(root, ".venv", "bin"), 0o755)

	linkInstalledDeps(root, checkout, nil)

	if _, err := os.Lstat(filepath.Join(checkout, ".venv")); err == nil {
		t.Error(".venv linked without a Python marker to justify it")
	}
}

// Checkout preparation belongs to the project, not to an ever-growing list of
// ecosystems in the engine. A declared dependency is linked even when it has no
// marker the engine happens to recognize, and the gate runs against that link.
func TestAcceptedCheckoutLinksDeclaredDependency(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	_, root := projectWithDocs(t, s, nil)
	appendVerifyPreparation(t, root, `link_deps = ["tools/pytest"]
mode = "custom"
custom = "test -f tools/pytest/bin/runner"`)
	g := gitProject(t, root)
	sha := mustHead(t, g)
	// This installed tool is deliberately untracked: only link_deps can make it
	// available in the detached checkout.
	if err := os.MkdirAll(filepath.Join(root, "tools", "pytest", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tools", "pytest", "bin", "runner"), []byte("tool\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := verifyAcceptedCommit(context.Background(), g, root, sha, "build"); err != nil {
		t.Fatalf("declared dependency was not available to the clean-checkout gate: %v", err)
	}
}

// Setup creates build products inside the detached checkout. It must precede
// the gate and must not borrow build/ from the live tree.
func TestAcceptedCheckoutRunsDeclaredSetupBeforeGate(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	_, root := projectWithDocs(t, s, nil)
	appendVerifyPreparation(t, root, `setup = "mkdir -p build && touch build/generated"
mode = "custom"
custom = "test -f build/generated"`)
	g := gitProject(t, root)
	sha := mustHead(t, g)

	if _, err := verifyAcceptedCommit(context.Background(), g, root, sha, "build"); err != nil {
		t.Fatalf("declared setup did not prepare the clean checkout before its gate: %v", err)
	}
}

// A path absent from the commit is actionable configuration feedback, not a
// shell-specific "not found" error. build/ intentionally is neither linked
// nor set up here.
func TestAcceptedCheckoutNamesCMakeBuildDirectory(t *testing.T) {
	checkout := t.TempDir()
	if got := missingGatePath(checkout, "cmake --build build && ctest"); got != "build" {
		t.Fatalf("missing gate path = %q, want build", got)
	}
	if err := os.Mkdir(filepath.Join(checkout, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := missingGatePath(checkout, "cmake --build build && ctest"); got != "" {
		t.Fatalf("existing build path reported as missing: %q", got)
	}
}

func TestAcceptedCheckoutNamesUndeclaredMissingGatePath(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	_, root := projectWithDocs(t, s, nil)
	appendVerifyPreparation(t, root, `mode = "custom"
custom = "test -f build/generated"`)
	g := gitProject(t, root)
	sha := mustHead(t, g)

	_, err := verifyAcceptedCommit(context.Background(), g, root, sha, "build")
	if err == nil {
		t.Fatal("gate unexpectedly passed without its uncommitted build/ path")
	}
	want := "the gate references build/, which the commit does not include — declare it in setup or link_deps"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("gate error = %q, want actionable missing-path guidance %q", err, want)
	}
}

func appendVerifyPreparation(t *testing.T, root, preparation string) {
	t.Helper()
	path := filepath.Join(root, ".ducklab", "project.toml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const verifyHeader = "[verify]\n"
	at := strings.Index(string(body), verifyHeader)
	if at < 0 {
		t.Fatal("project fixture lacks [verify]")
	}
	start := at + len(verifyHeader)
	end := strings.Index(string(body[start:]), "\n[")
	if end < 0 {
		end = len(body)
	} else {
		end += start + 1
	}

	// The fixture already supplies mode and custom. Replace those values rather
	// than emitting duplicate TOML keys, so each test reaches checkout behavior.
	replacements := map[string]bool{}
	for _, line := range strings.Split(preparation, "\n") {
		if key, _, ok := strings.Cut(line, "="); ok {
			replacements[strings.TrimSpace(key)] = true
		}
	}
	var kept []string
	for _, line := range strings.Split(string(body[start:end]), "\n") {
		key, _, hasKey := strings.Cut(line, "=")
		if !hasKey || !replacements[strings.TrimSpace(key)] {
			kept = append(kept, line)
		}
	}
	updated := string(body[:start]) + preparation + "\n" + strings.Join(kept, "\n") + string(body[end:])
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
}
