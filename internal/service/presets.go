package service

import (
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
)

// Project presets (B-459).
//
// A local web page needs a run command, a URL, a health check and a smoke
// that fits it, and those lived in separate project settings a newcomer found
// only after the code existed and nothing would preview. A preset, chosen when
// the project starts, sets them, records the delivery conventions in the
// project memory (which every build prompt carries) and hands the same
// conventions to the intake as a reference document, so the requirements are
// written for the thing that will actually be served. The person's own brief
// is never edited.

// ProjectPreset is one kind of project Ducklab can set up.
type ProjectPreset struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Summary     string `json:"summary"`
	conventions string
}

var projectPresets = []ProjectPreset{
	{
		ID:      "web-page",
		Label:   "A web page that runs locally (one file)",
		Summary: "One self-contained index.html, opened from a small local server. No build step, no dependencies.",
		conventions: "- Deliver one self-contained `index.html` at the project root: HTML, CSS and JavaScript inline, no external requests, no build step, no package dependencies.\n" +
			"- It is served by the project's run command (Python's `http.server` on the project's port) and must work opened that way.\n" +
			"- Behaviour tests live in `tests/*.test.mjs` and run with Node's built-in runner: keep a `package.json` whose `test` script is `node --test tests/`. Logic the tests need goes in an inline `<script type=\"module\">` that also exports to a sibling `logic.mjs` only if the tests cannot reach it otherwise.\n",
	},
	{
		ID:      "web-app",
		Label:   "A local web app (several files, no build step)",
		Summary: "index.html plus plain CSS and JavaScript modules, opened from a small local server. No build step, no dependencies.",
		conventions: "- Deliver a static web app at the project root: `index.html` plus plain CSS files and ES modules (`*.mjs` or `type=\"module\"` scripts). No bundler, no build step, no package dependencies, no external requests.\n" +
			"- It is served by the project's run command (Python's `http.server` on the project's port) and must work opened that way.\n" +
			"- Behaviour tests live in `tests/*.test.mjs` and run with Node's built-in runner: keep a `package.json` whose `test` script is `node --test tests/`. Put logic in modules the tests can import directly.\n",
	},
}

func presetByID(id string) (ProjectPreset, bool) {
	for _, p := range projectPresets {
		if p.ID == id {
			return p, true
		}
	}
	return ProjectPreset{}, false
}

// ProjectPresets lists what the start flow can offer.
func (s *Service) ProjectPresets() []ProjectPreset {
	return append([]ProjectPreset(nil), projectPresets...)
}

// presetPort picks a port for the project: stable for the same project id,
// and free at the moment of choosing (several projects run side by side).
func presetPort(projectID string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(projectID))
	base := 41000 + int(h.Sum32()%900)
	for i := 0; i < 100; i++ {
		port := base + i
		if l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			_ = l.Close()
			return port
		}
	}
	return base
}

// presetPython finds the Python that serves a static preset. Review of #122:
// `python3` is not guaranteed (Windows usually installs `python`), and a
// preset that fails only when the app is first launched fails far from the
// choice that caused it. Resolved before the project is created; an absent
// Python refuses the preset with the fix.
var presetPython = func() (string, error) {
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil && path != "" {
			return name, nil
		}
	}
	return "", fmt.Errorf("this preset serves the page with Python's built-in web server, and no python3 or python was found on PATH; install Python 3 or choose \"Something else\"")
}

// applyPreset configures the project for the preset and returns the path of
// the reference document handed to the intake.
func applyPreset(projectRoot, projectID, brief, python string, p ProjectPreset) (string, error) {
	tomlPath := filepath.Join(projectRoot, ".ducklab", "project.toml")
	cfg, err := config.LoadProject(tomlPath)
	if err != nil {
		return "", err
	}
	port := presetPort(projectID)
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	cfg.Run.Command = fmt.Sprintf("%s -m http.server %d --bind 127.0.0.1", python, port)
	cfg.Run.URL = url
	cfg.Run.Health = url
	// The product smoke must not start a second server on the port the
	// running app holds; checking the deliverable exists is what a static
	// page can honestly prove without a browser.
	// Python, not `test`: `test` does not exist under Windows' cmd /C.
	cfg.Run.Smoke = python + ` -c "import os,sys; sys.exit(0 if os.path.isfile('index.html') else 1)"`
	cfg.Run.SmokeExpect = "exit"
	if err := config.SaveProject(tomlPath, cfg); err != nil {
		return "", err
	}

	conventions := "## Project setup: " + p.Label + "\n\n" + p.conventions
	mem, err := artifact.LoadMemory(projectRoot)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(mem.Description) == "" && strings.TrimSpace(brief) != "" {
		mem.Description = truncateRunes(strings.TrimSpace(brief), 600)
	}
	mem.Conventions = strings.TrimSpace(strings.TrimSpace(mem.Conventions) + "\n" + p.conventions)
	if err := artifact.SaveMemory(projectRoot, mem); err != nil {
		return "", err
	}

	ref := filepath.Join(projectRoot, ".ducklab", "preset.md")
	body := "# Delivery constraints chosen when the project was created\n\n" +
		"These are settled: write requirements and specifications for this delivery, " +
		"not for another stack.\n\n" + conventions +
		fmt.Sprintf("\nThe app is served at %s by `%s`.\n", url, cfg.Run.Command)
	if err := os.WriteFile(ref, []byte(body), 0o644); err != nil {
		return "", err
	}
	return ref, nil
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
