package config

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSetKeyWritesNestedValues(t *testing.T) {
	cfg := DefaultProject("p", "P")
	for _, tc := range []struct {
		key, val string
		check    func() bool
	}{
		{"name", "Renamed", func() bool { return cfg.Name == "Renamed" }},
		{"describe", "a harness", func() bool { return cfg.Describe == "a harness" }},
		{"autonomy", "auto", func() bool { return string(cfg.Autonomy) == "auto" }},
		{"verify.timeout_s", "42", func() bool { return cfg.Verify.TimeoutS == 42 }},
	} {
		if err := SetKey(cfg, tc.key, tc.val); err != nil {
			t.Fatalf("SetKey(%q): %v", tc.key, err)
		}
		if !tc.check() {
			t.Errorf("SetKey(%q, %q) did not take effect", tc.key, tc.val)
		}
	}
}

// An unknown key must fail loudly. A `project set` that accepts a typo and
// changes nothing leaves the user believing a setting was applied.
func TestSetKeyWritesSlicesAndMapLeavesRoundTrip(t *testing.T) {
	cfg := DefaultProject("p", "P")
	for _, tc := range []struct {
		key, value string
		want       []string
	}{
		{"verify.link_deps", "node_modules,vendor", []string{"node_modules", "vendor"}},
		{"shell.allow_prefixes", "go ,make ", []string{"go ", "make "}},
		{"shell.deny", "shutdown,reboot", []string{"shutdown", "reboot"}},
		{"git.protected_paths", ".github,docs", []string{".github", "docs"}},
	} {
		if err := SetKey(cfg, tc.key, tc.value); err != nil {
			t.Fatalf("SetKey(%q): %v", tc.key, err)
		}
	}
	for _, tc := range []struct{ key, value string }{
		{"roster.implementer", "worker"},
		{"modes.build", "pair"},
	} {
		if err := SetKey(cfg, tc.key, tc.value); err != nil {
			t.Fatalf("SetKey(%q): %v", tc.key, err)
		}
	}
	path := filepath.Join(t.TempDir(), "project.toml")
	if err := SaveProject(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(loaded.Verify.LinkDeps, []string{"node_modules", "vendor"}) ||
		!slices.Equal(loaded.Shell.AllowPrefixes, []string{"go ", "make "}) ||
		!slices.Equal(loaded.Shell.Deny, []string{"shutdown", "reboot"}) ||
		!slices.Equal(loaded.Git.ProtectedPaths, []string{".github", "docs"}) {
		t.Errorf("slice values did not survive round trip: %#v", loaded)
	}
	if loaded.Roster[RoleImplementer] != DucklingID("worker") || loaded.Modes[StageBuild] != ModePair {
		t.Errorf("map leaves did not survive round trip: roster=%#v modes=%#v", loaded.Roster, loaded.Modes)
	}
}

func TestSetKeyClearsSliceAndRejectsMalformedMapLeaf(t *testing.T) {
	cfg := DefaultProject("p", "P")
	if err := SetKey(cfg, "shell.deny", ""); err != nil {
		t.Fatalf("clear slice: %v", err)
	}
	if len(cfg.Shell.Deny) != 0 {
		t.Fatal("empty value did not clear slice")
	}
	for _, key := range []string{"shell.deny", "roster.typo", "modes.typo"} {
		value := "a,,b"
		if key != "shell.deny" {
			value = "worker"
		}
		if err := SetKey(cfg, key, value); err == nil {
			t.Errorf("SetKey(%q, %q) was accepted", key, value)
		}
	}
}

func TestSetKeyRejectsUnknownKeys(t *testing.T) {
	cfg := DefaultProject("p", "P")
	err := SetKey(cfg, "verify.timout_s", "42")
	if err == nil {
		t.Fatal("a misspelled key was accepted")
	}
	if !strings.Contains(err.Error(), "verify.timeout_s") {
		t.Errorf("the error should suggest the real key, got: %v", err)
	}
}

func TestSetKeyRejectsWrongType(t *testing.T) {
	cfg := DefaultProject("p", "P")
	if err := SetKey(cfg, "verify.timeout_s", "soon"); err == nil {
		t.Fatal("a non-numeric value was accepted for an int field")
	} else if cfg.Verify.TimeoutS == 0 {
		t.Error("a rejected assignment must leave the previous value intact")
	}
}

// Keys is what the error message offers, so it has to actually enumerate the
// struct rather than a stale hand-written list.
func TestKeysCoversNestedFields(t *testing.T) {
	keys := Keys()
	for _, want := range []string{"name", "describe", "autonomy", "verify.timeout_s"} {
		if !slices.Contains(keys, want) {
			t.Errorf("Keys() is missing %q", want)
		}
	}
	if !slices.IsSorted(keys) {
		t.Error("Keys() should be sorted so the error message is stable")
	}
}

// Changing the id would leave project.toml and the engine registry disagreeing
// about which project this is.
func TestSetKeyRefusesIdentityFields(t *testing.T) {
	cfg := DefaultProject("p", "P")
	for _, k := range []string{"id", "schema", "created"} {
		if err := SetKey(cfg, k, "x"); err == nil {
			t.Errorf("SetKey(%q) was allowed", k)
		}
		if slices.Contains(Keys(), k) {
			t.Errorf("Keys() offers %q, which SetKey refuses", k)
		}
	}
	if cfg.ID != "p" {
		t.Error("the id changed despite the refusal")
	}
}
