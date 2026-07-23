package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/jrullan/ducklab/internal/prim"
	"github.com/jrullan/ducklab/internal/project"
	"github.com/jrullan/ducklab/internal/run"
	"github.com/jrullan/ducklab/internal/source"
	"github.com/jrullan/ducklab/internal/strategy"
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

// projectRequirement augments a task goal with the project's context preamble
// AND any prior failed attempts at this goal (so a re-run avoids the dead end),
// inferring and persisting a description the first time if none is set. Returns
// the effective requirement, a just-inferred description (if any), and how many
// prior failed attempts are being fed in.
func projectRequirement(src source.Client, repo, goal string, opts source.Options) (effReq, inferred string, priorFails int) {
	p, _ := project.Load(repo)
	if strings.TrimSpace(p.Description) == "" {
		if desc := inferDescription(src, repo, opts); desc != "" {
			p.Description = desc
			_ = project.Save(repo, p)
			inferred = desc
		}
	}
	var parts []string
	if pre := p.Context(); pre != "" {
		parts = append(parts, pre)
	}
	if att := project.AttemptsContext(repo, goal); att != "" {
		parts = append(parts, att)
	}
	priorFails = project.Count(repo, goal)
	parts = append(parts, "Task:\n"+goal)
	return strings.Join(parts, "\n\n"), inferred, priorFails
}

// recordFailure remembers an escalated run's approach so the next attempt at the
// same goal is told to avoid it. Goal and mode come from the run's own state.
func recordFailure(repo string, r *run.Run, o strategy.Outcome) {
	goal, _ := r.Get("requirement")
	mode, _ := r.Get("mode")
	g := asString(goal)
	if g == "" {
		return
	}
	detail := ""
	if v, ok := r.Read("execution_review.md"); ok { // plan: B's verdict
		detail = v
	} else if v, name := lastReview(r); name != "" { // driver: observer review
		detail = v
	} else if v, ok := r.Read("test_output_final.txt"); ok {
		detail = v
	} else if v, ok := r.Read("test_output.txt"); ok { // solo
		detail = v
	}
	diff, _ := r.Read("diff_final.patch")
	_ = project.AddAttempt(repo, project.Attempt{
		Goal:   g,
		Mode:   asString(mode),
		Reason: o.Message,
		Detail: prim.TruncateMiddle(strings.TrimSpace(detail), 800),
		Diff:   prim.TruncateMiddle(strings.TrimSpace(diff), 1500),
	})
}
