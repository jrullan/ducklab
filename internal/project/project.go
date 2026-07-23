// Package project is ducklab's per-repo session memory: a one-line description
// of what's being built plus a log of accepted task goals. It persists to a
// gitignored .ducklab/project.md so it survives closing the session, and it is
// injected as a context preamble into every task so a follow-up knows what it is
// continuing. A fresh clone with no note can re-infer the description from git
// history + files.
package project

import (
	"os"
	"path/filepath"
	"strings"
)

// Project is the persisted session memory for one repo.
type Project struct {
	Description string
	Goals       []string
}

const maxGoalsInContext = 8

// Path is the note's location (gitignored, ducklab-owned).
func Path(repo string) string { return filepath.Join(repo, ".ducklab", "project.md") }

// Load reads the note, reporting whether one existed.
func Load(repo string) (Project, bool) {
	data, err := os.ReadFile(Path(repo))
	if err != nil {
		return Project{}, false
	}
	var p Project
	section := ""
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.EqualFold(t, "# Project"):
			section = "desc"
		case strings.EqualFold(t, "# Goals"):
			section = "goals"
		case t == "":
			// skip
		case section == "desc" && p.Description == "":
			p.Description = t
		case section == "goals" && strings.HasPrefix(t, "- "):
			p.Goals = append(p.Goals, strings.TrimSpace(t[2:]))
		}
	}
	return p, true
}

// Save writes the note and ensures .ducklab/ is gitignored.
func Save(repo string, p Project) error {
	if err := os.MkdirAll(filepath.Dir(Path(repo)), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Project\n")
	if p.Description != "" {
		b.WriteString(p.Description + "\n")
	}
	b.WriteString("\n# Goals\n")
	for _, g := range p.Goals {
		b.WriteString("- " + g + "\n")
	}
	if err := os.WriteFile(Path(repo), []byte(b.String()), 0o644); err != nil {
		return err
	}
	ensureIgnored(repo, ".ducklab/")
	return nil
}

// AddGoal appends an accepted task goal (deduped against the last entry).
func AddGoal(repo, goal string) error {
	goal = strings.TrimSpace(strings.ReplaceAll(goal, "\n", " "))
	if goal == "" {
		return nil
	}
	p, _ := Load(repo)
	if n := len(p.Goals); n > 0 && p.Goals[n-1] == goal {
		return nil
	}
	p.Goals = append(p.Goals, goal)
	return Save(repo, p)
}

// SetDescription updates the one-line description.
func SetDescription(repo, desc string) error {
	p, _ := Load(repo)
	p.Description = strings.TrimSpace(strings.ReplaceAll(desc, "\n", " "))
	return Save(repo, p)
}

// Context is the preamble injected into a task, or "" when there's nothing to say.
func (p Project) Context() string {
	if strings.TrimSpace(p.Description) == "" && len(p.Goals) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("You are continuing work on an existing project.\n")
	if p.Description != "" {
		b.WriteString("Project: " + p.Description + "\n")
	}
	if len(p.Goals) > 0 {
		start := 0
		if len(p.Goals) > maxGoalsInContext {
			start = len(p.Goals) - maxGoalsInContext
		}
		b.WriteString("Recently completed tasks in this project:\n")
		for _, g := range p.Goals[start:] {
			b.WriteString("- " + g + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func ensureIgnored(repo, pattern string) {
	p := filepath.Join(repo, ".gitignore")
	data, _ := os.ReadFile(p)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == pattern {
			return
		}
	}
	body := string(data)
	if len(body) > 0 && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	body += pattern + "\n"
	_ = os.WriteFile(p, []byte(body), 0o644)
}
