// Package skill reads the skills a project and a machine make available to
// ducklings (05 §7).
//
// A skill is a directory with a SKILL.md in it. That is the whole design: a
// duckling can propose one with the ordinary fs_write tool, and it goes through
// the ordinary write guard and the ordinary human accept before anyone can use
// it (05 §7.1). There is no runtime registry to compromise, and reviewing a new
// skill is reading a diff.
package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Scope says where a skill was found.
type Scope string

const (
	// ScopeProject is .ducklab/skills/ inside a project.
	ScopeProject Scope = "project"
	// ScopeGlobal is the machine-wide skills directory.
	ScopeGlobal Scope = "global"
)

// Arg is one declared argument.
type Arg struct {
	Name     string `yaml:"name" json:"name"`
	Type     string `yaml:"type" json:"type"`
	Required bool   `yaml:"required" json:"required"`
}

// Skill is a skill's manifest plus where it came from.
type Skill struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	Version     int    `yaml:"version" json:"version"`
	Args        []Arg  `yaml:"args" json:"args"`
	// Entry is the executable to run, relative to the skill directory. Empty
	// means the skill is documentation: read, not run.
	Entry    string `yaml:"entry" json:"entry"`
	TimeoutS int    `yaml:"timeout_s" json:"timeout_s"`

	// Body is everything after the frontmatter — what a duckling reads.
	Body string `yaml:"-" json:"body,omitempty"`
	// Dir is the skill's directory.
	Dir string `yaml:"-" json:"dir"`
	// Scope is where it was found.
	Scope Scope `yaml:"-" json:"scope"`
}

// Runnable reports whether the skill has something to execute.
//
// The documentation-only form is the default the spec asks for: it is the
// cheap and safe one, because reading a skill cannot do anything to the
// project (05 §7).
func (s Skill) Runnable() bool { return s.Entry != "" }

// Parse reads a SKILL.md.
func Parse(content string) (*Skill, error) {
	front, body, err := splitFrontmatter(content)
	if err != nil {
		return nil, err
	}
	var sk Skill
	if err := yaml.Unmarshal([]byte(front), &sk); err != nil {
		return nil, fmt.Errorf("frontmatter: %w", err)
	}
	sk.Body = strings.TrimSpace(body)
	return &sk, nil
}

func splitFrontmatter(content string) (front, body string, err error) {
	content = strings.TrimLeft(content, "\ufeff \t\r\n")
	if !strings.HasPrefix(content, "---") {
		return "", "", fmt.Errorf("SKILL.md must start with a --- frontmatter block")
	}
	rest := content[3:]
	rest = strings.TrimPrefix(rest, "\r")
	rest = strings.TrimPrefix(rest, "\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", fmt.Errorf("SKILL.md frontmatter is not closed with ---")
	}
	after := rest[end+4:]
	after = strings.TrimPrefix(after, "\r")
	after = strings.TrimPrefix(after, "\n")
	return rest[:end], after, nil
}

// Load reads one skill directory.
func Load(dir string, scope Scope) (*Skill, error) {
	content, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(dir), err)
	}
	sk, err := Parse(string(content))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(dir), err)
	}
	sk.Dir = dir
	sk.Scope = scope
	// The directory name is the address a duckling uses, so it wins over a
	// name field that drifted from it.
	if sk.Name == "" {
		sk.Name = filepath.Base(dir)
	}
	return sk, nil
}

// List returns every skill visible to a project, sorted by name.
//
// A project skill shadows a global one of the same name (05 §7). Shadowing is
// not merging: the project's version is used whole, because a skill half from
// one place and half from another is a thing nobody can review.
func List(projectRoot, globalDir string) ([]*Skill, []error) {
	var problems []error
	byName := map[string]*Skill{}

	load := func(root string, scope Scope) {
		if root == "" {
			return
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return // no skills directory is the normal case, not a problem
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			sk, err := Load(filepath.Join(root, e.Name()), scope)
			if err != nil {
				// A broken skill is reported, not fatal: one unparseable
				// SKILL.md must not hide every other skill on the machine.
				problems = append(problems, err)
				continue
			}
			byName[sk.Name] = sk
		}
	}
	load(globalDir, ScopeGlobal)
	load(ProjectDir(projectRoot), ScopeProject) // project last: it shadows

	out := make([]*Skill, 0, len(byName))
	for _, sk := range byName {
		out = append(out, sk)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, problems
}

// Find returns one skill by name, with project shadowing global.
func Find(projectRoot, globalDir, name string) (*Skill, error) {
	all, _ := List(projectRoot, globalDir)
	for _, sk := range all {
		if sk.Name == name {
			return sk, nil
		}
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("no skill %q; this project has no skills", name)
	}
	names := make([]string, len(all))
	for i, sk := range all {
		names[i] = sk.Name
	}
	return nil, fmt.Errorf("no skill %q; available: %s", name, strings.Join(names, ", "))
}

// ProjectDir is where a project's own skills live.
func ProjectDir(projectRoot string) string {
	if projectRoot == "" {
		return ""
	}
	return filepath.Join(projectRoot, ".ducklab", "skills")
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// whenWord marks a description that says when to reach for the skill.
var whenWord = regexp.MustCompile(`(?i)\b(use|when|whenever|reach for|if|after|before|for)\b`)

// Validate reports everything wrong with a skill.
//
// All of it at once. A validator that stops at the first problem turns fixing
// a skill into a guessing game with one answer per round.
func Validate(sk *Skill) []string {
	var problems []string

	if sk.Name == "" {
		problems = append(problems, "name is required")
	} else if !nameRe.MatchString(sk.Name) {
		problems = append(problems, fmt.Sprintf("name %q must be lowercase letters, digits and dashes", sk.Name))
	}
	if sk.Dir != "" && sk.Name != "" && filepath.Base(sk.Dir) != sk.Name {
		problems = append(problems, fmt.Sprintf("name %q does not match its directory %q; a duckling addresses the skill by directory", sk.Name, filepath.Base(sk.Dir)))
	}

	// The description is the whole interface. It is all a duckling sees in
	// skill_list, so a bare noun phrase — "PDF extraction" — tells it the
	// skill exists and nothing about when to reach for it (05 §7).
	switch {
	case strings.TrimSpace(sk.Description) == "":
		problems = append(problems, "description is required")
	case len(strings.Fields(sk.Description)) < 6:
		problems = append(problems, "description is too short to say when to use the skill; write a sentence, not a label")
	case !whenWord.MatchString(sk.Description):
		problems = append(problems, "description says what the skill does but not when to use it; add a sentence starting \"Use when …\"")
	}

	if sk.Version < 1 {
		problems = append(problems, "version must be 1 or more")
	}
	if sk.TimeoutS < 0 {
		problems = append(problems, "timeout_s cannot be negative")
	}

	seen := map[string]bool{}
	for i, a := range sk.Args {
		if a.Name == "" {
			problems = append(problems, fmt.Sprintf("args[%d] has no name", i))
			continue
		}
		if seen[a.Name] {
			problems = append(problems, fmt.Sprintf("argument %q is declared twice", a.Name))
		}
		seen[a.Name] = true
		switch a.Type {
		case "string", "int", "bool", "":
		default:
			problems = append(problems, fmt.Sprintf("argument %q has unknown type %q; want string, int or bool", a.Name, a.Type))
		}
	}

	if sk.Entry != "" {
		if filepath.IsAbs(sk.Entry) || strings.Contains(sk.Entry, "..") {
			problems = append(problems, fmt.Sprintf("entry %q must be a path inside the skill directory", sk.Entry))
		} else if sk.Dir != "" {
			if _, err := os.Stat(filepath.Join(sk.Dir, sk.Entry)); err != nil {
				problems = append(problems, fmt.Sprintf("entry %q does not exist", sk.Entry))
			}
		}
	}
	if sk.Entry == "" && strings.TrimSpace(sk.Body) == "" {
		problems = append(problems, "a skill with no entry is documentation, but its body is empty")
	}
	return problems
}
