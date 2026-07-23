package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/jrullan/ducklab/internal/prim"
	"github.com/jrullan/ducklab/internal/project"
	"github.com/jrullan/ducklab/internal/source"
)

// inferDescription asks a model for a one-line description of the project, from
// its files and git history — the "returning to an existing/cloned repo" case,
// where the commit story is the initial context.
func inferDescription(src source.Client, repo string, opts source.Options) string {
	_, log := prim.Git("log --oneline -20", repo)
	files := prim.RepoListing(repo)
	msgs := []source.Message{
		{Role: "system", Content: "You describe software projects in one short line."},
		{Role: "user", Content: fmt.Sprintf(
			"Based on these files and recent commits, describe in ONE line (max 12 words) what this "+
				"project is. Output only the description, nothing else.\n\nFiles:\n%s\n\nRecent commits:\n%s",
			files, log)},
	}
	res, err := src.Complete(context.Background(), msgs, opts)
	if err != nil {
		return ""
	}
	return firstLine(strings.TrimSpace(res.Content))
}

// projectRequirement augments a task goal with the project's context preamble,
// inferring and persisting a description the first time if none is set. Returns
// the effective requirement plus the description if it was just inferred.
func projectRequirement(src source.Client, repo, goal string, opts source.Options) (effReq, inferred string) {
	p, _ := project.Load(repo)
	if strings.TrimSpace(p.Description) == "" {
		if desc := inferDescription(src, repo, opts); desc != "" {
			p.Description = desc
			_ = project.Save(repo, p)
			inferred = desc
		}
	}
	pre := p.Context()
	if pre == "" {
		return goal, inferred
	}
	return pre + "\n\nTask:\n" + goal, inferred
}
