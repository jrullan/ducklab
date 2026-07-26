package duckling

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/xplat"
)

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func isolateData(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("LocalAppData", filepath.Join(root, "local"))
	t.Setenv("AppData", filepath.Join(root, "roaming"))
}

// Probing costs real model calls; the result must survive across processes or
// every run pays for it again.
func TestCapsCacheRoundTripsAcrossInstances(t *testing.T) {
	isolateData(t)

	c := LoadCapsCache()
	if err := c.Put("beelink", "gemma-4", &Capabilities{
		NativeTools: true, JSONMode: true, ContextTokens: 65536,
	}); err != nil {
		t.Fatal(err)
	}

	reloaded := LoadCapsCache()
	got, ok := reloaded.Get("beelink", "gemma-4")
	if !ok {
		t.Fatal("cache did not survive a reload")
	}
	if !got.NativeTools || got.ContextTokens != 65536 {
		t.Errorf("cached record lost fields: %+v", got)
	}
	if got.ProbedAt == "" {
		t.Error("ProbedAt was not stamped; the entry could never expire")
	}
}

// Keyed by provider AND model: two ducklings on the same model share one probe.
func TestCapsCacheIsKeyedByProviderAndModel(t *testing.T) {
	isolateData(t)
	c := LoadCapsCache()
	c.Put("beelink", "gemma-4", &Capabilities{NativeTools: true})

	if _, ok := c.Get("beelink", "gemma-4"); !ok {
		t.Error("same provider+model not found")
	}
	if _, ok := c.Get("openrouter", "gemma-4"); ok {
		t.Error("a different provider returned another provider's record")
	}
	if _, ok := c.Get("beelink", "qwen-3"); ok {
		t.Error("a different model returned another model's record")
	}
}

// A model behind an endpoint can change, so a probe must not be trusted forever.
func TestCapsCacheExpiresStaleEntries(t *testing.T) {
	isolateData(t)
	c := LoadCapsCache()
	c.entries["beelink:old"] = Capabilities{
		NativeTools: true,
		ProbedAt:    time.Now().Add(-40 * 24 * time.Hour).UTC().Format(time.RFC3339),
	}
	if _, ok := c.Get("beelink", "old"); ok {
		t.Error("an entry older than the TTL was served as fresh")
	}

	c.entries["beelink:recent"] = Capabilities{
		NativeTools: true,
		ProbedAt:    time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339),
	}
	if _, ok := c.Get("beelink", "recent"); !ok {
		t.Error("a recent entry was treated as stale")
	}
}

func TestCapsCacheIgnoresEntriesWithNoTimestamp(t *testing.T) {
	isolateData(t)
	c := LoadCapsCache()
	c.entries["beelink:untimed"] = Capabilities{NativeTools: true}
	if _, ok := c.Get("beelink", "untimed"); ok {
		t.Error("an entry with no ProbedAt was served; its age is unknown")
	}
}

// A corrupt cache must be rebuilt by probing, not crash the engine.
func TestCorruptCacheIsDiscardedNotFatal(t *testing.T) {
	isolateData(t)
	c := LoadCapsCache()
	c.Put("beelink", "x", &Capabilities{NativeTools: true})

	if err := writeFile(c.path, "{not json"); err != nil {
		t.Fatal(err)
	}
	reloaded := LoadCapsCache()
	if len(reloaded.Entries()) != 0 {
		t.Error("a corrupt cache produced entries")
	}
	if err := reloaded.Put("beelink", "y", &Capabilities{}); err != nil {
		t.Errorf("could not write after a corrupt read: %v", err)
	}
}

// ProviderCaps drops probe metadata, which the provider layer has no use for.
func TestProviderCapsDropsProbeMetadata(t *testing.T) {
	got := ProviderCaps(&Capabilities{
		NativeTools: true, JSONMode: true, ContextTokens: 8192, Vision: true,
		ProbedAt: "2026-07-26T00:00:00Z",
	})
	if !got.NativeTools || !got.JSONMode || got.ContextTokens != 8192 || !got.Vision {
		t.Errorf("capabilities lost in conversion: %+v", got)
	}
}

func TestProviderCapsNilIsSafe(t *testing.T) {
	if got := ProviderCaps(nil); got.ContextTokens == 0 {
		t.Error("nil caps produced a zero context window; a run would send nothing")
	}
}

// The cache must land where the rest of ducklab's data lives, not one level
// deeper. A misplaced cache is never found, so every run silently re-probes.
func TestCapsCacheLivesInTheDataDir(t *testing.T) {
	isolateData(t)
	c := LoadCapsCache()
	if err := c.Put("beelink", "m", &Capabilities{NativeTools: true}); err != nil {
		t.Fatal(err)
	}
	dir, err := xplat.DataDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "caps.json")
	if c.path != want {
		t.Errorf("cache path = %q, want %q", c.path, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("cache not written to the data dir: %v", err)
	}
}
