package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProjectRenderContract(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "project.toml")
	data := []byte(`schema = 1
id = "demo"
name = "Demo"
autonomy = "guarded"
[render]
command = "node capture.mjs"
url = "http://example/{engine}/{token}"
scenes = ["/", "/runs"]
viewport = "800x600"
timeout_s = 9
artifacts = "captures/*.png"
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := LoadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	if !p.RenderConfigured || p.Render.Command != "node capture.mjs" || p.Render.Viewport != "800x600" || len(p.Render.Scenes) != 2 {
		t.Fatalf("render contract not loaded: %#v configured=%v", p.Render, p.RenderConfigured)
	}
}

func TestRenderViewportValidation(t *testing.T) {
	p := DefaultProject("demo", "Demo")
	p.Render.Viewport = "bad"
	if err := p.Validate("project.toml"); err == nil {
		t.Fatal("invalid viewport accepted")
	}
}
