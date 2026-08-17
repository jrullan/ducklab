package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Isolation must not orphan the build caches: a gate that recompiles the
// world from an empty HOME pays minutes of downloads per verify and its
// chatter gets misread as compile errors (B-048).
func TestIsolatedGateKeepsTheBuildCaches(t *testing.T) {
	env, cleanup, err := isolatedStateEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	home, _ := os.UserHomeDir()
	want := map[string]string{
		"GOMODCACHE": filepath.Join(home, "go", "pkg", "mod"),
		"GOCACHE":    filepath.Join(home, ".cache", "go-build"),
	}
	got := map[string]string{}
	var isolatedHome string
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		if _, ok := want[k]; ok {
			got[k] = v
		}
		if k == "HOME" {
			isolatedHome = v
		}
	}
	for k := range want {
		if got[k] == "" {
			t.Errorf("%s missing from the gate environment — the cache died with HOME", k)
		} else if strings.HasPrefix(got[k], isolatedHome) {
			t.Errorf("%s points into the throwaway HOME (%s) — cold cache every gate", k, got[k])
		}
	}
}
