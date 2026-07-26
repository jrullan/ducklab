package duckling

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/xplat"
)

// capsTTL is how long a probe result is trusted. Probing costs real model
// calls on a paid endpoint, so it must not happen per run; but a model behind
// an endpoint can change, so it must not be cached forever either (02 §7).
const capsTTL = 30 * 24 * time.Hour

// CapsCache persists probe results keyed by "provider:model".
//
// Keyed by provider AND model, not by duckling id: two ducklings pointing at
// the same model behind the same endpoint have the same capabilities, and
// re-probing for each would pay twice for one answer.
type CapsCache struct {
	mu      sync.Mutex
	path    string
	entries map[string]Capabilities
}

// LoadCapsCache reads the cache, returning an empty one if it does not exist.
// A corrupt cache is not an error: it is discarded and rebuilt by probing.
func LoadCapsCache() *CapsCache {
	c := &CapsCache{entries: map[string]Capabilities{}}
	dir, err := xplat.DataDir()
	if err != nil {
		return c
	}
	// DataDir already ends in "ducklab"; joining it again produced
	// .../ducklab/ducklab/caps.json, so nothing ever found the cache and
	// every run re-probed.
	c.path = filepath.Join(dir, "caps.json")

	data, err := os.ReadFile(c.path)
	if err != nil {
		return c
	}
	var entries map[string]Capabilities
	if err := json.Unmarshal(data, &entries); err != nil {
		return c
	}
	c.entries = entries
	return c
}

func capsKey(provider config.ProviderID, model string) string {
	return string(provider) + ":" + model
}

// Get returns a cached capability record if it is present and still fresh.
func (c *CapsCache) Get(provider config.ProviderID, model string) (*Capabilities, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	caps, ok := c.entries[capsKey(provider, model)]
	if !ok {
		return nil, false
	}
	if caps.ProbedAt == "" {
		return nil, false
	}
	probed, err := time.Parse(time.RFC3339, caps.ProbedAt)
	if err != nil || time.Since(probed) > capsTTL {
		return nil, false
	}
	return &caps, true
}

// Put stores a capability record and writes the cache.
func (c *CapsCache) Put(provider config.ProviderID, model string, caps *Capabilities) error {
	if caps == nil {
		return nil
	}
	c.mu.Lock()
	stored := *caps
	stored.ProbedAt = time.Now().UTC().Format(time.RFC3339)
	c.entries[capsKey(provider, model)] = stored
	data, err := json.MarshalIndent(c.entries, "", "  ")
	path := c.path
	c.mu.Unlock()

	if err != nil || path == "" {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return xplat.AtomicWrite(path, data, 0o644)
}

// Entries returns a copy of the cache, for `duckling list`.
func (c *CapsCache) Entries() map[string]Capabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]Capabilities, len(c.entries))
	for k, v := range c.entries {
		out[k] = v
	}
	return out
}
