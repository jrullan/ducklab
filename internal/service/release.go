package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/release"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/strategy"
	"github.com/jrullan/ducklab/internal/tools"
	"github.com/jrullan/ducklab/internal/vcs"
)

// ReleaseRequest asks for a release to be planned.
type ReleaseRequest struct {
	// Bump is major, minor or patch. Empty means minor (05 §9.1).
	Bump string `json:"bump"`
	// Revise is what to change about the draft already on the table — the
	// same door every document stage has. The scribe gets the prior
	// proposal and this note, and rewrites; the older paused release run is
	// superseded when the new draft lands.
	Revise string `json:"revise,omitempty"`
}

// ReleasePlan collects what has shipped and drafts the notes (05 §9.1).
//
// The contents are collected deterministically from accepted runs and tags.
// One scribe turn writes the prose a user reads, and nothing else: what
// actually shipped is a matter of record, and a release whose inventory was
// paraphrased is a release nobody can audit.
func (s *Service) ReleasePlan(ctx context.Context, projectID string, req ReleaseRequest) (*runlog.Run, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	if err := checkRunnable(entry.Path); err != nil {
		return nil, err
	}

	git := vcs.New(entry.Path)
	tags, err := git.Tags()
	if err != nil {
		return nil, fmt.Errorf("release: %w", err)
	}
	prev, hadPrev := release.Latest(tags)
	next, err := release.Bump(prev, req.Bump)
	if err != nil {
		return nil, err
	}
	// A revision is ABOUT the draft on the table, whatever bump made it:
	// the UI asking for changes does not know the original bump, and
	// recomputing from a default could aim at a different version.
	if strings.TrimSpace(req.Revise) != "" {
		if v, ok := newestProposed(entry.Path); ok {
			next = v
		}
	}
	since := ""
	if hadPrev {
		since = prev.String()
	}

	items, landed, unverifiedIDs, err := s.releaseInventory(ctx, projectID, entry.Path, since)
	if err != nil {
		return nil, err
	}
	notes := release.Notes{
		Version: next, Since: since, Milestones: release.Group(items), Landed: landed,
		Unverified: len(unverifiedIDs), UnverifiedTasks: unverifiedIDs,
	}
	// A revision reads the draft it revises. Refused without one: a note
	// about a draft that does not exist is a launch mistake worth catching.
	revise := strings.TrimSpace(req.Revise)
	priorDraft := ""
	if revise != "" {
		raw, rerr := os.ReadFile(release.Path(entry.Path, next) + ".proposed")
		if rerr != nil {
			return nil, fmt.Errorf("release revise: no draft for %s to revise — draft one first", next)
		}
		priorDraft = string(raw)
	}

	run := &runlog.Run{
		ID:        runlog.GenerateRunID(),
		ProjectID: projectID,
		Stage:     "release",
		Mode:      "solo",
		Status:    "running",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		Stream:    true,
		// Nothing executable runs, so the verdict is UNVERIFIED until a person
		// approves the notes (P3).
		Gate: "none",
	}
	writer, err := runlog.NewWriter(entry.Path, run)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	rs := &runState{
		run: run, writer: writer, runDir: writer.RunDir(),
		projectPath: entry.Path, cancel: cancel, done: make(chan struct{}),
	}
	s.attachWriter(rs, writer)
	s.runsMu.Lock()
	s.runs[run.ID] = rs
	s.runsMu.Unlock()

	writer.AppendEvent("run_start", map[string]interface{}{
		"stage": "release", "mode": "solo",
		"version": next.String(), "since": since, "tasks": len(items),
	})

	// The old draft's run is superseded the moment its revision starts —
	// the same rule the document stages follow: a gate over a draft that is
	// being replaced is a gate nobody can decide.
	if revise != "" {
		s.runsMu.RLock()
		var moot []string
		for id, other := range s.runs {
			if id != run.ID && other.run.ProjectID == projectID && other.run.Stage == "release" &&
				other.run.Status == "paused" {
				moot = append(moot, id)
			}
		}
		s.runsMu.RUnlock()
		for _, id := range moot {
			s.resolveSuperseded(id, "changes requested: "+revise)
		}
	}

	go s.executeRelease(runCtx, rs, entry.Path, notes, revise, priorDraft)
	return run, nil
}

// releaseInventory classifies every commit in the release range. A
// Ducklab-Run trailer requires a corresponding accepted task run; no trailer is
// ordinary landed work. Anything else makes a completeness claim impossible.
func (s *Service) releaseInventory(ctx context.Context, projectID, root, sinceTag string) ([]release.Item, []release.Landed, []string, error) {
	runs, err := s.RunList(ctx, RunFilter{ProjectID: projectID})
	if err != nil {
		return nil, nil, nil, err
	}
	commits, err := vcs.New(root).CommitsAfter(sinceTag)
	if err != nil {
		return nil, nil, nil, err
	}
	byID := map[string]*runlog.Run{}
	for _, r := range runs {
		if r.Accepted && r.TaskID != "" {
			byID[r.ID] = r
		}
	}
	titles := s.taskTitles(ctx, projectID)
	var items []release.Item
	var landed []release.Landed
	var unverified, uncovered []string
	git := vcs.New(root)
	for _, c := range commits {
		if c.DucklabRun == "" {
			entry := release.Landed{SHA: c.SHA, Subject: c.Subject, Author: c.Author}
			if match := localPRRe.FindStringSubmatch(c.Subject); match != nil {
				fmt.Sscanf(match[1], "%d", &entry.PRNumber)
				entry.PRTitle = strings.TrimSpace(strings.TrimSuffix(c.Subject, match[0]))
			} else if pr, ok := git.PRForCommit(ctx, c.SHA); ok {
				entry.PRNumber, entry.PRTitle = pr.Number, pr.Title
			}
			landed = append(landed, entry)
			continue
		}
		r := byID[c.DucklabRun]
		if r == nil {
			uncovered = append(uncovered, c.SHA)
			continue
		}
		t := titles[r.TaskID]
		items = append(items, release.Item{TaskID: r.TaskID, Title: t.title, Milestone: t.milestone, CommitSHA: c.SHA, Summary: t.summary})
		if r.Verdict == "UNVERIFIED" {
			unverified = append(unverified, r.TaskID)
		}
	}
	if len(uncovered) > 0 {
		return nil, nil, nil, fmt.Errorf("release inventory incomplete: commits with unresolved Ducklab-Run trailers: %s", strings.Join(uncovered, ", "))
	}
	return items, landed, unverified, nil
}

var localPRRe = regexp.MustCompile(`\s*\(#(\d+)\)$`)

type taskFacts struct{ title, milestone, summary string }

func (s *Service) taskTitles(ctx context.Context, projectID string) map[string]taskFacts {
	out := map[string]taskFacts{}
	tasks, err := s.TaskList(ctx, projectID)
	if err != nil {
		// A release can be described without the plan; the ids alone are still
		// true. Failing here would block a release over a missing document.
		return out
	}
	for _, t := range tasks {
		out[t.ID] = taskFacts{title: t.Title, milestone: t.Milestone, summary: firstParagraph(t.Body, 280)}
	}
	return out
}

// firstParagraph is a task body's opening, trimmed to a line: enough for a
// scribe to know what the work was for, short enough for forty of them to
// fit one prompt. Bullet lists (the deliverables) are not the opening.
func firstParagraph(body string, max int) string {
	for _, para := range strings.Split(strings.ReplaceAll(strings.TrimSpace(body), "\r\n", "\n"), "\n\n") {
		var kept []string
		for _, l := range strings.Split(para, "\n") {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "* ") || strings.HasSuffix(l, ":") {
				continue
			}
			kept = append(kept, l)
		}
		if len(kept) == 0 {
			continue
		}
		out := strings.Join(kept, " ")
		if len(out) > max {
			cut := out[:max]
			if i := strings.LastIndex(cut, " "); i > max/2 {
				cut = cut[:i]
			}
			out = cut + "…"
		}
		return out
	}
	return ""
}

func (s *Service) executeRelease(ctx context.Context, rs *runState, projectRoot string, notes release.Notes, revise, priorDraft string) {
	defer recoverRun(rs)
	defer close(rs.done)
	defer rs.writer.Close()

	prose := ""
	if len(notes.Milestones) > 0 || len(notes.Landed) > 0 {
		var err error
		prose, err = s.scribeNotes(ctx, rs, projectRoot, notes, revise, priorDraft)
		if err != nil {
			s.failRun(rs, err)
			return
		}
		// Never trust the reply to be prose-only: a scribe that returns a
		// whole document would be wrapped by Render into a doubled release.
		prose = extractReleaseProse(prose)
	}

	// Written as a proposal: a release is a claim about the software, and a
	// person signs it (05 §9.1).
	path := release.Path(projectRoot, notes.Version) + ".proposed"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		s.failRun(rs, err)
		return
	}
	if err := os.WriteFile(path, []byte(release.Render(notes, prose)), 0o644); err != nil {
		s.failRun(rs, err)
		return
	}
	rel, _ := filepath.Rel(projectRoot, path)

	rs.run.Verdict = "UNVERIFIED"
	rs.writer.AppendEvent("release_drafted", map[string]interface{}{
		"version": notes.Version.String(), "path": rel, "tasks": len(notes.Milestones),
	})
	rs.run.Status = "paused"
	rs.run.PendingKind = "gate"
	rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
	rs.run.PendingData = map[string]interface{}{
		"release": rel, "version": notes.Version.String(),
	}
	rs.writer.AppendEvent("human_needed", map[string]interface{}{
		"kind": "gate", "release": rel, "version": notes.Version.String(),
	})
	rs.writer.WriteState()
}

// scribeNotes runs the one turn a release involves a model in.
func (s *Service) scribeNotes(ctx context.Context, rs *runState, projectRoot string, notes release.Notes, revise, priorDraft string) (string, error) {
	projCfg, err := config.LoadProject(filepath.Join(projectRoot, ".ducklab", "project.toml"))
	if err != nil {
		return "", fmt.Errorf("load project config: %w", err)
	}
	roster, _ := s.resolveRoster(projCfg, "release")
	limitsValue := projectBudget(budget.Budget{
		MaxUSD: s.cfg.Defaults.Budget.MaxUSD, MaxTokens: int64(s.cfg.Defaults.Budget.MaxTokens),
		MaxWallclockS: s.cfg.Defaults.Budget.MaxWallclockS, MaxTurns: s.cfg.Defaults.Budget.MaxTurns,
	}, projCfg.Budget)
	limits := &limitsValue
	tracker := budget.NewTracker(limits)
	recordLimits(rs, limits)
	rs.setTracker(tracker)
	ectx := &tools.ExecContext{ProjectRoot: projectRoot, RunID: rs.run.ID}
	cache := &loopCache{
		svc: s, tracker: tracker,
		writer:  s.llmWriter(rs, tracker),
		capLift: rs.capLifted.Load,
		loops:   map[config.DucklingID]*agent.Loop{},
	}
	s.attachStreaming(rs, cache)

	params := &strategy.ExecuteParams{
		LiveToolEvents: true,
		ProjectRoot:    projectRoot,
		Prompt:         scribePrompt(notes) + revisionAddendum(revise, priorDraft),
		Runner:         s.runnerFor(cache, roster, ectx),
		Roster:         roster,
		OnEvent: func(kind string, data map[string]interface{}) {
			rs.writer.AppendEvent(kind, data)
		},
	}
	res, err := strategy.ExecuteScript(ctx, strategy.ReleaseScript(), params)
	recordSpend(rs, tracker)
	if err != nil {
		return "", pendingOrErr(res, err)
	}
	if res == nil || res.Outcome == nil {
		return "", nil
	}
	return res.Outcome.Text, nil
}

// revisionAddendum carries the person's note and the draft it is about.
// The prose section of the prior draft is what the scribe revises; the
// inventory is regenerated deterministically either way.
func revisionAddendum(revise, priorDraft string) string {
	if strings.TrimSpace(revise) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Revision requested\n\nA person read the draft below and asks for this change:\n\n")
	b.WriteString(strings.TrimSpace(revise))
	b.WriteString("\n\n## The draft being revised\n\n")
	// Prose only. The addendum used to show the whole prior file —
	// frontmatter, title, What shipped — and ask "rewrite the notes"; the
	// scribe reasonably returned a whole document, Render wrapped it again,
	// and the proposal carried the release twice (B-116).
	b.WriteString(strings.TrimSpace(extractReleaseProse(priorDraft)))
	b.WriteString("\n\nRewrite the prose with the change applied, and return ONLY the prose " +
		"paragraphs — no frontmatter, no version title, no What-shipped list; the engine " +
		"renders those. Keep what the note does not touch.\n")
	return b.String()
}

// extractReleaseProse cuts a release document (or a scribe reply shaped like
// one) down to its prose: frontmatter blocks, the version heading and
// everything from "## What shipped" onward are the engine's to render, and
// trusting a reply to be prose-only is how a document ships twice (B-116).
func extractReleaseProse(text string) string {
	lines := strings.Split(text, "\n")
	var kept []string
	inFront := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if inFront {
				inFront = false
				continue
			}
			// A frontmatter fence only opens at the start of the text or
			// right after prior scaffolding; a horizontal rule mid-prose
			// stays.
			if len(kept) == 0 || i == 0 {
				inFront = true
				continue
			}
		}
		if inFront {
			continue
		}
		if strings.HasPrefix(trimmed, "## What shipped") {
			break
		}
		if versionHeadingRe.MatchString(trimmed) {
			continue
		}
		kept = append(kept, line)
	}
	out := strings.TrimSpace(strings.Join(kept, "\n"))
	if out == "" {
		return strings.TrimSpace(text)
	}
	return out
}

var versionHeadingRe = regexp.MustCompile(`^#{1,2}\s+(Release\s+)?v?\d+\.\d+\.\d+\s*$`)

// newestProposed finds the highest-versioned .proposed draft on disk.
func newestProposed(root string) (release.Version, bool) {
	entries, err := os.ReadDir(release.Dir(root))
	if err != nil {
		return release.Version{}, false
	}
	var best release.Version
	found := false
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".md.proposed") {
			continue
		}
		if v, ok := release.ParseVersion(strings.TrimSuffix(name, ".md.proposed")); ok {
			if !found || release.Newer(v, best) {
				best, found = v, true
			}
		}
	}
	return best, found
}

// scribePrompt hands over the inventory and asks only for the prose.
//
// The list is stated, not summarised: the scribe is writing about work, and it
// cannot invent an entry it was never given.
func scribePrompt(n release.Notes) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Release %s\n\n", n.Version)
	if n.Since != "" {
		fmt.Fprintf(&b, "Everything accepted since %s.\n\n", n.Since)
	} else {
		b.WriteString("The first release.\n\n")
	}
	b.WriteString("## The accepted work\n\n")
	for _, m := range n.Milestones {
		fmt.Fprintf(&b, "### %s\n\n", m.ID)
		for _, it := range m.Items {
			title := it.Title
			if title == "" {
				title = it.TaskID
			}
			if it.Summary != "" {
				fmt.Fprintf(&b, "- %s: %s — %s\n", it.TaskID, title, it.Summary)
			} else {
				fmt.Fprintf(&b, "- %s: %s\n", it.TaskID, title)
			}
		}
		b.WriteString("\n")
	}
	if len(n.Landed) > 0 {
		b.WriteString("## Landed outside the loop\n\n")
		for _, it := range n.Landed {
			fmt.Fprintf(&b, "- %s (%s) — %s\n", it.Subject, it.SHA, it.Author)
		}
		b.WriteString("\n")
	}
	b.WriteString("Write the release notes for the people who use this software. " +
		"The inventory above is recorded separately and does not need repeating; " +
		"write only what changed for them and why it matters.\n\n" +
		"Everything you need is in this message: each item carries the task's own summary. " +
		"Do not read the tasks one by one — your tool budget here is small and reading them all will spend it before you write a word. " +
		"Answer with the notes as your reply, in prose, grouped by what a user would notice.\n")
	return b.String()
}

// ReleaseCut promotes a drafted release and tags the commit (05 §9.1).
//
// No model runs here. Cutting a release is a decision a person already made
// when they approved the draft; this records it.
func (s *Service) ReleaseCut(ctx context.Context, projectID, version string) (map[string]interface{}, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	v, ok := release.ParseVersion(version)
	if !ok {
		return nil, fmt.Errorf("release cut: %q is not a version like v1.2.3", version)
	}

	final := release.Path(entry.Path, v)
	draft := final + ".proposed"
	body, err := os.ReadFile(draft)
	if os.IsNotExist(err) {
		// A cut that failed after promoting the document leaves the notes in
		// place and no tag. Reading them lets the retry finish the job instead
		// of reporting a draft that is missing because it already succeeded
		// halfway.
		body, err = os.ReadFile(final)
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("release cut: no draft for %s; run `ducklab release plan` first", v)
		}
	}
	if err != nil {
		return nil, err
	}

	git := vcs.New(entry.Path)
	if git.HasTag(v.String()) {
		// Refused rather than moved. A tag that changes where it points makes
		// every earlier statement about that release retroactively false.
		return nil, fmt.Errorf("release cut: %s is already tagged", v)
	}
	// The desktop embeds its checked-in bundle. Refuse before promoting the
	// draft when its toolchain is absent, rather than tagging stale assets.
	if err := preflightDesktopToolchain(entry.Path); err != nil {
		return nil, err
	}

	if err := os.WriteFile(final, body, 0o644); err != nil {
		return nil, err
	}
	if err := os.Remove(draft); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// A release tag fixes the publicly declared version, so every
	// project-owned manifest that records a version must move with it —
	// otherwise build output, logs and render-contracts contradict the tag
	// (B-230). Sync the files before staging so the commit contains them
	// and the tag points at a tree that agrees with itself.
	synced, err := syncVersionedManifests(entry.Path, v)
	if err != nil {
		return nil, err
	}
	bundled, err := rebuildDesktopBundle(ctx, entry.Path)
	if err != nil {
		return nil, err
	}

	// The document is committed before the tag, so the tag points at a commit
	// that contains the notes describing it — and, from the sync above, the
	// matching manifest versions.
	adds := append(synced, final)
	adds = append(adds, bundled...)
	if err := git.Add(adds...); err != nil {
		return nil, fmt.Errorf("release cut: %w", err)
	}
	sha := ""
	if clean, cerr := git.IsClean(); cerr == nil && !clean {
		if sha, err = git.Commit(fmt.Sprintf("ducklab: release %s", v)); err != nil {
			return nil, fmt.Errorf("release cut: %w", err)
		}
	} else {
		sha, _ = git.HeadSHA()
	}
	if err := git.Tag(v.String(), fmt.Sprintf("ducklab release %s", v)); err != nil {
		return nil, fmt.Errorf("release cut: %w", err)
	}

	// The tag is pushed under the same on_accept policy a push-configured accept
	// uses (B-266). The cut itself already succeeded and the tag is durable; a
	// failed publication is recorded audibly and reported without reversing the
	// release, and the local tag stands; the worded warning rides alongside the
	// normal result so the operator sees the tag did not reach the remote.
	rel, _ := filepath.Rel(entry.Path, final)
	outer := map[string]interface{}{
		"version": v.String(), "tag": v.String(), "commit": sha, "notes": rel,
	}
	if err := s.publishReleaseTag(ctx, projectID, entry.Path, "release", v.String()); err != nil {
		outer["warning"] = err.Error()
	}
	return outer, nil
}

// desktopLookPath is a variable so toolchain refusal can be tested without
// depending on the host's installed Node toolchain.
var (
	desktopLookPath       = exec.LookPath
	desktopCommandContext = exec.CommandContext
)

// preflightDesktopToolchain confirms that a project which ships the embedded
// desktop bundle can rebuild it. Projects without the desktop source do not
// have a bundle for a release cut to own.
func preflightDesktopToolchain(projectRoot string) error {
	if _, err := os.Stat(filepath.Join(projectRoot, "cmd", "ducklab-desktop", "frontend")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("release cut: stat desktop frontend: %w", err)
	}
	for _, tool := range []string{"node", "npm"} {
		if _, err := desktopLookPath(tool); err != nil {
			return fmt.Errorf("release cut: missing desktop toolchain: %s", tool)
		}
	}
	return nil
}

// rebuildDesktopBundle regenerates the frontend assets that the desktop embeds
// and copies them to the tracked bundle directory for the release commit.
func rebuildDesktopBundle(ctx context.Context, projectRoot string) ([]string, error) {
	desktop := filepath.Join(projectRoot, "cmd", "ducklab-desktop", "frontend")
	if _, err := os.Stat(desktop); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("release cut: stat desktop frontend: %w", err)
	}
	frontend := filepath.Join(projectRoot, "frontend")
	cmd := desktopCommandContext(ctx, "npm", "run", "build")
	cmd.Dir = frontend
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("release cut: rebuild desktop frontend: %w: %s", err, strings.TrimSpace(string(output)))
	}
	source := filepath.Join(frontend, "dist")
	target := filepath.Join(desktop, "dist")
	if err := os.RemoveAll(target); err != nil {
		return nil, fmt.Errorf("release cut: remove desktop bundle: %w", err)
	}
	if err := copyDirectory(source, target); err != nil {
		return nil, fmt.Errorf("release cut: copy desktop bundle: %w", err)
	}
	return []string{filepath.Join("cmd", "ducklab-desktop", "frontend", "dist")}, nil
}

func copyDirectory(source, target string) error {
	return filepath.WalkDir(source, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, rel)
		if d.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

// syncVersionedManifests rewrites every project-owned manifest that records
// the project's own version to the given release version, leaving dependency
// constraints untouched.
//
// Enumerated rather than scanned: a version-bearing file is a decision a
// project makes about what the software claims to be, and discovering them by
// pattern (say, every package.json) would sweep in a vendored or example
// manifest whose version is not ducklab's. When a project grows another
// manifest, this list is where it is declared.
//
// The paths are relative to the project root; a missing entry is a no-op (not
// every project ships a frontend). The rewrite keeps the file valid JSON with
// the same fields in the same order, so the diff stays human-sized.
func syncVersionedManifests(projectRoot string, v release.Version) ([]string, error) {
	synced := []string{}
	for _, rel := range []string{"frontend/package.json", "frontend/package-lock.json"} {
		path := filepath.Join(projectRoot, rel)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("release cut: stat %s: %w", rel, err)
		}
		changed, err := rewriteManifestVersion(path, v.String()[1:])
		if err != nil {
			return nil, err
		}
		if changed {
			synced = append(synced, rel)
		}
	}
	return synced, nil
}

// rewriteManifestVersion sets the manifest's own "version" field to want,
// leaving every other byte — in particular the dependency constraints, and in
// a package-lock both the top-level version and the root package entry's
// version — as they were.
//
// Line-surgical rather than a JSON round-trip: re-marshalling a map would
// reorder keys (json.Marshal sorts map keys), turning a one-line version bump
// of a commit-sized lockfile into a file-wide diff of dependency entries that
// did not change.
func rewriteManifestVersion(path, want string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("release cut: read %s: %w", path, err)
	}
	// Validate before editing so a malformed manifest is refused rather than
	// rewritten by guesswork.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false, fmt.Errorf("release cut: parse %s: %w", path, err)
	}

	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	depth := 0 // which object the current line's start is inside
	inPackages := false
	pkgKey := "" // the entry of "packages" whose field we are inside
	changed := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// A key that opens an entry of the flat "packages" map; the root
		// project's entry is the one whose key is "".
		if inPackages && depth == 2 && strings.HasSuffix(trimmed, "{") {
			if k, ok := manifestKey(trimmed); ok {
				pkgKey = k
			}
		}
		if strings.HasPrefix(trimmed, `"version":`) {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, `"version":`))
			close := strings.LastIndex(rest, `"`)
			if close >= 1 {
				old := rest[1:close]
				suffix := rest[close+1:] // the comma (and anything after it)
				topLevel := depth == 1 && !inPackages
				rootEntry := inPackages && depth == 3 && pkgKey == ""
				if (topLevel || rootEntry) && old != want {
					indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
					lines[i] = indent + `"version": "` + want + `"` + suffix
					changed = true
				}
			}
		}
		if trimmed == `"packages": {` {
			inPackages = true
		}
		depth += strings.Count(trimmed, "{") - strings.Count(trimmed, "}")
		if inPackages && depth <= 1 {
			inPackages = false
		}
	}

	if !changed {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return false, fmt.Errorf("release cut: write %s: %w", path, err)
	}
	return true, nil
}

// manifestKey reads the key of a line like `"node_modules/foo": {` or the
// lockfile's root package entry `"": {`.
func manifestKey(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, `"`) {
		return "", false
	}
	end := strings.Index(trimmed, `":`)
	if end < 1 {
		return "", false
	}
	return trimmed[1:end], true
}

// ReleaseSummary is one release, cut or drafted.
type ReleaseSummary struct {
	Version    string `json:"version"`
	Since      string `json:"since,omitempty"`
	Tasks      int    `json:"tasks"`
	Unverified int    `json:"unverified,omitempty"`
	// UnverifiedTasks names the accepted-without-a-gate changes.
	UnverifiedTasks []string `json:"unverified_tasks,omitempty"`
	// Drafted is true while the notes are still awaiting a person. A draft and
	// a cut release are not the same claim, and a list that showed them alike
	// would let an unapproved one be read as shipped.
	Drafted bool `json:"drafted"`
	Tagged  bool `json:"tagged"`
}

// ReleaseList returns the releases on file, newest version first.
func (s *Service) ReleaseList(ctx context.Context, projectID string) ([]ReleaseSummary, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	names, err := os.ReadDir(release.Dir(entry.Path))
	if os.IsNotExist(err) {
		return []ReleaseSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	tags, _ := vcs.New(entry.Path).Tags()
	tagged := map[string]bool{}
	for _, t := range tags {
		tagged[t] = true
	}

	out := make([]ReleaseSummary, 0, len(names))
	for _, n := range names {
		if n.IsDir() {
			continue
		}
		name := n.Name()
		draft := strings.HasSuffix(name, ".proposed")
		name = strings.TrimSuffix(name, ".proposed")
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		version := strings.TrimSuffix(name, ".md")
		// A draft of a version that is already tagged is not a release and
		// not a decision either: the cut promoted the notes and the tag was
		// laid; whatever left the .proposed behind (a checkout that restored
		// it, a plan re-run) cannot be cut again — ReleaseCut refuses a
		// tagged version. Listing it beside the cut one showed "v0.5.0
		// tagged" and "v0.5.0 drafted" as if there were two.
		if draft && tagged[version] {
			continue
		}
		body, err := os.ReadFile(filepath.Join(release.Dir(entry.Path), n.Name()))
		if err != nil {
			continue
		}
		sum := summariseRelease(version, string(body))
		sum.Drafted = draft
		sum.Tagged = tagged[version]
		out = append(out, sum)
	}
	sort.Slice(out, func(i, j int) bool {
		a, aok := release.ParseVersion(out[i].Version)
		b, bok := release.ParseVersion(out[j].Version)
		if aok && bok {
			return release.Newer(a, b)
		}
		return out[i].Version > out[j].Version
	})
	return out, nil
}

// ReleaseGet returns one release's markdown, draft or final.
func (s *Service) ReleaseGet(ctx context.Context, projectID, version string) (string, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return "", err
	}
	v, ok := release.ParseVersion(version)
	if !ok {
		return "", fmt.Errorf("%q is not a version like v1.2.3", version)
	}
	final := release.Path(entry.Path, v)
	if body, err := os.ReadFile(final); err == nil {
		return string(body), nil
	}
	body, err := os.ReadFile(final + ".proposed")
	if os.IsNotExist(err) {
		return "", fmt.Errorf("no release %s", v)
	}
	return string(body), err
}

func summariseRelease(version, body string) ReleaseSummary {
	sum := ReleaseSummary{Version: version}
	for _, line := range strings.Split(body, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "since":
			sum.Since = strings.TrimSpace(v)
		case "tasks":
			fmt.Sscanf(strings.TrimSpace(v), "%d", &sum.Tasks)
		case "unverified":
			fmt.Sscanf(strings.TrimSpace(v), "%d", &sum.Unverified)
		case "unverified_tasks":
			for _, id := range strings.Split(v, ",") {
				if id = strings.TrimSpace(id); id != "" {
					sum.UnverifiedTasks = append(sum.UnverifiedTasks, id)
				}
			}
		}
	}
	return sum
}
