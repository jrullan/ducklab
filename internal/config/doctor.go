package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Finding is one deterministic configuration problem and its optional fix.
type Finding struct {
	Key      string `json:"key"`
	Proposed string `json:"proposed"`
	Reason   string `json:"reason"`
}

// Doctor inspects projectPath's configuration and repository without changing either.
// Findings are appended in rule and key order; it never ranges over a map.
func Doctor(projectPath string) ([]Finding, error) {
	configPath := filepath.Join(projectPath, ".ducklab", "project.toml")
	p, err := LoadProject(configPath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, err
	}
	out := make([]Finding, 0, 8)
	add := func(key, proposed, reason string) { out = append(out, Finding{key, proposed, reason}) }

	// A frontend's dependencies must be available to an acceptance checkout.
	if frontendPresent(projectPath) && !contains(p.Verify.LinkDeps, "frontend/node_modules") {
		add("verify.link_deps", "frontend/node_modules", "frontend is present but its node_modules is not linked into acceptance checkouts")
	}
	remote := gitRemoteConfigured(projectPath)
	_, hasRemote := raw["remote"]
	_, hasGitHub := raw["github"]
	if remote && !hasRemote && !hasGitHub {
		add("github.enabled", "true", "a git remote is configured but no remote or github configuration declares how ducklab should use it")
	}
	if hasGitHub && !githubConsumed(p) {
		add("github", "", "github configuration is present but no configured command uses GitHub or pull requests")
	}
	if verifyCommand(p) == "" {
		add(verifyKey(p), defaultVerifyCommand(projectPath), "the selected verify mode has no command")
	}
	if p.Budget.MaxUSD == 0 {
		add("budget.max_usd", "5", "the project budget is zero")
	}
	for _, tool := range detectedTools(projectPath) {
		if !allowlisted(p.Shell.AllowPrefixes, tool) {
			add("shell.allow_prefixes", tool, "the project toolchain is not allowed by shell policy")
		}
	}
	for _, mode := range modesInUse(p) {
		for _, role := range requiredRoles(mode) {
			if !seatConfigured(p, mode, role) {
				add("mode_seats."+mode+"."+role, "", "a mode in use has no configured required seat")
			}
		}
	}
	return out, nil
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
func frontendPresent(root string) bool {
	_, err := os.Stat(filepath.Join(root, "frontend", "package.json"))
	return err == nil
}
func gitRemoteConfigured(root string) bool {
	b, err := os.ReadFile(filepath.Join(root, ".git", "config"))
	return err == nil && strings.Contains(string(b), "[remote ")
}
func githubConsumed(p *Project) bool {
	for _, s := range []string{p.Verify.Tests, p.Verify.Build, p.Verify.Lint, p.Verify.Custom, p.Install.Command, p.Run.Command} {
		if strings.Contains(strings.ToLower(s), "gh ") || strings.Contains(strings.ToLower(s), "github") || strings.Contains(strings.ToLower(s), "pr ") {
			return true
		}
	}
	return false
}
func verifyCommand(p *Project) string {
	switch p.Verify.Mode {
	case "tests":
		return p.Verify.Tests
	case "build":
		return p.Verify.Build
	case "lint":
		return p.Verify.Lint
	case "custom":
		return p.Verify.Custom
	case "none":
		return "none"
	default:
		if p.Verify.Tests != "" {
			return p.Verify.Tests
		}
		if p.Verify.Build != "" {
			return p.Verify.Build
		}
		if p.Verify.Lint != "" {
			return p.Verify.Lint
		}
		return p.Verify.Custom
	}
}
func verifyKey(p *Project) string {
	switch p.Verify.Mode {
	case "build":
		return "verify.build"
	case "lint":
		return "verify.lint"
	case "custom":
		return "verify.custom"
	default:
		return "verify.tests"
	}
}
func defaultVerifyCommand(root string) string {
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
		return "go test ./..."
	}
	if _, err := os.Stat(filepath.Join(root, "package.json")); err == nil {
		return "npm test"
	}
	return ""
}
func detectedTools(root string) []string {
	tools := []string{}
	for _, x := range []struct{ file, tool string }{{"go.mod", "go "}, {"package.json", "npm "}, {"frontend/package.json", "npm "}, {"Cargo.toml", "cargo "}, {"pyproject.toml", "python "}, {"Makefile", "make "}} {
		if _, err := os.Stat(filepath.Join(root, x.file)); err == nil && !contains(tools, x.tool) {
			tools = append(tools, x.tool)
		}
	}
	return tools
}
func allowlisted(prefixes []string, tool string) bool {
	for _, p := range prefixes {
		if strings.TrimSpace(p) == strings.TrimSpace(tool) {
			return true
		}
	}
	return false
}
func modesInUse(p *Project) []string {
	out := []string{}
	for _, stage := range []Stage{StageIntake, StageSpec, StagePlan, StageBuild, StageReview, StageRelease, StageOperate} {
		if mode := p.Modes[stage]; mode != "" && !contains(out, string(mode)) {
			out = append(out, string(mode))
		}
	}
	return out
}
func requiredRoles(mode string) []string {
	switch mode {
	case "solo":
		return []string{"implementer"}
	case "pair":
		return []string{"implementer", "reviewer"}
	case "council":
		return []string{"architect", "reviewer"}
	case "split":
		return []string{"architect", "implementer", "reviewer"}
	case "tournament":
		return []string{"implementer", "judge"}
	}
	return nil
}
func seatConfigured(p *Project, mode, role string) bool {
	if v := p.ModeSeats[mode][role]; len(v) > 0 {
		return true
	}
	if v := p.RosterSeats[Role(role)]; len(v) > 0 {
		return true
	}
	return p.Roster[Role(role)] != ""
}
