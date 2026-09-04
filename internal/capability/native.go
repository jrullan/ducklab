package capability

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Native is the C/C++ toolchain capability. It contains no project names,
// task IDs, file paths, or framework assumptions.
type Native struct{}

func (Native) ID() string { return "c-native" }

func (Native) Detect(ctx Context) Contributions {
	if !nativeCompileCommand(ctx.TaskVerification) {
		return Contributions{}
	}
	return Contributions{Detection: Detection{
		Capability: "c-native", Evidence: []string{"task Verification invokes a native compiler"},
	}}
}

func (Native) Checks(ctx Context) []Check {
	if !nativeCompileCommand(ctx.TaskVerification) {
		return nil
	}
	var checks []Check
	if command := apiSafetyCommand(ctx.TaskVerification); command != "" {
		policy := ctx.Policies["c-native.api-contract"]
		if policy != "off" {
			enforcement := Required
			if policy == "diagnostic" {
				enforcement = Diagnostic
			}
			checks = append(checks, Check{
				Capability:  "c-native",
				Name:        "API and type safety",
				Command:     command,
				Enforcement: enforcement,
			})
		}
	}
	if command := strictSyntaxCommand(ctx.TaskVerification); command != "" {
		policy := ctx.Policies["c-native.warnings"]
		if policy != "off" {
			enforcement := Diagnostic
			if policy == "required" {
				enforcement = Required
			}
			checks = append(checks, Check{
				Capability:  "c-native",
				Name:        "compiler warnings",
				Command:     command,
				Enforcement: enforcement,
			})
		}
	}
	return checks
}

var completeTypedef = regexp.MustCompile(`(?s)\btypedef\s+(?:struct|union|enum)\s*(?:[A-Za-z_][A-Za-z0-9_]*\s*)?\{.*?\}\s*([A-Za-z_][A-Za-z0-9_]*)\s*;`)
var headerGuard = regexp.MustCompile(`(?m)^\s*#ifndef\s+([A-Za-z_][A-Za-z0-9_]*)\s*$`)

// Inspect compiles a stack-neutral view of the task's header contract. A
// translation unit can include multiple task headers, so defining the same
// complete typedef independently in two of them is an integration failure
// even when the task's one source file compiles in isolation.
func (Native) Inspect(ctx Context) ([]Inspection, error) {
	if !nativeCompileCommand(ctx.TaskVerification) {
		return nil, nil
	}
	policy := ctx.Policies["c-native.header-contract"]
	if policy == "off" {
		return nil, nil
	}
	enforcement := Required
	if policy == "diagnostic" {
		enforcement = Diagnostic
	}
	files := append(append([]string{}, ctx.ProducedFiles...), ctx.ConsumedFiles...)
	sort.Strings(files)
	definitions := map[string]string{}
	seenFiles := map[string]bool{}
	taskHeaderGuards := map[string]map[string]string{}
	for _, relative := range files {
		if filepath.Ext(relative) != ".h" || seenFiles[relative] {
			continue
		}
		seenFiles[relative] = true
		body, err := os.ReadFile(filepath.Join(ctx.ProjectRoot, filepath.Clean(relative)))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if match := headerGuard.FindSubmatch(body); match != nil {
			base := filepath.Base(relative)
			if taskHeaderGuards[base] == nil {
				taskHeaderGuards[base] = map[string]string{}
			}
			taskHeaderGuards[base][string(match[1])] = relative
		}
		for _, match := range completeTypedef.FindAllStringSubmatch(string(body), -1) {
			name := match[1]
			if first, duplicate := definitions[name]; duplicate && first != relative {
				return []Inspection{{
					Capability: "c-native", Name: "header contract", Enforcement: enforcement,
					Detail: fmt.Sprintf("complete typedef %s is independently defined in both %s and %s; keep one canonical definition and include its owning header", name, first, relative),
				}}, nil
			}
			definitions[name] = relative
		}
	}
	if finding, err := inspectShadowedTaskHeaders(ctx.ProjectRoot, taskHeaderGuards, seenFiles, enforcement); err != nil || finding != nil {
		if finding == nil {
			return nil, err
		}
		return []Inspection{*finding}, err
	}
	return nil, nil
}

// inspectShadowedTaskHeaders catches a subtle integration failure that an
// isolated compiler command cannot: two different headers with a task-owned
// basename resolve differently as -I ordering changes. Different include
// guards do not make that lookup deterministic; they only allow both APIs to
// enter one translation unit. Restricting the scan to basenames already in the
// task contract avoids treating unrelated, intentionally duplicated names as
// task failures.
func inspectShadowedTaskHeaders(root string, task map[string]map[string]string, taskFiles map[string]bool, enforcement Enforcement) (*Inspection, error) {
	if root == "" || len(task) == 0 {
		return nil, nil
	}
	var finding *Inspection
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".ducklab", "build", "node_modules", "vendor":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		base := entry.Name()
		guards, relevant := task[base]
		if !relevant || strings.ToLower(filepath.Ext(base)) != ".h" {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if taskFiles[relative] {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var canonical string
		for _, candidate := range guards {
			if canonical == "" || candidate < canonical {
				canonical = candidate
			}
		}
		canonicalBody, err := os.ReadFile(filepath.Join(root, filepath.Clean(canonical)))
		if err != nil {
			return err
		}
		if string(canonicalBody) == string(body) {
			return nil
		}
		finding = &Inspection{
			Capability: "c-native", Name: "header shadowing", Enforcement: enforcement,
			Detail: fmt.Sprintf("%s and %s are different headers with basename %s; include-path order can silently select the wrong API even when their include guards differ. Keep one canonical owning header", canonical, relative, base),
		}
		return fs.SkipAll
	})
	return finding, err
}

func apiSafetyCommand(command string) string {
	if !nativeCompileCommand(command) || strings.Contains(command, "-Werror") {
		return ""
	}
	return command + " -Werror=implicit-function-declaration -Werror=incompatible-pointer-types -Werror=int-conversion"
}

func strictSyntaxCommand(command string) string {
	if !nativeCompileCommand(command) || strings.Contains(command, "-Werror") {
		return ""
	}
	return command + " -Wall -Wextra -Werror"
}

func nativeCompileCommand(command string) bool {
	if strings.ContainsAny(command, "\n;") || strings.Contains(command, "&&") || strings.Contains(command, "||") {
		return false
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	compiler := strings.TrimPrefix(fields[0], "./")
	if slash := strings.LastIndex(compiler, "/"); slash >= 0 {
		compiler = compiler[slash+1:]
	}
	switch compiler {
	case "cc", "gcc", "clang", "c++", "g++", "clang++":
	default:
		return false
	}
	return strings.Contains(command, "-fsyntax-only") || fieldsContain(fields, "-c")
}

func fieldsContain(fields []string, want string) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	return false
}

// DefaultRegistry is the built-in capability set. The execution core depends
// only on Registry; adding another stack means registering another provider.
func DefaultRegistry() *Registry {
	return NewRegistry(Native{}, GTK4UI{}, GTK4Clipboard{}, X11Image{}, GLibAsync{}, Go{}, Python{}, Node{}, Rust{}, Meson{}, TypeScript{})
}
