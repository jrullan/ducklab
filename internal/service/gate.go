package service

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/verify"
)

// Establishing a gate.
//
// Detection runs once, at `project init`. On an empty folder it finds nothing,
// records `none`, and never looks again — so a project that acquires a test
// suite on its second day keeps producing UNVERIFIED runs, and nobody finds
// out until they wonder why nothing ever says PASSED.
//
// A gate is never adopted behind someone's back. It decides what a verdict
// means, and changing that silently would make two runs incomparable while
// both claimed to be measured the same way. So detection reports, and a person
// adopts.

// GateStatus is what a project's gate is, and what it could be.
type GateStatus struct {
	// Mode and Command are what the project is configured with now.
	Mode     string   `json:"mode"`
	Command  string   `json:"command"`
	LinkDeps []string `json:"link_deps"`
	Setup    string   `json:"setup"`
	// Detected and DetectedCommand are what detection finds in the tree today.
	Detected        string `json:"detected"`
	DetectedCommand string `json:"detected_command,omitempty"`
	// Adoptable is true when detection found something the project is not
	// using. That is the only case worth acting on.
	Adoptable bool `json:"adoptable"`
	// Verdict is what runs currently produce at best. Spelled out because
	// "mode: none" does not obviously mean "nothing can ever pass".
	BestVerdict string `json:"best_verdict"`
}

// ProjectGate reports the configured gate beside the detectable one.
func (s *Service) ProjectGate(ctx context.Context, projectID string) (*GateStatus, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	projCfg, err := config.LoadProject(filepath.Join(entry.Path, ".ducklab", "project.toml"))
	if err != nil {
		return nil, err
	}

	current := verify.Gate(projCfg.Verify.Mode)
	detected, detectedCmd, derr := verify.DetectWith(entry.Path, projCfg.Capabilities)
	if derr != nil {
		detected = verify.GateNone
	}

	st := &GateStatus{
		Mode:            string(current),
		Command:         gateCommandFor(projCfg.Verify),
		LinkDeps:        projCfg.Verify.LinkDeps,
		Setup:           projCfg.Verify.Setup,
		Detected:        string(detected),
		DetectedCommand: detectedCmd,
		BestVerdict:     "PASSED",
	}
	if current == verify.GateNone {
		st.BestVerdict = "UNVERIFIED"
	}
	// Only when there is something better than what is in force. Offering to
	// adopt what is already configured is noise.
	st.Adoptable = detected != verify.GateNone && current == verify.GateNone
	return st, nil
}

// ProjectGateAdopt writes the detected gate into the project.
func (s *Service) ProjectGateAdopt(ctx context.Context, projectID string) (*GateStatus, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(entry.Path, ".ducklab", "project.toml")
	projCfg, err := config.LoadProject(path)
	if err != nil {
		return nil, err
	}
	detected, cmd, err := verify.DetectWith(entry.Path, projCfg.Capabilities)
	if err != nil || detected == verify.GateNone {
		return nil, fmt.Errorf(
			"nothing runnable was found in %s. Set a gate by hand:\n"+
				"  ducklab project set verify.mode tests\n"+
				"  ducklab project set verify.tests \"<command>\"", entry.Path)
	}

	projCfg.Verify.Mode = string(detected)
	switch detected {
	case verify.GateTests:
		projCfg.Verify.Tests = cmd
	case verify.GateBuild:
		projCfg.Verify.Build = cmd
	case verify.GateLint:
		projCfg.Verify.Lint = cmd
	}
	if err := config.SaveProject(path, projCfg); err != nil {
		return nil, err
	}
	return s.ProjectGate(ctx, projectID)
}

// gateCommandFor reads the command a verify config would run.
func gateCommandFor(v config.Verify) string {
	switch verify.Gate(v.Mode) {
	case verify.GateTests:
		return v.Tests
	case verify.GateBuild:
		return v.Build
	case verify.GateLint:
		return v.Lint
	case "custom":
		return v.Custom
	}
	return ""
}

// gateAdvice is the one-line note a run leaves when it had no gate.
//
// Returned rather than acted on. A run that quietly adopted a gate would
// change what its own verdict means halfway through, and the person reading it
// would have no way to know which rules applied.
func gateAdvice(projectRoot string, v config.Verify, selections ...config.Capabilities) string {
	if verify.Gate(v.Mode) != verify.GateNone {
		return ""
	}
	selection := config.Capabilities{Auto: true}
	if len(selections) > 0 {
		selection = selections[0]
	}
	detected, cmd, err := verify.DetectWith(projectRoot, selection)
	if err != nil || detected == verify.GateNone {
		return "this project has no gate, so no run can do better than UNVERIFIED"
	}
	return fmt.Sprintf(
		"this project has no gate, so no run can do better than UNVERIFIED — "+
			"but %q looks runnable here now. Adopt it with: ducklab project gate --adopt", cmd)
}

// GateResult is a gate that was actually run.
type GateResult struct {
	Gate     string  `json:"gate"`
	Command  string  `json:"command"`
	ExitCode int     `json:"exit_code"`
	Output   string  `json:"output"`
	Duration float64 `json:"duration_s"`
	// Green is the one thing a reader wants first, computed rather than left
	// to every client to derive from an exit code.
	Green bool `json:"green"`
}

// GateRun executes the project's gate and reports what happened.
//
// On demand, never on a page load. A gate is a whole test suite on a real
// project, and a screen that ran one every time it opened would make looking
// expensive — which is how people stop looking.
func (s *Service) GateRun(ctx context.Context, projectID string) (*GateResult, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	projCfg, err := config.LoadProject(filepath.Join(entry.Path, ".ducklab", "project.toml"))
	if err != nil {
		return nil, err
	}
	res, err := verify.Run(ctx, entry.Path, projCfg.Verify, verify.Identity{RunID: "", ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	return &GateResult{
		Gate: string(res.Gate), Command: res.Command,
		ExitCode: res.ExitCode, Output: res.Output, Duration: res.Duration,
		// A gate that could not run is not green, whatever its exit code says.
		Green: res.ExitCode == 0 && res.Gate != verify.GateNone,
	}, nil
}
