package duckling

import (
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

// Vision declared in config must survive into the registry: it was saved
// faithfully and dropped in the conversion, so the list reported false for
// every duckling and the edit form un-ticked the box the person had just
// ticked — while attached screenshots reached no model.
func TestDeclaredVisionSurvivesIntoTheRegistry(t *testing.T) {
	yes := true
	d := FromConfig("seer", config.Duckling{
		Provider: "p", Model: "m",
		Caps: config.Caps{Vision: &yes},
	})
	if !d.Caps.Vision {
		t.Fatal("vision = true in config listed as false")
	}
}
