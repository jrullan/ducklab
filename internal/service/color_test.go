package service

import (
	"strings"
	"testing"
)

// Eight slots is what the palette has; past that it stops clearing the
// colour-vision floor and a ninth colour would only look distinct.
func TestAColourOutsideThePaletteIsRefused(t *testing.T) {
	s := writableService(t, "pato-uno")
	err := s.DucklingSet("pato-uno", DucklingView{Provider: "fake", Model: "m", Color: 9})
	if err == nil {
		t.Fatal("a ninth colour was accepted")
	}
	if !strings.Contains(err.Error(), "1-8") {
		t.Errorf("the error does not say the range: %v", err)
	}
}

func TestAChosenColourReachesTheRegistry(t *testing.T) {
	s := writableService(t, "pato-uno")
	if err := s.DucklingSet("pato-uno", DucklingView{Provider: "fake", Model: "m", Color: 5}); err != nil {
		t.Fatal(err)
	}
	d, err := s.ducklings.Get("pato-uno")
	if err != nil {
		t.Fatal(err)
	}
	if d.Color != 5 {
		t.Errorf("color = %d, want 5", d.Color)
	}
}

// The fleet listing ranged a map, so it came back in a different order on every
// call and anything that assigned meaning by position changed on reload.
func TestTheFleetListingIsOrdered(t *testing.T) {
	s := writableService(t, "zeta", "alpha", "mid")
	for i := 0; i < 5; i++ {
		list, err := s.DucklingList(nil)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, d := range list {
			ids = append(ids, string(d.ID))
		}
		if strings.Join(ids, ",") != "alpha,mid,zeta" {
			t.Fatalf("listing %d = %v", i, ids)
		}
	}
}
