// Package verify handles gate detection and execution. The gate's exit code
// is the only signal — parsing test output to decide "it looks fine" is forbidden.
package verify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/capability"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/xplat"
)

// Gate is a verification tier.
type Gate string

const (
	GateTests  Gate = "tests"
	GateBuild  Gate = "build"
	GateLint   Gate = "lint"
	GateNone   Gate = "none"
	GateCustom Gate = "custom"
)

// Result is the result of running a gate.
type Result struct {
	Gate     Gate
	Command  string
	ExitCode int
	Output   string
	Duration float64
}

// MissingToolchain remains an alias for callers that classify detection
// failures. Stack knowledge and the concrete error now live with the adapter.
type MissingToolchain = capability.MissingToolchain

// Detect is the compatibility facade for project-gate resolution. The
// verifier knows only the resolved kind and command; stack detection belongs
// to capability providers.
func Detect(root string) (Gate, string, error) {
	return DetectWith(root, config.Capabilities{Auto: true})
}

// DetectWith applies the project's adapter selection while resolving a gate.
// An already configured gate is never rewritten; this governs discovery and
// explicit adoption only.
func DetectWith(root string, selection config.Capabilities) (Gate, string, error) {
	profile, err := capability.DefaultRegistry().ResolveProject(capability.Context{
		ProjectRoot: root, Policies: selection.Policy,
	}, selection.Auto, selection.Enabled, selection.Disabled)
	if err != nil {
		return GateNone, "", err
	}
	if profile.Gate == nil {
		return GateNone, "", nil
	}
	return Gate(profile.Gate.Kind), profile.Gate.Command, nil
}

// Identity names the run and project whose process tree is being executed.
type Identity struct {
	RunID     string
	ProjectID string
}

// Run executes the gate command in the given root.
func Run(ctx context.Context, root string, cfg config.Verify, identities ...Identity) (*Result, error) {
	identity := Identity{}
	if len(identities) > 0 {
		identity = identities[0]
	}
	if identity.RunID == "" {
		identity.RunID = fmt.Sprintf("manual-%d", time.Now().Unix())
	}
	if identity.ProjectID == "" {
		identity.ProjectID = "manual"
	}
	gate := Gate(cfg.Mode)
	cmd := ""

	if gate == "auto" {
		detected, detectedCmd, err := Detect(root)
		if err != nil {
			return nil, err
		}
		gate = detected
		cmd = detectedCmd
	} else {
		switch gate {
		case GateTests:
			cmd = cfg.Tests
		case GateBuild:
			cmd = cfg.Build
		case GateLint:
			cmd = cfg.Lint
		case GateCustom:
			cmd = cfg.Custom
		case GateNone:
			return &Result{
				Gate:     GateNone,
				Command:  "",
				ExitCode: 0,
				Output:   "no executable gate",
			}, nil
		}
	}

	if cmd == "" {
		return &Result{
			Gate:     GateNone,
			Command:  "",
			ExitCode: 0,
			Output:   "no command configured for gate",
		}, nil
	}

	// I3: nothing is unbounded, and this is the worst place to forget it — the
	// gate runs on every round of every run. timeout_s was read from the
	// config, stored, and never applied, so a command that hangs held the run
	// open until a person noticed.
	timeoutS := cfg.TimeoutS
	if timeoutS <= 0 {
		timeoutS = 900
	}
	// Bounded by the timeout AND by the caller: the run's own context rides
	// in, so an abort — or a graceful engine shutdown, which cancels every
	// run — kills the gate's process group instead of orphaning a pytest
	// that holds database connections for the next run to trip over. Four
	// such orphans, one per stalled T-075 attempt, each born at an engine
	// restart that took the old Background timer down with it.
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutS)*time.Second)
	defer cancel()
	started := time.Now()

	// A gate can start product binaries, which must not inherit the engine's
	// registry or configuration. Give the entire gate process tree a fresh,
	// disposable state home instead.
	env, cleanup, err := isolatedStateEnvironment(identity)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	shellCmd := xplat.ShellContext(ctx, root, env, cmd)
	output, err := shellCmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(interface{ ExitCode() int }); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// Binary missing — treat as UNVERIFIED, not FAILED
			return &Result{
				Gate:     GateNone,
				Command:  cmd,
				ExitCode: -1,
				Output:   fmt.Sprintf("gate command failed to start: %v", err),
			}, nil
		}
	}

	if ctx.Err() != nil {
		// Nothing was verified, so the verdict must be UNVERIFIED rather than
		// FAILED: the code did not fail, our limit did (05 §5.2). GateNone is
		// what Verdict reads for that.
		return &Result{
			Gate:    GateNone,
			Command: cmd,
			// Non-zero, so nothing downstream reads a killed gate as a pass.
			ExitCode: -1,
			Output: fmt.Sprintf("%s\n[the gate timed out after %ds and was killed]",
				string(output), timeoutS),
			Duration: time.Since(started).Seconds(),
		}, nil
	}

	return &Result{
		Gate:     gate,
		Command:  cmd,
		ExitCode: exitCode,
		Output:   string(output),
	}, nil
}

// Verdict computes the verdict from a gate result.
func Verdict(result *Result) string {
	if result.Gate == GateNone {
		return "UNVERIFIED"
	}
	if result.ExitCode == 0 {
		return "PASSED"
	}
	return "FAILED"
}

// IsGreen returns whether the gate result is green.
func IsGreen(result *Result) bool {
	return result.ExitCode == 0 && result.Gate != GateNone
}

// IsRed returns whether the gate result is red.
func IsRed(result *Result) bool {
	return result.ExitCode != 0 && result.Gate != GateNone
}

// isolatedStateEnvironment returns an environment whose state locations all
// live under a new temporary directory. The directory remains available for the
// full child process tree and is removed once the gate has exited.
func isolatedStateEnvironment(identities ...Identity) ([]string, func(), error) {
	identity := Identity{}
	if len(identities) > 0 {
		identity = identities[0]
	}
	if identity.RunID == "" {
		identity.RunID = fmt.Sprintf("manual-%d", time.Now().Unix())
	}
	if identity.ProjectID == "" {
		identity.ProjectID = "manual"
	}
	stateRoot, err := os.MkdirTemp("", "ducklab-verify-")
	if err != nil {
		return nil, nil, fmt.Errorf("create isolated gate state: %w", err)
	}

	values := map[string]string{
		"DUCKLAB_RUN_ID":     identity.RunID,
		"DUCKLAB_PROJECT_ID": identity.ProjectID,
		"XDG_CONFIG_HOME":    filepath.Join(stateRoot, "config"),
		"XDG_DATA_HOME":      filepath.Join(stateRoot, "data"),
		"XDG_STATE_HOME":     filepath.Join(stateRoot, "state"),
		"HOME":               filepath.Join(stateRoot, "home"),
		"USERPROFILE":        filepath.Join(stateRoot, "home"),
		"AppData":            filepath.Join(stateRoot, "appdata"),
		"LocalAppData":       filepath.Join(stateRoot, "localappdata"),
	}
	// Isolate engine state, not content-addressed build caches. Scrubbing HOME
	// otherwise makes every gate redownload and rebuild its toolchain.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		cache := func(envKey, def string) {
			if os.Getenv(envKey) == "" {
				values[envKey] = def
			}
		}
		// An explicit GOPATH outranks the HOME-derived default, and the
		// module cache follows whichever GOPATH won — T-050's spec: a
		// custom GOPATH with a hardcoded HOME-derived GOMODCACHE splits
		// the toolchain across two worlds.
		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			gopath = filepath.Join(home, "go")
		}
		cache("GOPATH", gopath)
		moduleRoot := gopath
		if i := strings.IndexAny(moduleRoot, string(os.PathListSeparator)); i >= 0 {
			moduleRoot = moduleRoot[:i]
		}
		// GOMODCACHE is also where Go stores downloaded toolchains selected by
		// GOTOOLCHAIN, so preserving it preserves both download caches.
		cache("GOMODCACHE", filepath.Join(moduleRoot, "pkg", "mod"))
		cache("GOCACHE", filepath.Join(home, ".cache", "go-build"))
		cache("npm_config_cache", filepath.Join(home, ".npm"))
	}
	// A scrubbed HOME has no .gitconfig, and the service tests commit in
	// their temp repos: without an identity every one of them dies at
	// "please tell me who you are". The gate signs as itself — a fixed,
	// obviously-synthetic identity, so no test commit ever wears the
	// person's name.
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME":     "ducklab gate",
		"GIT_AUTHOR_EMAIL":    "gate@ducklab.invalid",
		"GIT_COMMITTER_NAME":  "ducklab gate",
		"GIT_COMMITTER_EMAIL": "gate@ducklab.invalid",
	} {
		if os.Getenv(k) == "" {
			values[k] = v
		}
	}

	// Remove inherited values first. Windows environment variable names are
	// case-insensitive, so use EqualFold on every platform to avoid duplicates.
	env := make([]string, 0, len(os.Environ())+len(values))
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		isolated := false
		for stateKey := range values {
			if strings.EqualFold(key, stateKey) {
				isolated = true
				break
			}
		}
		if !isolated {
			env = append(env, entry)
		}
	}
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env, func() { _ = os.RemoveAll(stateRoot) }, nil
}
