package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/bug"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/store"
	"github.com/jrullan/ducklab/internal/verify"
)

// B-523. Calculator B-014's triage proposed a two-portion split whose
// deliverables promised a pointer-event test, yet neither portion owned a test
// path or carried a test slice. The gate showed nothing wrong, the person
// accepted, and bug_promote then refused three times with "split proposal
// requires test or coverage work but no portion claims it" — a rule that ran
// only at promote. The operator, over MCP, had no way to edit the proposal and
// was stuck.
//
// The rule now runs where the proposal is made, through the same function
// promote calls (promotionPortionsFor), so the two can never disagree. A
// failing proposal is repaired by the engine when the fix is unambiguous, else
// the triager gets one turn to correct it, else the gate says plainly what is
// wrong and how to fix it.

// unclaimedTestWorkError is the one promote refusal the engine may repair by
// itself: the triage promised test work and no portion of the split took it.
type unclaimedTestWorkError struct{ msg string }

func (e *unclaimedTestWorkError) Error() string { return e.msg }

// promotionPortionsFor is the single judgement of whether a stored triage
// contract can become tasks. Promote calls it to build the lanes; triage
// calls it to know, before the gate, whether promote will refuse.
func promotionPortionsFor(projectRoot string, rec *store.Bug) ([]promotionPortion, error) {
	var portions []agent.SplitProposal
	if rec.Proposal != "" {
		if err := json.Unmarshal([]byte(rec.Proposal), &portions); err != nil {
			return nil, fmt.Errorf("read stored split proposal: %w", err)
		}
	}
	return preparePromotionPortions(projectRoot, rec, portions)
}

// proposalEditHint is what a person or operator does about a refused split.
// Named in every refusal: the MCP operator that met B-014's did not know the
// proposal was editable at all.
const proposalEditHint = "edit the proposal (desktop: the bug's Edit the proposal on the Bugs board; MCP: bug_edit, with the person's consent) and promote again"

// applyTriageContract writes the triage's work contract onto a bug record:
// where (component, suspected files), what (task title, deliverables, split)
// and how it will be verified. Accept applies it to the stored report; the
// triage-time check applies it to a copy, so both read the same fields the
// same way.
func applyTriageContract(rec *store.Bug, p map[string]interface{}) error {
	// The half of the answer that says WHERE. It lived only in the run's
	// event stream, so promoting the bug days later carried the reporter's
	// prose and nothing the triage had worked out — and an implementer
	// started from "the left edge label does not update" and a 1361-line
	// file with the location already computed and thrown away.
	if v, _ := p["component"].(string); v != "" {
		rec.Component = v
	}
	if v, _ := p["task_title"].(string); v != "" {
		rec.TaskTitle = v
	}
	if v, _ := p["test_strategy"].(string); v != "" {
		rec.TestStrategy = v
	}
	if v, _ := p["test_reason"].(string); v != "" {
		rec.TestReason = v
	}
	if v, _ := p["reason"].(string); v != "" {
		rec.TriageReason = v
	}
	if files, ok := p["suspected_files"].([]interface{}); ok {
		var names []string
		for _, f := range files {
			if name, _ := f.(string); name != "" {
				names = append(names, name)
			}
		}
		rec.SuspectedFiles = strings.Join(names, "\n")
	}
	if files, ok := p["suspected_files"].([]string); ok {
		rec.SuspectedFiles = strings.Join(files, "\n")
	}
	if items, ok := p["deliverables"].([]interface{}); ok {
		var lines []string
		for _, it := range items {
			if line, _ := it.(string); strings.TrimSpace(line) != "" {
				lines = append(lines, strings.TrimSpace(line))
			}
		}
		rec.Deliverables = strings.Join(lines, "\n")
	}
	if items, ok := p["deliverables"].([]string); ok {
		rec.Deliverables = strings.Join(items, "\n")
	}
	if proposal, ok := p["proposal"]; ok {
		data, err := json.Marshal(proposal)
		if err != nil {
			return fmt.Errorf("store split proposal: %w", err)
		}
		rec.Proposal = string(data)
	}
	return nil
}

// triageProposalMap is the gate's record of one triager answer.
func triageProposalMap(bugID string, t *agent.Triage) map[string]interface{} {
	p := map[string]interface{}{
		"bug": bugID, "severity": t.Severity, "reason": t.Reason,
		"component": t.Component, "task_title": t.TaskTitle,
		"suspected_files": t.SuspectedFiles,
		"test_strategy":   t.TestStrategy, "test_reason": t.TestReason,
	}
	if len(t.Proposal) > 0 {
		p["proposal"] = t.Proposal
	}
	if len(t.Deliverables) > 0 {
		p["deliverables"] = t.Deliverables
	}
	if t.DuplicateOf != "" {
		p["duplicate_of"] = t.DuplicateOf
	}
	if t.Reproducible != nil {
		p["reproducible"] = *t.Reproducible
	}
	return p
}

// triageProposalList reads a gate's proposals in either shape: the slice a
// live run built, or what JSON made of it after a restart.
func triageProposalList(raw interface{}) []map[string]interface{} {
	switch v := raw.(type) {
	case []map[string]interface{}:
		return v
	case []interface{}:
		var out []map[string]interface{}
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// triageCandidate is the stored report as it would read after this proposal
// is accepted, or nil when there is nothing promote could be asked to do: no
// such bug, already a task, or a duplicate (closed by being one).
func (s *Service) triageCandidate(projectID string, p map[string]interface{}) (*store.Bug, error) {
	if dup, _ := p["duplicate_of"].(string); dup != "" {
		return nil, nil
	}
	id, _ := p["bug"].(string)
	db, err := s.openProjectDB(projectID)
	if err != nil {
		return nil, nil
	}
	defer db.Close()
	rec, err := db.GetBug(id)
	if err != nil || rec.TaskID != "" {
		return nil, nil
	}
	candidate := *rec
	if err := applyTriageContract(&candidate, p); err != nil {
		return nil, err
	}
	return &candidate, nil
}

// triageProposalProblem asks promote's own question of a proposal still at
// the gate.
func (s *Service) triageProposalProblem(projectID, projectRoot string, p map[string]interface{}) (*store.Bug, error) {
	candidate, err := s.triageCandidate(projectID, p)
	if err != nil || candidate == nil {
		return candidate, err
	}
	_, err = promotionPortionsFor(projectRoot, candidate)
	return candidate, err
}

// settleTriageProposal returns the proposal the gate should offer: the
// original when promote would take it, a repaired one when the engine or the
// triager could repair it, else the original carrying the problem and the fix.
func (s *Service) settleTriageProposal(rs *runState, projectRoot string, p map[string]interface{}, retry func(problem string) (map[string]interface{}, error)) map[string]interface{} {
	projectID := rs.run.ProjectID
	bugID, _ := p["bug"].(string)
	candidate, problem := s.triageProposalProblem(projectID, projectRoot, p)
	if problem == nil {
		return p
	}
	record := func(outcome, repair string) {
		data := map[string]interface{}{"bug": bugID, "outcome": outcome, "problem": problem.Error()}
		if repair != "" {
			data["repair"] = repair
		}
		rs.writer.AppendEvent("triage_repair", data)
	}
	if candidate != nil {
		repaired, note, ok := repairUnclaimedTestWork(projectRoot, candidate, p, problem)
		if !ok {
			repaired, note, ok = repairTestFirstSplit(p, problem)
		}
		if ok {
			if _, again := s.triageProposalProblem(projectID, projectRoot, repaired); again == nil {
				repaired["proposal_repair"] = "repaired by the engine: " + note
				record("engine", note)
				return repaired
			}
		}
	}
	if retry != nil {
		revised, err := retry(problem.Error())
		if err == nil && revised != nil {
			if _, again := s.triageProposalProblem(projectID, projectRoot, revised); again == nil {
				note := "the triager revised its proposal after promote's rule refused it: " + problem.Error()
				revised["proposal_repair"] = note
				record("triager", note)
				return revised
			}
		}
	}
	// Still refused. The original answer stays on the table — the revision
	// failed too, and the gate should not trade one broken split for another
	// — and the problem is said where the decision is made.
	p["promote_blocker"] = problem.Error()
	p["promote_fix"] = triagePromoteFix(bugID)
	record("unrepaired", "")
	return p
}

func triagePromoteFix(bugID string) string {
	return fmt.Sprintf("Accepting records the classification, but promote will refuse %s until its split satisfies the rule above: "+
		"give the test work to the portion that owns the behavior under test — a test path matching the project's test globs in its owns, "+
		"or an acceptance slice such as \"Regression test covers <behavior>\". To do that, %s. Or reject this triage and run it again.",
		bugID, proposalEditHint)
}

// triageRepairPrompt asks the triager to correct one proposal. It runs as a
// fresh turn, so it carries the original prompt and the answer being fixed.
func triageRepairPrompt(prompt string, p map[string]interface{}, problem string) string {
	answer, _ := json.MarshalIndent(p, "", "  ")
	var sb strings.Builder
	sb.WriteString(prompt)
	sb.WriteString("\n\n## Promote would refuse your proposal\n\nYou answered:\n\n```json\n")
	sb.Write(answer)
	sb.WriteString("\n```\n\nDucklab checks every proposal against the rule promote enforces, and this one fails:\n\n> ")
	sb.WriteString(problem)
	sb.WriteString("\n\nAnswer again with the complete triage JSON — the same contract as before — with the proposal corrected. ")
	sb.WriteString("Usually that means giving the test work to the portion that owns the behavior under test: add a repository test path to that portion's owns, ")
	sb.WriteString("or make one of its 1-2 acceptance slices read \"Regression test covers <behavior>\". Keep every path in exactly one portion. Change nothing else that was right.\n")
	return sb.String()
}

var repairWordPattern = regexp.MustCompile(`[a-z0-9]+`)

// repairStopwords are words every portion of every split shares; counting
// them would make any two portions look alike.
var repairStopwords = map[string]bool{
	"that": true, "this": true, "with": true, "from": true, "into": true, "when": true,
	"then": true, "than": true, "have": true, "work": true, "works": true, "same": true,
	"like": true, "does": true, "should": true, "must": true, "also": true, "them": true,
	"they": true, "their": true, "each": true, "only": true, "after": true, "before": true,
	"still": true, "without": true, "behavior": true, "behaviour": true, "identically": true,
	"correctly": true, "properly": true, "used": true, "uses": true, "using": true,
}

func repairTokens(text string) map[string]bool {
	out := map[string]bool{}
	for _, w := range repairWordPattern.FindAllString(strings.ToLower(text), -1) {
		if len(w) < 4 || repairStopwords[w] || verify.MentionsTests(w) {
			continue
		}
		out[w] = true
	}
	return out
}

// repairUnclaimedTestWork gives the promised test work to the one portion
// that plainly owns the behavior under test, when there is such a portion.
//
// Plainly: it shares at least two words with what the triage said to test,
// and every other portion shares at most a third as many. Anything closer is
// a judgement, and judgements go back to the triager. The repair is the
// smallest one the rule accepts: test paths the triage itself named, else a
// "Regression test covers …" slice when the portion has room for one (the
// contract allows two).
func repairUnclaimedTestWork(projectRoot string, rec *store.Bug, p map[string]interface{}, problem error) (map[string]interface{}, string, bool) {
	var unclaimed *unclaimedTestWorkError
	if !errors.As(problem, &unclaimed) {
		return nil, "", false
	}
	raw, err := json.Marshal(p["proposal"])
	if err != nil {
		return nil, "", false
	}
	var portions []agent.SplitProposal
	if err := json.Unmarshal(raw, &portions); err != nil || len(portions) < 2 {
		return nil, "", false
	}
	var testLines []string
	for _, line := range strings.Split(rec.Deliverables, "\n") {
		if line = strings.TrimSpace(line); line != "" && verify.MentionsTests(line) {
			testLines = append(testLines, line)
		}
	}
	if len(testLines) == 0 && verify.MentionsTests(rec.TestReason) {
		testLines = []string{strings.TrimSpace(rec.TestReason)}
	}
	if len(testLines) == 0 {
		return nil, "", false
	}
	want := repairTokens(strings.Join(testLines, "\n"))
	scores := make([]int, len(portions))
	best := -1
	for i, portion := range portions {
		have := repairTokens(portion.Title + "\n" + strings.Join(portion.Acceptance, "\n"))
		for w := range want {
			if have[w] {
				scores[i]++
			}
		}
		if best < 0 || scores[i] > scores[best] {
			best = i
		}
	}
	if scores[best] < 2 {
		return nil, "", false
	}
	for i, score := range scores {
		if i != best && score*3 > scores[best] {
			return nil, "", false
		}
	}

	var globs []string
	if cfg, err := config.LoadProject(filepath.Join(projectRoot, ".ducklab", "project.toml")); err == nil {
		globs = cfg.Verify.TestGlobs
	}
	owned := map[string]bool{}
	for _, portion := range portions {
		for _, path := range portion.Owns {
			owned[cleanLanePath(path)] = true
		}
	}
	var named []string
	for _, path := range promotionNamedTestPaths(projectRoot, rec, globs) {
		if !owned[path] {
			named = append(named, path)
		}
	}
	target := &portions[best]
	var note string
	switch {
	case len(named) > 0:
		target.Owns = append(append([]string(nil), target.Owns...), named...)
		note = fmt.Sprintf("gave the test path(s) the triage named (%s) to portion %d %q, the only portion that owns the behavior under test",
			strings.Join(named, ", "), best+1, target.Title)
	case len(target.Acceptance) < 2:
		slice := "Regression test covers: " + testLines[0]
		target.Acceptance = append(append([]string(nil), target.Acceptance...), slice)
		note = fmt.Sprintf("added %q to portion %d %q, the only portion that owns the behavior under test", slice, best+1, target.Title)
	default:
		// Both slices are taken and no test path was named: which slice to
		// give up, or which test file to invent, is not the engine's call.
		return nil, "", false
	}
	if _, err := bug.ValidatePortions(splitToPortions(portions)); err != nil {
		return nil, "", false
	}
	out := make(map[string]interface{}, len(p)+1)
	for k, v := range p {
		out[k] = v
	}
	out["proposal"] = portions
	return out, note, true
}

// repairTestFirstSplit merges a test-only portion into the code portion when
// the split has exactly one of each: the test goes where test-first can write
// it. The merged portion keeps the code portion's title and slices; a test
// slice joins them only while the contract's two-slice limit allows, since
// test-first itself is the regression proof. Any other shape — two code
// portions the test could belong to, two test-only portions — is a judgement,
// and judgements go back to the triager.
func repairTestFirstSplit(p map[string]interface{}, problem error) (map[string]interface{}, string, bool) {
	var split *testFirstSplitError
	if !errors.As(problem, &split) || len(split.testOnly) != 1 || len(split.code) != 1 {
		return nil, "", false
	}
	raw, err := json.Marshal(p["proposal"])
	if err != nil {
		return nil, "", false
	}
	var portions []agent.SplitProposal
	if err := json.Unmarshal(raw, &portions); err != nil {
		return nil, "", false
	}
	t, c := split.testOnly[0], split.code[0]
	if t >= len(portions) || c >= len(portions) {
		return nil, "", false
	}
	test, code := portions[t], portions[c]
	merged := agent.SplitProposal{
		Title:      code.Title,
		Acceptance: append([]string(nil), code.Acceptance...),
		Owns:       append([]string(nil), code.Owns...),
	}
	for _, path := range test.Owns {
		if !slices.Contains(merged.Owns, path) {
			merged.Owns = append(merged.Owns, path)
		}
	}
	if len(merged.Acceptance) < 2 && len(test.Acceptance) > 0 {
		merged.Acceptance = append(merged.Acceptance, test.Acceptance[0])
	}
	var repaired []agent.SplitProposal
	for i, portion := range portions {
		switch i {
		case c:
			repaired = append(repaired, merged)
		case t:
		default:
			repaired = append(repaired, portion)
		}
	}
	if _, err := bug.ValidatePortions(splitToPortions(repaired)); err != nil {
		return nil, "", false
	}
	out := make(map[string]interface{}, len(p)+1)
	for k, v := range p {
		out[k] = v
	}
	out["proposal"] = repaired
	note := fmt.Sprintf("merged test-only portion %d %q into portion %d %q so test-first writes its test in the fix task's lane",
		t+1, test.Title, c+1, code.Title)
	return out, note, true
}

func splitToPortions(in []agent.SplitProposal) []bug.Portion {
	out := make([]bug.Portion, len(in))
	for i, p := range in {
		out[i] = bug.Portion{Title: p.Title, Acceptance: p.Acceptance, Owns: p.Owns}
	}
	return out
}

// triagePromoteBlockers lists the proposals on a gate that promote would
// refuse, each with its problem and fix.
func triagePromoteBlockers(proposals []map[string]interface{}) []map[string]interface{} {
	var out []map[string]interface{}
	for _, p := range proposals {
		problem, _ := p["promote_blocker"].(string)
		if problem == "" {
			continue
		}
		id, _ := p["bug"].(string)
		fix, _ := p["promote_fix"].(string)
		out = append(out, map[string]interface{}{"bug": id, "problem": problem, "fix": fix})
	}
	return out
}

func triageBlockerDetail(blockers []map[string]interface{}) string {
	parts := make([]string, 0, len(blockers))
	for _, b := range blockers {
		parts = append(parts, fmt.Sprintf("%s's split proposal cannot be promoted as written: %s", b["bug"], b["problem"]))
	}
	return strings.Join(parts, "; ") + " — " + proposalEditHint + " after accepting, or reject and triage again"
}

// appliedTriageBlockers re-asks promote's question of the reports an accept
// just wrote. The stored record is the authority: the gate's flag was computed
// against the tree as it was when the triage ran.
func (s *Service) appliedTriageBlockers(projectID string, proposals []map[string]interface{}) []map[string]interface{} {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil
	}
	db, err := s.openProjectDB(projectID)
	if err != nil {
		return nil
	}
	defer db.Close()
	var out []map[string]interface{}
	for _, p := range proposals {
		id, _ := p["bug"].(string)
		rec, err := db.GetBug(id)
		if err != nil || rec.TaskID != "" || bug.Status(rec.Status) != bug.Triaged {
			continue
		}
		if _, err := promotionPortionsFor(entry.Path, rec); err != nil {
			out = append(out, map[string]interface{}{"bug": id, "problem": err.Error(), "fix": triagePromoteFix(id)})
		}
	}
	return out
}
