package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/vcs"
)

// An omitted mode is an engine decision, not a launcher convenience. Desktop,
// autopilot, and MCP all enter through RunStart, so each must receive the same
// phase setting, project habit, and final solo fallback, persisted with why.
func TestOmittedBuildModeHasOneResolutionAndProvenanceAcrossEntrances(t *testing.T) {
	for _, tc := range []struct {
		name           string
		settingsMode   string
		projectMode    config.Mode
		wantMode       string
		wantModeSource string
	}{
		{name: "settings phase default wins over project habit", settingsMode: "pair", projectMode: config.ModeSolo, wantMode: "pair", wantModeSource: "settings"},
		{name: "project habit fills an unset settings phase default", projectMode: config.ModePair, wantMode: "pair", wantModeSource: "project"},
		{name: "solo is the final fallback", wantMode: "solo", wantModeSource: "fallback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno", "pato-dos")
			s.cfg.Defaults.BuildMode = tc.settingsMode
			projectID, dir := omittedModeProject(t, s, tc.projectMode)

			for _, entrance := range []struct{ name, origin string }{
				{name: "desktop"},
				{name: "autopilot", origin: "autopilot"},
				{name: "MCP", origin: "mcp"},
			} {
				t.Run(entrance.name, func(t *testing.T) {
					run, err := s.RunStart(context.Background(), projectID, RunRequest{
						TaskID: "T-001", Origin: entrance.origin, DryRun: true,
					})
					if err != nil {
						t.Fatal(err)
					}
					if run.Mode != tc.wantMode {
						t.Errorf("omitted mode = %q, want %q", run.Mode, tc.wantMode)
					}

					// state.json, not only the in-memory response, is the audit record.
					onDisk, err := os.ReadFile(filepath.Join(dir, ".ducklab", "runs", run.ID, "state.json"))
					if err != nil {
						t.Fatal(err)
					}
					var state map[string]interface{}
					if err := json.Unmarshal(onDisk, &state); err != nil {
						t.Fatal(err)
					}
					if got, _ := state["mode_source"].(string); got != tc.wantModeSource {
						t.Errorf("state.json mode_source = %q, want %q", got, tc.wantModeSource)
					}
				})
			}
		})
	}
}

// Every resolved seat is an audit fact: the record must say whether it was
// pinned in project.toml, supplied by Settings, chosen in this request, or
// spread by the resolver. Otherwise a reader can see who ran but not answer
// why that duckling won over the other available choices.
func TestRunRosterSourcesRecordEverySeatDecision(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configure  func(*testing.T, *Service, string)
		req        RunRequest
		wantSource string
	}{
		{
			name: "project",
			configure: func(t *testing.T, s *Service, dir string) {
				cfg, err := config.LoadProject(filepath.Join(dir, ".ducklab", "project.toml"))
				if err != nil {
					t.Fatal(err)
				}
				cfg.Roster[config.RoleImplementer] = "luna"
				if err := writeProjectTOML(filepath.Join(dir, ".ducklab", "project.toml"), cfg); err != nil {
					t.Fatal(err)
				}
			},
			req: RunRequest{Mode: "solo"}, wantSource: "project",
		},
		{
			name: "settings",
			configure: func(_ *testing.T, s *Service, _ string) {
				s.cfg.Defaults.ModeDucklings = map[string][]string{"solo": {"terra"}}
			},
			req: RunRequest{Mode: "solo"}, wantSource: "settings",
		},
		{
			name:      "request",
			configure: func(_ *testing.T, _ *Service, _ string) {},
			req:       RunRequest{Mode: "solo", Ducklings: []string{"terra"}}, wantSource: "request",
		},
		{
			name:      "spread",
			configure: func(_ *testing.T, _ *Service, _ string) {},
			req:       RunRequest{Mode: "solo"}, wantSource: "spread",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := serviceWithDucklings(t, "luna", "terra")
			projectID, dir := omittedModeProject(t, s, "")
			tc.configure(t, s, dir)
			tc.req.TaskID = "T-001"

			run, err := s.RunStart(context.Background(), projectID, tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.waitForRun(context.Background(), run.ID); err != nil {
				t.Fatal(err)
			}

			// state.json is the durable answer, including after the engine restarts.
			stateBytes, err := os.ReadFile(filepath.Join(dir, ".ducklab", "runs", run.ID, "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			var state struct {
				RosterSources map[string]string `json:"roster_sources"`
			}
			if err := json.Unmarshal(stateBytes, &state); err != nil {
				t.Fatal(err)
			}
			if got := state.RosterSources["implementer"]; got != tc.wantSource {
				t.Errorf("state.json implementer source = %q, want %q", got, tc.wantSource)
			}

			// run_get must expose the same provenance rather than making clients
			// read the run directory themselves.
			detail, err := s.RunGet(context.Background(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got := detail.Run.RosterSources["implementer"]; got != tc.wantSource {
				t.Errorf("run_get implementer source = %q, want %q", got, tc.wantSource)
			}
		})
	}
}

func omittedModeProject(t *testing.T, s *Service, mode config.Mode) (string, string) {
	t.Helper()
	projectID, dir := projectWithConfig(t, s, "omitted-mode")
	cfg, err := config.LoadProject(filepath.Join(dir, ".ducklab", "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if mode != "" {
		cfg.Modes = config.Modes{config.StageBuild: mode}
	}
	if err := writeProjectTOML(filepath.Join(dir, ".ducklab", "project.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(artifact.DocsDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path(dir, artifact.KindPlan), []byte("## M-001 — Core\n\n### T-001 — Resolve modes\n\nDo it.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git := vcs.New(dir)
	if err := git.Init(); err != nil {
		t.Fatal(err)
	}
	return projectID, dir
}
