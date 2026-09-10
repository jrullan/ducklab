package stage

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
)

// The plan amendment: Review's light exit, executed light.
//
// The first implementation ran it as a whole-document revision, which meant a
// cosmetic two-task amendment carried the entire plan in every prompt — 30k
// tokens a call, times a council, times rounds — and required the model to
// re-emit all hundred tasks verbatim, betting the document on it not
// truncating. The architect now returns ONLY the new task sections; the
// engine copies everything else by code, assigns real ids, and places the
// tasks under their milestone. What a model never re-types, a model cannot
// lose.

func runExtend(ctx context.Context, p Params, current *artifact.Document) (*Result, error) {
	kind := p.Stage.Kind()
	if p.Stage != Plan {
		return nil, fmt.Errorf("extend amends the plan; %s grows through a brief", p.Stage)
	}
	if current == nil || len(current.Sections) == 0 {
		return nil, fmt.Errorf("no plan to extend")
	}

	prior := p.PriorFragment
	if prior == "" && p.Drafts != nil {
		// Tests and direct callers may carry the prior architect fragment via
		// the stand-pat draft channel; the service supplies the proposal above.
		if drafts := p.Drafts(); len(drafts) > 0 {
			prior = drafts[0]
		}
	}
	effectiveChange := effectiveExtendChange(p)
	prompt, err := buildExtendPrompt(p.ProjectRoot, current, effectiveChange, previousExtensionFragment(current, prior))
	if err != nil {
		return nil, err
	}
	// This is an update over an approved topology. Re-running the first-draft
	// manifest persona gives the amendment turn two incompatible jobs: return a
	// JSON topology and return only new Markdown task fragments. Use the same
	// update script as sectioned and fragment revisions.
	script := artifactUpdateScript(kind.Prefix(), p.Mode, p.Critics)
	if p.Rounds > 0 {
		script.MaxRounds = p.Rounds
	}
	// No document contract on an amendment. ArtifactScript demands
	// markdown_sections:M — a full plan's shape — while the amendment prompt
	// demands a T-900 fragment: two contradictory contracts in one turn.
	// Models split between them: one fused its task into an M- heading to
	// satisfy the validator (the phantom-task shape), another obeyed the
	// fragment and was executed by the M contract — "no sections matching M
	// found". The fragment contract in the prompt is the only one that
	// speaks; runExtend's own parse and refusal handling judge the reply.
	for i := range script.Turns {
		script.Turns[i].Contract = ""
	}
	// The evidence rides the architect's own turn, like a bug's screenshots
	// ride the triager's.
	if len(p.Images) > 0 {
		for i := range script.Turns {
			if script.Turns[i].Role == config.RoleArchitect {
				script.Turns[i].Images = p.Images
				break
			}
		}
	}
	raw, err := p.Execute(ctx, script, prompt)
	if err != nil {
		return nil, err
	}

	taskFragment, namedReplacements, err := extractNamedPlanReplacements(raw, current)
	if err != nil {
		return nil, err
	}
	tasks, real := parsePlanItems(taskFragment)
	if real == 0 && len(namedReplacements) == 0 {
		// A council revise that stood pat replies in prose; the draft it
		// stood on is still the amendment. Fall back before refusing.
		for _, draft := range drafts(p) {
			fragment, replacements, extractErr := extractNamedPlanReplacements(draft, current)
			if extractErr != nil {
				return nil, extractErr
			}
			if t2, r2 := parsePlanItems(fragment); r2 > 0 || len(replacements) > 0 {
				tasks, real, namedReplacements = t2, r2, replacements
				break
			}
		}
	}
	if real == 0 && len(namedReplacements) == 0 {
		// By contract this is the architect judging the change core — or
		// producing nothing usable. Either way the person gets the words.
		return nil, fmt.Errorf("the architect added no tasks: %s", clip(raw))
	}

	var proposed *artifact.Document
	var dependencyAmendments []planDependencyAmendment
	if p.SplitTask != "" {
		if len(namedReplacements) > 0 {
			return nil, fmt.Errorf("a task split cannot also replace named plan sections")
		}
		proposed, err = mergeSplit(current, p.SplitTask, tasks)
		if err != nil {
			return nil, err
		}
	} else {
		var additions []artifact.Section
		additions, dependencyAmendments, err = partitionExtensionTasks(current, tasks)
		if err != nil {
			return nil, err
		}
		proposed = mergeExtension(current, additions)
		if err = applyDependencyAmendments(current, proposed, additions, dependencyAmendments); err != nil {
			return nil, err
		}
		if proposed, err = applyNamedPlanReplacements(proposed, namedReplacements); err != nil {
			return nil, err
		}
	}
	proposed.Front.Kind = kind
	proposed.Front.Project = current.Front.Project
	if dropped := dedupeSections(proposed); len(dropped) > 0 && p.OnEvent != nil {
		p.OnEvent("dedupe", map[string]interface{}{"kind": string(kind), "dropped": dropped})
	}
	reviewAsk := extensionReviewAsk(effectiveChange, dependencyAmendments, namedReplacements)
	mechanical, semantic, err := reviewComposition(ctx, p, kind, reviewAsk, current, proposed)
	if err != nil {
		return nil, err
	}
	if err := writeProposal(p, kind, proposed); err != nil {
		return nil, err
	}
	return &Result{Kind: kind, Proposed: proposed, Raw: raw,
		CompositionMechanical: mechanical, CompositionReview: semantic}, nil
}

type planDependencyAmendment struct {
	TaskID    string
	DependsOn []string
}

// partitionExtensionTasks admits one deliberately narrow mutation of an
// existing task: a dependency-only stub. Everything else keeps the historical
// refusal, so an extension cannot smuggle a Work unit or task-body rewrite in
// beside new work.
func partitionExtensionTasks(current *artifact.Document, tasks []artifact.Section) ([]artifact.Section, []planDependencyAmendment, error) {
	existing := map[string]*artifact.Section{}
	for mi := range current.Sections {
		for ti := range current.Sections[mi].Children {
			task := &current.Sections[mi].Children[ti]
			existing[strings.ToUpper(task.ID)] = task
		}
	}
	var additions []artifact.Section
	var amendments []planDependencyAmendment
	for _, task := range tasks {
		old := existing[strings.ToUpper(strings.TrimSpace(task.ID))]
		if old == nil || looksLikeMilestoneDecl(task) {
			additions = append(additions, task)
			continue
		}
		if strings.TrimSpace(task.Title) != "" && !strings.EqualFold(strings.TrimSpace(task.Title), strings.TrimSpace(old.Title)) {
			return nil, nil, existingTaskRewriteError(task.ID)
		}
		depends := task.Field("depends on")
		if strings.TrimSpace(depends) == "" || !dependencyOnlyStub(task.Body) {
			return nil, nil, existingTaskRewriteError(task.ID)
		}
		amendments = append(amendments, planDependencyAmendment{TaskID: old.ID, DependsOn: splitPlanItems(depends)})
	}
	return additions, amendments, nil
}

func existingTaskRewriteError(id string) error {
	return fmt.Errorf("plan extension tried to rewrite existing task %s; extension may add tasks and dependency-only stubs but must not silently rewrite existing work", id)
}

func dependencyOnlyStub(body string) bool {
	seen := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(trimmed), "**depends on:**") && !seen {
			seen = true
			continue
		}
		return false
	}
	return seen
}

func splitPlanItems(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(strings.Trim(item, "`")); item != "" {
			out = append(out, strings.ToUpper(item))
		}
	}
	return out
}

func applyDependencyAmendments(current, proposed *artifact.Document, additions []artifact.Section, amendments []planDependencyAmendment) error {
	if len(amendments) == 0 {
		return nil
	}
	currentTasks := map[string]*artifact.Section{}
	for mi := range current.Sections {
		for ti := range current.Sections[mi].Children {
			task := &current.Sections[mi].Children[ti]
			currentTasks[strings.ToUpper(task.ID)] = task
		}
	}
	proposedTasks := map[string]*artifact.Section{}
	for mi := range proposed.Sections {
		for ti := range proposed.Sections[mi].Children {
			task := &proposed.Sections[mi].Children[ti]
			proposedTasks[strings.ToUpper(task.ID)] = task
		}
	}
	placeholder := map[string]string{}
	var existing []artifact.Section
	for _, milestone := range current.Sections {
		existing = append(existing, milestone.Children...)
	}
	for _, task := range additions {
		if looksLikeMilestoneDecl(task) {
			continue
		}
		realID := fmt.Sprintf("T-%03d", NextFree(existing, "T"))
		existing = append(existing, artifact.Section{ID: realID})
		key := strings.ToUpper(task.ID)
		if placeholder[key] == "" {
			placeholder[key] = realID
		}
	}
	for _, amendment := range amendments {
		old, target := currentTasks[strings.ToUpper(amendment.TaskID)], proposedTasks[strings.ToUpper(amendment.TaskID)]
		if old == nil || target == nil {
			return fmt.Errorf("dependency amendment target %s does not exist", amendment.TaskID)
		}
		deps := splitPlanItems(old.Field("depends on"))
		seen := map[string]bool{}
		for _, dep := range deps {
			seen[dep] = true
		}
		added := 0
		for _, dep := range amendment.DependsOn {
			if real := placeholder[dep]; real != "" {
				dep = real
			}
			dep = strings.ToUpper(dep)
			if dep == strings.ToUpper(target.ID) || proposedTasks[dep] == nil {
				return fmt.Errorf("dependency amendment for %s names unknown or self dependency %s", target.ID, dep)
			}
			if !seen[dep] {
				seen[dep] = true
				deps = append(deps, dep)
				added++
			}
		}
		if added == 0 {
			return fmt.Errorf("dependency amendment for %s adds no dependency", target.ID)
		}
		setTaskScalarField(target, "Depends on", strings.Join(deps, ", "))
	}
	return nil
}

func setTaskScalarField(task *artifact.Section, field, value string) {
	fieldPrefix := "**" + field + ":**"
	lines := strings.Split(task.Body, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), strings.ToLower(fieldPrefix)) {
			lines[i] = fieldPrefix + " " + value
			task.Body = strings.TrimSpace(strings.Join(lines, "\n"))
			if task.Fields == nil {
				task.Fields = map[string]string{}
			}
			task.Fields[strings.ToLower(field)] = value
			return
		}
	}
	task.Body = strings.TrimSpace(task.Body + "\n\n" + fieldPrefix + " " + value)
	if task.Fields == nil {
		task.Fields = map[string]string{}
	}
	task.Fields[strings.ToLower(field)] = value
}

func extendChange(p Params) string {
	if p.SplitTask == "" {
		return p.Extend
	}
	return fmt.Sprintf("Replace task %s with exactly two narrowly-scoped task sections. Each replacement must have a non-empty **Owns:** field, and their Owns lanes must be pairwise disjoint. Preserve the original task's traceability (Milestone and Implements).", p.SplitTask)
}

// effectiveExtendChange is the one authority for every actor judging a revised
// amendment. The original request remains provenance; the operator's revision
// is authoritative wherever it narrows or changes that request.
func effectiveExtendChange(p Params) string {
	change := strings.TrimSpace(extendChange(p))
	revision := strings.TrimSpace(p.Revision)
	if revision == "" {
		return change
	}
	return "Original requested change:\n" + change +
		"\n\nOperator revisions, in order (each authoritative where it changes or narrows what precedes it):\n" + revision
}

type namedPlanReplacement struct {
	Heading  string
	Markdown string
}

func extensionReviewAsk(change string, dependencies []planDependencyAmendment, replacements []namedPlanReplacement) string {
	var scope []string
	for _, amendment := range dependencies {
		scope = append(scope, amendment.TaskID+" Depends on only")
	}
	for _, replacement := range replacements {
		scope = append(scope, "## "+replacement.Heading)
	}
	if len(scope) == 0 {
		return change
	}
	return strings.TrimSpace(change) + "\n\nEngine-authorized changes to existing plan content: " + strings.Join(scope, "; ") + ". No other existing task or named section may change."
}

var planIDHeading = regexp.MustCompile(`^[A-Z][A-Z0-9_-]*-\d+(?:\s|$)`)

type planHeadingBlock struct {
	Heading    string
	Start, End int
}

// namedPlanHeadingBlocks finds human-facing H2 sections embedded in a plan's
// preserved Markdown. A following task H3 also ends the named section: plans
// commonly place derived tables between two tasks even though only M/T ids are
// indexed by the artifact parser.
func namedPlanHeadingBlocks(text string) []planHeadingBlock {
	lines := strings.Split(text, "\n")
	var starts []planHeadingBlock
	inFence := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### ") {
			continue
		}
		heading := strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
		if !planIDHeading.MatchString(strings.ToUpper(heading)) {
			starts = append(starts, planHeadingBlock{Heading: heading, Start: i})
		}
	}
	for i := range starts {
		end := len(lines)
		inFence = false
		for j := starts[i].Start + 1; j < len(lines); j++ {
			trimmed := strings.TrimSpace(lines[j])
			if strings.HasPrefix(trimmed, "```") {
				inFence = !inFence
				continue
			}
			if inFence {
				continue
			}
			if strings.HasPrefix(trimmed, "## ") && !strings.HasPrefix(trimmed, "### ") {
				end = j
				break
			}
			if strings.HasPrefix(trimmed, "### ") && planIDHeading.MatchString(strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(trimmed, "### ")))) {
				end = j
				break
			}
		}
		starts[i].End = end
	}
	return starts
}

func extractNamedPlanReplacements(raw string, current *artifact.Document) (string, []namedPlanReplacement, error) {
	known := map[string]int{}
	for _, block := range namedPlanHeadingBlocks(artifact.RenderBody(current)) {
		known[strings.ToLower(block.Heading)]++
	}
	blocks := namedPlanHeadingBlocks(raw)
	if len(blocks) == 0 {
		return raw, nil, nil
	}
	lines := strings.Split(raw, "\n")
	remove := make([]bool, len(lines))
	var replacements []namedPlanReplacement
	seen := map[string]bool{}
	for _, block := range blocks {
		key := strings.ToLower(block.Heading)
		if known[key] != 1 {
			return "", nil, fmt.Errorf("plan extension cannot replace named section %q: expected exactly one existing H2 with that title", block.Heading)
		}
		if seen[key] {
			return "", nil, fmt.Errorf("plan extension repeats named section replacement %q", block.Heading)
		}
		seen[key] = true
		for i := block.Start; i < block.End; i++ {
			remove[i] = true
		}
		replacements = append(replacements, namedPlanReplacement{
			Heading: block.Heading, Markdown: strings.TrimSpace(strings.Join(lines[block.Start:block.End], "\n")),
		})
	}
	var kept []string
	for i, line := range lines {
		if !remove[i] {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n")), replacements, nil
}

func applyNamedPlanReplacements(doc *artifact.Document, replacements []namedPlanReplacement) (*artifact.Document, error) {
	if len(replacements) == 0 {
		return doc, nil
	}
	body := artifact.RenderBody(doc)
	for _, replacement := range replacements {
		blocks := namedPlanHeadingBlocks(body)
		match := -1
		for i, block := range blocks {
			if strings.EqualFold(block.Heading, replacement.Heading) {
				if match >= 0 {
					return nil, fmt.Errorf("plan contains more than one named section %q", replacement.Heading)
				}
				match = i
			}
		}
		if match < 0 {
			return nil, fmt.Errorf("plan has no named section %q to replace", replacement.Heading)
		}
		lines := strings.Split(body, "\n")
		block := blocks[match]
		updated := append([]string{}, lines[:block.Start]...)
		updated = append(updated, strings.Split(replacement.Markdown, "\n")...)
		updated = append(updated, lines[block.End:]...)
		body = strings.Join(updated, "\n")
	}
	parsed, err := artifact.Parse(body, artifact.KindPlan)
	if err != nil {
		return nil, err
	}
	parsed.Front = doc.Front
	return parsed, nil
}

// normalizeFragment makes the architect's fragment parseable as a plan: the
// contract's `## TASK — title` headings become H3 tasks under one synthetic
// milestone, which is what the plan parser reads — models also emit H3 or a
// dash id ("TASK-1") and both survive. The synthetic milestone never reaches
// the plan; mergeExtension flattens it away.
func normalizeFragment(raw string) string {
	var b strings.Builder
	b.WriteString("## M-000 — amendment fragment\n")
	for _, line := range strings.Split(raw, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "## ") && !strings.HasPrefix(t, "### ") {
			line = "#" + t // demote H2 headings to H3
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// buildExtendPrompt is compact by design: the plan as an OUTLINE (ids and
// titles — placement and duplicate-checking need no bodies), the spec as a
// wiring list, the change, and the fragment contract.
func buildExtendPrompt(projectRoot string, plan *artifact.Document, change, priorFragment string) (string, error) {
	var b strings.Builder

	memory, err := artifact.LoadMemory(projectRoot)
	if err == nil {
		if mc := memory.PromptContext(); mc != "" {
			b.WriteString(mc)
			b.WriteString("\n\n")
		}
	}

	b.WriteString("## Your task\n\nExtend this plan for the change below, WITHOUT a redesign. " +
		"Return ONLY the new task section(s) — never the rest of the plan; the engine merges " +
		"your fragment into the document itself.\n\n")
	b.WriteString("## The effective change\n\n" + strings.TrimSpace(change) + "\n\n")
	if strings.TrimSpace(priorFragment) != "" {
		b.WriteString("## Your previous amendment fragment to revise\n\n" + strings.TrimSpace(priorFragment) + "\n\n")
	}

	b.WriteString("## The plan today (outline)\n\n")
	for _, m := range plan.Sections {
		fmt.Fprintf(&b, "## %s — %s\n", m.ID, m.Title)
		for _, t := range m.Children {
			fmt.Fprintf(&b, "- %s — %s\n", t.ID, t.Title)
		}
	}
	b.WriteString("\n")
	if blocks := namedPlanHeadingBlocks(artifact.RenderBody(plan)); len(blocks) > 0 {
		b.WriteString("## Named plan sections you may replace\n\n")
		for _, block := range blocks {
			b.WriteString("- ## " + block.Heading + "\n")
		}
		b.WriteString("\n")
	}

	if spec, sErr := artifact.Load(projectRoot, artifact.KindSpec); sErr == nil && spec != nil && len(spec.Sections) > 0 {
		b.WriteString("## Spec sections you may wire to\n\n")
		for _, sp := range spec.Sections {
			fmt.Fprintf(&b, "- %s — %s\n", sp.ID, sp.Title)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Rules\n\n" +
		"- One to three tasks, the fewest that deliver the change, each formatted exactly:\n\n" +
		"## T-900 — <imperative title>\n" +
		"**Milestone:** <an existing M-id from the outline; omit to use the last>\n" +
		"**Implements:** <existing SPEC ids that genuinely cover this, comma-separated; omit " +
		"if none — the task will wear spec-debt until the spec catches up>\n" +
		"<the body, in the shape below>\n\n" +
		TaskBodyContract +
		"- Use placeholder ids in order: T-900 for the first task, T-901 for the second, T-902 for the third — " +
		"real ids are assigned by the engine. When one of these tasks consumes what another delivers, say so " +
		"with **Depends on:** naming the placeholder (e.g. `**Depends on:** T-900`); the engine rewrites it to " +
		"the real id. Never depend on a task that comes later in your own list.\n" +
		"- To make an EXISTING task wait for new work, append a stub: `## T-NNN — <exact current title>` plus only `**Depends on:**` with old dependencies and the new placeholder. Other changes are refused.\n" +
		"- To replace a listed named section, emit its exact `## <heading>` and complete body. H3 is reserved for task ids.\n" +
		"- Never invent SPEC ids; wire only to the list above.\n" +
		"- This amendment cannot remove tasks. Retire superseded tasks separately with task_remove.\n" +
		"- If the change alters what the product IS — its requirements — return NO sections: " +
		"one sentence saying why, and the person will write a feature brief instead.\n")
	return b.String(), nil
}

// previousExtensionFragment extracts only tasks added by the prior amendment.
// The proposal is a merged plan, but its added task ids do not exist in the
// approved plan it was based on.
func previousExtensionFragment(current *artifact.Document, raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	proposed, err := artifact.Parse(raw, artifact.KindPlan)
	if err != nil || proposed == nil || len(proposed.Sections) == 0 {
		return raw
	}
	existing := map[string]bool{}
	for _, milestone := range current.Sections {
		for _, task := range milestone.Children {
			existing[task.ID] = true
		}
	}
	var b strings.Builder
	for _, milestone := range proposed.Sections {
		for _, task := range milestone.Children {
			if existing[task.ID] {
				continue
			}
			fmt.Fprintf(&b, "## %s — %s\n\n%s\n\n", task.ID, task.Title, task.Body)
		}
	}
	return strings.TrimSpace(b.String())
}

// mergeSplit replaces target with exactly two proposed sections. It deliberately
// shares the normal fragment merger, then proves the replacements own disjoint
// non-empty lanes before a proposal can reach the approval gate.
func mergeSplit(current *artifact.Document, target string, tasks []artifact.Section) (*artifact.Document, error) {
	if len(tasks) != 2 {
		return nil, fmt.Errorf("splitting %s requires exactly two replacement sections", target)
	}
	var original *artifact.Section
	originalMilestone := ""
	for _, milestone := range current.Sections {
		for i := range milestone.Children {
			if milestone.Children[i].ID == target {
				copy := milestone.Children[i]
				original = &copy
				originalMilestone = milestone.ID
			}
		}
	}
	if original == nil {
		return nil, fmt.Errorf("no task %s in the plan", target)
	}
	for i := range tasks {
		if len(tasks[i].Owns) == 0 {
			return nil, fmt.Errorf("split replacement %q must declare a non-empty Owns lane", tasks[i].Title)
		}
		if len(tasks[i].Implements) == 0 {
			tasks[i].Implements = append([]string(nil), original.Implements...)
		}
		if tasks[i].Fields == nil {
			tasks[i].Fields = map[string]string{}
		}
		tasks[i].Fields["milestone"] = originalMilestone
		if !strings.Contains(tasks[i].Body, "**Milestone:**") {
			tasks[i].Body = "**Milestone:** " + originalMilestone + "\n" + tasks[i].Body
		}
	}
	out := mergeExtension(current, tasks)
	for mi := range out.Sections {
		children := out.Sections[mi].Children[:0]
		for _, task := range out.Sections[mi].Children {
			if task.ID != target {
				children = append(children, task)
			}
		}
		out.Sections[mi].Children = children
	}
	if collisions := artifact.LaneCollisions(out); len(collisions) > 0 {
		return nil, fmt.Errorf("split lanes overlap: %s", collisions[0].Detail)
	}
	return out, nil
}

// mergeExtension appends the produced tasks to a copy of the current plan:
// fresh sequential ids, placed under the named milestone or the last one.
// The untouched hundred tasks are copied by code, which cannot truncate.
func mergeExtension(current *artifact.Document, tasks []artifact.Section) *artifact.Document {
	out := *current
	out.Sections = make([]artifact.Section, len(current.Sections))
	for i := range current.Sections {
		out.Sections[i] = clonePlanSection(current.Sections[i])
	}

	var existing []artifact.Section
	for _, m := range out.Sections {
		existing = append(existing, m.Children...)
	}

	// An architect extending the plan may declare a NEW milestone: a heading
	// like "## M-015 — Dashboard UI" above its tasks. Flattened, that heading
	// arrived in the task list and became a phantom task — title, no body —
	// which the person launched first, and a test-writer handed an empty
	// brief invented one. A fragment section wearing the milestone prefix is
	// a placement declaration: find it by id or title, create it with a real
	// id when it is genuinely new, and alias the fragment's id so the tasks'
	// own Milestone: fields resolve to the milestone that actually exists.
	alias := map[string]string{}
	lastDeclared := ""
	resolveMilestone := func(decl artifact.Section) string {
		for i := range out.Sections {
			if strings.EqualFold(out.Sections[i].ID, decl.ID) ||
				strings.EqualFold(strings.TrimSpace(out.Sections[i].Title), strings.TrimSpace(decl.Title)) {
				return out.Sections[i].ID
			}
		}
		id := fmt.Sprintf("M-%03d", NextFree(out.Sections, "M"))
		out.Sections = append(out.Sections, artifact.Section{ID: id, Title: decl.Title})
		return id
	}

	// Placeholder task ids (T-900, T-901, …) map to the real ids assigned
	// below, in emission order, so a fragment's own **Depends on:** survives
	// the merge. An architect that repeats T-900 for every task (the old
	// contract) still gets a sane answer: the FIRST task wearing it — the one
	// later tasks mean when they say "the one before".
	placeholder := map[string]string{}
	var placedIDs []string

	for _, t := range tasks {
		if looksLikeMilestoneDecl(t) {
			real := resolveMilestone(t)
			alias[strings.ToUpper(t.ID)] = real
			lastDeclared = real
			continue
		}
		id := fmt.Sprintf("T-%03d", NextFree(existing, "T"))
		if key := strings.ToUpper(strings.TrimSpace(t.ID)); key != "" {
			if _, seen := placeholder[key]; !seen {
				placeholder[key] = id
			}
		}
		placedIDs = append(placedIDs, id)
		milestone := strings.TrimSpace(t.Field("milestone"))
		if real, ok := alias[strings.ToUpper(milestone)]; ok {
			milestone = real
		}
		if milestone == "" {
			milestone = lastDeclared
		}
		// The placement field did its job; the task body should not carry it.
		var kept []string
		for _, line := range strings.Split(t.Body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "**Milestone:**") {
				continue
			}
			kept = append(kept, line)
		}
		task := artifact.Section{
			ID:         id,
			Title:      t.Title,
			Body:       strings.TrimSpace(strings.Join(kept, "\n")),
			Implements: t.Implements,
			Owns:       append([]string(nil), t.Owns...),
		}
		existing = append(existing, task)

		placed := false
		for i := range out.Sections {
			if strings.EqualFold(out.Sections[i].ID, milestone) {
				out.Sections[i].Children = append(out.Sections[i].Children, task)
				placed = true
				break
			}
		}
		if !placed && len(out.Sections) > 0 {
			last := len(out.Sections) - 1
			out.Sections[last].Children = append(out.Sections[last].Children, task)
		}
	}
	rewriteDependsOn(&out, placedIDs, placeholder)
	return &out
}

func clonePlanSection(section artifact.Section) artifact.Section {
	out := section
	out.Implements = append([]string(nil), section.Implements...)
	out.Owns = append([]string(nil), section.Owns...)
	if section.Fields != nil {
		out.Fields = make(map[string]string, len(section.Fields))
		for key, value := range section.Fields {
			out.Fields[key] = value
		}
	}
	out.Children = make([]artifact.Section, len(section.Children))
	for i := range section.Children {
		out.Children[i] = clonePlanSection(section.Children[i])
	}
	return out
}

var placeholderRef = regexp.MustCompile(`\bT-9\d\d\b`)

// rewriteDependsOn replaces placeholder ids inside the new tasks'
// **Depends on:** lines with the real ids they became. A dependency on a
// task's own id is dropped; a placeholder nobody wore is left as written,
// where the person will see it.
func rewriteDependsOn(doc *artifact.Document, newIDs []string, placeholder map[string]string) {
	isNew := map[string]bool{}
	for _, id := range newIDs {
		isNew[id] = true
	}
	for mi := range doc.Sections {
		for ti := range doc.Sections[mi].Children {
			t := &doc.Sections[mi].Children[ti]
			if !isNew[t.ID] || !strings.Contains(t.Body, "**Depends on:**") {
				continue
			}
			lines := strings.Split(t.Body, "\n")
			for li, line := range lines {
				if !strings.HasPrefix(strings.TrimSpace(line), "**Depends on:**") {
					continue
				}
				rewritten := placeholderRef.ReplaceAllStringFunc(line, func(ref string) string {
					if real, ok := placeholder[strings.ToUpper(ref)]; ok {
						return real
					}
					return ref
				})
				// Drop a self-reference the rewrite may have produced.
				parts := strings.SplitN(rewritten, ":**", 2)
				if len(parts) == 2 {
					var keep []string
					for _, ref := range strings.Split(parts[1], ",") {
						ref = strings.TrimSpace(ref)
						if ref != "" && !strings.EqualFold(ref, t.ID) {
							keep = append(keep, ref)
						}
					}
					if len(keep) == 0 {
						rewritten = ""
					} else {
						rewritten = parts[0] + ":** " + strings.Join(keep, ", ")
					}
				}
				lines[li] = rewritten
			}
			t.Body = strings.TrimSpace(strings.Join(lines, "\n"))
			if t.Fields != nil {
				if v, ok := t.Fields["depends on"]; ok {
					t.Fields["depends on"] = placeholderRef.ReplaceAllStringFunc(v, func(ref string) string {
						if real, ok := placeholder[strings.ToUpper(ref)]; ok {
							return real
						}
						return ref
					})
				}
			}
		}
	}
}

// looksLikeMilestoneDecl separates placement from work by the evidence, not
// the id alone. The contract asks for T-900 headings, but an architect fused
// its milestone and its task into one M- heading — carrying a full brief,
// Implements, out-of-scope notes — and the id-only rule filed real work as a
// declaration and refused the run. A declaration is a bare heading: empty
// body, or field lines only. Prose is a brief, and a brief is a task,
// whatever id the model dressed it in.
func looksLikeMilestoneDecl(s artifact.Section) bool {
	if !strings.HasPrefix(strings.ToUpper(s.ID), "M-") {
		return false
	}
	for _, line := range strings.Split(s.Body, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "**") {
			continue
		}
		return false // prose: this is work wearing the wrong id
	}
	return true
}

// parsePlanItems reads a plan fragment: headings normalized under one
// synthetic milestone, flattened to items (task edits, new tasks, milestone
// declarations/edits). real counts the items that are WORK — refusal prose
// parses as the synthetic milestone's body and yields none.
func parsePlanItems(raw string) (items []artifact.Section, real int) {
	produced, perr := artifact.Parse(normalizeFragment(raw), artifact.KindPlan)
	if perr != nil {
		return nil, 0
	}
	for _, sec := range produced.Sections {
		items = append(items, sec.Children...)
	}
	for _, t := range items {
		if !looksLikeMilestoneDecl(t) {
			real++
		}
	}
	return items, real
}
