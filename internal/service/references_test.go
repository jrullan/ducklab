package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

// A wiki outside the project root is the commonest home of adoption
// context, and the fs tools rightly cannot reach it. The loader is the one
// channel: bounded, ordered, and honest about what its caps dropped.
func TestLoadReferencesIsBoundedAndHonest(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "b-module.md"), []byte("module notes"), 0o644)
	os.WriteFile(filepath.Join(dir, "a-overview.md"), []byte(strings.Repeat("x", refPerFileChars+500)), 0o644)
	os.WriteFile(filepath.Join(dir, "image.png"), []byte("binary"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "deep.txt"), []byte("deep note"), 0o644)

	rendered, loaded, dropped, err := loadReferences([]string{dir}, config.References{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 3 || len(dropped) != 0 {
		t.Fatalf("loaded %d dropped %d: %+v %+v", len(loaded), len(dropped), loaded, dropped)
	}
	if !loaded[0].Truncated || loaded[0].Chars != refPerFileChars {
		t.Errorf("oversize file not truncated to the cap: %+v", loaded[0])
	}
	for _, want := range []string{"## Reference documents", "the code is the truth", "BACKGROUND, not as scope", "never derive requirements from it", "a-overview.md", "module notes", "deep note", "[truncated"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered lacks %q", want)
		}
	}
	if strings.Contains(rendered, "image.png") {
		t.Error("a binary was loaded as a reference")
	}
	// A path that does not exist refuses the launch, naming itself.
	if _, _, _, err := loadReferences([]string{filepath.Join(dir, "nope.md")}, config.References{}); err == nil || !strings.Contains(err.Error(), "nope.md") {
		t.Errorf("missing reference not refused by name: %v", err)
	}
}

// A mature multi-module wiki is bigger than a README: the defaults cut
// MiEmpresa's spec set to a fifth. The budget is the project's to declare.
func TestLoadReferencesHonoursProjectCaps(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "big.md"), []byte(strings.Repeat("x", refPerFileChars+500)), 0o644)
	os.WriteFile(filepath.Join(dir, "second.md"), []byte("second"), 0o644)

	_, loaded, dropped, err := loadReferences([]string{dir}, config.References{
		PerFileChars: refPerFileChars + 1_000,
		MaxFiles:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Truncated || loaded[0].Chars != refPerFileChars+500 {
		t.Fatalf("raised per-file cap not honoured: %+v", loaded)
	}
	if len(dropped) != 1 || !strings.Contains(dropped[0], "second.md") {
		t.Fatalf("lowered max_files not honoured: %+v", dropped)
	}
}
