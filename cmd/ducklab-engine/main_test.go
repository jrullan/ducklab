package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/build"
)

func TestEngineVersionCommandsPrintAndExitWithoutStartingEngine(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "ducklab-engine")
	buildCtx, buildCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer buildCancel()
	buildCmd := exec.CommandContext(buildCtx, "go", "build", "-o", binary, "./cmd/ducklab-engine")
	buildCmd.Dir = root
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build ducklab-engine: %v\n%s", err, output)
	}

	for _, arg := range []string{"version", "--version"} {
		t.Run(arg, func(t *testing.T) {
			envRoot := t.TempDir()
			stateHome := filepath.Join(envRoot, "state")
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(envRoot, "config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(envRoot, "data"))
			t.Setenv("XDG_STATE_HOME", stateHome)
			t.Setenv("LocalAppData", filepath.Join(envRoot, "local"))
			t.Setenv("AppData", filepath.Join(envRoot, "roaming"))

			engineJSON := filepath.Join(stateHome, "ducklab", "engine.json")
			if err := os.MkdirAll(filepath.Dir(engineJSON), 0o755); err != nil {
				t.Fatal(err)
			}
			live := map[string]any{"pid": os.Getpid(), "port": 43123, "token": "live-token", "version": "live", "started_at": "now", "state_dir": "live-state"}
			original, err := json.Marshal(live)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(engineJSON, original, 0o600); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, arg)
			cmd.Dir = root
			output, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("%s did not exit promptly (it may have started the engine): %v\n%s", arg, ctx.Err(), output)
			}
			if err != nil {
				t.Fatalf("%s failed: %v\n%s", arg, err, output)
			}
			text := string(output)
			if !strings.Contains(text, build.Semver()) || !strings.Contains(text, build.Provenance()) {
				t.Fatalf("%s output = %q, want semver %q and provenance %q", arg, text, build.Semver(), build.Provenance())
			}
			if strings.Contains(text, "listening on") {
				t.Fatalf("%s started an engine listener: %q", arg, text)
			}
			got, err := os.ReadFile(engineJSON)
			if err != nil {
				t.Fatalf("%s removed engine.json: %v", arg, err)
			}
			if string(got) != string(original) {
				t.Fatalf("%s mutated live engine.json: got %q, want %q", arg, got, original)
			}
		})
	}
}
