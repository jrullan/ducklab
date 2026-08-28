package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/config"
)

// OpenRouter's permaslugs carry a release date; a model appears under each
// release it had. The scorecard wants the newest — and never a different
// model that merely shares a prefix.
func TestMatchIndexPicksTheNewestReleaseOfTheModel(t *testing.T) {
	items := []indexItem{
		{Permaslug: "deepseek/deepseek-v4-pro-20260423", Coding: 59.4},
		{Permaslug: "deepseek/deepseek-v4-pro-20260813", Coding: 68.8},
		{Permaslug: "deepseek/deepseek-v4-pro-max-20260901", Coding: 90},
		{Permaslug: "openai/gpt-5.6-luna", Coding: 71.4},
	}
	got, ok := matchIndex(items, nil, "deepseek/deepseek-v4-pro")
	if !ok || got.Coding != 68.8 {
		t.Fatalf("got %+v ok=%v, want the 20260813 release", got, ok)
	}
	if got, ok := matchIndex(items, nil, "openai/gpt-5.6-luna"); !ok || got.Coding != 71.4 {
		t.Fatalf("exact permaslug: %+v ok=%v", got, ok)
	}
	if _, ok := matchIndex(items, nil, "deepseek/deepseek-v4"); ok {
		t.Fatal("a shorter model name matched a longer model's permaslug")
	}
	if _, ok := matchIndex(items, nil, "qwen/qwen3.8-max"); ok {
		t.Fatal("an absent model matched")
	}
}

// The permaslug OpenRouter serves for an id is the truth about which release
// the duckling calls — and it is spelled differently from the id for whole
// vendors. Anthropic's "claude-opus-4.8" serves "claude-4.8-opus-20260528";
// DeepSeek's "-0731" variant serves "-20260731"; and the id "deepseek-v4-pro"
// serves the April release even though an August one is listed. The map
// from /models decides; the dated-suffix guess is only the fallback.
func TestMatchIndexFollowsTheServedPermaslug(t *testing.T) {
	items := []indexItem{
		{Permaslug: "anthropic/claude-4.8-opus-20260528", Coding: 74.3},
		{Permaslug: "deepseek/deepseek-v4-flash-20260731", Coding: 69.1},
		{Permaslug: "deepseek/deepseek-v4-pro-20260423", Coding: 59.4},
		{Permaslug: "deepseek/deepseek-v4-pro-20260813", Coding: 68.8},
	}
	canonical := map[string]string{
		"anthropic/claude-opus-4.8":       "anthropic/claude-4.8-opus-20260528",
		"deepseek/deepseek-v4-flash-0731": "deepseek/deepseek-v4-flash-20260731",
		"deepseek/deepseek-v4-pro":        "deepseek/deepseek-v4-pro-20260423",
	}
	for model, want := range map[string]float64{"anthropic/claude-opus-4.8": 74.3, "deepseek/deepseek-v4-flash-0731": 69.1, "deepseek/deepseek-v4-pro": 59.4} {
		got, ok := matchIndex(items, canonical, model)
		if !ok || got.Coding != want {
			t.Errorf("%s: got %+v ok=%v, want %v", model, got, ok, want)
		}
	}
	// Without the map, opus and the -0731 variant are unfindable, and pro
	// falls back to the newest dated release.
	if _, ok := matchIndex(items, nil, "anthropic/claude-opus-4.8"); ok {
		t.Error("opus matched without the served-permaslug map — by what?")
	}
	if got, _ := matchIndex(items, nil, "deepseek/deepseek-v4-pro"); got.Coding != 68.8 {
		t.Errorf("fallback = %+v, want newest dated release", got)
	}
}

// A person's declared index is a statement; the fetch fills only the seats
// nobody declared, and says where its number came from.
func TestExternalIndexCarriesProvenance(t *testing.T) {
	cache := &indexCache{AsOf: "2026-08-18", Source: "artificial-analysis", Items: []indexItem{{Permaslug: "z-ai/glm-5.2-20260616", Coding: 68.8, Intelligence: 52.6, Agentic: 45.7}}}
	got := externalIndexFor(cache, "z-ai/glm-5.2")
	if got == nil || got.CodingScore != 68.8 || got.IntelligenceScore != 52.6 || got.AgenticScore != 45.7 {
		t.Fatalf("index = %+v", got)
	}
	if got.AsOf != "2026-08-18" || got.Source != "artificial-analysis via openrouter (z-ai/glm-5.2-20260616)" {
		t.Errorf("provenance = %q as of %q", got.Source, got.AsOf)
	}
	if externalIndexFor(nil, "z-ai/glm-5.2") != nil {
		t.Error("no cache must mean no index, not a zero one")
	}
	declared := &config.ExternalIndex{CodingScore: 1, Source: "me", AsOf: "2026-01-01"}
	if declared.Source != "me" {
		t.Fatal("fixture")
	}
}

func TestOpenRouterProviderIsRecognisedByHost(t *testing.T) {
	if !isOpenRouter(config.Provider{BaseURL: "https://openrouter.ai/api/v1"}) {
		t.Error("openrouter.ai not recognised")
	}
	if isOpenRouter(config.Provider{BaseURL: "http://10.0.0.5:8000/v1"}) || isOpenRouter(config.Provider{BaseURL: "https://api.openai.com/v1"}) {
		t.Error("a non-OpenRouter host was taken for it")
	}
}

// End to end through Scorecards: an OpenRouter duckling with no declared index
// gets the fetched one; a declared index is left alone; a local duckling gets
// none; and the answer is cached on disk so the next service does not fetch.
func TestScorecardsFillUndeclaredIndicesFromOpenRouter(t *testing.T) {
	isolate(t)
	t.Setenv("OR_KEY_FOR_TEST", "k")
	calls := 0
	orig := fetchIndexFn
	fetchIndexFn = func(p config.Provider) (*indexCache, error) {
		calls++
		// FetchedAt must be NOW: a hardcoded past date left the on-disk cache
		// permanently stale (TTL 24h), so the second service served it AND
		// kicked the background refresh — and the final assert only passed by
		// WINNING the race against that goroutine's calls++. On an idle box it
		// won; on a loaded gate at midnight it lost ("2 fetches"), killing an
		// approved run (T-248, r-20260828-003439-defz). B-221's fifth member.
		return &indexCache{FetchedAt: time.Now().UTC().Format(time.RFC3339), AsOf: "2026-08-18", Source: "artificial-analysis", Items: []indexItem{
			{Permaslug: "z-ai/glm-5.2-20260616", Coding: 68.8},
			{Permaslug: "openai/gpt-5.6-luna-20260709", Coding: 71.4},
		}}, nil
	}
	t.Cleanup(func() { fetchIndexFn = orig })

	cfg := config.DefaultGlobal()
	cfg.Providers["openrouter"] = config.Provider{Kind: "openai", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "OR_KEY_FOR_TEST"}
	cfg.Providers["beelink"] = config.Provider{Kind: "openai", BaseURL: "http://localhost:8081/v1"}
	cfg.Ducklings["glm52"] = config.Duckling{Provider: "openrouter", Model: "z-ai/glm-5.2"}
	cfg.Ducklings["luna"] = config.Duckling{Provider: "openrouter", Model: "openai/gpt-5.6-luna", Index: &config.ExternalIndex{CodingScore: 50, Source: "me", AsOf: "2026-01-01"}}
	cfg.Ducklings["local"] = config.Duckling{Provider: "beelink", Model: "z-ai/glm-5.2"}
	s, err := New(cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	cards, err := s.Scorecards(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Scorecard{}
	for _, c := range cards {
		byID[c.ID] = c
	}
	if idx := byID["glm52"].Index; idx == nil || idx.CodingScore != 68.8 || !strings.Contains(idx.Source, "artificial-analysis via openrouter") {
		t.Fatalf("glm52 index = %+v, want the fetched 68.8 with provenance", idx)
	}
	if idx := byID["luna"].Index; idx == nil || idx.CodingScore != 50 || idx.Source != "me" {
		t.Fatalf("luna's declared index was overwritten: %+v", idx)
	}
	if byID["local"].Index != nil {
		t.Fatalf("a local duckling borrowed an OpenRouter index: %+v", byID["local"].Index)
	}
	if calls != 1 {
		t.Fatalf("fetched %d times, want 1", calls)
	}

	// A fresh service reads the day-old-or-newer cache from disk: no fetch.
	s2, err := New(cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	fetchIndexFn = func(p config.Provider) (*indexCache, error) { calls++; return nil, fmt.Errorf("must not be called") }
	cards2, _ := s2.Scorecards(context.Background())
	for _, c := range cards2 {
		if c.ID == "glm52" && (c.Index == nil || c.Index.CodingScore != 68.8) {
			t.Fatalf("cache miss: %+v", c.Index)
		}
	}
	if calls != 1 {
		t.Fatalf("the on-disk cache was ignored (%d fetches)", calls)
	}
}
