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

// CapsCache persists probe results keyed by "provider:model". Registry callers
// append a concrete OpenRouter endpoint tag to model when one is pinned.
//
// Keyed by provider AND model, not by duckling id: two ducklings pointing at
// the same model behind the same endpoint have the same capabilities, and
// re-probing for each would pay twice for one answer.
//
// Writers are serialized end to end (writeMu): the in-memory update, the
// marshal and the file write happen in one order, so the file always ends
// with the newest state. Concurrent runs record image evidence through the
// same cache (B-515); with only mu held around the marshal, two writers
// could finish their file writes in the opposite order and the file kept
// the older map.
type CapsCache struct {
	writeMu sync.Mutex
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

// beforeCapsWrite, when set by a test, runs between a writer's marshal and
// its file write: the window in which writers could pass each other.
var beforeCapsWrite func()

func capsKey(provider config.ProviderID, model string) string {
	return string(provider) + ":" + model
}

// Get returns a cached probe record if it is present and still fresh. A
// vision-only record (image evidence from run traffic, B-515) is not a probe
// result: it says nothing about tools, JSON mode or thinking control, so it
// must not stand in for one and stop a duckling from being probed.
func (c *CapsCache) Get(provider config.ProviderID, model string) (*Capabilities, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	caps, ok := c.fresh(capsKey(provider, model))
	if !ok || caps.VisionOnly {
		return nil, false
	}
	return &caps, true
}

// Vision returns the fresh vision answer for provider and model — from a
// probe or from real image traffic — and when it was recorded.
func (c *CapsCache) Vision(provider config.ProviderID, model string) (vision bool, at string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	caps, ok := c.fresh(capsKey(provider, model))
	if !ok {
		return false, "", false
	}
	return caps.Vision, caps.ProbedAt, true
}

// fresh returns the record under key when it is present and within capsTTL.
// Callers hold mu.
func (c *CapsCache) fresh(key string) (Capabilities, bool) {
	caps, ok := c.entries[key]
	if !ok || caps.ProbedAt == "" {
		return Capabilities{}, false
	}
	probed, err := time.Parse(time.RFC3339, caps.ProbedAt)
	if err != nil || time.Since(probed) > capsTTL {
		return Capabilities{}, false
	}
	return caps, true
}

// RecordVision stores what one real image request proved (B-515): the
// endpoint answered it (vision true) or rejected the image (false). It
// changes only the vision answer. A fresh probe record keeps its other
// capabilities and is re-stamped; without one, a vision-only record is
// written, which Get does not return. An answer equal to the fresh one is
// not written again — every call of an image-bearing turn reaches here, and
// rewriting the file per call would be pure churn. changed reports a write.
func (c *CapsCache) RecordVision(provider config.ProviderID, model string, vision bool) (changed bool, err error) {
	key := capsKey(provider, model)
	return c.update(func() bool {
		caps, ok := c.fresh(key)
		if ok && caps.Vision == vision {
			return false
		}
		if !ok {
			caps = Capabilities{VisionOnly: true}
		}
		caps.Vision = vision
		caps.ProbedAt = time.Now().UTC().Format(time.RFC3339)
		c.entries[key] = caps
		return true
	})
}

// Put stores a capability record and writes the cache.
func (c *CapsCache) Put(provider config.ProviderID, model string, caps *Capabilities) error {
	if caps == nil {
		return nil
	}
	_, err := c.update(func() bool {
		stored := *caps
		stored.ProbedAt = time.Now().UTC().Format(time.RFC3339)
		c.entries[capsKey(provider, model)] = stored
		return true
	})
	return err
}

// update applies change under mu and, when it changed something, writes the
// whole cache — serialized with every other writer by writeMu, so the order
// of file writes is the order of the changes.
func (c *CapsCache) update(change func() bool) (bool, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	if !change() {
		c.mu.Unlock()
		return false, nil
	}
	data, err := json.MarshalIndent(c.entries, "", "  ")
	path := c.path
	c.mu.Unlock()

	if beforeCapsWrite != nil {
		beforeCapsWrite()
	}
	if err != nil || path == "" {
		return true, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return true, err
	}
	return true, xplat.AtomicWrite(path, data, 0o644)
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
