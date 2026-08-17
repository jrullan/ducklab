package verify

import (
	"path/filepath"
	"strings"
	"testing"
)

// Isolation must not orphan the build caches: a gate that recompiles the
// world from an empty HOME pays minutes of downloads per verify and its
// chatter gets misread as compile errors (B-048).
func TestIsolatedGateKeepsTheBuildCaches(t *testing.T) {
	// Go derives its module-cache default from GOPATH. Preserve that derivation
	// rather than reverting a caller with a non-default shared GOPATH to HOME.
	sharedGoPath := filepath.Join(t.TempDir(), "shared-go")
	sharedBuildCache := filepath.Join(t.TempDir(), "shared-build")
	t.Setenv("GOPATH", sharedGoPath)
	t.Setenv("GOMODCACHE", "")
	t.Setenv("GOCACHE", sharedBuildCache)

	env, cleanup, err := isolatedStateEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	want := map[string]string{
		// GOPATH is the root for Go's shared downloads; GOMODCACHE also holds
		// the automatically downloaded GOTOOLCHAIN module.
		"GOPATH":      sharedGoPath,
		"GOMODCACHE": filepath.Join(sharedGoPath, "pkg", "mod"),
		"GOCACHE":    sharedBuildCache,
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
	// And the gate must be able to SIGN: scrubbed HOME means no .gitconfig,
	// so the environment itself carries a synthetic git identity.
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "GIT_AUTHOR_EMAIL=gate@ducklab.invalid") {
		t.Error("the gate environment carries no git identity — every test commit dies unnamed")
	}
	for k := range want {
		if got[k] == "" {
			t.Errorf("%s missing from the gate environment — the cache died with HOME", k)
		} else if got[k] != want[k] {
			t.Errorf("%s = %q, want shared Go cache path %q", k, got[k], want[k])
		} else if strings.HasPrefix(got[k], isolatedHome) {
			t.Errorf("%s points into the throwaway HOME (%s) — cold cache every gate", k, got[k])
		}
	}
}
