package capability

import (
	"os"
	"path/filepath"
	"regexp"
)

// GeneratedArtifacts recognizes a project-owned freshness contract instead
// of teaching Ducklab how any particular generator works. A repository that
// exposes `make api-check` has already defined both the authoritative outputs
// and how to reproduce them; the harness only makes that contract mandatory.
type GeneratedArtifacts struct{}

func (GeneratedArtifacts) ID() string { return "generated-artifacts" }

func (GeneratedArtifacts) Detect(ctx Context) Contributions {
	if !hasAPICheckTarget(ctx.ProjectRoot) {
		return Contributions{}
	}
	return Contributions{Detection: Detection{
		Capability: "generated-artifacts",
		Evidence:   []string{"Makefile target api-check"},
	}}
}

func (GeneratedArtifacts) Checks(ctx Context) []Check {
	if !hasAPICheckTarget(ctx.ProjectRoot) {
		return nil
	}
	return []Check{{
		Capability:  "generated-artifacts",
		Name:        "generated API freshness",
		Command:     "make api-check",
		Enforcement: Required,
	}}
}

var apiCheckTarget = regexp.MustCompile(`(?m)^api-check\s*:`)

func hasAPICheckTarget(root string) bool {
	body, err := os.ReadFile(filepath.Join(root, "Makefile"))
	return err == nil && apiCheckTarget.Match(body)
}
