package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/tools"
	"github.com/jrullan/ducklab/internal/vcs"

	"github.com/jrullan/ducklab/internal/skill"
	"github.com/jrullan/ducklab/internal/xplat"
)

// globalSkillsDir is the machine-wide skills directory (02 §1).
//
// Empty when the data directory cannot be determined, which skill.List treats
// as "no global skills" rather than an error: a machine with nowhere to put
// them is a machine that has none.
func globalSkillsDir() string {
	dir, err := xplat.DataDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "ducklab", "skills")
}

// SkillSummary is one skill as a client sees it.
type SkillSummary struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Scope       skill.Scope `json:"scope"`
	Runnable    bool        `json:"runnable"`
	// Pending is a skill a run wrote that nobody has accepted yet. Shown, not
	// hidden: a person needs to know why a duckling could not use it.
	Pending bool        `json:"pending,omitempty"`
	Version int         `json:"version"`
	Args    []skill.Arg `json:"args,omitempty"`
	// Problems is what `skill validate` would say. Carried in the list so a
	// broken skill is visible where someone is already looking, rather than
	// only when they think to validate it.
	Problems []string `json:"problems,omitempty"`
}

// SkillList lists the skills available to a project.
func (s *Service) SkillList(projectID string) ([]SkillSummary, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	all, problems := skill.List(entry.Path, globalSkillsDir())
	g := vcs.New(entry.Path)

	out := make([]SkillSummary, 0, len(all)+len(problems))
	for _, sk := range all {
		out = append(out, SkillSummary{
			Name: sk.Name, Description: sk.Description, Scope: sk.Scope,
			Runnable: sk.Runnable(), Version: sk.Version, Args: sk.Args,
			Pending:  sk.Scope == skill.ScopeProject && !g.PathIsCommitted(sk.Dir),
			Problems: skill.Validate(sk),
		})
	}
	// A directory that failed to parse has no name to report, so it is listed
	// by the problem itself. Silently omitting it is how a skill someone just
	// wrote appears not to exist.
	for _, p := range problems {
		out = append(out, SkillSummary{Problems: []string{p.Error()}})
	}
	return out, nil
}

// SkillGet returns one skill, including its body.
func (s *Service) SkillGet(projectID, name string) (*skill.Skill, []string, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, nil, err
	}
	sk, err := skill.Find(entry.Path, globalSkillsDir(), name)
	if err != nil {
		return nil, nil, err
	}
	return sk, skill.Validate(sk), nil
}

// validateProposedSkills checks skills a run wrote into .ducklab/skills/.
//
// A duckling may propose a skill with the ordinary fs_write tool (05 §7.1).
// That directory is not privileged: the write guard applied, the human accept
// still stands between it and being usable, and this runs on the way past so
// nobody accepts a skill that cannot work. The check is on the working tree,
// which is what the human is about to accept.
func validateProposedSkills(projectRoot string) []string {
	dir := skill.ProjectDir(projectRoot)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sk, err := skill.Load(filepath.Join(dir, e.Name()), skill.ScopeProject)
		if err != nil {
			out = append(out, err.Error())
			continue
		}
		for _, p := range skill.Validate(sk) {
			out = append(out, sk.Name+": "+p)
		}
	}
	return out
}

// SkillNew scaffolds a skill directory (03 §3.9).
//
// Written by the engine, not the client: clients hold no state (I11), and a
// CLI that wrote into a project would be a second thing that can create files
// there.
func (s *Service) SkillNew(projectID, name string, runnable bool) (string, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return "", err
	}
	if !skillNameRe.MatchString(name) {
		return "", fmt.Errorf("skill name %q must be lowercase letters, digits and dashes", name)
	}
	dir := filepath.Join(skill.ProjectDir(entry.Path), name)
	if _, err := os.Stat(dir); err == nil {
		// Never overwrite: a scaffold that clobbers the skill someone was
		// halfway through writing is worse than an error.
		return "", fmt.Errorf("skill %q already exists at %s", name, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	// The template's description is a sentence with a "Use when", because that
	// is what validate demands and what a duckling actually chooses by. A
	// scaffold that fails its own validator teaches the wrong shape.
	manifest := "---\nname: " + name + "\n" +
		"description: TODO — say what this does. Use when TODO.\nversion: 1\n"
	if runnable {
		manifest += "args:\n  - name: TODO\n    type: string\n    required: true\nentry: run.sh\ntimeout_s: 120\n"
	}
	manifest += "---\n\nTODO: what a duckling needs to know to use this.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(manifest), 0o644); err != nil {
		return "", err
	}
	if runnable {
		script := "#!/bin/sh\n# Arguments arrive as --name=value and as DUCKLAB_ARG_NAME.\n# DUCKLAB_SKILL_DIR is this directory.\nset -e\n\necho \"TODO\"\n"
		if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0o755); err != nil {
			return "", err
		}
	}
	return dir, nil
}

var skillNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// SkillRun runs a skill from a client, for a human testing one.
//
// Same code path as the tool a duckling calls, so a skill that works here
// works there. It is not a run: nothing is logged as a model call, because no
// model was involved.
func (s *Service) SkillRun(ctx context.Context, projectID, name string, args map[string]interface{}) (output string, failed bool, err error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return "", false, err
	}
	projCfg, err := config.LoadProject(filepath.Join(entry.Path, ".ducklab", "project.toml"))
	if err != nil {
		return "", false, err
	}
	ectx := &tools.ExecContext{
		ProjectRoot:     entry.Path,
		ShellPolicy:     projCfg.Shell,
		Verify:          projCfg.Verify,
		GlobalSkillsDir: globalSkillsDir(),
		// A person ran this command. They are the one the acceptance gate
		// protects, and testing a skill you just wrote is the reason to have
		// the command at all.
		AllowUnacceptedSkills: true,
	}
	payload, err := json.Marshal(map[string]interface{}{"name": name, "args": args})
	if err != nil {
		return "", false, err
	}
	res, err := (&tools.SkillRun{}).Execute(ctx, ectx, payload)
	if err != nil {
		return "", false, err
	}
	// The skill ran and exited non-zero. That is an answer, not a transport
	// failure, and the caller needs the output to see why.
	return res.Content, res.IsError, nil
}
