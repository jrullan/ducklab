package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jrullan/ducklab/internal/release"
)

// The list showed "v0.5.0 tagged" and "v0.5.0 drafted" as if there were two
// releases: a stale .proposed had survived beside the cut notes (a checkout
// restored it after the cut removed it). A draft of a tagged version cannot
// be cut — ReleaseCut refuses a laid tag — so it is not a decision waiting
// for anyone, and the list leaves it out.
func TestReleaseListHidesAStaleDraftOfATaggedVersion(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	g := gitProject(t, dir)
	os.MkdirAll(release.Dir(dir), 0o755)
	notes := "---\nkind: release\nversion: v0.5.0\nsince: v0.4.0\ntasks: 2\n---\n\n# v0.5.0\n"
	if err := os.WriteFile(filepath.Join(release.Dir(dir), "v0.5.0.md"), []byte(notes), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release.Dir(dir), "v0.5.0.md.proposed"), []byte(notes), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release.Dir(dir), "v0.6.0.md.proposed"), []byte("---\nkind: release\nversion: v0.6.0\nsince: v0.5.0\ntasks: 1\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.AddAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit("notes"); err != nil {
		t.Fatal(err)
	}
	if err := g.Tag("v0.5.0", "release"); err != nil {
		t.Fatal(err)
	}

	list, err := s.ReleaseList(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list = %+v, want the cut v0.5.0 and the v0.6.0 draft only", list)
	}
	if list[0].Version != "v0.6.0" || !list[0].Drafted || list[0].Tagged {
		t.Errorf("first = %+v, want the v0.6.0 draft", list[0])
	}
	if list[1].Version != "v0.5.0" || list[1].Drafted || !list[1].Tagged {
		t.Errorf("second = %+v, want v0.5.0 cut and tagged, once", list[1])
	}
}
