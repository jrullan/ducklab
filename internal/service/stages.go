package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"errors"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/stage"
	"github.com/jrullan/ducklab/internal/strategy"
	"github.com/jrullan/ducklab/internal/tools"
)

// StageRequest starts an artifact stage.
type StageRequest struct {
	Stage string `json:"stage"`
	Mode  string `json:"mode"`
	// From seeds intake with an existing document instead of interviewing.
	From string `json:"from"`
	// Rounds overrides the script's own limit. Zero means the script decides,
	// which is two for a council.
	Rounds int `json:"rounds"`
	// Revise is what to change about the draft already on the table. Set only
	// when revising: it turns the run from "write this document" into "edit
	// this one", which is the answer to a proposal that is almost right.
	Revise   string `json:"revise"`
	Autonomy string `json:"autonomy"`
	// AgentTurns caps model calls per reply for every seat in this stage —
	// the intake that died at 12/12 had no way to be launched with more.
	// Zero keeps the defaults; -1 lifts the cap.
	AgentTurns int `json:"agent_turns,omitempty"`
	// Settle is the spec-debt eraser: a spec revision documenting, as built,
	// the amendment tasks no section covers — each gaining a Covers: field
	// the engine wires back into the plan on accept. The person never writes
	// this prompt; the engine assembles it from the debt itself.
	Settle bool `json:"settle,omitempty"`
	// Ducklings seats THIS run only — the chip clicked into a different
	// pick. The team's saved seats stay untouched; an override is a choice
	// about one run, not a settings edit. Order is the stage's own:
	// architect first, critics after.
	Ducklings []string `json:"ducklings,omitempty"`
	// Images are data URLs riding an amendment: the screenshot that shows
	// the cosmetic change better than a paragraph describes it. Shown to the
	// architect only when it can see; dropped with a recorded warning when
	// it cannot, because a text-only model sent an image array 400s.
	Images []string `json:"images,omitempty"`
	// Extend is the light path out of Review: a small change that deserves
	// tasks but not a redesign. It runs as a plan revision — the architect
	// adds the fewest tasks that deliver it, wiring Implements: to existing
	// SPEC sections where they genuinely cover it. What nothing covers wears
	// a spec-debt marker until the spec catches up. Changes that alter what
	// the product IS belong to a brief, and the note tells the architect to
	// add nothing in that case so the empty diff says so.
	Extend string `json:"extend,omitempty"`
	// SplitTask replaces this task with exactly two independently-owned tasks
	// through the same proposal and approval gate as every plan amendment.
	SplitTask string `json:"split_task,omitempty"`
	// Adopt turns intake into a survey: the architect reads the tree and
	// writes the requirements the code ALREADY satisfies, instead of
	// interviewing a person about a product that is still an idea. For a
	// codebase that exists, this is the front door.
	Adopt bool `json:"adopt,omitempty"`
	// Refs name reference documents — files, or directories of .md/.txt —
	// loaded bounded into the prompt as context. They live where the
	// ducklings' fs tools cannot reach (a wiki outside the project root),
	// so the prompt is the one honest channel; the run records what was
	// included and what the caps dropped.
	Refs   []string `json:"refs,omitempty"`
	Stream bool     `json:"stream"`
	// resumed is set only by RunResume: the run keeps its recorded ceilings
	// and its ledger continues from what it already spent.
	resumed bool
}

// StageStart runs intake, spec or plan and leaves a proposal for the human.
//
// Returns immediately with a running run, like every other long operation: the
// desktop watches the council converse rather than blocking on a request.
func (s *Service) StageStart(ctx context.Context, projectID string, req StageRequest) (*runlog.Run, error) {
	if !stage.Valid(req.Stage) {
		return nil, fmt.Errorf("unknown stage %q (available: intake, spec, plan)", req.Stage)
	}
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	// Refuse an impossible document run before it acquires an id, a queue
	// entry, and a misleading failed-run record. The desktop normally prevents
	// these launches, but CLI and MCP callers deserve the same invariant.
	if req.Stage == "spec" {
		reqs, loadErr := artifact.Load(entry.Path, artifact.KindRequirements)
		if loadErr != nil {
			return nil, loadErr
		}
		if liveRequirementCount(reqs) == 0 {
			if proposed, _ := artifact.LoadProposed(entry.Path, artifact.KindRequirements); proposed != nil {
				return nil, fmt.Errorf("spec needs accepted requirements — review and accept the requirements proposal first")
			}
			return nil, fmt.Errorf("spec needs accepted requirements — add an intention and accept the resulting requirements first")
		}
	}
	if req.Stage == "plan" && req.Revise == "" && req.Extend == "" && req.SplitTask == "" {
		plan, loadErr := artifact.Load(entry.Path, artifact.KindPlan)
		if loadErr != nil {
			return nil, loadErr
		}
		if len(plan.Sections) == 0 {
			reqs, reqErr := artifact.Load(entry.Path, artifact.KindRequirements)
			if reqErr != nil {
				return nil, reqErr
			}
			if liveRequirementCount(reqs) == 0 {
				if proposed, _ := artifact.LoadProposed(entry.Path, artifact.KindRequirements); proposed != nil {
					return nil, fmt.Errorf("plan needs accepted requirements — review and accept the requirements proposal first")
				}
				return nil, fmt.Errorf("plan needs accepted requirements — add an intention and accept the resulting requirements first")
			}
			spec, specErr := artifact.Load(entry.Path, artifact.KindSpec)
			if specErr != nil {
				return nil, specErr
			}
			if len(spec.Sections) == 0 {
				if proposed, _ := artifact.LoadProposed(entry.Path, artifact.KindSpec); proposed != nil {
					return nil, fmt.Errorf("plan needs an accepted specification — review and accept the specification proposal first")
				}
				return nil, fmt.Errorf("plan needs an accepted specification — draft and accept the specification first")
			}
		}
	}
	// The debt settle: one click, no prose — the engine knows what is owed.
	if req.Settle {
		if req.Stage != "spec" {
			return nil, fmt.Errorf("settle teaches the SPEC what was built; %s has no debt to settle", req.Stage)
		}
		tasks, tErr := s.TaskList(ctx, projectID)
		if tErr != nil {
			return nil, tErr
		}
		// Only DELIVERED debt settles: the spec documents what exists, and
		// an amendment task still todo would be written up as as-built
		// behaviour nobody built. It settles after its build is accepted.
		var debt []TaskView
		waiting := 0
		for _, t := range tasks {
			if !t.SpecDebt {
				continue
			}
			if t.Status == "accepted" {
				debt = append(debt, t)
			} else {
				waiting++
			}
		}
		if len(debt) == 0 {
			if waiting > 0 {
				return nil, fmt.Errorf("%d task(s) wear spec-debt but none are built yet — "+
					"the spec documents what exists; build and accept them first", waiting)
			}
			return nil, fmt.Errorf("no task wears spec-debt — there is nothing to settle")
		}
		req.Revise = specSettleNote(debt)
	}

	// The plan amendment: Review's "this needs more, but not a redesign".
	// Solo and one round unless asked otherwise: the architect returns a
	// fragment the engine merges, and a council re-reading a hundred-task
	// outline to add two tasks is cost without judgment.
	if strings.TrimSpace(req.Extend) != "" || strings.TrimSpace(req.SplitTask) != "" {
		if req.Stage != "plan" {
			return nil, fmt.Errorf("extend amends the plan; %s grows through a brief", req.Stage)
		}
		plan, pErr := artifact.Load(entry.Path, artifact.KindPlan)
		if pErr != nil || plan == nil || len(plan.Sections) == 0 {
			return nil, fmt.Errorf("no plan to extend yet — the design cycle creates it; " +
				"describe what to build in a brief instead")
		}
		if strings.TrimSpace(req.SplitTask) != "" && plan.Section(req.SplitTask) == nil {
			return nil, fmt.Errorf("no task %s in the plan", req.SplitTask)
		}
		if req.Mode == "" {
			req.Mode = "solo"
		}
		if req.Rounds == 0 {
			req.Rounds = 1
		}
	}

	// A first plan over a fully as-built spec has nothing to plan: every
	// section is already delivered by the tree the project adopted. Refusing
	// beats letting a model invent tasks to build what is built — the plan
	// will grow from feature briefs and bug promotions, which create it on
	// their own.
	if req.Stage == "plan" && req.Revise == "" {
		plan, _ := artifact.Load(entry.Path, artifact.KindPlan)
		if plan == nil || len(plan.Sections) == 0 {
			if spec, sErr := artifact.Load(entry.Path, artifact.KindSpec); sErr == nil && len(spec.Sections) > 0 {
				open := 0
				for _, sp := range spec.Sections {
					if strings.EqualFold(sp.Field("priority"), "wont") {
						continue
					}
					if v := strings.ToLower(strings.TrimSpace(sp.Field("as-built"))); v == "yes" || v == "true" {
						continue
					}
					open++
				}
				if open == 0 {
					return nil, fmt.Errorf("nothing to plan: every spec section is as-built or excluded. " +
						"The plan grows from feature briefs and bug reports — extend the requirements, " +
						"or file a bug, and the tasks will follow")
				}
			}
		}
	}
	if req.Adopt {
		if req.Stage != "intake" {
			return nil, fmt.Errorf("adopt is an intake variant; %s reads the documents intake produces", req.Stage)
		}
		// Adoption surveys a tree into first requirements. A project that
		// already has approved ones grows through the extension flow, where
		// the approved document is the ground truth — a second survey would
		// put two ground truths in one prompt.
		if reqs, lErr := artifact.Load(entry.Path, artifact.KindRequirements); lErr == nil && len(reqs.Sections) > 0 {
			return nil, fmt.Errorf("this project already has requirements; add to them with a brief instead")
		}
	}

	// A revision IS the decision on the draft it revises: "keep it, change
	// this". The run that produced that draft used to wait at its gate
	// forever — the person had answered, in another form, and the inbox
	// never learned. Resolved here, at the moment the person asks, not when
	// the revision lands: the decision happened now, whatever the revision's
	// fate.
	if req.Revise != "" {
		kind := stage.Name(req.Stage).Kind()
		if prop, pErr := artifact.LoadProposed(entry.Path, kind); pErr == nil && prop != nil && prop.Front.RunID != "" {
			s.resolveSuperseded(prop.Front.RunID, "changes requested: "+req.Revise)
			// The revision inherits what the revised run was launched with.
			// A spec drafted over 13 reference documents was once revised
			// with the note "use the reference documents" — and the revise
			// request carried none, so the model revised blind (B-087).
			// Reloaded fresh from the same paths, not copied: the person may
			// have edited a document between draft and revision.
			if len(req.Refs) == 0 && req.From == "" {
				if prior, ok := loadStageRequest(filepath.Join(entry.Path, ".ducklab", "runs", prop.Front.RunID)); ok {
					req.Refs = prior.Refs
					req.From = prior.From
				}
			}
		}
	}

	// Recorded as what will actually run, not as a constant. A report that
	// says every stage was a council when half were solo is a report that
	// cannot answer the question it exists for.
	mode := req.Mode
	if mode == "" {
		mode = "council"
	}
	run := &runlog.Run{
		ID:         runlog.GenerateRunID(),
		ProjectID:  projectID,
		Stage:      req.Stage,
		Mode:       mode,
		Status:     "running",
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
		Autonomy:   orDefault(req.Autonomy, "guarded"),
		AgentTurns: req.AgentTurns,
		// Always. Streaming is display state the bus fans out to whoever
		// watches; gating it on the launcher's flag meant a stage launched
		// from the CLI showed a person watching in the desktop no text and
		// no thinking for its whole length (Neocapture, 2026-08-30). Every
		// other run kind already streams unconditionally.
		Stream: true,
		// An artifact stage has no executable gate: the verdict is UNVERIFIED
		// until a person approves it, and saying so is the honest label (P3).
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

	writer.AppendEvent("run_start", map[string]interface{}{"stage": req.Stage, "mode": mode})

	// The request outlives the goroutine: an answered question re-enters the
	// stage with the same brief, mode and revision, and the request used to
	// live nowhere a resume could find it.
	writeStageRequest(rs.runDir, req)
	if data, err := json.Marshal(req); err == nil {
		_ = json.Unmarshal(data, &run.StageRequest)
	}

	// Document stages still operate in the person's checkout. Unlike build and
	// test-first worktrees, they must retain the queue's per-project tree hold.
	s.queue.submit(s, &queued{
		rs: rs, ctx: runCtx,
		exec: func(c context.Context) { s.executeStage(c, rs, entry.Path, req) },
	})
	return run, nil
}

// knownIDs collects every section id across the project's approved
// documents and pending proposals, for the council's structure check.
func (s *Service) knownIDs(projectRoot string) map[string]bool {
	known := map[string]bool{}
	for _, kind := range []artifact.Kind{artifact.KindIntent, artifact.KindRequirements, artifact.KindSpec, artifact.KindPlan} {
		if doc, err := artifact.Load(projectRoot, kind); err == nil {
			for _, id := range doc.IDs() {
				known[id] = true
			}
		}
		if doc, err := artifact.LoadProposed(projectRoot, kind); err == nil && doc != nil {
			for _, id := range doc.IDs() {
				known[id] = true
			}
		}
	}
	return known
}

// smallImplementerSeat reports whether the project's build implementer is a
// local seat — a small model, by the founding thesis — so document stages
// can portion the plan for it (ducklab_portion_control).
func (s *Service) smallImplementerSeat(projectID string) bool {
	cfg, err := s.projectConfig(projectID)
	if err != nil {
		return false
	}
	roster, _ := s.resolveRoster(cfg, "build")
	id := roster[config.RoleImplementer]
	if id == "" {
		return false
	}
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	d, ok := s.cfg.Ducklings[id]
	if !ok {
		return false
	}
	p, ok := s.cfg.Providers[d.Provider]
	if !ok {
		return false
	}
	return IsLocalHost(p.BaseURL)
}

func liveRequirementCount(doc *artifact.Document) int {
	count := 0
	for _, section := range doc.Sections {
		if !strings.EqualFold(section.Field("status"), "dropped") {
			count++
		}
	}
	return count
}

// stageRequestFile persists what a stage run was asked, beside its record.
const stageRequestFile = "stage_request.json"

func writeStageRequest(runDir string, req StageRequest) {
	if data, err := json.Marshal(req); err == nil {
		_ = os.WriteFile(filepath.Join(runDir, stageRequestFile), data, 0o644)
	}
}

// loadStageRequest rebuilds a stage run's request from its record. The false
// return keeps older runs honest: a run started before requests were
// persisted cannot be resumed, only relaunched.
func loadStageRequest(runDir string) (StageRequest, bool) {
	var req StageRequest
	data, err := os.ReadFile(filepath.Join(runDir, stageRequestFile))
	if err != nil || json.Unmarshal(data, &req) != nil {
		return req, false
	}
	return req, true
}

func (s *Service) executeStage(ctx context.Context, rs *runState, projectRoot string, req StageRequest) {
	defer recoverRun(rs)
	defer close(rs.done)
	defer rs.writer.Close()

	projCfg, err := config.LoadProject(projectRoot + "/.ducklab/project.toml")
	if err != nil {
		s.failRun(rs, fmt.Errorf("load project config: %w", err))
		return
	}

	seed := req.From
	if seed != "" {
		if data, err := os.ReadFile(seed); err == nil {
			seed = string(data)
		}
		// A --from that is not a readable path is treated as the brief text
		// itself: a user pasting a sentence should not have to make a file.
	}
	// Capture the person's words before references, digests, or any other
	// generated context are appended. Intent is provenance, not a prompt dump.
	originalBrief := seed
	intentText := originalBrief
	if strings.TrimSpace(req.Revise) != "" {
		// A requested correction is itself new human intent. Do not duplicate
		// the inherited seed and call it new; preserve the words that changed it.
		intentText = req.Revise
	}
	if req.Stage == "intake" && !req.Adopt && !req.resumed && strings.TrimSpace(intentText) != "" {
		if _, err := artifact.AppendIntent(projectRoot, rs.run.ID, rs.run.StartedAt, intentText); err != nil {
			s.failRun(rs, fmt.Errorf("record intent: %w", err))
			return
		}
	}
	if strings.TrimSpace(originalBrief) != "" {
		rs.writer.WriteBrief(originalBrief)
	}
	// References load AFTER the roster and tracker below: which mode fits —
	// inline or digest — is the architect seat's property, and digestion is
	// real spend that must land on this run's ledger.

	roster, warning := s.resolveRoster(projCfg, rs.run.Mode)
	// A request naming its own seats overrides for THIS run alone. An empty
	// request means THE RESOLVED ROSTER DECIDES — project seats included.
	// The legacy fallback re-read the GLOBAL mode line-up over the resolver:
	// the launcher showed the project's terra as council reviewer and the
	// run seated the global glm52, and only an explicit pick could win
	// (B-081). Council's extra critics come from the resolved reviewer
	// seat-list for the same reason.
	lineup := req.Ducklings
	var filled []config.Role
	var critics []config.DucklingID
	if len(lineup) == 0 {
		for _, id := range s.rosterIDs(projCfg, rs.run.Mode, config.RoleReviewer) {
			if _, err := s.ducklings.Get(config.DucklingID(id)); err == nil {
				critics = append(critics, config.DucklingID(id))
			}
		}
	} else {
		if rs.run.Mode == "solo" && len(lineup) > 1 {
			lineup = lineup[:1]
		}
		filled = applyStageLineup(roster, lineup)
		critics = s.criticsFrom(rs.run.Mode, lineup)
	}
	rs.run.Roster = rosterStrings(roster)
	rs.run.RosterSources = s.rosterSources(projCfg, rs.run.Mode, req.Ducklings, nil)
	for _, role := range filled {
		rs.run.RosterSources[string(role)] = "request"
	}
	if warning != "" {
		rs.run.Warning = warning
		rs.writer.AppendEvent("warning", map[string]interface{}{"detail": warning})
	}

	limitsValue := projectBudget(budget.Budget{
		MaxUSD: s.cfg.Defaults.Budget.MaxUSD, MaxTokens: int64(s.cfg.Defaults.Budget.MaxTokens),
		MaxWallclockS: s.cfg.Defaults.Budget.MaxWallclockS, MaxTurns: s.cfg.Defaults.Budget.MaxTurns,
	}, projCfg.Budget)
	limits := &limitsValue
	tracker := budget.NewTracker(limits)
	if req.resumed {
		// A resumed stage continues its own life: recorded ceilings, ledger
		// seeded with what it already spent — a tracker reborn at zero would
		// make "answer the question" a way to double the budget.
		limits = &budget.Budget{
			MaxUSD: rs.run.Budget.Limit.USD, MaxTokens: rs.run.Budget.Limit.Tokens,
			MaxTurns: rs.run.Budget.Limit.Turns, MaxWallclockS: rs.run.Budget.Limit.WallclockS,
		}
		tracker = budget.NewTracker(limits)
		tracker.Spend.AddTokens(rs.run.Budget.Tokens)
		tracker.Spend.AddUSD(rs.run.Budget.USD)
		tracker.Spend.RestoreWallclock(rs.run.Budget.WallclockS)
		for i := 0; i < rs.run.Budget.Turns; i++ {
			tracker.Spend.AddTurn()
		}
	}
	recordLimits(rs, limits)
	rs.setTracker(tracker)
	if len(req.Refs) > 0 {
		refs, rerr := s.stageReferences(ctx, rs, projCfg, req.Stage, req.Refs, roster[config.RoleArchitect])
		if rerr != nil {
			s.failRun(rs, fmt.Errorf("references: %w", rerr))
			return
		}
		// References are prompt context, recorded by their own run evidence.
		// They must not be folded into the person's verbatim Intent entry.
		seed += refs
	}
	// `seed` now carries prompt context as well as the brief. brief.md was
	// deliberately written above, before that enrichment.
	ectx := &tools.ExecContext{
		ProjectRoot: projectRoot,
		DeterministicAnswers: map[string]string{
			"project root": fmt.Sprintf("use `.` for tool paths; the absolute project root is `%s`", projectRoot),
		},
		RunID:       rs.run.ID,
		Autonomy:    config.Autonomy(rs.run.Autonomy),
		ShellPolicy: projCfg.Shell,
		Answers:     rs.answers(),
		RefPaths:    rs.refFiles(),
		OnRefRead:   rs.markRefRead,
		// The architect's survey guide may be a GLOBAL skill (repo-survey);
		// without this the stage's skill_list showed project skills only.
		GlobalSkillsDir: globalSkillsDir(),
	}
	cache := &loopCache{
		svc: s, tracker: tracker,
		writer:  s.llmWriter(rs, tracker),
		capLift: rs.capLifted.Load,
		loops:   map[config.DucklingID]*agent.Loop{},
	}
	s.attachStreaming(rs, cache)

	// The amendment's evidence, gated like the triager's: only a seeing
	// architect is shown images.
	images := req.Images
	if len(images) > 0 {
		arch := roster[config.RoleArchitect]
		if dcfg, ok := s.cfg.Ducklings[arch]; !ok || dcfg.Caps.Vision == nil || !*dcfg.Caps.Vision {
			rs.writer.AppendEvent("warning", map[string]interface{}{
				"detail": fmt.Sprintf("%d image(s) dropped: architect %s has no vision capability", len(images), arch),
			})
			images = nil
		} else {
			total := 0
			kept := images[:0]
			for _, im := range images {
				if total += len(im); total > 8<<20 {
					rs.writer.AppendEvent("warning", map[string]interface{}{
						"detail": "image(s) beyond the 8MB budget were dropped",
					})
					break
				}
				kept = append(kept, im)
			}
			images = kept
		}
	}

	// The architect replies from the most recent Execute, kept for the
	// stand-pat fallback: sectioned updates call Execute once per pass, and
	// the fallback must read the pass it belongs to, never a stale one.
	var lastArchitectTexts atomic.Pointer[[]string]
	var inventory *agent.Inventory
	adoptSurvey := req.Adopt
	if req.Stage == "spec" && !req.Adopt {
		if reqs, e := artifact.Load(projectRoot, artifact.KindRequirements); e == nil && reqs.Front.Origin == "adopted" {
			if sp, e := artifact.Load(projectRoot, artifact.KindSpec); e == nil && len(sp.Sections) == 0 {
				adoptSurvey = true
			}
		}
	}
	result, err := stage.Run(ctx, stage.Params{
		ProjectRoot: projectRoot,
		Stage:       stage.Name(req.Stage),
		RunID:       rs.run.ID,
		Seed:        seed,
		Mode:        req.Mode,
		Rounds:      s.roundsFor(rs.run.Mode, req.Rounds),
		Revision:    req.Revise,
		SmallSeat:   s.smallImplementerSeat(rs.run.ProjectID),
		OnEvent:     func(kind string, data map[string]interface{}) { rs.writer.AppendEvent(kind, data) },
		// An amendment revision edits its own pending fragment, not the
		// approved plan it originally extended.
		PriorFragment: func() string {
			if req.Extend == "" || req.Revise == "" {
				return ""
			}
			if proposed, _ := artifact.LoadProposed(projectRoot, artifact.KindPlan); proposed != nil {
				return proposed.Raw
			}
			return ""
		}(),
		Adopt: adoptSurvey,
		OnInventory: func(inv *agent.Inventory) error {
			inventory = inv
			data, e := json.MarshalIndent(inv, "", "  ")
			if e != nil {
				return e
			}
			if e = rs.writer.WriteInventory(data); e != nil {
				return e
			}
			detail := map[string]interface{}{"items": inv.Items, "kinds": inventoryKinds(inv), "capped": inv.Capped}
			if inv.Capped {
				detail["detail"] = "inventory capped at 60 items"
			}
			return rs.writer.AppendEvent("survey_inventory", detail)
		},
		Extend:    req.Extend,
		SplitTask: req.SplitTask,
		Images:    images,
		// A small architect gets the engine as its working memory: below
		// 64k of declared context, document updates run sectioned — one
		// triage pass, then one fresh conversation per touched section.
		SectionWise: func() bool {
			d, derr := s.ducklings.Get(roster[config.RoleArchitect])
			return derr == nil && d.Caps.ContextTokens > 0 && d.Caps.ContextTokens < 65536
		}(),
		Ducklings: ducklingList(roster),
		Critics:   critics,
		// Critics receive the recorded survey surfaces as named targets; the
		// final lexical coverage result is persisted after the proposal lands.
		// The architect's earlier replies from the latest Execute, newest
		// first, without the final one: the stand-pat fallback's memory.
		Drafts: func() []string {
			texts := lastArchitectTexts.Load()
			if texts == nil {
				return nil
			}
			all := *texts
			if len(all) < 2 {
				return nil
			}
			prior := make([]string, 0, len(all)-1)
			for i := len(all) - 2; i >= 0; i-- {
				prior = append(prior, all[i])
			}
			return prior
		},
		Execute: func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
			// Context-fit preflight: the engine knows the prompt AND every
			// seat's declared window before a single token is paid. A stage
			// whose opening prompt eats most of a small local seat's context
			// was a predictable loop — predicted now, at the door.
			seats := []config.DucklingID{roster[config.RoleArchitect]}
			seats = append(seats, critics...)
			warns, fatal := s.contextFitNotes(len(prompt), seats)
			for _, wmsg := range warns {
				rs.run.Warning = wmsg
				rs.writer.AppendEvent("warning", map[string]interface{}{"detail": wmsg})
			}
			if fatal != "" {
				return "", fmt.Errorf("%s", fatal)
			}
			res, rerr := strategy.ExecuteScript(ctx, s.applyRoleTurns(script, req.AgentTurns), &strategy.ExecuteParams{
				LiveToolEvents: true,
				ProjectRoot:    projectRoot,
				ResumeFrom:     resumeTurn(rs.run),
				// Decisions the person already made ride the prompt, like on
				// build and test runs: a resumed stage replays from scratch,
				// and a model that cannot see the answers re-asks them.
				Prompt:      prompt + rs.answeredDecisions(),
				ExecContext: ectx,
				Runner:      s.runnerFor(cache, roster, ectx),
				Roster:      roster,
				InventoryUnaccounted: func() []agent.InventoryItem {
					if !adoptSurvey || inventory == nil {
						return nil
					}
					return inventory.Items
				}(),
				InventoryCoverage: func(raw string) []agent.InventoryItem {
					if !adoptSurvey || inventory == nil {
						return nil
					}
					doc, e := artifact.Parse(raw, stage.Name(req.Stage).Kind())
					if e != nil {
						return inventory.Items
					}
					return inventoryUnaccounted(inventory.Items, doc)
				},
				KnownIDs:  s.knownIDs(projectRoot),
				SmallSeat: s.smallImplementerSeat(rs.run.ProjectID),
				StructureCheck: func(raw string) []string {
					if req.Stage != "plan" {
						return nil
					}
					doc, err := artifact.Parse(raw, artifact.KindPlan)
					if err != nil {
						return nil
					}
					return capabilityStructureFindings(doc)
				},
				OnEvent: func(kind string, data map[string]interface{}) {
					rs.writer.AppendEvent(kind, data)
					if kind == "turn_interrupted" {
						rs.run.InterruptedTurn = &runlog.InterruptedTurn{Round: intValue(data["round"]), Index: intValue(data["turn"]), Role: stringValueAny(data["role"]), Notes: stringValueAny(data["notes"]), Looked: stringSliceAny(data["looked"])}
						rs.writer.WriteState()
					} else if kind == "turn_end" && data["incomplete"] != true {
						rs.run.InterruptedTurn = nil
						rs.writer.WriteState()
						s.pauseAtSafePoint(rs)
					}
				},
			})
			if res != nil {
				texts := res.RoleTexts[string(config.RoleArchitect)]
				lastArchitectTexts.Store(&texts)
			}
			if rerr != nil {
				// pendingOrErr, or the question dies with the run: this
				// closure returned the raw error, so the pendingErr branch
				// below it never fired — a spec architect that asked the
				// human left "human input needed" on the record with no
				// question, no pending state, and nothing to answer.
				return "", pendingOrErr(res, rerr)
			}
			// The document is the FOLD of the architect's passes, never the
			// last reply alone: a round-2 revision that re-emits only the
			// sections it retouched must not erase the rest (B-089).
			if texts := res.RoleTexts[string(config.RoleArchitect)]; len(texts) > 1 {
				folded, kept := stage.FoldPasses(texts, stage.Name(req.Stage).Kind())
				if len(kept) > 0 {
					rs.writer.AppendEvent("sections_folded", map[string]interface{}{
						"ids": kept, "detail": "the final revision re-emitted only what it changed; these sections survive from the earlier pass",
					})
					return folded, nil
				}
			}
			return res.Text, nil
		},
	})

	recordSpend(rs, tracker)

	if err != nil {
		var pending *pendingErr
		if errors.As(err, &pending) {
			s.pauseForQuestion(rs, pending.q)
			return
		}
		s.failRun(rs, err)
		return
	}
	if req.Stage == "intake" && !req.Adopt && strings.TrimSpace(intentText) == "" {
		// Empty Intake interviews the person. Once it succeeds, preserve those
		// answers as the intention that actually produced the proposal.
		if answers := rs.intentAnswers(); answers != "" {
			if _, appendErr := artifact.AppendIntent(projectRoot, rs.run.ID, rs.run.StartedAt, answers); appendErr != nil {
				s.failRun(rs, fmt.Errorf("record interviewed intent: %w", appendErr))
				return
			}
		}
	}
	if req.Stage == "intake" {
		// Add provenance while the proposal is still at its gate, not during
		// promotion after the person has already read it. The edge is engine
		// metadata, but it remains part of the document they approve.
		if _, _, linkErr := artifact.LinkRequirementsProposal(projectRoot, rs.run.ID); linkErr != nil {
			s.failRun(rs, fmt.Errorf("link intent to requirements proposal: %w", linkErr))
			return
		}
		if linked, loadErr := artifact.LoadProposed(projectRoot, artifact.KindRequirements); loadErr == nil && linked != nil {
			result.Proposed = linked
		}
	}

	rs.writer.AppendEvent("proposal", map[string]interface{}{
		"kind":     string(result.Kind),
		"sections": len(result.Proposed.Sections),
		"remapped": len(result.Remapped),
	})
	if adoptSurvey && inventory != nil {
		unaccounted := inventoryUnaccounted(inventory.Items, result.Proposed)
		if rs.run.PendingData == nil {
			rs.run.PendingData = map[string]interface{}{}
		}
		rs.run.PendingData["unaccounted"] = unaccounted
		if len(unaccounted) > 0 {
			names := make([]string, 0, len(unaccounted))
			for _, item := range unaccounted {
				names = append(names, item.Name)
			}
			rs.run.Warning = fmt.Sprintf("this adoption survey proposal leaves %d inventoried surface(s) unaccounted: %s", len(unaccounted), strings.Join(names, ", "))
		}
		rs.writer.AppendEvent("survey_coverage", map[string]interface{}{"unaccounted": unaccounted})
	}
	// What this proposal would DESTROY, said before the decision. A run asked
	// to ADD one section replaced the whole spec with only that section —
	// sixteen approved sections gone — and the promote asked no questions:
	// the person learned days later, from a model asking a human what a
	// contract said, because the contract's section no longer existed. Never
	// blocked (a wholesale rewrite can be intended), only never hidden.
	if current, cErr := artifact.Load(projectRoot, result.Kind); cErr == nil && current != nil {
		removed := missingSectionIDs(current.Sections, result.Proposed.Sections)
		if len(removed) > 0 {
			rs.run.Warning = fmt.Sprintf(
				"this proposal REMOVES %d of %d existing sections (%s) — an addition keeps what it adds to; accept only if the removal is intended",
				len(removed), len(current.Sections), strings.Join(removed, ", "))
			rs.writer.AppendEvent("sections_removed", map[string]interface{}{
				"ids": removed, "of": len(current.Sections),
			})
		}
		// The subtler gutting: every section id survives while the bodies are
		// replaced with placeholders — "[Content remains unchanged]" — which
		// the id check cannot see. A council's own reviewer caught a draft
		// doing exactly this, flagged it critical, and the proposal landed
		// with no warning of its own. Content shrinkage is the tell.
		if cur, prop := sectionsBodySize(current.Sections), sectionsBodySize(result.Proposed.Sections); cur > 500 && prop*100 < cur*60 {
			gut := fmt.Sprintf(
				"this proposal's content is %d%% smaller than the approved document (%d → %d chars) — placeholder bodies gut a document while keeping its section ids; accept only if the shrinkage is intended",
				100-(prop*100/cur), cur, prop)
			if rs.run.Warning != "" {
				rs.run.Warning += " · " + gut
			} else {
				rs.run.Warning = gut
			}
			rs.writer.AppendEvent("sections_gutted", map[string]interface{}{
				"chars_before": cur, "chars_after": prop,
			})
		}
		// A plan revision can reuse a task ID for different work. The board
		// deliberately stops counting accepted runs for that old meaning on
		// acceptance; make that consequence visible at the proposal gate.
		if result.Kind == artifact.KindPlan {
			if runs, rErr := s.RunList(ctx, RunFilter{ProjectID: rs.run.ProjectID}); rErr == nil {
				if rewritten := acceptedHistoryRewriteCount(runs, current, result.Proposed); rewritten > 0 {
					history := fmt.Sprintf("this proposal rewrites %d task bodies whose accepted history will stop counting after acceptance", rewritten)
					if rs.run.Warning != "" {
						rs.run.Warning += " · " + history
					} else {
						rs.run.Warning = history
					}
					rs.writer.AppendEvent("task_history_rewritten", map[string]interface{}{"count": rewritten})
				}
			}
		}
	}
	// No executable gate exists for a document, so the verdict is UNVERIFIED
	// and the human gate is the only gate (P3).
	rs.run.Verdict = "UNVERIFIED"
	rs.writer.AppendEvent("verdict", map[string]interface{}{"verdict": "UNVERIFIED"})

	rs.run.Status = "paused"
	rs.run.PendingKind = "gate"
	rs.run.PendingSince = time.Now().UTC().Format(time.RFC3339)
	if rs.run.PendingData == nil {
		rs.run.PendingData = map[string]interface{}{}
	}
	rs.run.PendingData["artifact"] = string(result.Kind)
	rs.run.PendingData["sections"] = len(result.Proposed.Sections)
	// In digest mode "the references were considered" is a claim about tool
	// use, not about the prompt — so the gate names the documents no one
	// opened, and the person weighs the draft knowing it. The same honesty
	// contract as unverified_tasks on a release.
	if unread := rs.unreadRefs(); len(unread) > 0 {
		rs.run.PendingData["unread_refs"] = unread
	}
	rs.writer.AppendEvent("human_needed", map[string]interface{}{
		"kind": "gate", "verdict": "UNVERIFIED", "artifact": string(result.Kind),
	})
	rs.writer.WriteState()
}

// sectionsBodySize totals the text a document actually carries.
func inventoryKinds(inv *agent.Inventory) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range inv.Items {
		if !seen[item.Kind] {
			seen[item.Kind] = true
			out = append(out, item.Kind)
		}
	}
	return out
}

func sectionsBodySize(secs []artifact.Section) int {
	total := 0
	for _, sec := range secs {
		total += len(sec.Body) + len(sec.Title)
		for _, ch := range sec.Children {
			total += len(ch.Body) + len(ch.Title)
		}
	}
	return total
}

// missingSectionIDs lists the ids present in current and absent from
// proposed — what accepting the proposal would erase.
func missingSectionIDs(current, proposed []artifact.Section) []string {
	have := map[string]bool{}
	for _, sec := range proposed {
		have[sec.ID] = true
	}
	var removed []string
	for _, sec := range current {
		if !have[sec.ID] {
			removed = append(removed, sec.ID)
		}
	}
	return removed
}

// ArtifactGet returns a committed artifact and any pending proposal.
func (s *Service) ArtifactGet(ctx context.Context, projectID, kind string) (map[string]interface{}, error) {
	if !artifact.ValidKind(kind) {
		return nil, fmt.Errorf("unknown artifact %q", kind)
	}
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	k := artifact.Kind(kind)
	if k == artifact.KindIntent {
		if _, err := artifact.EnsureIntent(entry.Path); err != nil {
			return nil, err
		}
	}

	current, err := artifact.Load(entry.Path, k)
	if err != nil {
		return nil, err
	}
	proposed, err := artifact.LoadProposed(entry.Path, k)
	if err != nil {
		return nil, err
	}
	diff, err := artifact.Diff(entry.Path, k)
	if err != nil {
		return nil, err
	}

	out := map[string]interface{}{
		"kind":     kind,
		"markdown": current.Raw,
		"sections": sectionViews(current.Sections),
		"version":  current.Front.Version,
		"approved": current.Front.Approved(),
		// Which run produced this. Carried so a client can go and fetch what
		// that run was asked for; the proposal already said it and the
		// accepted document did not, so the brief vanished on acceptance.
		"run_id": current.Front.RunID,
	}
	if proposed != nil {
		prop := map[string]interface{}{
			"markdown": proposed.Raw,
			"sections": sectionViews(proposed.Sections),
			"run_id":   proposed.Front.RunID,
			"diff":     diff,
		}
		// Carried from the proposing run while it waits at its gate, so the
		// Cycle card can say which digest-mode references were never opened.
		s.runsMu.RLock()
		if rs, ok := s.runs[proposed.Front.RunID]; ok {
			if unread := rs.unreadRefs(); len(unread) > 0 {
				prop["unread_refs"] = unread
			}
		}
		s.runsMu.RUnlock()
		out["proposal"] = prop
	}
	return out, nil
}

// ArtifactPromote accepts a pending proposal.
func (s *Service) ArtifactPromote(ctx context.Context, projectID, kind, approvedBy string) (map[string]interface{}, error) {
	if !artifact.ValidKind(kind) {
		return nil, fmt.Errorf("unknown artifact %q", kind)
	}
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	// Read the proposal before promoting: Promote removes the file, and its
	// frontmatter is where the producing run's id lives.
	runID := ""
	if proposed, _ := artifact.LoadProposed(entry.Path, artifact.Kind(kind)); proposed != nil {
		runID = proposed.Front.RunID
	}
	var intentRequirements []string
	if artifact.Kind(kind) == artifact.KindRequirements && runID != "" {
		if _, linked, linkErr := artifact.LinkRequirementsProposal(entry.Path, runID); linkErr != nil {
			return nil, linkErr
		} else {
			intentRequirements = linked
		}
	}
	if _, err := artifact.Promote(entry.Path, artifact.Kind(kind), approvedBy); err != nil {
		return nil, err
	}
	if artifact.Kind(kind) == artifact.KindRequirements && runID != "" {
		if err := artifact.ResolveIntent(entry.Path, runID, "accepted", intentRequirements); err != nil {
			return nil, err
		}
	}
	// Close the run that produced it. Promoting answered its gate, and a run
	// left paused on a question already decided sits in the inbox forever:
	// three of them had accumulated on the timesheet project, each still
	// claiming to be waiting for an answer that had been given hours before.
	s.resolveStageRun(runID, approvedBy)
	// The settle's other half: Covers: fields in the accepted spec wire the
	// named tasks' Implements in the plan, and the spec-debt markers come
	// off because the coverage is now real and human-approved.
	var wired map[string][]string
	if artifact.Kind(kind) == artifact.KindSpec {
		wired = wireCoveredTasks(entry.Path)
	}
	// The trace check runs on promotion, not on demand: an artifact accepted
	// into a broken spine should say so immediately, while the person who
	// accepted it is still looking.
	res, err := s.TraceCheck(ctx, projectID)
	if err != nil {
		return nil, err
	}
	// The proposal has just been consumed, so this reads approved artifacts —
	// which is what a report about what was accepted should read.
	out := map[string]interface{}{"promoted": kind, "trace_errors": res.Errors}
	if len(wired) > 0 {
		out["wired"] = wired
	}
	return out, nil
}

// resolveStageRun marks the run behind an accepted artifact as finished.
//
// It is deliberately not RunAccept: that path commits a diff, and a document
// stage produced no diff to commit. What it owes is the gate — clear the
// pending block, record who decided, and let the run leave the inbox.
func (s *Service) resolveStageRun(runID, approvedBy string) {
	if runID == "" {
		return
	}
	s.runsMu.RLock()
	rs, ok := s.runs[runID]
	s.runsMu.RUnlock()
	if !ok || rs.run.Status != "paused" {
		return
	}
	rs.run.Status = "done"
	rs.run.Accepted = true
	rs.run.Resolution = "accepted by " + approvedBy
	rs.run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	clearPending(rs.run)
	// ensureWriter attaches the bus hook, so appending here is what tells a
	// connected desktop the run has left the inbox — no refresh needed.
	if w, err := s.ensureWriter(rs); err == nil {
		w.AppendEvent("gate_resolved", map[string]interface{}{
			"resolution": "accepted", "by": approvedBy,
		})
		w.WriteState()
	}
}

// TraceResult is a spine check and what it was run against.
type TraceResult struct {
	Errors []artifact.TraceError `json:"errors"`
	// Proposed names the stages whose pending proposal was checked instead of
	// the approved artifact. Empty means every stage was the approved one.
	Proposed []string `json:"proposed,omitempty"`
}

// TraceCheck walks the spine. Deterministic and model-free.
//
// A pending proposal stands in for the artifact it would replace, so the check
// describes the document you are about to accept rather than the one you
// accepted last week. That is the only moment it can change a decision.
func (s *Service) TraceCheck(ctx context.Context, projectID string) (*TraceResult, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	spine, proposed, err := artifact.LoadSpinePending(entry.Path)
	if err != nil {
		return nil, err
	}
	errs := spine.Check()
	if errs == nil {
		errs = []artifact.TraceError{}
	}
	out := &TraceResult{Errors: errs}
	for _, k := range proposed {
		out.Proposed = append(out.Proposed, string(k))
	}
	return out, nil
}

// TraceShow walks the spine from one id.
func (s *Service) TraceShow(ctx context.Context, projectID, id string) (*artifact.Node, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	spine, err := artifact.LoadSpine(entry.Path)
	if err != nil {
		return nil, err
	}
	return spine.Walk(id)
}

// TaskView is a task as a client sees it.
type TaskView struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Milestone  string   `json:"milestone"`
	Implements []string `json:"implements,omitempty"`
	Complexity string   `json:"complexity,omitempty"`
	DependsOn  []string `json:"depends_on,omitempty"`
	Status     string   `json:"status"`
	Branch     string   `json:"branch,omitempty"`
	Body       string   `json:"body,omitempty"`
	// Blocked says why, in one sentence, when Status is "blocked". A column
	// that shows work stopped without saying what stopped it is a column that
	// sends you reading run logs.
	Blocked string `json:"blocked,omitempty"`
	// Waiting says, in one sentence, what an in-progress task's run is
	// paused on when it is not a gate — a question, a budget, a provider —
	// so the card can say "test · paused: a question awaits your answer"
	// instead of filing the task under Review as if a person had work to
	// judge.
	Waiting string `json:"waiting,omitempty"`
	// TestReady says a committed failing test already defines done for this
	// task: the natural next act is the build that makes it pass. Without
	// this, an accepted test-first read as a finished task.
	TestReady bool `json:"test_ready,omitempty"`
	// Next are the actions a person may legally start from this task, in the
	// order a client should offer them (docs/ux-evaluation.md §5.4).
	Next []string `json:"next,omitempty"`
	// BuildOnly carries the triager's verification judgment: the honest
	// check for this fix is eyes, not an automated test — the front door is
	// a plain build. Recommended, never imposed.
	BuildOnly bool `json:"build_only,omitempty"`
	// SpecDebt marks a task no spec section covers. This includes promoted bug
	// tasks: their bug edge justifies them, but does not document the behavior
	// in the spec. The scribe settles the debt by teaching the spec what was built.
	SpecDebt bool `json:"spec_debt,omitempty"`
}

// TaskBodyUpdate proposes a prose-only plan amendment. The approved plan is
// deliberately untouched here: artifact.Promote supplies the human approval,
// stale-document check, and durable attribution boundary.
func (s *Service) TaskBodyUpdate(ctx context.Context, projectID, taskID, body string) (*TaskView, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	plan, err := artifact.Load(entry.Path, artifact.KindPlan)
	if err != nil {
		return nil, err
	}
	for i := range plan.Sections {
		for j := range plan.Sections[i].Children {
			task := &plan.Sections[i].Children[j]
			if task.ID != taskID {
				continue
			}
			if pending, err := artifact.LoadProposed(entry.Path, artifact.KindPlan); err != nil {
				return nil, err
			} else if pending != nil {
				return nil, fmt.Errorf("a plan amendment is already awaiting approval")
			}
			if taskBodyHasFields(body) {
				return nil, fmt.Errorf("task-body amendments contain prose only; task metadata and Owns lanes are immutable")
			}
			prose := strings.TrimSpace(body)
			task.Body = canonicalTaskFields(task.Fields) + prose
			run := &runlog.Run{ID: runlog.GenerateRunID(), ProjectID: projectID, Stage: "plan", Mode: "human", Status: "paused", StartedAt: time.Now().UTC().Format(time.RFC3339), Gate: "none", Verdict: "UNVERIFIED", PendingKind: "gate"}
			writer, err := runlog.NewWriter(entry.Path, run)
			if err != nil {
				return nil, err
			}
			rs := &runState{run: run, writer: writer, runDir: writer.RunDir(), projectPath: entry.Path, done: make(chan struct{})}
			s.attachWriter(rs, writer)
			s.runsMu.Lock()
			s.runs[run.ID] = rs
			s.runsMu.Unlock()
			if err := artifact.WriteProposal(entry.Path, artifact.KindPlan, plan, run.ID, []string{"human"}); err != nil {
				_ = writer.Close()
				return nil, err
			}
			writer.AppendEvent("task_body_amendment", map[string]interface{}{"task": taskID, "by": "human", "detail": "proposed prose-only scope refinement; approval is required before relaunch"})
			writer.AppendEvent("human_needed", map[string]interface{}{"kind": "gate", "artifact": "plan", "verdict": "UNVERIFIED"})
			writer.WriteState()
			// Return the exact effective section that was persisted in the proposal.
			// The editor submits prose, but a task representation includes its
			// canonical metadata too; returning only prose made this response
			// disagree with the amendment a relaunch will actually receive.
			return &TaskView{ID: task.ID, Title: task.Title, Milestone: plan.Sections[i].ID, Implements: task.Implements, Complexity: task.Field("complexity"), DependsOn: splitList(task.Field("depends on")), Body: task.Body}, nil
		}
	}
	return nil, fmt.Errorf("no task %s in the plan", taskID)
}

// taskBodyHasFields prevents submitted prose from overwriting plan metadata.
// Bold fields are all parser-visible; unbolded fields are accepted only for
// the parser's documented vocabulary.
func taskBodyHasFields(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		key := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "**"), "**"))
		if key, _, ok := strings.Cut(key, ":"); ok {
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "implements", "priority", "status", "complexity", "depends on", "role hint", "acceptance", "owns", "milestone":
				return true
			}
		}
	}
	return false
}

// canonicalTaskFields preserves every parsed field, including unbolded source
// fields, in one stable form so Render cannot duplicate or drop metadata.
func canonicalTaskFields(fields map[string]string) string {
	if len(fields) == 0 {
		return ""
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		label := strings.Title(key)
		if key == "depends on" {
			label = "Depends on"
		}
		fmt.Fprintf(&b, "**%s:** %s\n", label, fields[key])
	}
	b.WriteString("\n")
	return b.String()
}

// TaskList reads tasks from the plan and folds in what runs have done to them.
//
// The plan is the source of truth for what the tasks ARE; run records are the
// source for their status. Keeping the status in the document would mean a
// model rewriting the plan could mark its own work accepted.
func (s *Service) TaskList(ctx context.Context, projectID string) ([]TaskView, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	// The gate mode decides whether test-first is even offerable here.
	gateMode := ""
	if projCfg, cfgErr := config.LoadProject(entry.Path + "/.ducklab/project.toml"); cfgErr == nil {
		gateMode = projCfg.Verify.Mode
	}

	plan, err := artifact.Load(entry.Path, artifact.KindPlan)
	if err != nil {
		return nil, err
	}
	runs, err := s.RunList(ctx, RunFilter{ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	// A task ID is reusable. Only a run whose recorded body differs from the
	// current body belongs to an old meaning of that ID; plan regeneration alone
	// must not invalidate every task's history.
	hashes := taskBodyHashes(plan)
	runs = runsForCurrentTaskBodies(runs, hashes)

	status, blocked, waiting, testReady, failedStage, pinned := deriveTaskRunStateWaiting(runs)
	branches := map[string]string{}
	for _, r := range runs {
		if r.TaskID != "" && r.Accepted && r.Stage == "build" && r.Branch != "" {
			branches[r.TaskID] = r.Branch
		}
	}

	// The spec's sections for the debt check. Every accepted task without an
	// Implements edge is debt, regardless of whether it came from an amendment
	// or a promoted bug; bug provenance is a separate traceability edge.
	specIDs := map[string]bool{}
	if spec, sErr := artifact.Load(entry.Path, artifact.KindSpec); sErr == nil {
		for _, sp := range spec.Sections {
			specIDs[sp.ID] = true
			for _, c := range sp.Children {
				specIDs[c.ID] = true
			}
		}
	}
	bugTasks := map[string]bool{}
	// The triager's verification judgment per task, when its bug carries
	// one: "build-only" flips the task's front door from the TDD chain to a
	// plain build — recommended, and reversible by the person in one click.
	buildOnly := map[string]bool{}
	if db, dbErr := s.openProjectDB(projectID); dbErr == nil {
		if recs, lErr := db.ListBugs(); lErr == nil {
			for _, b := range recs {
				if b.TaskID != "" {
					bugTasks[b.TaskID] = true
					if b.TestStrategy == "build-only" {
						buildOnly[b.TaskID] = true
					}
				}
			}
		}
		db.Close()
	}

	var out []TaskView
	for _, m := range plan.Sections {
		for _, t := range m.Children {
			st := status[t.ID]
			if st == "" {
				st = "todo"
			}
			deps := splitList(t.Field("depends on"))
			// A task whose dependencies are not accepted cannot be started.
			// For a long time that was display only: the board showed
			// "waiting on T-022" and offered Run anyway, and RunStart never
			// looked at dependencies at all.
			depsWaiting := false
			if st == "todo" {
				if waiting := unmetDeps(deps, status); len(waiting) > 0 {
					st = "blocked"
					depsWaiting = true
					blocked[t.ID] = "waiting on " + strings.Join(waiting, ", ")
				}
			}
			out = append(out, TaskView{
				ID: t.ID, Title: t.Title, Milestone: m.ID,
				Implements: t.Implements,
				Complexity: t.Field("complexity"),
				DependsOn:  deps,
				Status:     st,
				Branch:     branches[t.ID],
				Body:       t.Body,
				Blocked:    blocked[t.ID],
				Waiting:    waiting[t.ID],
				// A committed test AWAITS its build — so the flag speaks
				// only while building is the next move (todo, blocked). It
				// outlived the build twice: once on an accepted task, then
				// on one in Review whose build was already done and waiting
				// for a person, still urging "build it to make it pass".
				TestReady: testReady[t.ID] && (st == "todo" || st == "blocked"),
				Next: taskNextActions(st, gateMode, !pinned[t.ID], depsWaiting, testReady[t.ID],
					failedStage[t.ID] == "test", buildOnly[t.ID]),
				SpecDebt:  taskSpecDebt(t.ID, t.Implements, specIDs, bugTasks),
				BuildOnly: buildOnly[t.ID],
			})
		}
	}
	return out, nil
}

// unmetDeps returns the dependencies that are not accepted yet, in the order
// the task listed them. A dependency naming a task that does not exist counts
// as unmet: a typo in the plan should stop the work, not quietly permit it.
func unmetDeps(deps []string, status map[string]string) []string {
	var waiting []string
	for _, dep := range deps {
		if status[dep] != "accepted" {
			waiting = append(waiting, dep)
		}
	}
	return waiting
}

// TaskNext returns the first task ready to be started.
//
// The dependency check used to live here, duplicated from nothing — it was the
// only place in the product that knew a task could be waiting on another. Now
// TaskList marks those blocked, so "todo" already means ready, and a task whose
// last run failed is blocked rather than silently offered again as if it were
// fresh work.
func (s *Service) TaskNext(ctx context.Context, projectID string) (*TaskView, error) {
	tasks, err := s.TaskList(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		if tasks[i].Status == "todo" {
			return &tasks[i], nil
		}
	}
	return nil, nil
}

// --- helpers -------------------------------------------------------------------

func sectionViews(sections []artifact.Section) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(sections))
	for _, s := range sections {
		v := map[string]interface{}{
			"id": s.ID, "title": s.Title, "body": s.Body,
		}
		if len(s.Implements) > 0 {
			v["implements"] = s.Implements
		}
		if len(s.Fields) > 0 {
			v["fields"] = s.Fields
		}
		if len(s.Children) > 0 {
			v["children"] = sectionViews(s.Children)
		}
		out = append(out, v)
	}
	return out
}

func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func ducklingList(roster map[config.Role]config.DucklingID) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range roster {
		if id != "" && !seen[string(id)] {
			seen[string(id)] = true
			out = append(out, string(id))
		}
	}
	return out
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// buildTaskPrompt assembles what an implementer is asked for a build run.
//
// Two pieces of context that a fresh prompt would lose: the project note,
// injected into every turn, and what previous runs on THIS task already tried
// and failed. A small model re-run on the same task repeats the same dead end,
// so naming the dead ends is the cheapest correction available (04 §1.5).
func (s *Service) buildTaskPrompt(ctx context.Context, projectID, projectRoot, taskID string) string {
	var b strings.Builder

	if m, err := artifact.LoadMemory(projectRoot); err == nil {
		if mc := m.PromptContext(); mc != "" {
			b.WriteString(mc)
			b.WriteString("\n\n")
		}
	}

	b.WriteString("## Your task\n\n")
	if task := s.findTask(ctx, projectID, taskID); task != nil {
		fmt.Fprintf(&b, "%s — %s\n", task.ID, task.Title)
		if lane := s.taskLane(projectRoot, taskID); len(lane) > 0 {
			b.WriteString("\n## Lane notice\n\nThis task owns: " + strings.Join(lane, ", ") + ". Concurrent runs own other lanes; do not modify paths outside this lane unless strictly required.\n")
		}
		if strings.TrimSpace(task.Body) != "" {
			b.WriteString("\n" + strings.TrimSpace(task.Body) + "\n")
		}
		if len(task.Implements) > 0 {
			// Who else delivers these sections, and therefore what is not this
			// task's to write.
			//
			// This used to say "This task delivers SPEC-003" and then hand over
			// the whole section. Every section in a real plan is delivered by
			// two to five tasks — one project had five on SPEC-002 — so the
			// sentence was false and the model reasonably read the section as
			// its scope. Twice in one session a task implemented its sibling's
			// work as well, the gate went green because the code was correct,
			// and the next run found nothing left to do.
			b.WriteString(scopeNote(task.Implements, s.siblingTasks(ctx, projectID, taskID, task.Implements)))
			if spec := s.specSections(projectRoot, task.Implements); spec != "" {
				// "contributes to", not "delivers": the heading was half the
				// instruction the model was following.
				b.WriteString("\n## The specification this task contributes to\n\n" + spec)
			}
		}
	} else {
		fmt.Fprintf(&b, "Implement task %s\n", taskID)
	}

	if prior := s.failedAttempts(ctx, projectID, taskID); len(prior) > 0 {
		b.WriteString("\n")
		b.WriteString(artifact.RenderFailedAttempts(prior))
	}
	return b.String()
}

// scopeNote tells a task what is its part of a spec section, and what is not.
//
// The wording is the whole mechanism. It used to say "This task delivers
// SPEC-003" and then hand over the whole section, and every section in a real
// plan is delivered by several tasks — one project had five on SPEC-002 — so
// the sentence was false and the model reasonably read the section as its
// scope. Twice in one session a task implemented its sibling's work as well;
// the gate went green because the code was correct, and the next run found
// nothing left to do.
func scopeNote(implements []string, siblings []TaskView) string {
	if len(siblings) == 0 {
		return fmt.Sprintf("\nThis task delivers %s.\n", strings.Join(implements, ", "))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nThis task delivers **part of** %s. "+
		"The rest of those sections belongs to other tasks:\n\n", strings.Join(implements, ", "))
	for _, sib := range siblings {
		fmt.Fprintf(&b, "- %s — %s\n", sib.ID, sib.Title)
	}
	// A real dependency needs an answer other than "do all of it", or the
	// model will do all of it — which is exactly what happened.
	b.WriteString("\nDo not implement those. Work another task has been given is work that will " +
		"be done twice or not at all, and either way the plan stops meaning anything. If your " +
		"part genuinely cannot stand without something listed above, write the smallest thing " +
		"that lets yours work and say so in your summary — do not deliver the whole of it.\n")
	return b.String()
}

// siblingTasks are the other tasks that deliver any of the same spec sections.
//
// They are what this task must not implement. Named rather than merely
// excluded: "do not do more than your task" is advice a model cannot check
// itself against, and "T-004 does the geometry calculations" is a fact it can.
func (s *Service) siblingTasks(ctx context.Context, projectID, taskID string, implements []string) []TaskView {
	tasks, err := s.TaskList(ctx, projectID)
	if err != nil {
		return nil
	}
	mine := map[string]bool{}
	for _, id := range implements {
		mine[strings.ToUpper(strings.TrimSpace(id))] = true
	}
	var out []TaskView
	for _, t := range tasks {
		if strings.EqualFold(t.ID, taskID) {
			continue
		}
		// Work already accepted is not a warning, it is the tree the task is
		// being written against — and the model can read it there.
		if t.Status == "accepted" {
			continue
		}
		for _, id := range t.Implements {
			if mine[strings.ToUpper(strings.TrimSpace(id))] {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

func (s *Service) findTask(ctx context.Context, projectID, taskID string) *TaskView {
	tasks, err := s.TaskList(ctx, projectID)
	if err == nil {
		for i := range tasks {
			if strings.EqualFold(tasks[i].ID, taskID) {
				return &tasks[i]
			}
		}
	}
	// The database keeps its own row for a promoted task, so a plan and a
	// database that disagree — the exact wreckage a half-done removal leaves —
	// still yield the title and body rather than a one-line prompt.
	if db, dbErr := s.openProjectDB(projectID); dbErr == nil {
		defer db.Close()
		if rec, gErr := db.GetTask(taskID); gErr == nil && rec != nil {
			// Next carries run: the fallback exists so a plan/database
			// divergence stays runnable, and a database row records no
			// dependency edges to wait on.
			return &TaskView{ID: rec.ID, Title: rec.Title, Body: rec.Body,
				Status: rec.Status, Next: []string{"run"}}
		}
	}
	return nil
}

func (s *Service) taskLane(projectRoot, taskID string) []string {
	plan, err := artifact.Load(projectRoot, artifact.KindPlan)
	if err != nil {
		return nil
	}
	for _, m := range plan.Sections {
		for _, task := range m.Children {
			if strings.EqualFold(task.ID, taskID) {
				if len(task.Owns) > 0 {
					return append([]string(nil), task.Owns...)
				}
				return append([]string(nil), m.Owns...)
			}
		}
	}
	return nil
}

func (s *Service) specSections(projectRoot string, ids []string) string {
	spec, err := artifact.Load(projectRoot, artifact.KindSpec)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, id := range ids {
		if sec := spec.Section(id); sec != nil {
			fmt.Fprintf(&b, "### %s — %s\n%s\n\n", sec.ID, sec.Title, strings.TrimSpace(sec.Body))
		}
	}
	return b.String()
}

// failedAttempts summarises prior failed runs on a task, newest first, capped
// at three: the point is to name the dead ends, not to replay the history.
func (s *Service) failedAttempts(ctx context.Context, projectID, taskID string) []artifact.FailedAttempt {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil
	}
	runs, err := s.RunList(ctx, RunFilter{ProjectID: projectID})
	if err != nil {
		return nil
	}
	plan, planErr := artifact.Load(entry.Path, artifact.KindPlan)
	if planErr == nil {
		runs = runsForCurrentTaskBodies(runs, taskBodyHashes(plan))
	}
	var failed []*runlog.Run
	for _, r := range runs {
		if r.TaskID == taskID && r.Verdict == "FAILED" {
			failed = append(failed, r)
		}
	}
	sort.Slice(failed, func(i, j int) bool { return failed[i].StartedAt > failed[j].StartedAt })
	if len(failed) > 3 {
		failed = failed[:3]
	}

	out := make([]artifact.FailedAttempt, 0, len(failed))
	for _, r := range failed {
		out = append(out, artifact.FailedAttempt{
			RunID: r.ID, Mode: r.Mode,
			Summary: s.runSummary(r),
			Gate:    s.gateSummary(r),
		})
	}
	return out
}

// runSummary is derived from the run's own record, never by asking a model to
// recall what it did.
func (s *Service) runSummary(r *runlog.Run) string {
	events, err := runlog.ReadEvents(s.RunDir(r.ID))
	if err != nil {
		return "no summary recorded"
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == "error" {
			if msg, ok := events[i].Data["error"].(string); ok {
				return msg
			}
		}
	}
	return "the gate stayed red"
}

func (s *Service) gateSummary(r *runlog.Run) string {
	out, err := s.RunVerify(context.Background(), r.ID, 6)
	if err != nil || strings.TrimSpace(out) == "" {
		return ""
	}
	return strings.TrimSpace(out)
}

// artifactKindForStage maps a stage to the document its run proposes.
//
// Empty for stages that write no artifact: a build run's human gate is about
// code, and promoting something there would be inventing a decision nobody
// made.
func artifactKindForStage(stage string) string {
	switch stage {
	case "intake":
		return "requirements"
	case "spec":
		return "spec"
	case "plan":
		return "plan"
	}
	return ""
}

// ArtifactDiscard drops a pending proposal by explicit request.
//
// The lifecycle keeps a rejected proposal on disk (05 §1.1 step 8) — a failed
// attempt is a record, not clutter — so discarding is a person's own act, never
// a side effect of the reject. This is that act.
func (s *Service) ArtifactDiscard(ctx context.Context, projectID, kind string) error {
	if !artifact.ValidKind(kind) {
		return fmt.Errorf("unknown artifact %q", kind)
	}
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return err
	}
	return artifact.DiscardProposal(entry.Path, artifact.Kind(kind))
}

// taskBodyHash identifies the meaning of a task independently of the plan file.
func taskBodyHashForTask(ctx context.Context, s *Service, projectID, taskID string) string {
	if taskID == "" {
		return ""
	}
	if tv := s.findTask(ctx, projectID, taskID); tv != nil {
		return taskBodyHash(tv.Body)
	}
	return ""
}

// normalizeTaskBody is what the hash covers: the task's substance, not its
// traceability. Accepting a spec-alignment that added ONE **Implements:**
// line to 64 tasks changed every raw hash, the recycled-id boundary
// discarded every accepted run, and 64 shipped tasks reappeared in todo
// (B-093). An Implements line is an edge to the spec, not the work
// definition — annotating it must not orphan the task's history.
func normalizeTaskBody(body string) string {
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "**Implements:**") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func taskBodyHash(body string) string {
	h := sha256.Sum256([]byte(normalizeTaskBody(body)))
	return hex.EncodeToString(h[:])
}

// rawTaskBodyHash is the pre-B-093 form, kept so records written before the
// normalization still count for an unchanged task.
func rawTaskBodyHash(body string) string {
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:])
}

// acceptedHistoryRewriteCount reports task bodies whose accepted history
// currently counts but would stop counting if the proposed plan were accepted.
// taskBodyHash deliberately compares normalized bodies, so Implements-only
// traceability edits do not produce this warning.
func acceptedHistoryRewriteCount(runs []*runlog.Run, current, proposed *artifact.Document) int {
	currentHashes := taskBodyHashes(current)
	accepted := map[string]bool{}
	for _, r := range runsForCurrentTaskBodies(runs, currentHashes) {
		if r != nil && r.Accepted && r.TaskID != "" {
			accepted[r.TaskID] = true
		}
	}
	if proposed == nil {
		return 0
	}
	count := 0
	for _, milestone := range proposed.Sections {
		for _, task := range milestone.Children {
			if accepted[task.ID] && currentHashes[task.ID] != "" && taskBodyHash(task.Body) != currentHashes[task.ID] {
				count++
			}
		}
	}
	return count
}

// taskBodyHashes carries both accepted spellings per task: the normalized
// hash (what new runs record) and the raw one (what old runs recorded).
func taskBodyHashes(plan *artifact.Document) map[string]string {
	out := map[string]string{}
	if plan == nil {
		return out
	}
	for _, m := range plan.Sections {
		for _, t := range m.Children {
			out[t.ID] = taskBodyHash(t.Body)
			out[t.ID+"\x00raw"] = rawTaskBodyHash(t.Body)
			// A record written before this plan revision hashed the body as
			// it stood THEN. The only edit normalization forgives is the
			// Implements line, so the normalized form IS that older raw body
			// when nothing else changed — covered by the normalized entry.
			out[t.ID+"\x00rawnorm"] = rawTaskBodyHash(normalizeTaskBody(t.Body))
		}
	}
	return out
}

// runsForCurrentTaskBodies is the single history boundary used by all status
// consumers. Empty hashes are legacy records and remain usable; new records
// from a changed task body are historical for the recycled ID. A recorded
// hash matches if it is the current body's normalized hash, its raw hash, or
// the raw hash of its normalized text — the three spellings one unchanged
// task has worn across the B-093 fix.
func runsForCurrentTaskBodies(runs []*runlog.Run, hashes map[string]string) []*runlog.Run {
	out := make([]*runlog.Run, 0, len(runs))
	for _, r := range runs {
		if r == nil || r.TaskID == "" {
			out = append(out, r)
			continue
		}
		if r.TaskBodyHash == "" && strings.HasPrefix(r.StartedAt, "2020-") {
			continue
		}
		if current, ok := hashes[r.TaskID]; ok && r.TaskBodyHash != "" &&
			r.TaskBodyHash != current &&
			r.TaskBodyHash != hashes[r.TaskID+"\x00raw"] &&
			r.TaskBodyHash != hashes[r.TaskID+"\x00rawnorm"] {
			continue
		}
		out = append(out, r)
	}
	return out
}

// deriveTaskRunState folds a task's run history (newest first) into its
// board state. Extracted so the precedence rules — newest run wins, except
// an accepted run outranks a later failure — are pinned without a disk.
func deriveTaskRunState(runs []*runlog.Run) (status, blocked map[string]string, testReady map[string]bool, failedStage map[string]string, pinned map[string]bool) {
	status, blocked, _, testReady, failedStage, pinned = deriveTaskRunStateWaiting(runs)
	return
}

// deriveTaskRunStateWaiting is deriveTaskRunState with the waiting reasons.
func deriveTaskRunStateWaiting(runs []*runlog.Run) (status, blocked, waiting map[string]string, testReady map[string]bool, failedStage map[string]string, pinned map[string]bool) {
	waiting = map[string]string{}
	// A task's status is its MOST RECENT run, and RunList answers newest
	// first, so the first run seen for a task wins.
	//
	// This used to assign unconditionally on every branch, letting an older
	// run overwrite a newer one: a task accepted this morning went back to
	// "in progress" because a run from last week appeared later in the list.
	// While RunList ranged over a map it was worse than wrong, it was
	// unstable — the same board could show two different columns on two
	// consecutive loads.
	status = map[string]string{}
	blocked = map[string]string{}
	// A committed failing test: the task's definition of done exists and
	// awaits the build that satisfies it.
	testReady = map[string]bool{}
	// The stage of the run that blocked its task, when one did.
	failedStage = map[string]string{}
	// Whether TaskRemove would refuse: an accepted run pins its task for good,
	// an open one until it is decided. Tracked here so the offered action and
	// the refusal can never disagree.
	pinned = map[string]bool{}
	for _, r := range runs {
		if r.TaskID == "" {
			continue
		}
		// A conversation ABOUT a task is not an attempt AT it: chat runs
		// carry the task id for their dossier, and one that ended "done,
		// unaccepted" was read as a failed attempt — stamping "the last run
		// done — retry" on a delivered task. And a run that changed nothing
		// says nothing either: it wears FAILED for honest pass-rates, but
		// "the work was already in the tree" is not a verdict on the task.
		if r.Stage == "chat" || (r.NoChanges && !(r.Accepted && r.Stage == "build")) {
			continue
		}
		if r.Accepted || r.Status == "running" || r.Status == "queued" || r.Status == "paused" {
			pinned[r.TaskID] = true
		}
		// An accepted test-first run is not a finished task — it is the
		// definition of done, committed. The task stays buildable, and says
		// the test is waiting. A RETIRED one says nothing at all: its commit
		// was reverted, so it neither awaits a build nor — via the Accepted
		// fold below — marks the task accepted.
		if r.Stage == "test" && r.Accepted {
			if r.RevertSHA == "" {
				testReady[r.TaskID] = true
			}
			continue
		}
		if status[r.TaskID] != "" {
			// An older ACCEPTED run outranks a newer failure: the accepted
			// work is in the tree, and a redundant rerun that died does not
			// un-commit it. T-101 read "blocked — the last run failed" over
			// a build accepted one minute earlier.
			if r.Accepted && status[r.TaskID] == "blocked" {
				status[r.TaskID] = "accepted"
				delete(blocked, r.TaskID)
				delete(failedStage, r.TaskID)
			}
			continue
		}
		switch {
		case r.Accepted:
			status[r.TaskID] = "accepted"
		case r.Status == "running" || r.Status == "queued":
			status[r.TaskID] = "in_progress"
		case r.Status == "paused" && r.PendingKind == "gate":
			// A person has work to judge: the diff, the draft.
			status[r.TaskID] = "review"
		case r.Status == "paused":
			// Paused on a question, a budget, a provider or a restart is not
			// review — nothing is finished; the run is mid-work and needs a
			// hand. The card stays in progress and says what it waits for.
			status[r.TaskID] = "in_progress"
			waiting[r.TaskID] = pausedTaskNote(r)
		default:
			// Failed and aborted used to land back in "todo", where a task that
			// had been tried and broken looked exactly like one nobody had
			// touched. The board could not tell you the difference.
			status[r.TaskID] = "blocked"
			blocked[r.TaskID] = "the last run " + r.Status + " — read it, then retry or change the task"
			// Which phase failed decides the retry the task offers: a failed
			// build retries by building, a failed TEST retries the chain.
			failedStage[r.TaskID] = r.Stage
		}
	}
	return status, blocked, waiting, testReady, failedStage, pinned
}

// pausedTaskNote is the one line a card wears while its run waits on
// something other than a gate.
func pausedTaskNote(r *runlog.Run) string {
	stage := r.Stage
	if stage == "" {
		stage = "run"
	}
	switch r.PendingKind {
	case "question":
		return stage + " run paused: a question awaits your answer"
	case "budget":
		return stage + " run paused: it hit its budget cap — lift it and resume"
	case "provider":
		return stage + " run paused: the provider is unreachable — resume when it is back"
	case "engine_restart":
		return stage + " run paused: the engine restarted — resume it"
	case "error":
		return stage + " run paused on an error — read it, then resume or abort"
	}
	return stage + " run paused — see the run"
}

// taskSpecDebt reports an uncovered task. Bug provenance does not exempt a
// task: the bug→task edge explains why it exists, while spec-debt records that
// the approved spec has not caught up. A project with no spec at all owes none.
// bugTasks is retained in the signature for callers that already gather bug
// provenance for other task-list projections.
func taskSpecDebt(taskID string, implements []string, specIDs, bugTasks map[string]bool) bool {
	_ = taskID
	_ = bugTasks
	if len(specIDs) == 0 {
		return false
	}
	for _, im := range implements {
		if specIDs[im] {
			return false
		}
	}
	return true
}

// specSettleNote frames the debt as a spec revision. The Covers: field is the
// contract's return path: on accept, the engine wires each named task's
// Implements back in the plan, and the marker comes off by itself.
func specSettleNote(debt []TaskView) string {
	var b strings.Builder
	b.WriteString("Teach this specification what was already built, WITHOUT redesigning it.\n\n")
	b.WriteString("These plan tasks are covered by no spec section (spec-debt):\n\n")
	for _, t := range debt {
		fmt.Fprintf(&b, "- %s — %s\n", t.ID, t.Title)
		if body := strings.TrimSpace(t.Body); body != "" {
			if len(body) > 400 {
				body = body[:400] + "…"
			}
			fmt.Fprintf(&b, "  %s\n", strings.ReplaceAll(body, "\n", "\n  "))
		}
	}
	b.WriteString("\nRules for this settlement:\n" +
		"- Add or extend the FEWEST sections that honestly describe the behaviour these tasks " +
		"deliver. Describe what IS built — invent nothing aspirational; the code is your " +
		"source, read it.\n" +
		"- Mark each such section **As-built:** yes, and give it **Covers:** naming the task " +
		"ids it covers (e.g. Covers: T-110, T-112).\n" +
		"- Wire UPWARD too: give each section **Implements:** naming the existing requirement " +
		"that genuinely covers this behaviour. Never invent a requirement id. When NO existing " +
		"requirement covers it, leave Implements off — the section will surface on the " +
		"traceability rail as requirements-debt, which is the truth: the amendment added " +
		"capability the requirements have not caught up with, and the person extends them " +
		"through a brief.\n" +
		"- Every other section comes back exactly as it is, same id, same wording.\n")
	return b.String()
}

// wireCoveredTasks reads the approved spec's Covers: fields and writes the
// named tasks' Implements in the plan. The settle run's other half: the
// architect declared coverage in the document a person accepted; the wiring
// is mechanical and the marker comes off because the coverage is real.
func wireCoveredTasks(projectRoot string) map[string][]string {
	spec, err := artifact.Load(projectRoot, artifact.KindSpec)
	if err != nil || spec == nil {
		return nil
	}
	covers := map[string][]string{}
	var walk func(secs []artifact.Section)
	walk = func(secs []artifact.Section) {
		for _, sp := range secs {
			for _, taskID := range splitList(sp.Field("covers")) {
				covers[taskID] = append(covers[taskID], sp.ID)
			}
			walk(sp.Children)
		}
	}
	walk(spec.Sections)
	if len(covers) == 0 {
		return nil
	}
	plan, err := artifact.Load(projectRoot, artifact.KindPlan)
	if err != nil || plan == nil {
		return nil
	}
	wired := map[string][]string{}
	changed := false
	for mi := range plan.Sections {
		for ti := range plan.Sections[mi].Children {
			t := &plan.Sections[mi].Children[ti]
			var add []string
			for _, specID := range covers[t.ID] {
				if !slices.Contains(t.Implements, specID) {
					add = append(add, specID)
				}
			}
			if len(add) == 0 {
				continue
			}
			// The edge lives as a `**Implements:**` line in the section's
			// body text — Render re-emits bodies verbatim, so the body is
			// what must change, not the parsed slice beside it.
			lines := strings.Split(t.Body, "\n")
			placed := false
			for i, line := range lines {
				if strings.HasPrefix(strings.TrimSpace(line), "**Implements:**") {
					lines[i] = strings.TrimRight(line, " ") + ", " + strings.Join(add, ", ")
					placed = true
					break
				}
			}
			if placed {
				t.Body = strings.Join(lines, "\n")
			} else {
				line := "**Implements:** " + strings.Join(add, ", ")
				if strings.TrimSpace(t.Body) == "" {
					t.Body = line
				} else {
					t.Body = line + "\n\n" + t.Body
				}
			}
			t.Implements = append(t.Implements, add...)
			wired[t.ID] = add
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := os.WriteFile(artifact.Path(projectRoot, artifact.KindPlan),
		[]byte(artifact.Render(plan)), 0o644); err != nil {
		return nil
	}
	return wired
}

// contextFitNotes sizes the opening prompt against each participating seat's
// declared context window. Past ~40% the seat starts its work already
// cramped — warned, so the person can reseat via the chips. Past ~90% it
// cannot meaningfully work at all — refused before a token is spent, naming
// the numbers and the levers. A seat with no declared window is skipped:
// silence about the unknown beats a guess.
func (s *Service) contextFitNotes(promptChars int, seats []config.DucklingID) (warns []string, fatal string) {
	promptTokens := promptChars / 4
	seen := map[config.DucklingID]bool{}
	for _, id := range seats {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		d, err := s.ducklings.Get(id)
		if err != nil || d.Caps.ContextTokens <= 0 {
			continue
		}
		pct := promptTokens * 100 / d.Caps.ContextTokens
		switch {
		case pct >= 90:
			fatal = fmt.Sprintf("the opening prompt is ~%dk tokens — %d%% of %s's declared "+
				"context (%dk). This seat cannot meaningfully work; reseat it (click its chip) "+
				"or trim what the stage carries", promptTokens/1000, pct, id, d.Caps.ContextTokens/1000)
			return
		case pct >= 40:
			warns = append(warns, fmt.Sprintf("the opening prompt is ~%dk tokens — %d%% of %s's "+
				"declared context (%dk). Headroom is thin: expect loops or truncation; a larger "+
				"seat is one chip-click away", promptTokens/1000, pct, id, d.Caps.ContextTokens/1000))
		}
	}
	return
}
