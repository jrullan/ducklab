package config

import (
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
