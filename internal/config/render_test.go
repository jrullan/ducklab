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

// Review of #125: a bare [render] a person wrote is the documented way to
// capture with [run].command, and it survives load and save; the empty table
// older SaveProject versions wrote (every key, every value zero) does not
// declare anything, and SaveProject no longer writes it.
func TestABareRenderTableIsDeclaredAndTheGeneratedOneIsNot(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("schema = 1\nid = \"demo\"\nname = \"Demo\"\nautonomy = \"guarded\"\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	bare := write("bare.toml", "[run]\ncommand = \"npm start\"\n[render]\n")
	p, err := LoadProject(bare)
	if err != nil {
		t.Fatal(err)
	}
	if !p.RenderConfigured {
		t.Fatal("a bare [render] is not declared")
	}
	if err := SaveProject(bare, p); err != nil {
		t.Fatal(err)
	}
	if again, err := LoadProject(bare); err != nil || !again.RenderConfigured {
		data, _ := os.ReadFile(bare)
		t.Fatalf("a bare [render] did not survive a save (%v):\n%s", err, data)
	}

	generated := write("generated.toml", "[run]\ncommand = \"npm start\"\n\n[render]\n  command = \"\"\n  url = \"\"\n  ready = \"\"\n  viewport = \"\"\n  timeout_s = 0\n  artifacts = \"\"\n")
	p, err = LoadProject(generated)
	if err != nil {
		t.Fatal(err)
	}
	if p.RenderConfigured {
		t.Fatal("the table older SaveProject versions wrote reads as declared")
	}
	if err := SaveProject(generated, p); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(generated); strings.Contains(string(data), "[render]") {
		t.Fatalf("SaveProject still writes an empty [render]:\n%s", data)
	}
}
