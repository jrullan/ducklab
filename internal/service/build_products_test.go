package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

// B-364: Fledge's first landing would have committed cargo's target/ — 470
// files, 133 MB — because the task produced no .gitignore and the staging
// boundary knew node_modules and .venv but nothing of Rust. Build products
// never land, the accept says what it left out, and the reviewer's diff does
// not carry them either.
func TestAcceptWorktreeNeverLandsCargoTarget(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	git := gitProject(t, dir)
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-cargo-target")
	for name, body := range map[string]string{
		"Cargo.toml":               "[package]\nname = \"x\"\nversion = \"0.1.0\"\n",
		"src/lib.rs":               "pub fn x() {}\n",
		"target/debug/x.d":         "x\n",
		"target/debug/deps/x.rlib": "binary\n",
		"target/.rustc_info.json":  "{}\n",
		"target/CACHEDIR.TAG":      "Signature\n",
	} {
		path := filepath.Join(run.WorktreePath, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The ordinary intent-to-add that makes run files visible to the diff has
	// already happened by the time a person accepts; stageRun must remove the
	// build output from the index, not merely omit it from its own add.
	if err := vcs.New(run.WorktreePath).AddAll(); err != nil {
		t.Fatal(err)
	}

	result, err := s.RunAccept(context.Background(), run.ID, "")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	diff, err := git.ShowCommit(result.CommitSHA)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(diff, "target/") {
		t.Fatalf("landed commit contains cargo build output:\n%s", diff)
	}
	for _, want := range []string{"Cargo.toml", "src/lib.rs"} {
		if !strings.Contains(diff, want) {
			t.Fatalf("landed commit lost %s:\n%s", want, diff)
		}
	}
	events, err := runlog.ReadEvents(filepath.Join(dir, ".ducklab", "runs", run.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Type == "landing_excluded" {
			paths, _ := e.Data["paths"].([]interface{})
			for _, p := range paths {
				if p == "target" {
					return
				}
			}
			t.Fatalf("landing_excluded does not name target: %v", e.Data)
		}
	}
	t.Fatal("the accept did not record what it left out")
}

func TestReviewDiffExcludesBuildProducts(t *testing.T) {
	dir := t.TempDir()
	git := gitProject(t, dir)
	for name, body := range map[string]string{
		"Cargo.toml":        "[package]\nname = \"x\"\n",
		"src/lib.rs":        "pub fn x() {}\n",
		"target/debug/x.d":  "x\n",
		".pytest_cache/v/x": "cache\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := &runlog.Run{ID: "r-diff"}
	diff, err := git.DiffExcluding(runDiffExclusions(run, dir, dir)...)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(diff, "target/") {
		t.Fatalf("review diff carries cargo build output:\n%s", diff)
	}
	if !strings.Contains(diff, "src/lib.rs") || !strings.Contains(diff, "Cargo.toml") {
		t.Fatalf("review diff lost the run's work:\n%s", diff)
	}
	// No Python marker: .pytest_cache is ordinary content here, and the diff
	// says so; the marker, not the name, decides.
	if !strings.Contains(diff, ".pytest_cache") {
		t.Fatalf("a cache directory without its ecosystem marker was excluded:\n%s", diff)
	}
}

// Markers decide the defaults; the project adds its own under
// [verify] build_products. Read from the tree being landed, where a worktree
// run creates its Cargo.toml, with the configuration from the project root.
func TestBuildProductPathsFollowMarkersAndProjectConfig(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	_, projectRoot := projectWithDocs(t, s, nil)
	appendVerifyPreparation(t, projectRoot, `build_products = ["out", "generated/cache"]`)
	tree := t.TempDir()
	if got := buildProductPaths(tree, projectRoot); strings.Join(got, ",") != "out,generated/cache" {
		t.Fatalf("no markers: %v, want only the configured products", got)
	}
	if err := os.WriteFile(filepath.Join(tree, "Cargo.toml"), []byte("[package]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "pyproject.toml"), []byte("[project]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := buildProductPaths(tree, projectRoot); strings.Join(got, ",") != "target,.pytest_cache,out,generated/cache" {
		t.Fatalf("markers + config: %v", got)
	}
	// Present only names what exists in the tree, so the accept event never
	// claims to have excluded something that was not there.
	if err := os.MkdirAll(filepath.Join(tree, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := presentExclusions(tree, landingExclusions(tree, projectRoot)); strings.Join(got, ",") != "target" {
		t.Fatalf("present exclusions = %v, want target only", got)
	}
}
