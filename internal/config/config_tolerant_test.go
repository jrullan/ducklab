package config

import (
	"os"
	"path/filepath"
	"testing"
)

// B-075: T-071's implementer declared the very key it was adding
// (verify.link_deps) in the live project.toml, and the running engine — one
// version older — refused to load the project at all, deadlocking the run
// that would have taught it the key. Unknown keys warn; they never refuse.
func TestLoadProjectToleratesKeysFromANewerTree(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "project.toml")
	os.WriteFile(path, []byte("schema = 1\nid = \"p\"\nname = \"p\"\n[verify]\n  mode = \"tests\"\n  tests = \"go test ./...\"\n  from_the_future = [\"x\"]\n"), 0o644)
	p, err := LoadProject(path)
	if err != nil {
		t.Fatalf("a key from a newer schema refused the whole project: %v", err)
	}
	if p.Verify.Tests != "go test ./..." {
		t.Errorf("known keys lost: %+v", p.Verify)
	}
	keys := UnknownProjectKeys(path)
	if len(keys) != 1 || keys[0] != "verify.from_the_future" {
		t.Errorf("unknown keys = %v", keys)
	}
	// A genuinely malformed file still refuses.
	os.WriteFile(path, []byte("schema = ["), 0o644)
	if _, err := LoadProject(path); err == nil {
		t.Error("malformed TOML accepted")
	}
}
