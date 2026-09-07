package capability

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jrullan/ducklab/internal/xplat"
)

// MissingToolchain distinguishes a detected stack from a project with no
// gate. The marker is evidence; the missing runner is an environment blocker.
type MissingToolchain struct {
	Tool   string
	Marker string
}

func (e *MissingToolchain) Error() string {
	return fmt.Sprintf("the tree has %s but %s is not installed on this machine", e.Marker, e.Tool)
}

type Go struct{}

func (Go) ID() string { return "go" }
func (Go) Detect(ctx Context) Contributions {
	hasModule := fileExists(ctx.ProjectRoot, "go.mod")
	hasLint := fileExists(ctx.ProjectRoot, ".golangci.yml")
	if !hasModule && !hasLint {
		return Contributions{}
	}
	c := Contributions{Detection: Detection{Capability: "go"}}
	if hasModule {
		c.Detection.Evidence = append(c.Detection.Evidence, "go.mod")
		if commandSucceeds(ctx.ProjectRoot, "go test ./... -run XXX -count=1") {
			c.Gates = append(c.Gates, GateCandidate{Capability: "go", Kind: "tests", Command: "go test ./...", Scope: ".", Priority: 10})
		} else {
			c.Gates = append(c.Gates, GateCandidate{Capability: "go", Kind: "build", Command: "go build ./...", Scope: ".", Priority: 50})
		}
	}
	if hasLint {
		c.Detection.Evidence = append(c.Detection.Evidence, ".golangci.yml")
		c.Gates = append(c.Gates, GateCandidate{Capability: "go", Kind: "lint", Command: "golangci-lint run", Scope: ".", Priority: 80})
	}
	return c
}

type Python struct{}

func (Python) ID() string { return "python" }
func (Python) Detect(ctx Context) Contributions {
	markers := existing(ctx.ProjectRoot, "pytest.ini", "pyproject.toml")
	if dirExists(ctx.ProjectRoot, "tests") {
		markers = append(markers, "tests/")
	}
	hasFiles := hasPythonFiles(ctx.ProjectRoot)
	if len(markers) == 0 && !hasFiles {
		return Contributions{}
	}
	c := Contributions{Detection: Detection{Capability: "python", Evidence: markers}}
	if len(markers) > 0 && commandSucceeds(ctx.ProjectRoot, "pytest -q --collect-only") {
		c.Gates = append(c.Gates, GateCandidate{Capability: "python", Kind: "tests", Command: "pytest -q", Scope: ".", Priority: 20})
	}
	if hasFiles {
		if len(c.Detection.Evidence) == 0 {
			c.Detection.Evidence = []string{"**/*.py"}
		}
		c.Gates = append(c.Gates, GateCandidate{Capability: "python", Kind: "build", Command: pythonInterpreter() + " -m compileall -q .", Scope: ".", Priority: 70})
	}
	return c
}

type Node struct{}

func (Node) ID() string { return "node" }
func (Node) Detect(ctx Context) Contributions {
	var c Contributions
	rootPackage := filepath.Join(ctx.ProjectRoot, "package.json")
	if fileExistsPath(rootPackage) {
		c.Detection = Detection{Capability: "node", Evidence: []string{"package.json"}}
		if hasTestScript(rootPackage) {
			c.Gates = append(c.Gates, GateCandidate{Capability: "node", Kind: "tests", Command: "npm test --silent", Scope: ".", Priority: 30})
		}
	}
	frontendPackage := filepath.Join(ctx.ProjectRoot, "frontend", "package.json")
	if fileExistsPath(frontendPackage) && hasTestScript(frontendPackage) {
		// A Go service with a separately runnable frontend once measured only
		// its backend. Nested test suites are supplemental candidates so the
		// resolver composes them without teaching Go about Node or TypeScript.
		if c.Detection.Capability == "" {
			c.Detection = Detection{Capability: "node"}
		}
		c.Detection.Evidence = append(c.Detection.Evidence, "frontend/package.json")
		command := "cd frontend && npx vitest run"
		if fileExists(ctx.ProjectRoot, "frontend/tsconfig.json") {
			c.Detection.Evidence = append(c.Detection.Evidence, "frontend/tsconfig.json")
			command = "cd frontend && npx tsc --noEmit && npx vitest run"
		}
		c.Gates = append(c.Gates, GateCandidate{Capability: "node", Kind: "tests", Command: command, Scope: "frontend", Priority: 11, Supplemental: true})
	}
	return c
}

type Rust struct{}

func (Rust) ID() string { return "rust" }
func (Rust) Detect(ctx Context) Contributions {
	if !fileExists(ctx.ProjectRoot, "Cargo.toml") {
		return Contributions{}
	}
	return Contributions{
		Detection: Detection{Capability: "rust", Evidence: []string{"Cargo.toml"}},
		Gates:     []GateCandidate{{Capability: "rust", Kind: "tests", Command: "cargo test", Scope: ".", Priority: 40}},
	}
}

// InspectPlanTask catches new Rust modules whose proposed Cargo verification
// cannot discover them. Cargo compiles crate and target roots, not every .rs
// file under src/. H3c produced plausible `cargo test <filter>` probes for
// three disconnected modules; all document critics approved because ownership
// and probe text looked coherent even though no crate root could import them.
// This knowledge belongs to the Rust provider, not document orchestration.
func (Rust) InspectPlanTask(ctx PlanTaskContext) []Inspection {
	if !strings.Contains(strings.ToLower(ctx.Verification), "cargo") {
		return nil
	}
	produces := planFieldItems(ctx.Body, "Produces")
	var disconnected []string
	hasIntegrationRoot := false
	for _, artifact := range produces {
		kind, path, ok := strings.Cut(strings.TrimSpace(strings.Trim(artifact, "`")), ":")
		if !ok {
			continue
		}
		path = filepath.ToSlash(strings.TrimSpace(path))
		if kind == "file" && cargoTargetRoot(path) || kind == "dir" && (path == "src" || strings.HasPrefix(path, "tests")) {
			hasIntegrationRoot = true
		}
		if kind != "file" || !strings.HasPrefix(path, "src/") || filepath.Ext(path) != ".rs" || cargoTargetRoot(path) {
			continue
		}
		// Editing an existing module does not require new root wiring; its
		// reachability is exercised by the project gate. The planning defect is
		// specific to newly introduced, undiscoverable source artifacts.
		if ctx.ProjectRoot != "" && fileExists(ctx.ProjectRoot, path) {
			continue
		}
		disconnected = append(disconnected, path)
	}
	if hasIntegrationRoot || len(disconnected) == 0 {
		return nil
	}
	return []Inspection{{
		Capability: "rust", Name: "source-reachability", Enforcement: Required,
		Detail: fmt.Sprintf("new Rust module(s) %s are not Cargo target roots and this task owns no Cargo.toml, crate root, or discoverable test target that can wire them into its Verification; give the task an independently discoverable target or place crate-root wiring in a later integration task that consumes these modules", strings.Join(disconnected, ", ")),
	}}
}

func cargoTargetRoot(path string) bool {
	if path == "Cargo.toml" || path == "build.rs" || path == "src/lib.rs" || path == "src/main.rs" {
		return true
	}
	if strings.HasPrefix(path, "src/bin/") {
		return strings.Count(strings.TrimPrefix(path, "src/bin/"), "/") == 0 || strings.HasSuffix(path, "/main.rs")
	}
	for _, prefix := range []string{"tests/", "examples/", "benches/"} {
		if strings.HasPrefix(path, prefix) && strings.HasSuffix(path, ".rs") {
			return true
		}
	}
	return false
}

func planFieldItems(body, label string) []string {
	prefix := "**" + strings.ToLower(label) + ":**"
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(trimmed), prefix) {
			continue
		}
		value := strings.TrimSpace(trimmed[len(prefix):])
		if value == "" || strings.EqualFold(value, "none") {
			return nil
		}
		var out []string
		for _, item := range strings.Split(value, ",") {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, item)
			}
		}
		return out
	}
	return nil
}

type Meson struct{}

func (Meson) ID() string { return "meson" }
func (Meson) Detect(ctx Context) Contributions {
	if !fileExists(ctx.ProjectRoot, "meson.build") {
		return Contributions{}
	}
	candidate := GateCandidate{
		Capability: "meson", Kind: "build", Scope: ".", Priority: 45,
		// Meson leaves build/ behind after a failed setup. build.ninja is the
		// configuration checkpoint; the adapter repairs partial state before
		// compiling instead of stranding every subsequent gate.
		Command: "if test -f build/build.ninja; then meson setup --reconfigure build; else rm -rf build && meson setup build; fi && ninja -C build",
	}
	if !commandSucceeds(ctx.ProjectRoot, "meson --version") {
		candidate.Unavailable = &MissingToolchain{Tool: "meson", Marker: "meson.build"}
	}
	return Contributions{
		Detection: Detection{Capability: "meson", Evidence: []string{"meson.build"}},
		Gates:     []GateCandidate{candidate},
	}
}

func (Meson) ObserveGate(observation GateObservation) []GateFinding {
	files := addedMesonSources(observation.Diff)
	dependency := map[string]bool{}
	for _, file := range observation.BuildGraphFiles {
		if !isMesonSource(file) {
			continue
		}
		dependency[file] = true
		if !slices.Contains(files, file) {
			files = append(files, file)
		}
	}
	if len(files) == 0 {
		return nil
	}
	uncovered := mesonUncoveredSources(observation.ProjectRoot, files)
	if uncovered == nil {
		// Older or custom Meson gates may not leave a compilation database.
		// "no work" is still sufficient evidence that newly added production
		// sources were not exercised by this gate.
		if !strings.Contains(strings.ToLower(observation.Output), "no work to do") {
			return nil
		}
		uncovered = files
	}
	if len(uncovered) == 0 {
		return nil
	}
	detail := "new production source files are absent from Meson's compilation database; the project build does not exercise them"
	if slices.ContainsFunc(uncovered, func(file string) bool { return dependency[file] }) {
		detail = "accepted dependency source files are absent from Meson's compilation database; the project build does not exercise the contracts this task consumes"
	}
	return []GateFinding{{
		Capability: "meson", Kind: "build-integration", Enforcement: Required,
		Detail: detail,
		Files:  uncovered,
	}}
}

func addedMesonSources(diff string) []string {
	var files []string
	var current string
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git a/") {
			fields := strings.Fields(line)
			current = ""
			if len(fields) >= 4 {
				current = strings.TrimPrefix(fields[3], "b/")
			}
			continue
		}
		if line != "new file mode 100644" || current == "" {
			continue
		}
		if isMesonSource(current) {
			files = append(files, current)
		}
	}
	return files
}

func isMesonSource(file string) bool {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".c", ".cc", ".cpp", ".cxx", ".vala", ".m", ".mm", ".rs", ".f", ".f90":
		return true
	}
	return false
}

// mesonUncoveredSources returns nil when no compilation database is
// available, otherwise the proposed compilation units the build did not see.
// Paths in compile_commands may be absolute or relative to each entry's
// directory, so both are normalized before comparison.
func mesonUncoveredSources(root string, files []string) []string {
	raw, err := os.ReadFile(filepath.Join(root, "build", "compile_commands.json"))
	if err != nil {
		return nil
	}
	var entries []struct {
		Directory string `json:"directory"`
		File      string `json:"file"`
	}
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	compiled := map[string]bool{}
	for _, entry := range entries {
		path := entry.File
		if !filepath.IsAbs(path) {
			base := entry.Directory
			if base == "" {
				base = root
			}
			path = filepath.Join(base, path)
		}
		compiled[filepath.Clean(path)] = true
	}
	// Preserve the distinction between an unavailable compilation database
	// (nil) and an available database that covers every proposed source
	// (non-nil, empty). ObserveGate uses that distinction before falling back
	// to interpreting the textual Ninja output.
	uncovered := make([]string, 0)
	for _, file := range files {
		if !compiled[filepath.Clean(filepath.Join(root, file))] {
			uncovered = append(uncovered, file)
		}
	}
	return uncovered
}

type TypeScript struct{}

func (TypeScript) ID() string { return "typescript" }
func (TypeScript) Detect(ctx Context) Contributions {
	if !fileExists(ctx.ProjectRoot, "tsconfig.json") {
		return Contributions{}
	}
	return Contributions{
		Detection: Detection{Capability: "typescript", Evidence: []string{"tsconfig.json"}},
		Gates:     []GateCandidate{{Capability: "typescript", Kind: "build", Command: "npx tsc --noEmit", Scope: ".", Priority: 60}},
	}
}

func fileExists(root, relative string) bool { return fileExistsPath(filepath.Join(root, relative)) }
func fileExistsPath(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
func dirExists(root, relative string) bool {
	info, err := os.Stat(filepath.Join(root, relative))
	return err == nil && info.IsDir()
}
func existing(root string, paths ...string) []string {
	var found []string
	for _, path := range paths {
		if fileExists(root, path) {
			found = append(found, path)
		}
	}
	return found
}
func commandSucceeds(root, command string) bool {
	return xplat.Shell(root, nil, command).Run() == nil
}
func hasTestScript(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, `"test"`) &&
		!strings.Contains(content, `"test": "echo \"Error: no test specified\" && exit 1"`)
}
func pythonInterpreter() string {
	// Modern Debian/Ubuntu may ship python3 without a python alias. A detected
	// gate must name an interpreter that exists on this machine.
	if _, err := exec.LookPath("python"); err == nil {
		return "python"
	}
	return "python3"
}
func hasPythonFiles(root string) bool {
	found := false
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if strings.HasSuffix(path, ".py") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
