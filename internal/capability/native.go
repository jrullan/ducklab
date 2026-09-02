package capability

import "strings"

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
	return NewRegistry(Native{}, GTK4Clipboard{}, X11Image{}, GLibAsync{}, Go{}, Python{}, Node{}, Rust{}, Meson{}, TypeScript{})
}
