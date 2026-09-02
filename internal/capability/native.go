package capability

import "strings"

// Native is the C/C++ toolchain capability. It contains no project names,
// task IDs, file paths, or framework assumptions.
type Native struct{}

func (Native) ID() string { return "c-native" }

func (Native) Detect(ctx Context) Contributions {
	if !nativeSyntaxCommand(ctx.TaskVerification) {
		return Contributions{}
	}
	return Contributions{Detection: Detection{
		Capability: "c-native", Evidence: []string{"task Verification invokes a native compiler"},
	}}
}

func (Native) Checks(ctx Context) []Check {
	command := strictSyntaxCommand(ctx.TaskVerification)
	if command == "" {
		return nil
	}
	policy := ctx.Policies["c-native.warnings"]
	if policy == "off" {
		return nil
	}
	enforcement := Diagnostic
	if policy == "required" {
		enforcement = Required
	}
	return []Check{{
		Capability:  "c-native",
		Name:        "compiler warnings",
		Command:     command,
		Enforcement: enforcement,
	}}
}

func strictSyntaxCommand(command string) string {
	if !nativeSyntaxCommand(command) || strings.Contains(command, "-Werror") {
		return ""
	}
	return command + " -Wall -Wextra -Werror"
}

func nativeSyntaxCommand(command string) bool {
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
	return strings.Contains(command, "-fsyntax-only")
}

// DefaultRegistry is the built-in capability set. The execution core depends
// only on Registry; adding another stack means registering another provider.
func DefaultRegistry() *Registry {
	return NewRegistry(Native{}, Go{}, Python{}, Node{}, Rust{}, Meson{}, TypeScript{})
}
