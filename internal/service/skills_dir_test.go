package service

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/xplat"
)

// The global skills directory is <data-dir>/skills (02 §1). DataDir already
// ends in /ducklab; a second join buried global skills at
// data/ducklab/ducklab/skills and the first real one was invisible (B-091).
func TestGlobalSkillsDirIsNotDoubled(t *testing.T) {
	dir := globalSkillsDir()
	if dir == "" {
		t.Skip("no data dir on this machine")
	}
	base, err := xplat.DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "skills"); dir != want {
		t.Errorf("globalSkillsDir() = %q, want %q", dir, want)
	}
	if strings.Contains(dir, filepath.Join("ducklab", "ducklab")) {
		t.Errorf("globalSkillsDir() = %q doubles the app dir", dir)
	}
}
