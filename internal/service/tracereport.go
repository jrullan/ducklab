package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/release"
	"github.com/jrullan/ducklab/internal/vcs"
)

// TraceReport renders the development report: what the software is, and the
// evidence that it is that — every requirement traced through spec to tasks
// to their status, the bug fixes, the releases, and the spine's breaks.
//
// Deterministic on purpose. The narrative is the approved requirements
// document — prose a person signed — and every claim below it is a fact from
// the plan, the runs or the tags. A development report a model embellished
// would be the exact kind of unfalsifiable artifact this project exists to
// avoid (I2's cousin: no model decides what the record says).
func (s *Service) TraceReport(ctx context.Context, projectID string) (string, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return "", err
	}
	reqs, err := artifact.Load(entry.Path, artifact.KindRequirements)
	if err != nil {
		return "", err
	}
	spec, _ := artifact.Load(entry.Path, artifact.KindSpec)
	plan, _ := artifact.Load(entry.Path, artifact.KindPlan)
	tasks, _ := s.TaskList(ctx, projectID)

	var b strings.Builder
	fmt.Fprintf(&b, "# %s — development report\n\n", entry.Name)
	fmt.Fprintf(&b, "Generated %s", time.Now().UTC().Format("2006-01-02"))
	if reqs.Front.Version > 0 {
		fmt.Fprintf(&b, " · requirements v%d", reqs.Front.Version)
		if reqs.Front.Approved() {
			fmt.Fprintf(&b, " (approved by %s)", reqs.Front.ApprovedBy)
		}
	}
	if spec != nil && spec.Front.Version > 0 {
		fmt.Fprintf(&b, " · spec v%d", spec.Front.Version)
	}
	if plan != nil && plan.Front.Version > 0 {
		fmt.Fprintf(&b, " · plan v%d", plan.Front.Version)
	}
	git := vcs.New(entry.Path)
	if tags, tErr := git.Tags(); tErr == nil {
		if v, ok := release.Latest(tags); ok {
			fmt.Fprintf(&b, " · latest release %s", v)
		}
	}
	b.WriteString("\n\n")

	// The summary. Everything in it is assembled from content a person
	// approved — the project's own description, the requirements preamble,
	// each must requirement's first sentence — never composed by a model: an
	// executive summary a model wrote would be the report editorialising
	// about itself.
	b.WriteString("## Summary\n\n")
	if reqs.Front.Origin == "adopted" {
		b.WriteString("_These requirements were surveyed from the existing codebase " +
			"(origin: adopted) and approved by a person._\n\n")
	}
	described := false
	if cfg, cErr := s.projectConfig(projectID); cErr == nil && strings.TrimSpace(cfg.Describe) != "" {
		b.WriteString(strings.TrimSpace(cfg.Describe))
		b.WriteString("\n\n")
		described = true
	}
	if p := strings.TrimSpace(reqs.Preamble); p != "" {
		b.WriteString(p)
		b.WriteString("\n\n")
		described = true
	}
	if !described && len(reqs.Sections) == 0 {
		b.WriteString("No requirements yet: the narrative starts at intake.\n\n")
	}
	var mains, outs []string
	for _, r := range reqs.Sections {
		if strings.EqualFold(r.Field("priority"), "wont") {
			outs = append(outs, r.Title)
			continue
		}
		if strings.EqualFold(r.Field("priority"), "must") {
			line := "**" + r.Title + "**"
			if fs := firstSentence(r.Body); fs != "" {
				line += " — " + fs
			}
			mains = append(mains, line)
		}
	}
	if len(mains) > 0 {
		b.WriteString("**Main features**\n\n")
		for _, m := range mains {
			fmt.Fprintf(&b, "- %s\n", m)
		}
		b.WriteString("\n")
	}
	if len(outs) > 0 {
		fmt.Fprintf(&b, "**Explicitly out of scope:** %s.\n\n", strings.Join(outs, "; "))
	}
	if len(reqs.Sections) > 0 {
		accepted := 0
		for _, t := range tasks {
			if t.Status == "accepted" {
				accepted++
			}
		}
		specCount := 0
		if spec != nil {
			specCount = len(spec.Sections)
		}
		fmt.Fprintf(&b, "**Scale:** %d requirements · %d spec sections · %d tasks, %d accepted.\n\n",
			len(reqs.Sections), specCount, len(tasks), accepted)
	}

	// Index: SPEC → its tasks; REQ → its specs.
	specsByReq := map[string][]artifact.Section{}
	if spec != nil {
		for _, sp := range spec.Sections {
			for _, r := range sp.Implements {
				specsByReq[r] = append(specsByReq[r], sp)
			}
		}
	}
	tasksBySpec := map[string][]TaskView{}
	for _, t := range tasks {
		for _, sp := range t.Implements {
			tasksBySpec[sp] = append(tasksBySpec[sp], t)
		}
	}

	// The matrix twice, on purpose: first as a table — the at-a-glance form
	// an auditor scans and a wiki embeds — then as blocks that carry the
	// prose. One row per requirement→spec edge, tasks aggregated with their
	// statuses inline, so the table stays scannable at four hops deep.
	if len(reqs.Sections) > 0 {
		b.WriteString("## Traceability matrix\n\n")
		b.WriteString("| Requirement | Priority | Spec section | Tasks (status) |\n")
		b.WriteString("|---|---|---|---|\n")
		for _, r := range reqs.Sections {
			prio := r.Field("priority")
			specs := specsByReq[r.ID]
			if len(specs) == 0 {
				cell := "⚠ none"
				if strings.EqualFold(prio, "wont") {
					cell = "excluded"
				}
				fmt.Fprintf(&b, "| %s %s | %s | %s | — |\n", r.ID, tableEscape(r.Title), prio, cell)
				continue
			}
			for i, sp := range specs {
				name := ""
				if i == 0 {
					name = fmt.Sprintf("%s %s", r.ID, tableEscape(r.Title))
				}
				var cells []string
				for _, t := range tasksBySpec[sp.ID] {
					cells = append(cells, fmt.Sprintf("%s (%s)", t.ID, t.Status))
				}
				taskCell := "⚠ none"
				if strings.EqualFold(sp.Field("priority"), "wont") {
					taskCell = "excluded"
				}
				if v := strings.ToLower(strings.TrimSpace(sp.Field("as-built"))); v == "yes" || v == "true" {
					// Delivered by the tree the project adopted, not by a task.
					taskCell = "as-built"
				}
				if len(cells) > 0 {
					taskCell = strings.Join(cells, ", ")
				}
				pcell := prio
				if i > 0 {
					pcell = ""
				}
				fmt.Fprintf(&b, "| %s | %s | %s %s | %s |\n", name, pcell, sp.ID, tableEscape(sp.Title), taskCell)
			}
		}
		b.WriteString("\n")
	}

	// And the same spine as blocks, because the table cannot carry the prose.
	b.WriteString("## Traceability — requirement → spec → tasks\n\n")
	for _, r := range reqs.Sections {
		fmt.Fprintf(&b, "### %s — %s", r.ID, r.Title)
		if pr := r.Field("priority"); pr != "" {
			fmt.Fprintf(&b, "  `%s`", pr)
		}
		b.WriteString("\n\n")
		if body := strings.TrimSpace(r.Body); body != "" {
			b.WriteString(body)
			b.WriteString("\n\n")
		}
		specs := specsByReq[r.ID]
		if len(specs) == 0 {
			if !strings.EqualFold(r.Field("priority"), "wont") {
				b.WriteString("- ⚠ no spec section implements this requirement\n\n")
			}
			continue
		}
		for _, sp := range specs {
			fmt.Fprintf(&b, "- **%s — %s**\n", sp.ID, sp.Title)
			ts := tasksBySpec[sp.ID]
			if len(ts) == 0 {
				if v := strings.ToLower(strings.TrimSpace(sp.Field("as-built"))); v == "yes" || v == "true" {
					b.WriteString("  - as-built: delivered by the existing code\n")
				} else if !strings.EqualFold(sp.Field("priority"), "wont") {
					b.WriteString("  - ⚠ no task delivers this section\n")
				}
				continue
			}
			for _, t := range ts {
				fmt.Fprintf(&b, "  - %s — %s · **%s**\n", t.ID, t.Title, t.Status)
			}
		}
		b.WriteString("\n")
	}

	// Bug fixes justify themselves by the report, not by a spec section.
	var fixes []TaskView
	for _, t := range tasks {
		if len(t.Implements) == 0 && strings.Contains(t.Body, "Fixes B-") {
			fixes = append(fixes, t)
		}
	}
	if len(fixes) > 0 {
		b.WriteString("## Bug fixes\n\n")
		sort.Slice(fixes, func(i, j int) bool { return fixes[i].ID < fixes[j].ID })
		for _, t := range fixes {
			report := ""
			for _, line := range strings.Split(t.Body, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "Fixes B-") {
					report = strings.TrimSuffix(strings.TrimSpace(line), ".")
					break
				}
			}
			fmt.Fprintf(&b, "- %s — %s (%s) · **%s**\n", t.ID, t.Title, report, t.Status)
		}
		b.WriteString("\n")
	}

	// Releases, newest last so the report reads forward in time.
	if tags, tErr := git.Tags(); tErr == nil {
		var versions []release.Version
		for _, t := range tags {
			if v, ok := release.ParseVersion(t); ok {
				versions = append(versions, v)
			}
		}
		if len(versions) > 0 {
			sort.Slice(versions, func(i, j int) bool {
				a, c := versions[i], versions[j]
				if a.Major != c.Major {
					return a.Major < c.Major
				}
				if a.Minor != c.Minor {
					return a.Minor < c.Minor
				}
				return a.Patch < c.Patch
			})
			b.WriteString("## Releases\n\n")
			for _, v := range versions {
				fmt.Fprintf(&b, "- %s\n", v)
			}
			b.WriteString("\n")
		}
	}

	// The breaks, because a development report that hides them is marketing.
	if res, tErr := s.TraceCheck(ctx, projectID); tErr == nil {
		if len(res.Errors) > 0 {
			fmt.Fprintf(&b, "## Spine health — %d break(s)\n\n", len(res.Errors))
			for _, e := range res.Errors {
				fmt.Fprintf(&b, "- %s — %s\n", e.ID, e.Detail)
			}
			b.WriteString("\n")
		} else {
			b.WriteString("## Spine health\n\nNo breaks: every must requirement is specified, " +
				"every spec section is delivered or excluded, every task is justified.\n")
		}
	}
	return b.String(), nil
}

// tableEscape keeps a title from breaking its own row.
func tableEscape(v string) string { return strings.ReplaceAll(v, "|", "\\|") }

// firstSentence extracts a body's opening claim: the text up to the first
// period-and-space, skipping field lines, capped so a run-on body cannot
// swallow the summary.
func firstSentence(body string) string {
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "**") || strings.HasPrefix(t, "-") {
			continue
		}
		if i := strings.Index(t, ". "); i > 0 {
			t = t[:i+1]
		}
		if len(t) > 220 {
			t = t[:220] + "…"
		}
		return t
	}
	return ""
}
