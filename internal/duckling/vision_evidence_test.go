package duckling

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
)

func evidenceRegistry(t *testing.T, ducks ...*Duckling) *Registry {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	r := NewRegistry()
	for _, d := range ducks {
		if err := r.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func statusOf(r *Registry, id config.DucklingID) string {
	for _, d := range r.List() {
		if d.ID == id {
			return d.VisionStatus
		}
	}
	return ""
}

// B-515: luna answered build turns that carried reference images, captures
// and diffs, and the consultant picker still said "not yet tested". A real
// image request is evidence: its success verifies, an explicit image
// rejection refutes, and provider weather says nothing.
func TestB515ImageEvidenceVerifiesRefutesOrSaysNothing(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		outcome string
		status  string
	}{
		{"answered", nil, ImageEvidenceVerified, VisionVerified},
		{"rejected the image", fmt.Errorf("chat: %w: no mmproj", provider.ErrVisionUnsupported), ImageEvidenceRefuted, VisionRefuted},
		{"rate limited", fmt.Errorf("chat: %w", provider.ErrRateLimit), ImageEvidenceNone, VisionDeclared},
		{"unreachable", fmt.Errorf("chat: %w", provider.ErrProviderUnavailable), ImageEvidenceNone, VisionDeclared},
		{"bad credentials", fmt.Errorf("chat: %w", provider.ErrAuth), ImageEvidenceNone, VisionDeclared},
		{"malformed reply", errors.New("unexpected end of JSON input"), ImageEvidenceNone, VisionDeclared},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := evidenceRegistry(t, &Duckling{ID: "luna", Provider: "openrouter", Model: "openai/gpt-5.6-luna", Caps: Capabilities{Vision: true}})
			outcome, changed := r.RecordImageEvidence("luna", tc.err)
			if outcome != tc.outcome || changed != (tc.outcome != ImageEvidenceNone) {
				t.Errorf("RecordImageEvidence = %q changed=%v, want %q", outcome, changed, tc.outcome)
			}
			if got := statusOf(r, "luna"); got != tc.status {
				t.Errorf("vision_status = %q, want %q", got, tc.status)
			}
			// The same answer again is not a change: every call of an image
			// turn reaches the recorder, and the file is not rewritten per call.
			if _, again := r.RecordImageEvidence("luna", tc.err); again {
				t.Error("the same evidence was recorded as a change twice")
			}
			// Persisted under the probe's key, so a restarted engine — a new
			// registry over the same data directory — still knows.
			fresh := NewRegistry()
			_ = fresh.Register(&Duckling{ID: "luna", Provider: "openrouter", Model: "openai/gpt-5.6-luna", Caps: Capabilities{Vision: true}})
			if got := statusOf(fresh, "luna"); got != tc.status {
				t.Errorf("after a restart vision_status = %q, want %q", got, tc.status)
			}
		})
	}
}

// The evidence uses the probe's key and freshness: an OpenRouter duckling
// pinned to an endpoint is its own record, and a stale answer is no answer.
func TestB515EvidenceUsesTheProbesKeyAndFreshness(t *testing.T) {
	pinned := &Duckling{ID: "kimi-di", Provider: "openrouter", Model: "moonshotai/kimi-k3", OpenRouterProvider: "deepinfra/bf16", Caps: Capabilities{Vision: true}}
	pooled := &Duckling{ID: "kimi", Provider: "openrouter", Model: "moonshotai/kimi-k3", Caps: Capabilities{Vision: true}}
	r := evidenceRegistry(t, pinned, pooled)
	r.RecordImageEvidence("kimi-di", nil)
	if statusOf(r, "kimi-di") != VisionVerified || statusOf(r, "kimi") != VisionDeclared {
		t.Errorf("pinned=%q pooled=%q: evidence must land under the pinned endpoint's key only", statusOf(r, "kimi-di"), statusOf(r, "kimi"))
	}
	if _, _, ok := r.capsCache().Vision("openrouter", "moonshotai/kimi-k3@deepinfra/bf16"); !ok {
		t.Error("no record under the probe's provider:model@endpoint key")
	}

	// A record older than the probe TTL is not evidence any more.
	c := r.capsCache()
	c.mu.Lock()
	rec := c.entries["openrouter:moonshotai/kimi-k3@deepinfra/bf16"]
	rec.ProbedAt = time.Now().Add(-capsTTL - time.Hour).UTC().Format(time.RFC3339)
	c.entries["openrouter:moonshotai/kimi-k3@deepinfra/bf16"] = rec
	c.mu.Unlock()
	if got := statusOf(r, "kimi-di"); got != VisionDeclared {
		t.Errorf("stale evidence: vision_status = %q, want declared", got)
	}
	if _, changed := r.RecordImageEvidence("kimi-di", nil); !changed {
		t.Error("evidence over a stale record was not written")
	}
}

// Image evidence is not a probe. Recorded on a duckling never probed, it
// must not stand in for the tools/JSON answers a probe gives — or the next
// run would skip the probe and seat the duckling on guessed capabilities.
// Over a fresh probe record it changes only the vision answer.
func TestB515EvidenceIsNotAProbe(t *testing.T) {
	r := evidenceRegistry(t,
		&Duckling{ID: "new", Provider: "p", Model: "new", Caps: Capabilities{Vision: true}},
		&Duckling{ID: "probed", Provider: "p", Model: "probed", Caps: Capabilities{Vision: true}})
	r.RecordImageEvidence("new", nil)
	if _, ok := r.CachedCaps("new"); ok {
		t.Error("a vision-only record was returned as a probe result")
	}
	if vision, _, ok := r.CachedVision("new"); !ok || !vision {
		t.Errorf("CachedVision = %v,%v, want the recorded success", vision, ok)
	}

	if err := r.capsCache().Put("p", "probed", &Capabilities{NativeTools: true, JSONMode: true, ContextTokens: 65536, Vision: true, ThinkingControl: "disabled"}); err != nil {
		t.Fatal(err)
	}
	r.RecordImageEvidence("probed", fmt.Errorf("%w", provider.ErrVisionUnsupported))
	got, ok := r.CachedCaps("probed")
	if !ok || got.Vision || !got.NativeTools || !got.JSONMode || got.ContextTokens != 65536 || got.ThinkingControl != "disabled" || got.VisionOnly {
		t.Errorf("probe record after a refusal = %+v (ok=%v), want only vision changed", got, ok)
	}
}

// VerifyVision — the consultant chat's check — reads run evidence before
// spending a probe: a duckling verified by a build is not re-tested.
func TestB515VerifyVisionTrustsRunEvidence(t *testing.T) {
	r := evidenceRegistry(t, &Duckling{ID: "luna", Provider: "dead", Model: "luna", Caps: Capabilities{Vision: true}})
	r.RecordImageEvidence("luna", nil)
	// No provider "dead" is registered: a probe would fail.
	vision, err := r.VerifyVision(t.Context(), "luna")
	if err != nil || !vision {
		t.Errorf("VerifyVision = %v, %v; want the recorded success without a probe", vision, err)
	}
}

// Concurrent runs record evidence through one cache. Every answer must be
// in memory and in the file at the end — the file is not left with an older
// map by writers finishing out of order. Run with -race.
func TestB515ConcurrentEvidenceWritesKeepEveryAnswer(t *testing.T) {
	var ducks []*Duckling
	for i := 0; i < 24; i++ {
		ducks = append(ducks, &Duckling{ID: config.DucklingID(fmt.Sprintf("d%02d", i)), Provider: "p", Model: fmt.Sprintf("m%02d", i), Caps: Capabilities{Vision: true}})
	}
	r := evidenceRegistry(t, ducks...)
	var wg sync.WaitGroup
	for i, d := range ducks {
		wg.Add(1)
		go func(i int, id config.DucklingID) {
			defer wg.Done()
			var err error
			if i%2 == 1 {
				err = provider.ErrVisionUnsupported
			}
			r.RecordImageEvidence(id, err)
			_ = r.List()
		}(i, d.ID)
	}
	wg.Wait()
	dir := os.Getenv("XDG_DATA_HOME")
	data, err := os.ReadFile(filepath.Join(dir, "ducklab", "caps.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk map[string]Capabilities
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatal(err)
	}
	for i, d := range ducks {
		want := i%2 == 0
		rec, ok := onDisk["p:"+d.Model]
		if !ok || rec.Vision != want {
			t.Errorf("%s on disk = %+v (present %v), want vision %v", d.ID, rec, ok, want)
		}
		if vision, _, ok := r.CachedVision(d.ID); !ok || vision != want {
			t.Errorf("%s in memory = %v (present %v), want %v", d.ID, vision, ok, want)
		}
	}
}

// The window the writer lock closes, forced open: the first writer is held
// between its marshal and its file write while a second writer records. The
// file must end with both answers — without the lock the second writer
// passed, wrote, and the first one's older map overwrote it.
func TestB515AWriterHeldAfterItsMarshalCannotBePassed(t *testing.T) {
	r := evidenceRegistry(t,
		&Duckling{ID: "a", Provider: "p", Model: "a", Caps: Capabilities{Vision: true}},
		&Duckling{ID: "b", Provider: "p", Model: "b", Caps: Capabilities{Vision: true}})
	r.capsCache()
	// Not a sync.Once: Once would block the second writer inside Do, which
	// is exactly the passing this test must allow to happen.
	var first atomic.Bool
	held, passed := make(chan struct{}), make(chan struct{})
	beforeCapsWrite = func() {
		if first.CompareAndSwap(false, true) {
			close(held)
			select {
			case <-passed:
			case <-time.After(300 * time.Millisecond):
			}
		}
	}
	t.Cleanup(func() { beforeCapsWrite = nil })
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); r.RecordImageEvidence("a", nil) }()
	<-held
	go func() { defer wg.Done(); r.RecordImageEvidence("b", nil); close(passed) }()
	wg.Wait()
	data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_DATA_HOME"), "ducklab", "caps.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk map[string]Capabilities
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatal(err)
	}
	if _, ok := onDisk["p:a"]; !ok {
		t.Error("the held writer's answer is missing from the file")
	}
	if _, ok := onDisk["p:b"]; !ok {
		t.Error("the second writer's answer was overwritten by the held writer's older map")
	}
}

// A restarted engine's first reader — the seat check, before anything has
// probed or listed — reads the cache from disk.
func TestB515ARestartedEnginesFirstVisionReadLoadsTheCache(t *testing.T) {
	r := evidenceRegistry(t, &Duckling{ID: "luna", Provider: "p", Model: "luna", Caps: Capabilities{Vision: true}})
	r.RecordImageEvidence("luna", provider.ErrVisionUnsupported)
	restarted := NewRegistry()
	_ = restarted.Register(&Duckling{ID: "luna", Provider: "p", Model: "luna", Caps: Capabilities{Vision: true}})
	if vision, _, ok := restarted.CachedVision("luna"); !ok || vision {
		t.Errorf("CachedVision after a restart = %v (known %v), want the recorded refusal", vision, ok)
	}
}
