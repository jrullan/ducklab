package service

import (
	"os"
	"path/filepath"

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
	Version     int         `json:"version"`
	Args        []skill.Arg `json:"args,omitempty"`
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

	out := make([]SkillSummary, 0, len(all)+len(problems))
	for _, sk := range all {
		out = append(out, SkillSummary{
			Name: sk.Name, Description: sk.Description, Scope: sk.Scope,
			Runnable: sk.Runnable(), Version: sk.Version, Args: sk.Args,
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
