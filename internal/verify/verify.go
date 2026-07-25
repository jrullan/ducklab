// Package verify handles gate detection and execution. The gate's exit code
// is the only signal — parsing test output to decide "it looks fine" is forbidden.
package verify

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// Detect auto-detects the verification gate for a project.
// Returns the gate and the command to run.
func Detect(root string) (Gate, string, error) {
	// Rung 1: Go tests
	if fileExists(filepath.Join(root, "go.mod")) {
		if commandSucceeds(root, "go test ./... -run XXX -count=1") {
			return GateTests, "go test ./...", nil
		}
		// Fall through to build
	}
	// Rung 2: Python pytest
	if fileExists(filepath.Join(root, "pytest.ini")) ||
		fileExists(filepath.Join(root, "pyproject.toml")) ||
		dirExists(filepath.Join(root, "tests")) {
		if commandSucceeds(root, "pytest -q --collect-only") {
			return GateTests, "pytest -q", nil
		}
	}
	// Rung 3: npm test
	if fileExists(filepath.Join(root, "package.json")) {
		if hasTestScript(filepath.Join(root, "package.json")) {
			return GateTests, "npm test --silent", nil
		}
	}
	// Rung 4: Cargo
	if fileExists(filepath.Join(root, "Cargo.toml")) {
		return GateTests, "cargo test", nil
	}
	// Rung 5: Go build
	if fileExists(filepath.Join(root, "go.mod")) {
		return GateBuild, "go build ./...", nil
	}
	// Rung 6: TypeScript
	if fileExists(filepath.Join(root, "tsconfig.json")) {
		return GateBuild, "npx tsc --noEmit", nil
	}
	// Rung 7: Python compile
	if hasPythonFiles(root) {
		return GateBuild, "python -m compileall -q .", nil
	}
	// Rung 8: golangci-lint
	if fileExists(filepath.Join(root, ".golangci.yml")) {
		return GateLint, "golangci-lint run", nil
	}
	return GateNone, "", nil
}

// Run executes the gate command in the given root.
func Run(root string, cfg config.Verify) (*Result, error) {
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

	shellCmd := xplat.Shell(root, nil, cmd)
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func commandSucceeds(root, cmd string) bool {
	shellCmd := xplat.Shell(root, nil, cmd)
	err := shellCmd.Run()
	return err == nil
}

func hasTestScript(packageJSONPath string) bool {
	data, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return false
	}
	content := string(data)
	// Simplified check: has a "test" script that's not the default stub
	return strings.Contains(content, `"test"`) &&
		!strings.Contains(content, `"test": "echo \"Error: no test specified\" && exit 1"`)
}

func hasPythonFiles(root string) bool {
	found := false
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if strings.HasSuffix(path, ".py") {
			found = true
			return fmt.Errorf("found")
		}
		return nil
	})
	return found
}
