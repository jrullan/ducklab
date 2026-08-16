// Package internal holds architecture tests that guard the dependency rules
// of 01-ARCHITECTURE.md §4.1. They are structural, not behavioural: they fail
// when a package reaches somewhere it must not, which is exactly the kind of
// drift that is invisible in review and expensive to undo later.
package internal

import (
	"os/exec"
	"strings"
	"testing"
)

const modulePath = "github.com/jrullan/ducklab"

// deps returns the transitive ducklab-internal dependencies of a package.
func deps(t *testing.T, pkg string) map[string]bool {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", pkg).Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	got := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, modulePath+"/") {
			got[strings.TrimPrefix(line, modulePath+"/")] = true
		}
	}
	return got
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
	out, err := exec.Command("go", "list", "./...").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	for _, pkg := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if pkg == "" || strings.HasSuffix(pkg, "/internal/cli") || strings.HasSuffix(pkg, "/cmd/ducklab") {
			continue
		}
		if deps(t, pkg)["internal/cli"] {
			t.Errorf("%s imports internal/cli", pkg)
		}
	}
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
	out, err := exec.Command("go", "list", "-f", "{{join .Imports \"\\n\"}}",
		modulePath+"/internal/engineapi").Output()
	if err != nil {
		t.Skip()
	}
	for _, imp := range strings.Split(string(out), "\n") {
		switch strings.TrimPrefix(imp, modulePath+"/") {
		case "internal/strategy", "internal/agent", "internal/conv", "internal/stage":
			t.Errorf("engineapi imports %s directly; handlers must call service only", imp)
		}
	}
}
