// Package internal holds architecture tests that guard the dependency rules
// of 01-ARCHITECTURE.md §4.1. They are structural, not behavioural: they fail
// when a package reaches somewhere it must not, which is exactly the kind of
// drift that is invisible in review and expensive to undo later.
package internal

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/jrullan/ducklab"

// deps returns the transitive ducklab-internal dependencies of a package.
//
// It reads the source files itself rather than asking `go list`. A test that
// shells out is opaque to Go's test cache — the cache keys on the files the
// test process opens, and a subprocess opens nothing it can see — so after
// internal/mcp gained an import of internal/service the cached "ok" stood
// while the clean checkout said FAIL (B-070). Reading the files here makes
// every import edge an input the cache tracks: change one, and the test
// runs again.
func deps(t *testing.T, pkg string) map[string]bool {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	var walk func(rel string)
	walk = func(rel string) {
		if got[rel] {
			return
		}
		got[rel] = true
		for _, imp := range importsOf(t, filepath.Join(root, filepath.FromSlash(rel))) {
			if strings.HasPrefix(imp, modulePath+"/") {
				walk(strings.TrimPrefix(imp, modulePath+"/"))
			}
		}
	}
	walk(strings.TrimPrefix(pkg, modulePath+"/"))
	return got
}

// importsOf lists the imports of the non-test Go files in dir. Build tags are
// not honoured: a dependency behind a tag is still a dependency the rule
// must see.
func importsOf(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read package dir %s: %v", dir, err)
	}
	seen := map[string]bool{}
	var out []string
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, spec := range f.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil || seen[path] {
				continue
			}
			seen[path] = true
			out = append(out, path)
		}
	}
	return out
}

// directImports lists a package's own imports (not transitive).
func directImports(t *testing.T, pkg string) []string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return importsOf(t, filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(pkg, modulePath+"/"))))
}

// AC-16 / 01 §4.1: the CLI is a client. If it can reach a domain package it
// will eventually grow a shortcut that the desktop app does not have, and the
// two surfaces drift out of parity.
func TestCLIImportsOnlyClientPackages(t *testing.T) {
	allowed := map[string]bool{
		"internal/cli":       true,
		"internal/engineclt": true,
		"internal/daemon":    true,
		"internal/xplat":     true,
		// The MCP operator surface is the third client: it speaks stdio to a
		// model and engineclt to the engine, and touches the domain no more
		// than the desktop does.
		"internal/mcp": true,
		// Version constants are a leaf: reporting who you are is not
		// server machinery. B-033 made it the single source of truth,
		// and every binary — CLI included — must be able to say it.
		"internal/build": true,
	}
	for pkg := range deps(t, modulePath+"/internal/cli") {
		if !allowed[pkg] {
			t.Errorf("internal/cli depends on %s; the CLI may import only engineclt, daemon and xplat", pkg)
		}
	}
}

// 01 §4.1: tools is a leaf. Importing an orchestration package from a tool
// would let a tool re-enter the agent loop.
func TestToolsDoesNotImportOrchestration(t *testing.T) {
	forbidden := []string{
		"internal/agent", "internal/conv", "internal/strategy",
		"internal/stage", "internal/service",
	}
	got := deps(t, modulePath+"/internal/tools")
	for _, f := range forbidden {
		if got[f] {
			t.Errorf("internal/tools depends on %s; tools must be a leaf", f)
		}
	}
}

// Nothing may import the client packages: they are the outermost layer.
func TestNoPackageImportsCLI(t *testing.T) {
	for _, pkg := range allPackages(t) {
		if pkg == "internal/cli" || pkg == "cmd/ducklab" {
			continue
		}
		if deps(t, modulePath+"/"+pkg)["internal/cli"] {
			t.Errorf("%s imports internal/cli", pkg)
		}
	}
}

// allPackages walks the module for directories holding non-test Go files —
// the same set `go list ./...` would name, found by reading the tree so the
// cache sees it.
func allPackages(t *testing.T) []string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	var pkgs []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "testdata" || name == "dist") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") && !strings.HasSuffix(d.Name(), "_test.go") {
			rel, _ := filepath.Rel(root, filepath.Dir(path))
			rel = filepath.ToSlash(rel)
			if len(pkgs) == 0 || pkgs[len(pkgs)-1] != rel {
				pkgs = append(pkgs, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return pkgs
}

// engineapi is a transport shim; it must not reach past service into the
// domain, or handlers start carrying logic (01 §4.1).
func TestEngineAPIDoesNotImportStrategyDirectly(t *testing.T) {
	got := deps(t, modulePath+"/internal/engineapi")
	if !got["internal/service"] {
		t.Fatal("engineapi does not import service; the layering assumption is wrong")
	}
	// engineapi reaches strategy only transitively through service, which is
	// fine; what matters is that it never grows its own orchestration.
	for _, imp := range directImports(t, modulePath+"/internal/engineapi") {
		switch strings.TrimPrefix(imp, modulePath+"/") {
		case "internal/strategy", "internal/agent", "internal/conv", "internal/stage":
			t.Errorf("engineapi imports %s directly; handlers must call service only", imp)
		}
	}
}
