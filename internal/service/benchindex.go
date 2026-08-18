package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/xplat"
)

// External indices for the flock's scorecards.
//
// OpenRouter serves Artificial Analysis's coding / intelligence / agentic
// indices at GET /benchmarks?source=artificial-analysis, under the same key
// the provider already needs. That is a source ducklab can NAME — the
// scorecard used to say "declare it by hand, there is no honest source",
// and now there is one. Declared indices still win: a person's statement is
// not overwritten by a fetch. The fetch fills the seats nobody declared.
//
// The endpoint allows 500 calls a day, so the answer is cached on disk for a
// day and refreshed in the background; the first call with no cache waits a
// few seconds so the board that asked gets an answer, not a blank.

const (
	indexSourceParam = "artificial-analysis"
	indexCacheTTL    = 24 * time.Hour
	indexFetchWait   = 5 * time.Second
)

type indexCache struct {
	FetchedAt string      `json:"fetched_at"`
	AsOf      string      `json:"as_of"`
	Source    string      `json:"source"`
	Items     []indexItem `json:"items"`
	// Canonical maps an OpenRouter model id ("anthropic/claude-opus-4.8") to
	// the permaslug it actually serves ("anthropic/claude-4.8-opus-20260528"),
	// from GET /models. Benchmark items are keyed by permaslug, and the two
	// spellings differ for whole vendors; a prefix guess would miss them.
	Canonical map[string]string `json:"canonical,omitempty"`
}

type indexItem struct {
	Permaslug    string  `json:"model_permaslug"`
	DisplayName  string  `json:"display_name"`
	Coding       float64 `json:"coding_index"`
	Intelligence float64 `json:"intelligence_index"`
	Agentic      float64 `json:"agentic_index"`
}

type indexFetcher struct {
	mu       sync.Mutex
	inflight bool
	cache    *indexCache
	loaded   bool
	// lastErr is the most recent fetch failure, for the scorecard's honesty
	// line; a stale-but-present cache is still served.
	lastErr string
}

func indexCachePath() (string, error) {
	dir, err := xplat.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "index", "openrouter-"+indexSourceParam+".json"), nil
}

// isOpenRouter says whether a provider is the one that serves the endpoint.
func isOpenRouter(p config.Provider) bool {
	u, err := url.Parse(p.BaseURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Hostname(), "openrouter.ai")
}

// openRouterProvider finds the configured OpenRouter provider with a key in
// the environment, or nil.
func openRouterProvider(cfg *config.Global) *config.Provider {
	for _, p := range cfg.Providers {
		if isOpenRouter(p) && (p.APIKeyEnv == "" || os.Getenv(p.APIKeyEnv) != "") {
			pp := p
			return &pp
		}
	}
	return nil
}

// current returns the cached indices, fetching if there is no cache (bounded
// wait) or refreshing in the background if the cache is older than a day.
func (f *indexFetcher) current(ctx context.Context, p *config.Provider) *indexCache {
	f.mu.Lock()
	if !f.loaded {
		f.loaded = true
		if path, err := indexCachePath(); err == nil {
			if raw, err := os.ReadFile(path); err == nil {
				var c indexCache
				if json.Unmarshal(raw, &c) == nil {
					f.cache = &c
				}
			}
		}
	}
	cache := f.cache
	stale := cache == nil
	if cache != nil {
		if t, err := time.Parse(time.RFC3339, cache.FetchedAt); err != nil || time.Since(t) > indexCacheTTL {
			stale = true
		}
	}
	if p == nil || !stale || f.inflight {
		f.mu.Unlock()
		return cache
	}
	f.inflight = true
	f.mu.Unlock()

	done := make(chan *indexCache, 1)
	go func() {
		c, err := fetchIndexFn(*p)
		f.mu.Lock()
		f.inflight = false
		if err != nil {
			f.lastErr = err.Error()
		} else {
			f.lastErr = ""
			f.cache = c
			if path, perr := indexCachePath(); perr == nil {
				if raw, merr := json.Marshal(c); merr == nil {
					_ = os.MkdirAll(filepath.Dir(path), 0o755)
					_ = os.WriteFile(path, raw, 0o644)
				}
			}
		}
		f.mu.Unlock()
		done <- c
	}()
	if cache != nil {
		// Serve what we have; the refresh lands for the next reader.
		return cache
	}
	select {
	case c := <-done:
		return c
	case <-time.After(indexFetchWait):
		return nil
	case <-ctx.Done():
		return nil
	}
}

// fetchIndexFn is swapped by tests; production always fetches.
var fetchIndexFn = fetchIndex

func fetchIndex(p config.Provider) (*indexCache, error) {
	base := strings.TrimSuffix(p.BaseURL, "/")
	client := &http.Client{Timeout: 20 * time.Second}
	get := func(path string, into interface{}) error {
		req, err := http.NewRequest("GET", base+path, nil)
		if err != nil {
			return err
		}
		if p.APIKeyEnv != "" {
			req.Header.Set("Authorization", "Bearer "+os.Getenv(p.APIKeyEnv))
		}
		for k, v := range p.Headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s: HTTP %d", path, resp.StatusCode)
		}
		if err := json.Unmarshal(body, into); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		return nil
	}
	var payload struct {
		Data []indexItem `json:"data"`
		Meta struct {
			AsOf   string `json:"as_of"`
			Source string `json:"source"`
		} `json:"meta"`
	}
	if err := get("/benchmarks?source="+indexSourceParam, &payload); err != nil {
		return nil, err
	}
	asOf := payload.Meta.AsOf
	if len(asOf) >= 10 {
		asOf = asOf[:10]
	}
	c := &indexCache{FetchedAt: time.Now().UTC().Format(time.RFC3339), AsOf: asOf, Source: payload.Meta.Source, Items: payload.Data}
	// The id→permaslug map. Not fatal when it fails: the dated-suffix
	// heuristic below still finds most models, and the miss is visible as
	// an absent index rather than a wrong one.
	var models struct {
		Data []struct {
			ID            string `json:"id"`
			CanonicalSlug string `json:"canonical_slug"`
		} `json:"data"`
	}
	if err := get("/models", &models); err == nil {
		c.Canonical = make(map[string]string, len(models.Data))
		for _, m := range models.Data {
			if m.ID != "" && m.CanonicalSlug != "" {
				c.Canonical[m.ID] = m.CanonicalSlug
			}
		}
	}
	return c, nil
}

var permaslugDate = regexp.MustCompile(`-(\d{8})$`)

// matchIndex finds the item for a model. The permaslug OpenRouter serves for
// the id (from /models) is the truth: it names the exact release the
// duckling is calling, and it is spelled differently from the id for whole
// vendors ("anthropic/claude-opus-4.8" serves "anthropic/claude-4.8-opus-
// 20260528"). Without it, an exact permaslug match, else the newest dated
// release under the id ("openai/gpt-5.6-terra-20260709").
func matchIndex(items []indexItem, canonical map[string]string, model string) (indexItem, bool) {
	if slug, ok := canonical[model]; ok {
		for _, it := range items {
			if it.Permaslug == slug {
				return it, true
			}
		}
	}
	var best indexItem
	bestDate, found := "", false
	for _, it := range items {
		if it.Permaslug == model {
			return it, true
		}
		if !strings.HasPrefix(it.Permaslug, model+"-") {
			continue
		}
		m := permaslugDate.FindStringSubmatch(it.Permaslug)
		if m == nil || strings.TrimSuffix(it.Permaslug, "-"+m[1]) != model {
			continue
		}
		if !found || m[1] > bestDate {
			best, bestDate, found = it, m[1], true
		}
	}
	return best, found
}

// externalIndexFor returns the fetched index for a duckling, or nil.
func externalIndexFor(cache *indexCache, model string) *config.ExternalIndex {
	if cache == nil {
		return nil
	}
	it, ok := matchIndex(cache.Items, cache.Canonical, model)
	if !ok {
		return nil
	}
	return &config.ExternalIndex{
		CodingScore: it.Coding, IntelligenceScore: it.Intelligence, AgenticScore: it.Agentic,
		Source: fmt.Sprintf("%s via openrouter (%s)", cache.Source, it.Permaslug), AsOf: cache.AsOf,
	}
}
