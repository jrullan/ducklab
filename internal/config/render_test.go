package config

import (
	"os"
	"path/filepath"
	"strings"
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

// B-466: an empty [render] table — which SaveProject writes into every
// project.toml — is the same as no [render]; any content declares it.
func TestAnEmptyRenderTableIsNotARenderContract(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "project.toml")
	if err := SaveProject(path, DefaultProject("demo", "Demo")); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "[render]") {
		t.Log("the encoder no longer writes an empty [render]; the load-side guard still holds")
	}
	p, err := LoadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.RenderConfigured {
		t.Fatalf("a saved default project reads as having a render contract:\n%s", data)
	}
	for key, value := range map[string]string{"render.artifacts": "shots/*.png", "render.command": "node shot.mjs", "render.timeout_s": "30"} {
		q := DefaultProject("demo", "Demo")
		if err := SetKey(q, key, value); err != nil {
			t.Fatal(err)
		}
		if !q.RenderConfigured {
			t.Errorf("%s=%s does not declare [render]", key, value)
		}
	}
	q := DefaultProject("demo", "Demo")
	if err := SetKey(q, "render.command", ""); err != nil {
		t.Fatal(err)
	}
	if q.RenderConfigured {
		t.Error("setting render.command to empty declared [render]")
	}
}
