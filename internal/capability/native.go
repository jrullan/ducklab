package capability

import "strings"

// Native is the C/C++ toolchain capability. It contains no project names,
// task IDs, file paths, or framework assumptions.
type Native struct{}

func (Native) ID() string { return "c-native" }

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
	if strings.ContainsAny(command, "\n;") || strings.Contains(command, "&&") || strings.Contains(command, "||") {
		return ""
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	compiler := strings.TrimPrefix(fields[0], "./")
	if slash := strings.LastIndex(compiler, "/"); slash >= 0 {
		compiler = compiler[slash+1:]
	}
	switch compiler {
	case "cc", "gcc", "clang", "c++", "g++", "clang++":
	default:
		return ""
	}
	if !strings.Contains(command, "-fsyntax-only") || strings.Contains(command, "-Werror") {
		return ""
	}
	return command + " -Wall -Wextra -Werror"
}

// DefaultRegistry is the built-in capability set. The execution core depends
// only on Registry; adding another stack means registering another provider.
func DefaultRegistry() *Registry {
	return NewRegistry(Native{})
}
