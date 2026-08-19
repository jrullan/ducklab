package tools

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/config"
)

// roleToolbelts is the CEILING for each role: the maximum set of tools a
// duckling in that role may ever be given (04 §2.5).
//
// A role fixes what its holder can do; a conversation script decides only when
// it speaks. A Turn may narrow this set, never widen it — otherwise a script
// could hand a reviewer fs_write, and "the reviewer evaluates, it never
// rewrites" would hold by convention instead of by construction.
//
// Names for tools that do not exist yet (artifact_read, skill_*, mcp_call, …)
// are listed deliberately: the ceiling is the spec's, and Available() filters
// it down to what is actually registered. That way adding a tool later needs
// no change here.
var roleToolbelts = map[config.Role][]string{
	config.RoleArchitect: {
		"fs_list", "fs_read", "fs_search", "git_log", "artifact_read", "ask_human",
		"ref_read",
	},
	config.RoleImplementer: {
		"fs_list", "fs_read", "fs_search", "fs_write", "fs_write_lines", "fs_patch", "fs_delete",
		"shell", "verify_run", "git_status", "git_diff", "artifact_read",
		"task_read", "skill_list", "skill_read", "skill_run", "mcp_call", "ask_human",
		"ask_advisor",
	},
	config.RoleReviewer: {
		"fs_read", "fs_search", "git_diff", "verify_run", "artifact_read",
		"ref_read",
	},
	config.RoleJudge: {
		"fs_read", "git_diff",
	},
	config.RoleTriager: {
		"fs_search", "fs_read", "bug_read", "git_log",
	},
	config.RoleAdvisor: {
		"fs_read", "fs_search", "artifact_read", "roster_read",
	},
	config.RoleScribe: {
		"fs_read", "artifact_read",
	},
	// The human turn is not an agent loop; it has no tools.
	config.RoleHuman: {},
}

// RoleToolbelt returns the ceiling for a role, as specified — including tools
// that are not implemented yet. An unknown role gets an empty belt: failing
// closed is the only safe default here.
func RoleToolbelt(role config.Role) []string {
	belt, ok := roleToolbelts[role]
	if !ok {
		return nil
	}
	out := make([]string, len(belt))
	copy(out, belt)
	return out
}

// RoleAllows reports whether a role's ceiling includes a tool.
func RoleAllows(role config.Role, tool string) bool {
	for _, name := range roleToolbelts[role] {
		if name == tool {
			return true
		}
	}
	return false
}

// Available returns the role's ceiling intersected with the registry, sorted
// for determinism. Prompts and request payloads are built from this, so a
// stable order keeps provider prompt caching effective.
func (r *Registry) Available(role config.Role) []string {
	var out []string
	for _, name := range RoleToolbelt(role) {
		if _, err := r.Get(name); err == nil {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// ReadOnly returns the role's available tools minus everything that can change
// the working tree. Computed from Tool.Mutating, so a new mutating tool is
// excluded automatically instead of having to be remembered.
func (r *Registry) ReadOnly(role config.Role) []string {
	var out []string
	for _, name := range r.Available(role) {
		t, err := r.Get(name)
		if err != nil || t.Mutating() {
			continue
		}
		out = append(out, name)
	}
	return out
}

// NarrowToolbelt resolves a requested toolbelt against a role's ceiling.
//
// spec is "" or "full" (the whole ceiling), "read-only" (the non-mutating part
// of the ceiling), or a comma-separated explicit list. An explicit list may
// only narrow: naming a tool outside the ceiling is an error, reported at
// script-load time rather than silently granted.
func (r *Registry) NarrowToolbelt(role config.Role, spec string) ([]string, error) {
	switch strings.TrimSpace(spec) {
	case "", "full":
		return r.Available(role), nil
	case "read-only":
		return r.ReadOnly(role), nil
	}

	var out []string
	for _, name := range strings.Split(spec, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !RoleAllows(role, name) {
			return nil, fmt.Errorf(
				"role %q may not use tool %q: a turn can only narrow a role's toolbelt, never widen it (allowed: %s)",
				role, name, strings.Join(RoleToolbelt(role), ", "))
		}
		if _, err := r.Get(name); err != nil {
			return nil, fmt.Errorf("role %q: %w", role, err)
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
