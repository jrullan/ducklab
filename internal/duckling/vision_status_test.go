package duckling

import (
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

// B-512: every consultant picker marks who can see images, and it must say
// what the chat will actually do with a screenshot — the declaration first,
// then the probe's answer. A probe that saw an image on an UNDECLARED
// duckling made Caps.Vision say true while the chat still refused.
func TestListReportsTheChatsVisionRule(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	r := NewRegistry()
	r.caps = LoadCapsCache()
	for _, d := range []*Duckling{
		{ID: "blind", Provider: "p", Model: "blind"},
		{ID: "claimed", Provider: "p", Model: "claimed", Caps: Capabilities{Vision: true}},
		{ID: "seer", Provider: "p", Model: "seer", Caps: Capabilities{Vision: true}},
		{ID: "no-projector", Provider: "p", Model: "no-projector", Caps: Capabilities{Vision: true}},
		{ID: "undeclared-seer", Provider: "p", Model: "undeclared-seer"},
	} {
		if err := r.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	for id, vision := range map[string]bool{"seer": true, "no-projector": false, "undeclared-seer": true} {
		d, _ := r.Get(config.DucklingID(id))
		if err := r.caps.Put(d.Provider, capabilityCacheModel(d), &Capabilities{Vision: vision}); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]string{
		"blind": VisionNone, "claimed": VisionDeclared, "seer": VisionVerified,
		"no-projector": VisionRefuted, "undeclared-seer": VisionNone,
	}
	for _, d := range r.List() {
		if d.VisionStatus != want[string(d.ID)] {
			t.Errorf("%s vision_status = %q, want %q", d.ID, d.VisionStatus, want[string(d.ID)])
		}
	}
}
