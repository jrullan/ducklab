package strategy

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/conv"
	"github.com/jrullan/ducklab/internal/tools"
)

// TurnRunner executes one turn and returns its outcome.
//
// Injected rather than called directly so the round scheduler — the part that
// must be deterministic — can be tested without a provider, a repo, or a
// model. The default implementation calls agent.RunTurn.
// TurnContext is where a turn sits and where it works.
type TurnContext struct {
	// Round and Index identify WHICH turn this is, not just what kind.
	// Streamed tokens have to be attributable to one turn: keyed by duckling
	// alone, a council's second architect turn appended to the first one's.
	Round int
	Index int
	// Root is the tree this turn may touch. Empty means the project root.
	//
	// A tournament creates an isolated worktree per contestant and then had no
	// way to tell the runner about it, so every contestant edited the shared
	// tree instead. Their patches came back empty, the judge correctly found
	// nothing to choose between, and the work still landed in the repository
	// with no one having picked it.
	Root string
}

type TurnRunner func(ctx context.Context, t *Turn, duckling config.DucklingID, prompt string, toolbelt []string, tc TurnContext) (*agent.Outcome, error)

// GateRunner runs the project's verification and reports green/red/none.
type GateRunner func(ctx context.Context) (gate string, log string, err error)

// ExecuteParams are the inputs to a script execution.
type ExecuteParams struct {
	ProjectRoot string
	TaskID      string
	Prompt      string
	// Deliverables is the task's numbered checklist — the implementer's work
	// contract (deliverables.go). Empty means no contract is asked for.
	Deliverables []string
	AgentLoop    *agent.Loop
	ExecContext  *tools.ExecContext
	Rounds       int
	// KnownIDs are the section ids that exist across the project's documents
	// (requirements, spec, plan). A document council's structure check flags
	// an Implements: target outside this set — eleven dangling references
	// reached a plan's gate (benchmark run 3). Empty means "do not check".
	KnownIDs map[string]bool
	// SmallSeat says the project's implementer is a small local seat: the
	// plan's structure check enforces the portion rule (≤3 top-level
	// deliverables per task) instead of only asking for it.
	SmallSeat bool
	// StructureCheck adds project/environment facts to the document's
	// deterministic structure check. The plan stage uses it to distinguish a
	// valid-but-missing capability from a likely misspelled local capability.
	StructureCheck func(raw string) []string

	// Runner executes a turn. Defaults to agent.RunTurn via AgentLoop.
	Runner TurnRunner
	// Gate runs verification between rounds. If nil the gate is "none" and
	// Until expressions referring to it evaluate accordingly — an honest
	// answer, never an assumed green (P3).
	Gate GateRunner
	// Diff returns the current working-tree diff, shown to the reviewer.
	Diff func() (string, error)
	// Roster maps a role to the duckling that plays it.
	Roster map[config.Role]config.DucklingID
	// InventoryUnaccounted is the lexical coverage gap from an adoption survey.
	// It is shown only to document critics, so they can aim at named gaps.
	InventoryUnaccounted []agent.InventoryItem
	// InventoryCoverage derives lexical gaps from the architect draft before critique.
	InventoryCoverage func(string) []agent.InventoryItem

	// TurnCaps overrides how many model calls one turn of a role may chain.
	// Absent leaves whatever the script or the mode carries.
	//
	// Needed because tournament and split build their turns themselves rather
	// than from a script, so walking a script's turns reaches four modes out of
	// six and a setting that applies to some modes is worse than none.
	TurnCaps map[config.Role]int
	// LiveToolEvents says the runner emits tool_call events itself, per call,
	// as they complete (agent.Loop.OnToolCall) — the post-turn batch here
	// would duplicate every one of them in the record.
	LiveToolEvents bool
	// OnEvent reports progress; optional.
	OnEvent func(kind string, data map[string]interface{})

	// EscalationCandidates are optional scorecard evidence supplied by the service.
	// A nil list preserves event-only observability; a non-nil list requires a
	// strictly stronger same-role candidate before suggesting a reseat.
	EscalationCandidates []EscalationCandidate
	CurrentLowerBound    float64
	ModeMedian           float64
	// ResumeFrom checkpoints an interrupted turn.
	ResumeFrom *ResumeTurn
}

// ResumeTurn is the durable checkpoint for an interrupted turn.
type ResumeTurn struct {
	Round int
	Index int
	Role  config.Role
	Notes string
	// Looked is what the interrupted turn had already read (tool + target),
	// handed back to the resumed seat so it does not start over.
	Looked []string
	// Findings carries the blocking review ledger across a process-level
	// pause. Resume skips earlier turns, so rebuilding this from the new empty
	// transcript is impossible.
	Findings []conv.Finding
}

// RoundRecord is what happened in one round.
type RoundRecord struct {
	Round   int
	Gate    string
	Verdict string
	Choice  string
}

// EscalationCandidate is scorecard evidence for a same-role reseat.
type EscalationCandidate struct {
	ID          string  `json:"id"`
	WilsonFloor float64 `json:"wilson_floor"`
	CostPerRun  float64 `json:"cost_per_run"`
	Why         string  `json:"why,omitempty"`
}

const stuckDeliverableReports = 3

// escalationEvidence is deliberately computed from structured run evidence,
// never from model prose.
type escalationEvidence struct {
	StuckItem     int
	StuckReports  int
	Turns         int
	ModeMedian    float64
	RedGates      int
	RedGateStreak int
	// UnansweredDeath records a turn that exhausted its reply loop or budget
	// before producing an answer; it is structured distress evidence itself.
	UnansweredDeath bool
}

func (e escalationEvidence) fired() []string {
	var out []string
	if e.StuckItem > 0 && e.StuckReports >= stuckDeliverableReports {
		out = append(out, "stuck_deliverable")
	}
	if e.ModeMedian > 0 && float64(e.Turns) > 2*e.ModeMedian {
		out = append(out, "turns_over_2x_mode_median")
	}
	if e.RedGateStreak >= 3 {
		out = append(out, "consecutive_red_round_gates")
	}
	if e.UnansweredDeath {
		out = append(out, "unanswered_death")
	}
	return out
}

func emitEscalationSuggestion(params *ExecuteParams, evidence escalationEvidence, point string) {
	triggers := evidence.fired()
	if len(triggers) == 0 {
		return
	}
	// nil means legacy/event-only callers: retain the evidence card. An
	// explicitly empty candidate set means scorecard machinery found nobody.
	if params.EscalationCandidates != nil {
		stronger := false
		for _, c := range params.EscalationCandidates {
			if c.WilsonFloor > params.CurrentLowerBound {
				stronger = true
				break
			}
		}
		if !stronger {
			return
		}
	}
	data := map[string]interface{}{
		"point": point, "thresholds_fired": triggers,
		"stuck_item": evidence.StuckItem, "stuck_reports": evidence.StuckReports,
		"turns": evidence.Turns, "mode_median": evidence.ModeMedian,
		"red_gate_streak": evidence.RedGateStreak,
		"diagnoses": map[string]interface{}{
			"seat_at_capacity":   map[string]interface{}{"turns": evidence.Turns, "mode_median": evidence.ModeMedian, "consecutive_red_gates": evidence.RedGateStreak},
			"task_brief_quality": "at-capacity and badly-briefed look identical; improve the task body before reseating",
		},
		"actions": []string{"relaunch_with_stronger_seat", "improve_task_body", "continue_as_is"},
	}
	if params.EscalationCandidates != nil {
		best := params.EscalationCandidates[0]
		for _, c := range params.EscalationCandidates[1:] {
			if c.WilsonFloor > best.WilsonFloor {
				best = c
			}
		}
		data["candidate"] = best
	}
	emit(params, "escalation_suggestion", data)
}

// ExecuteResult is the result of executing a script.
type ExecuteResult struct {
	Text       string
	Rounds     int
	Outcome    *agent.Outcome
	Transcript *conv.Transcript
	State      conv.State
	Records    []RoundRecord
	// RoleTexts holds every reply per role, in turn order — the memory Text
	// lacks. The stage layer falls back through an architect's earlier
	// drafts when the final revise carries no sections.
	RoleTexts map[string][]string
	// CandidateDigest identifies the exact body passed to the bounded final
	// reviewer. The stage gate compares it with the persisted proposal body.
	CandidateDigest string
	Error           error
}

// ExecuteSolo executes the solo mode.
func ExecuteSolo(ctx context.Context, params *ExecuteParams) (*ExecuteResult, error) {
	return ExecuteScript(ctx, SoloScript(), params)
}

// ExecuteTestFirst writes the failing test for a task.
func ExecuteTestFirst(ctx context.Context, params *ExecuteParams) (*ExecuteResult, error) {
	return ExecuteScript(ctx, TestFirstScript(), params)
}

// ExecuteTestFirstMode executes the test-writing script for a mode.
func ExecuteTestFirstMode(ctx context.Context, mode string, params *ExecuteParams) (*ExecuteResult, error) {
	return ExecuteScript(ctx, TestFirstScriptFor(mode), params)
}

// ExecutePair executes the pair mode.
func ExecutePair(ctx context.Context, params *ExecuteParams) (*ExecuteResult, error) {
	return ExecuteScript(ctx, PairScript(), params)
}

// ExecuteScript runs a conversation script.
//
// The loop is: for each round, run every turn in order, then evaluate the
// script's Until expression against the round's state. Turn order and round
// count are data; no model influences either (05 §3.1).
// maxConsultRetries bounds the [implementer ↔ advisor] inner loop per round.
const maxConsultRetries = 2

func consultRetryLimit(params *ExecuteParams) int {
	if params != nil && params.SmallSeat {
		// On slow local seats, repeated self-consultation consumes the wallclock
		// reserved for the independent reviewer. One applied note is a useful
		// rescue; after that, review findings are the next repair input.
		return 1
	}
	return maxConsultRetries
}

func ExecuteScript(ctx context.Context, script *Script, params *ExecuteParams) (*ExecuteResult, error) {
	result := &ExecuteResult{Transcript: &conv.Transcript{}}

	registry := registryFrom(params)
	if err := script.Validate(registry); err != nil {
		result.Error = err
		return result, err
	}
	until, err := conv.Compile(script.Until)
	if err != nil {
		result.Error = err
		return result, err
	}

	maxRounds := params.Rounds
	if maxRounds <= 0 {
		maxRounds = script.MaxRounds
	}
	if maxRounds <= 0 {
		maxRounds = 3
	}

	runner := params.Runner
	if runner == nil {
		runner = defaultRunner(params)
	}

	// findings carry the previous round's review into this round's implementer
	// prompt; this is what makes pair an iteration rather than two monologues.
	var findings []conv.Finding
	if params.ResumeFrom != nil {
		findings = append(findings, params.ResumeFrom.Findings...)
	}
	var correctiveNotes []string
	// What the reviewer may remember of its own previous round (reviewmemory.go).
	var lastReview *reviewMemory
	// What every other seat may remember of its own earlier turns: the
	// reads. A resumed or revising architect used to start from nothing.
	seatLooked := map[config.Role][]string{}
	if params.ResumeFrom != nil && len(params.ResumeFrom.Looked) > 0 {
		seatLooked[params.ResumeFrom.Role] = mergeLooked(nil, params.ResumeFrom.Looked)
	}
	// Council convergence (structurecheck.go): the previous architect draft,
	// the one-shot structure note for a retried revision, and whether the
	// latest revision changed anything at all.
	var lastArchitect *agent.Outcome
	var bestArchitect *agent.Outcome
	bestStructureProblems := int(^uint(0) >> 1)
	var bestStructureFindings []string
	structureAttempts := 0
	structureStagnation := 0
	previousStructureSignature := ""
	pendingStructureNote := ""
	var pendingRepairBase *agent.Outcome
	var pendingRepairSections []string
	identicalRevision := false
	stuck := map[int]int{}
	redGateStreak := 0
	var evidence escalationEvidence
	var planManifest *agent.PlanManifest
	materialize := func(detail string) error {
		if script.MaterializeCandidate == nil || lastArchitect == nil {
			return nil
		}
		candidate, err := script.MaterializeCandidate(result.RoleTexts[string(config.RoleArchitect)], lastArchitect)
		if err != nil {
			return err
		}
		lastArchitect = candidate
		result.Outcome, result.Text = candidate, candidate.Text
		result.CandidateDigest = documentCandidateDigest(candidate.Text)
		emit(params, "candidate_materialized", map[string]interface{}{
			"candidate_digest": result.CandidateDigest,
			"detail":           detail,
		})
		return nil
	}
	for _, entry := range result.Transcript.Entries {
		if parsed, parseErr := agent.ParseContract("json:plan_manifest", entry.Text); parseErr == nil {
			if manifest, ok := parsed.(*agent.PlanManifest); ok {
				planManifest = manifest
			}
		}
	}

	for round := 1; round <= maxRounds; round++ {
		result.Rounds = round
		// One structure retry per ROUND: per run, a plan whose round-2
		// draft collided its lanes was only recorded (benchmark run 5).
		structureAttempts = 0
		structureStagnation = 0
		previousStructureSignature = ""
		pendingRepairBase = nil
		pendingRepairSections = nil
		state := conv.State{Round: round}
		verdictsThisRound := 0
		operational := ""
		// Consults that sent the implementer straight back to work this
		// round. Bounded: the duck is a counselor, not a judge, and only the
		// reviewer and the gate are the independent check.
		consultRetries := 0
		consultLimit := consultRetryLimit(params)
		// A missing deliverables report is a protocol-level incomplete ending,
		// not something worth spending an independent reviewer on. One bounded
		// same-seat retry lets the implementer finish from the current tree.
		reportRetries := 0
		reportRetryNeedsWork := false
		// The implementer's latest deliverables report this round: data for
		// the reviewer, evidence for the duck, a gap to flag on approve.
		var lastReport *DeliverablesReport

		for i := 0; i < len(script.Turns); i++ {
			turn := script.Turns[i]
			if round > 1 && turn.Persona == PersonaPlanManifest {
				continue
			}
			// The person's configured role cap beats the script's baked-in
			// number, as TurnCaps has always documented — applied HERE, on the
			// turn copy every consumer sees, not inside one runner. It used to
			// be consulted only by split/tournament/rubberduck (CapFor) while
			// builds were rescued by applyRoleTurns patching the script; a
			// test-first — which passes TurnCaps and patches nothing — ran its
			// implementer at TestFirstScript's hardcoded 24 while role_turns
			// said 100 and the Settings fallback said 40. A strong seat died
			// reading a 30-file project with every configured number decorative.
			if turn.Persona == PersonaCritic || turn.MaxTurnsCeiling > 0 {
				// A document critic reads a draft that is in its prompt; the
				// script's six calls are the design. Other turns may declare the
				// same invariant explicitly: pair's independent reviewer gets a
				// fresh diff every round and is bounded at eight. The configured
				// role cap may lower these safety ceilings, never raise them.
				ceiling := turn.MaxTurns
				if turn.MaxTurnsCeiling > 0 {
					ceiling = turn.MaxTurnsCeiling
				}
				if c := CapFor(params.TurnCaps, turn.Role, turn.MaxTurns); c < ceiling {
					turn.MaxTurns = c
				} else {
					turn.MaxTurns = ceiling
				}
			} else {
				turn.MaxTurns = CapFor(params.TurnCaps, turn.Role, turn.MaxTurns)
			}
			if params.SmallSeat && script.Name == "pair" && turn.Role == config.RoleImplementer && turn.MaxTurns > 24 {
				// Pair mode promises an independent reviewer. A small local seat
				// can otherwise spend a configured high role cap on slow calls
				// until the run wallclock expires before review begins. Large
				// seats retain the explicit role-cap override contract.
				turn.MaxTurns = 24
			}

			if params.ResumeFrom != nil && (round < params.ResumeFrom.Round || (round == params.ResumeFrom.Round && i < params.ResumeFrom.Index)) {
				continue
			}

			if turn.Role == config.RoleHuman {
				// A human turn is scheduled by the stage runner, not here.
				continue
			}
			// A unanimous, finding-free approval is the end of a document
			// council. The old fixed sequence always bought a final architect
			// call before evaluating Until; in Neocapture corrida 9 that call
			// merely re-emitted an approved fragment. A requested change still
			// gets the revision turn below.
			if script.RevisionOpensNextRound && turn.Role == config.RoleArchitect && i == len(script.Turns)-1 &&
				verdictsThisRound > 0 && state.Verdict == "approve" && len(findings) == 0 && lastArchitect != nil {
				result.Outcome = lastArchitect
				result.Text = lastArchitect.Text
				emit(params, "revision_skipped", map[string]interface{}{
					"round": round, "detail": "all reviewers approved without findings; the reviewed draft is the proposal",
				})
				continue
			}
			// The previous round's revision IS this round's draft: the
			// critics judge it as it stands, and the architect speaks again
			// only after them.
			if script.RevisionOpensNextRound && round > 1 && verdictsThisRound == 0 && lastArchitect != nil &&
				turn.Role == config.RoleArchitect && (i == 0 || strings.HasPrefix(turn.Contract, "markdown_sections:")) {
				emit(params, "draft_carried", map[string]interface{}{"round": round, "detail": "the previous round's revision is this round's draft"})
				continue
			}

			toolbelt, err := turn.ResolveToolbelt(registry)
			if err != nil {
				result.Error = err
				return result, err
			}
			duckling := resolveDuckling(params, turn)
			if turn.Role == config.RoleImplementer {
				evidence.Turns++
				evidence.ModeMedian = params.ModeMedian
				evidence.RedGateStreak = redGateStreak
			}

			// Every document critic must judge the same deterministic object the
			// gate could persist. This includes the finding-free R1 fast path:
			// an intake amendment reviewer once approved four fragment sections,
			// then the stage merged sixteen and added provenance with no final
			// review and no candidate digest.
			if turn.Persona == PersonaCritic && lastArchitect != nil {
				if err := materialize("the candidate is frozen before a document critic reviews it"); err != nil {
					result.Error = err
					return result, err
				}
			}
			promptTranscript := result.Transcript
			if turn.Persona == PersonaCritic && script.MaterializeCandidate != nil {
				// The authoritative candidate below supersedes architect wire
				// fragments. Showing both made a small reviewer call REQ-900 and
				// its assigned REQ-016 duplicate requirements, buying a needless
				// second round. Keep human context; the existing OmitRole still
				// hides fellow reviewers for decorrelation.
				promptTranscript = transcriptWithoutRole(result.Transcript, config.RoleArchitect)
			}
			documentContract := turn.Contract
			promptParams := params
			if turn.Role == config.RoleReviewer && turn.Contract == "verdict" && params.Diff != nil {
				if diff, diffErr := params.Diff(); diffErr != nil {
					result.Error = diffErr
					return result, diffErr
				} else {
					// Contract selection and prompt construction must judge the exact
					// same snapshot. Calling Diff twice consumed two versions in a live
					// provider and made review memory describe the wrong hunks.
					frozen := diff
					copy := *params
					copy.Diff = func() (string, error) { return frozen, nil }
					promptParams = &copy
					if nativeCodeDiff(diff) {
						turn.Contract = "verdict:native"
					}
				}
			}
			prompt, err := buildPrompt(&turn, promptParams, promptTranscript, findings, correctiveNotes, operational, lastReport, lastReview, seatLooked[turn.Role])
			if turn.Role == config.RoleArchitect && turn.Contract == "markdown_sections:M" && verdictsThisRound > 0 {
				prompt += "\n\n## Reviewed topology amendments\n\nThe manifest constrained the initial render, but the reviewer has now checked its semantics. Apply supported reviewer corrections even when they change a manifest-derived Implements, Produces, Consumes, Verification, Owns, or Depends on field. Preserve all unrelated topology. The revised, deterministically validated plan becomes authoritative."
			}
			if turn.Persona == PersonaCritic && strings.HasPrefix(kindOfContract(turn.Contract, script), "plan") {
				prompt += "\n\nDucklab deterministically normalizes Toolchain entries to resolvable command and pkg-config module names. Treat normalized names such as `pkg-config:x11` as workspace facts; do not replace them with an OS package or library display name. `Consumes` means a task reads or depends on an artifact; it does not claim or modify that artifact and cannot by itself create a lane collision. Ownership conflicts arise from overlapping `Owns` or multiple `Produces` entries. Every task must implement at least one accepted SPEC id; `Implements: none` is invalid."
			}
			if turn.Persona == PersonaCritic && script.CriticScope != "" {
				prompt += "\n\n## Isolated review boundary — authoritative\n\n" + script.CriticScope
			}
			if turn.Persona == PersonaCritic && script.FragmentPrefix != "" {
				prompt = fragmentCriticContext(prompt)
			}
			// A fragment council's transcript contains the individual patches,
			// newest last. That is useful history but it is not the document: a
			// small critic treated the latest one-section repair as the complete
			// amendment and reported the other amended sections missing. Present
			// the deterministically materialized candidate as the review object.
			if turn.Persona == PersonaCritic && (script.MaterializeCandidate != nil || script.FragmentPrefix != "") && lastArchitect != nil {
				heading := "## Materialized candidate — authoritative"
				if script.FragmentPrefix != "" {
					heading = "## Materialized fragment candidate — authoritative"
				}
				prompt += "\n\n" + heading + "\n\n" + lastArchitect.Text +
					"\n\nReview ONLY the materialized candidate above. It is the exact body Ducklab will offer at the gate."
				if script.FragmentPrefix != "" {
					prompt += " Earlier architect messages may repeat the `" + fragmentPlaceholderForPrompt(script.FragmentPrefix) +
						"` new-section placeholder; that is the required input protocol, not a duplicate-id defect."
					prompt += " The authoritative candidate is already POST-MERGE: a section deleted with `**Delete:** yes` is correctly absent, and the tombstone itself must not persist. Never request a tombstone merely because the deleted section is absent here."
				}
			}
			// The draft a critic is about to judge is served by artifact_read
			// too: told "spec does not exist yet", a small seat asked nineteen
			// times (benchmark run 4).
			if params.ExecContext != nil && turn.Persona == PersonaCritic && lastArchitect != nil {
				if kind := kindOfContract(turn.Contract, script); kind != "" {
					if params.ExecContext.DraftUnderReview == nil {
						params.ExecContext.DraftUnderReview = map[string]string{}
					}
					params.ExecContext.DraftUnderReview[kind] = lastArchitect.Text
				}
			}
			var repairBase *agent.Outcome
			var repairSections []string
			if turn.Role == config.RoleArchitect && pendingStructureNote != "" {
				prompt += "\n\n" + pendingStructureNote
				pendingStructureNote = ""
				repairBase = pendingRepairBase
				pendingRepairBase = nil
				repairSections = pendingRepairSections
				pendingRepairSections = nil
			}
			if repairBase != nil {
				// A bounded structure repair already has every input it needs:
				// the complete checkpoint, exact H2 assignment and findings. In
				// Neocapture corrida 11, leaving document reads enabled made the
				// small architect re-explore requirements and then reproduce the
				// whole spec twice instead of returning SPEC-010. Remove that
				// branch of the state space for transactional repair turns.
				toolbelt = nil
				turn.Contract = "json:structure_patch"
			}
			if turn.Role == config.RoleArchitect && params.ProjectRoot != "" {
				prompt += "\n\n## Deterministic workspace facts\n\n- Tool project root: `.`\n- Absolute project root: `" + params.ProjectRoot + "`\n\nThese are harness facts, not user decisions. Use `.` for tool paths and never call `ask_human` to discover the project root."
			}
			if params.ResumeFrom != nil && round == params.ResumeFrom.Round && i == params.ResumeFrom.Index && params.ResumeFrom.Notes != "" {
				prompt += "\n\n## Resume checkpoint — continue, do not restart\n\nYour " + string(params.ResumeFrom.Role) + " turn was interrupted. Continue from the saved draft below. The reads listed in `What you already read` remain valid; do not repeat them unless the working tree invalidates one.\n\n" + resumeCheckpointNotes(params.ResumeFrom.Notes)
			}
			if err != nil {
				result.Error = err
				return result, err
			}

			startData := map[string]interface{}{
				"round": round, "turn": i, "role": string(turn.Role), "duckling": string(duckling),
			}
			if turn.Role == config.RoleImplementer && consultRetries > 0 {
				startData["retry"] = consultRetries
			}
			if repairBase != nil {
				startData["repair_attempt"] = structureAttempts + 1
				startData["repair_max"] = maxStructureAttempts
				startData["repair_sections"] = repairSections
				startData["repair_best_problem_count"] = bestStructureProblems
				startData["repair_stagnant_attempts"] = structureStagnation
				startData["repair_stagnation_limit"] = maxStructureStagnation
			}
			emit(params, "turn_start", startData)
			if params.ExecContext != nil {
				params.ExecContext.ExplorationCallLimit = 0
				if params.SmallSeat && turn.Role == config.RoleImplementer {
					// Slow small seats pay heavily for every observational loop.
					// Twelve calls still permit broad repository orientation, while
					// leaving room in the turn to edit and verify. A successful write
					// reopens another bounded inspect/act cycle.
					params.ExecContext.ExplorationCallLimit = 12
				}
				if turn.Role == config.RoleImplementer && (reportRetries > 0 || consultRetries > 0) {
					// A protocol retry continues on the same tree with the prior
					// evidence in its transcript. Give it enough observation for a
					// targeted re-read, not another full research phase.
					params.ExecContext.ExplorationCallLimit = 4
				}
			}
			narrowContinuation := consultRetries > 0 || (reportRetries > 0 && !reportRetryNeedsWork)
			if turn.Role == config.RoleImplementer && narrowContinuation && turn.MaxTurns > 8 {
				// A continuation has the current tree, prior evidence and one
				// narrow correction. An omitted report after a red gate is not
				// narrow: the seat is still fixing implementation evidence, so it
				// retains the normal implementer budget.
				// Bound only genuinely narrow continuations so pair mode still
				// reaches the mandatory reviewer within the run budget.
				turn.MaxTurns = 8
			}

			outcome, err := runner(ctx, &turn, duckling, prompt, toolbelt, TurnContext{Round: round, Index: script.TurnIndexBase + i})
			turn.Contract = documentContract
			if err == nil && repairBase == nil && turn.Role == config.RoleArchitect && script.ArchitectScopeID != "" {
				var scopeErr error
				outcome, scopeErr = scopeArchitectSection(outcome, documentContract, script.ArchitectScopeID, script.ArchitectScopeTitle)
				if scopeErr != nil {
					err = scopeErr
				}
			}
			repairScopeProblem := ""
			if err == nil && repairBase != nil {
				var merged *agent.Outcome
				var mergeErr error
				if _, structured := outcome.Parsed.(map[string]interface{}); structured {
					merged, mergeErr = applyStructurePatch(repairBase, outcome, documentContract, repairSections)
				} else {
					// Test and old-run compatibility: a resumed checkpoint may
					// still carry the former H2 repair response shape.
					merged, mergeErr = mergeStructureRepairScoped(repairBase, outcome, documentContract, repairSections)
				}
				outcome = merged
				if mergeErr != nil {
					repairScopeProblem = mergeErr.Error()
					emit(params, "structure_patch_rejected", map[string]interface{}{
						"round": round, "turn": i, "attempt": structureAttempts + 1,
						"sections": repairSections, "detail": repairScopeProblem,
					})
				}
			}
			if err == nil && turn.Persona == PersonaPlanManifest {
				manifest, ok := outcome.Parsed.(*agent.PlanManifest)
				if !ok || manifest == nil {
					err = fmt.Errorf("plan manifest turn returned no validated topology")
				} else {
					planManifest = manifest
					emit(params, "plan_manifest", map[string]interface{}{
						"round": round, "milestones": len(manifest.Milestones), "detail": "validated topology will constrain the rendered plan",
					})
				}
			}
			if err == nil && repairBase == nil && turn.Role == config.RoleArchitect &&
				strings.HasPrefix(turn.Contract, "markdown_sections:") && lastArchitect != nil {
				if materialized, sections := materializePartialRevision(lastArchitect, outcome, turn.Contract); len(sections) > 0 {
					outcome = materialized
					emit(params, "revision_materialized", map[string]interface{}{
						"round": round, "turn": i, "sections": sections,
						"detail": "partial revision merged transactionally into the last complete draft",
					})
				}
			}
			if err == nil && turn.Role == config.RoleArchitect && documentContract == "markdown_sections:REQ" {
				normalized, priorityChanges, normalizeErr := normalizeRequirementPriorities(outcome, documentContract)
				if normalizeErr != nil {
					err = normalizeErr
				} else {
					outcome = normalized
					if priorityChanges > 0 {
						emit(params, "structure_normalized", map[string]interface{}{
							"round": round, "turn": i, "fields": priorityChanges,
							"detail": "canonicalized unambiguous requirement Priority fields",
						})
					}
				}
			}
			enforcePlanManifest := documentContract == "markdown_sections:M" && round == 1 && verdictsThisRound == 0
			if err == nil && turn.Role == config.RoleArchitect && enforcePlanManifest {
				normalized, manifestChanges, normalizeErr := reconcilePlanManifest(outcome, planManifest, documentContract)
				if normalizeErr != nil {
					err = normalizeErr
				} else {
					outcome = normalized
					graphNormalized, graphChanges, graphErr := normalizePlanGraph(outcome, documentContract)
					if graphErr != nil {
						err = graphErr
					} else {
						outcome = graphNormalized
					}
					if err == nil && manifestChanges+graphChanges > 0 {
						emit(params, "structure_normalized", map[string]interface{}{
							"round": round, "turn": i, "fields": manifestChanges + graphChanges,
							"detail": "compiled the validated manifest and derived Owns and Depends on fields from the plan artifact graph",
						})
					}
				}
			}
			if outcome != nil {
				result.Outcome = outcome
				if turn.Role == config.RoleArchitect && params.InventoryCoverage != nil {
					params.InventoryUnaccounted = params.InventoryCoverage(outcome.Text)
				}
			}
			if err != nil {
				notes := partialTurnNotes(outcome)
				emit(params, "turn_interrupted", map[string]interface{}{"round": round, "turn": i, "role": string(turn.Role), "notes": notes,
					"looked": mergeLooked(seatLooked[turn.Role], lookedFrom(outcome)), "findings": findings})
				// What it managed
				// way out. This used to return first, so a turn that failed took
				// its whole record with it: a run that patched a file seventeen
				// times left a transcript of four events, and the only way to
				// see the work was to read llm.jsonl by hand. The failure is
				// exactly when that record is worth most.
				if errors.Is(err, agent.ErrNoAnswer) || errors.Is(err, agent.ErrBudgetExceeded) {
					evidence.UnansweredDeath = true
					reason := "budget_exceeded"
					if errors.Is(err, agent.ErrNoAnswer) {
						reason = "no_answer"
					}
					emit(params, "distress_evidence", map[string]interface{}{
						"kind": "unanswered_death", "role": string(turn.Role), "reason": reason,
					})
					emitEscalationSuggestion(params, evidence, "failed_run")
				}
				if outcome != nil {
					emitMessage(params, round, i, turn.Role, duckling, outcome)
					emit(params, "turn_end", map[string]interface{}{
						"round": round, "turn": i, "role": string(turn.Role),
						"incomplete": true,
					})
				}
				// A pause propagates untouched: the caller checkpoints the run
				// and the loop resumes from the top once answered.
				result.Error = err
				return result, err
			}
			// A document council's architect: check the structure of the draft
			// against the rules and the draft before it, once; and notice a
			// revision that changed nothing, which no further round will fix.
			if turn.Role == config.RoleArchitect && strings.HasPrefix(turn.Contract, "markdown_sections:") {
				if cur := sectionsOf(outcome); cur != nil {
					problems := structureFindings(sectionsOf(lastArchitect), cur, turn.Contract, params.KnownIDs, params.SmallSeat, outcome.Text)
					if params.StructureCheck != nil {
						problems = append(problems, params.StructureCheck(outcome.Text)...)
					}
					if enforcePlanManifest {
						problems = append(problems, planManifestFindings(planManifest, outcome)...)
					}
					if repairScopeProblem != "" {
						problems = append([]string{repairScopeProblem}, problems...)
					}
					if repairBase != nil && repairScopeProblem == "" {
						baseProblems := structureFindings(sectionsOf(lastArchitect), sectionsOf(repairBase), turn.Contract, params.KnownIDs, params.SmallSeat, repairBase.Text)
						if params.StructureCheck != nil {
							baseProblems = append(baseProblems, params.StructureCheck(repairBase.Text)...)
						}
						if enforcePlanManifest {
							baseProblems = append(baseProblems, planManifestFindings(planManifest, repairBase)...)
						}
						if len(problems) >= len(baseProblems) {
							repairScopeProblem = fmt.Sprintf("structure repair made no monotonic progress: %d findings before, %d after; checkpoint rolled back", len(baseProblems), len(problems))
							emit(params, "structure_patch_rejected", map[string]interface{}{
								"round": round, "turn": i, "attempt": structureAttempts + 1,
								"sections": repairSections, "detail": repairScopeProblem,
							})
							outcome = repairBase
							result.Outcome = outcome
							problems = append([]string{repairScopeProblem}, baseProblems...)
						}
					}
					if len(problems) > 0 {
						structureAttempts++
						improved := structureAttempts == 1 || bestArchitect == nil || len(problems) < bestStructureProblems
						if improved {
							bestArchitect, bestStructureProblems = outcome, len(problems)
							bestStructureFindings = append([]string{}, problems...)
							structureStagnation = 0
						} else {
							structureStagnation++
						}
						signature := strings.Join(problems, "\n")
						emit(params, "structure_check", map[string]interface{}{
							"round": round, "turn": i, "findings": problems, "attempt": structureAttempts, "max_attempts": maxStructureAttempts,
						})
						if structureAttempts < maxStructureAttempts && signature != previousStructureSignature && structureStagnation < maxStructureStagnation {
							pendingStructureNote, pendingRepairSections = structureRepairInstruction(bestStructureFindings, sectionsOf(bestArchitect), structureRepairContext{
								Contract: turn.Contract,
								KnownIDs: params.KnownIDs,
							})
							if len(pendingRepairSections) == 0 {
								// An empty allowlist must never mean unrestricted repair.
								// If a finding cannot be mapped to an H2 checkpoint,
								// ask for a complete revision and validate it normally.
								pendingStructureNote = structureNote(bestStructureFindings)
								pendingRepairBase = nil
							} else {
								pendingRepairBase = bestArchitect
							}
							if repairScopeProblem != "" {
								pendingStructureNote += "\nThe previous patch was rejected without changing the checkpoint: " + repairScopeProblem + "\n"
							}
							previousStructureSignature = signature
							emitMessage(params, round, i, turn.Role, duckling, outcome)
							i-- // the architect goes again, findings in hand
							continue
						}
						best := bestArchitect
						if best == nil {
							best = outcome
						}
						result.Outcome, result.Text, result.Error = best, best.Text, ErrStructureFailed
						emit(params, "structure_failed", map[string]interface{}{
							"round": round, "turn": i, "findings": problems,
							"reason":             map[bool]string{true: "stalled", false: "attempts_exhausted"}[signature == previousStructureSignature || structureStagnation >= maxStructureStagnation],
							"best_problem_count": bestStructureProblems,
							"stagnant_attempts":  structureStagnation,
							"attempt":            structureAttempts,
							"max_attempts":       maxStructureAttempts,
							"stagnation_limit":   maxStructureStagnation,
							"stall_cause":        map[bool]string{true: "repeated_findings", false: "no_best_progress"}[signature == previousStructureSignature],
						})
						return result, ErrStructureFailed
					}
					if bestArchitect == nil || 0 < bestStructureProblems {
						bestArchitect, bestStructureProblems = outcome, 0
					}
					// A clean draft ends this repair chain. A later architect
					// revision starts a fresh checkpoint even in the same council
					// round; otherwise an older structurally clean but semantically
					// superseded draft would win over the new revision.
					structureAttempts = 0
					previousStructureSignature = ""
				}
				if lastArchitect != nil && i == len(script.Turns)-1 &&
					strings.TrimSpace(outcome.Text) == strings.TrimSpace(lastArchitect.Text) {
					identicalRevision = true
					emit(params, "revision_identical", map[string]interface{}{
						"round": round, "detail": "the revision is byte-identical to the previous draft; another round would change nothing",
					})
				}
				lastArchitect = outcome
			}
			// Fragment councils intentionally have no markdown_sections contract
			// on architect turns, but their draft is still the object reviewers
			// judge and the revision that opens a possible next round. Tracking it
			// must not depend on the output parser selected for that turn.
			if turn.Role == config.RoleArchitect && script.RevisionOpensNextRound && !strings.HasPrefix(turn.Contract, "markdown_sections:") {
				if script.FragmentPrefix != "" {
					lastArchitect = materializeFragment(lastArchitect, outcome, script.FragmentPrefix)
				} else {
					lastArchitect = outcome
				}
			}
			result.Outcome = outcome
			result.Text = outcome.Text
			// Every reply by role, in order. Text alone keeps only the LAST
			// turn, and for artifact councils that is the revise — a revise
			// that stands pat would otherwise erase the draft it stood on.
			if result.RoleTexts == nil {
				result.RoleTexts = map[string][]string{}
			}
			result.RoleTexts[string(turn.Role)] = append(result.RoleTexts[string(turn.Role)], outcome.Text)

			result.Transcript.Add(conv.Entry{
				Round: round, Index: script.TurnIndexBase + i, Role: turn.Role,
				Duckling: duckling, Text: transcriptText(outcome),
			})

			// An author reporting a work-contract item as partial, blocked, or
			// omitted is stronger evidence than a bare reviewer approval. T-006
			// produced exactly that contradiction: the reviewer approved while
			// acceptance slice 1 was explicitly partial, and auto mode committed
			// it. Convert the contradiction into a normal review ledger item so
			// it receives the same bounded repair loop as any other finding.
			if turn.Role == config.RoleReviewer && lastReport != nil {
				if v, ok := outcome.Parsed.(*agent.Verdict); ok && v != nil && v.Verdict == "approve" {
					if gap := incompleteDeliverables(lastReport, len(params.Deliverables)); len(gap) > 0 {
						v.Verdict = "request-changes"
						for _, id := range gap {
							item := fmt.Sprintf("acceptance slice %d", id)
							if id > 0 && id <= len(params.Deliverables) {
								item += ": " + params.Deliverables[id-1]
							}
							v.Findings = append(v.Findings, agent.Finding{
								Severity: "major", File: "*",
								Invariant: "Every numbered acceptance slice is complete before approval",
								Issue:     item + " remains undelivered in the implementer's completion report",
								Fix:       "complete the slice and report it done, or return concrete evidence that the report was wrong",
							})
						}
						emit(params, "deliverables_gap", map[string]interface{}{
							"round": round, "undelivered": gap, "original_verdict": "approve",
							"effective_verdict": "request-changes",
							"detail":            "reviewer approval was converted to request-changes because the work contract remains incomplete",
						})
					}
				}
			}

			if turn.Role == config.RoleReviewer && params.Diff != nil {
				if diff, derr := params.Diff(); derr == nil {
					lastReview = rememberReview(diff, outcome)
				}
			}
			seatLooked[turn.Role] = mergeLooked(seatLooked[turn.Role], lookedFrom(outcome))

			// What the model actually said, and what it did.
			//
			// turn_start and turn_end bracketed a turn whose content was never
			// recorded anywhere: the run log held eleven events and not one
			// carried a message, /transcript answered an empty document, and
			// the desktop's conversation lanes had nothing to render. The text
			// existed the whole time — it fed the internal transcript one line
			// above — it just never left the process.
			emitMessage(params, round, i, turn.Role, duckling, outcome)

			// Fold the turn's parsed contract value into the round state.
			switch v := outcome.Parsed.(type) {
			case *agent.Verdict:
				// The WORST verdict of the round, not the last: a council seats
				// several critics now, and one request-changes among approvals
				// is a request for changes. Overwriting meant the last critic
				// to speak decided for everyone.
				if verdictsThisRound == 0 || state.Verdict == "approve" {
					state.Verdict = v.Verdict
				}
				verdictsThisRound++
				// Findings accumulate across the round's critics — each saw a
				// different blind spot, which is the reason to seat more than
				// one — and reset with the next round's fresh draft.
				if verdictsThisRound == 1 {
					findings = toConvFindings(v.Findings)
				} else {
					findings = append(findings, toConvFindings(v.Findings)...)
				}
				state.NoFindings = len(findings) == 0
			case *agent.Choice:
				state.Choice = v.Choice
			}

			emit(params, "turn_end", map[string]interface{}{
				"round": round, "turn": i, "role": string(turn.Role),
				// If a history-duration pause is waiting for this safe point,
				// the service persists this completed turn as a conservative
				// replay checkpoint. Repeating one turn is cheaper than starting
				// the whole strategy over and losing its review ledger.
				"notes":    partialTurnNotes(outcome),
				"looked":   seatLooked[turn.Role],
				"findings": findings,
			})

			// The rubber duck: after the implementer's turn is closed on the
			// record, before the reviewer speaks, and only on measured
			// distress. See rubberduck.go.
			if turn.Role == config.RoleImplementer {
				if len(params.Deliverables) > 0 {
					lastReport = ParseDeliverablesReport(outcome.Text, len(params.Deliverables))
					// Self-contained: the texts ride along so a client can
					// render the checklist without re-deriving it from the task.
					reportData := map[string]interface{}{
						"round": round, "items": lastReport.Items, "unreported": lastReport.Unreported,
						"total": len(params.Deliverables), "undelivered": lastReport.Undelivered(),
						"deliverables": params.Deliverables,
					}
					if consultRetries > 0 {
						reportData["retry"] = consultRetries
					}
					reportData["missing"] = lastReport.Undelivered()
					emit(params, "deliverables_report", reportData)
					if lastReport.Unreported && reportRetries == 0 {
						reportRetries++
						reportRetryNeedsWork = !outcomeVerifiedAfterMutation(outcome)
						correctiveNotes = append(correctiveNotes,
							"Your previous implementer turn ended without the required deliverables JSON report. Continue from the CURRENT tree; do not restart research. Finish any work still pending, run verify_run, and end with one status entry for every numbered deliverable.")
						emit(params, "deliverables_retry", map[string]interface{}{
							"round": round, "retry": reportRetries, "of": 1,
							"detail": "the implementer omitted its completion report; retrying before review",
						})
						i--
						continue
					}
					missing := map[int]bool{}
					for _, id := range lastReport.Undelivered() {
						missing[id] = true
					}
					for id := range stuck {
						if !missing[id] {
							delete(stuck, id)
						}
					}
					for id := range missing {
						stuck[id]++
					}
					evidence.StuckItem, evidence.StuckReports = 0, 0
					for id, n := range stuck {
						if n >= stuckDeliverableReports && n > evidence.StuckReports {
							evidence.StuckItem, evidence.StuckReports = id, n
						}
					}
					evidence.ModeMedian = params.ModeMedian
					evidence.RedGateStreak = redGateStreak
				}
				if signals := measureDistressWithReport(outcome, lastReport); signals.Distressed() {
					// A complete report backed by a green verify after the last
					// mutation is ready for independent review. Tool friction from
					// earlier in that turn must not let the advisor replace the
					// reviewer with another open-ended implementation cycle.
					if completedAndVerified(lastReport, outcome) {
						emit(params, "advisor_skipped", map[string]interface{}{
							"round": round, "reason": "completed deliverables have a current green verify; proceeding to independent review",
						})
						continue
					}
					if len(evidence.fired()) > 0 {
						emitEscalationSuggestion(params, evidence, "distress_pause")
					}
					if summary, ok := operationalSummaryWithReport(outcome, lastReport); ok {
						operational = summary
					}
					note, stop, cerr := consultAdvisor(ctx, params, runner, registry, round, script.TurnIndexBase+i, duckling, outcome, signals)
					if cerr != nil {
						result.Error = cerr
						return result, cerr
					}
					if stop != nil {
						result.Error = stop
						return result, stop
					}
					if note != "" {
						correctiveNotes = append(correctiveNotes, note)
						// [imp ↔ adv] before the reviewer: advice applied
						// while the atasco is still warm costs one implementer
						// turn; sending wounded work to the reviewer costs a
						// reviewer turn AND the next round. Bounded, and the
						// note also stays for later rounds.
						if consultRetries < consultLimit {
							consultRetries++
							emit(params, "advisor_retry", map[string]interface{}{
								"round": round, "retry": consultRetries, "of": consultLimit,
							})
							i-- // run the implementer turn again, note in hand
							continue
						}
					}
				}
			}
		}

		// An operator pause requested during a turn lands from the turn_end
		// callback. That callback cancels this context only after the model's
		// work and message are durable. Do not start a gate with the cancelled
		// context: it would manufacture a red exit -1 and overwrite the real
		// pause with an UNVERIFIED gate decision.
		if err := ctx.Err(); err != nil {
			result.Error = err
			return result, err
		}

		// The gate runs after the round's turns, and it — not any model —
		// decides whether the work is green (I2).
		//
		// Except on a replayed round: a resume skips every turn of the rounds
		// before its checkpoint (their work is already in the tree), and the
		// gate that judged them is already in the record — re-running it
		// bought a full suite (36 s measured, minutes on a slow gate) before
		// the interrupted seat could even re-enter, on every question,
		// escalation and budget lift. The person read it as "continue" →
		// wait → and only then the run continues.
		replayedRound := params.ResumeFrom != nil && round < params.ResumeFrom.Round
		if params.Gate != nil && !replayedRound {
			// Announced BEFORE it runs: a full suite can legally take minutes,
			// and a transcript whose reviewer just approved while nothing moved
			// read as a hang — the person could not see the harness working.
			emit(params, "gate_started", map[string]interface{}{"round": round})
			gate, log, err := params.Gate(ctx)
			if err != nil {
				result.Error = err
				return result, err
			}
			state.Gate = gate
			// round_gate, not gate: the two carry different things under the same
			// name otherwise. The service's "gate" reports a verification —
			// which command ran and what it exited with. This reports a round's
			// outcome, green or red. A consumer taking the latest "gate" event
			// got whichever happened to come last, and the desktop's gate card
			// showed the right thing only because of event ordering.
			emit(params, "round_gate", map[string]interface{}{
				"result": gate, "round": round, "log": firstNStr(log, 4000),
			})
			if gate == "red" {
				redGateStreak++
			} else {
				redGateStreak = 0
			}
			evidence.RedGateStreak = redGateStreak
		}
		// Whether this round actually touched the tree. It used to be hardcoded
		// true, which was never read and never true in the interesting case.
		state.Changed = true
		if params.Diff != nil {
			if diff, err := params.Diff(); err == nil {
				state.Changed = strings.TrimSpace(diff) != ""
			}
		}

		result.State = state
		result.Records = append(result.Records, RoundRecord{
			Round: round, Gate: state.Gate, Verdict: state.Verdict, Choice: state.Choice,
		})

		done, err := until.Eval(&state)
		if err != nil {
			result.Error = err
			return result, err
		}
		if done {
			break
		}
		// No gate is configured: "green" cannot come, and waiting for it
		// bought two more rounds of an approved change (T-001, benchmark run
		// 5: 3 rounds, 2.15M tokens). Approved without a gate is UNVERIFIED —
		// honest — and it is final.
		if state.Gate == "none" && state.Verdict == "approve" {
			emit(params, "no_gate", map[string]interface{}{
				"round":  round,
				"detail": "the reviewer approved and no verification gate is configured — the run ends UNVERIFIED; further rounds could not turn it green",
			})
			break
		}
		// A revision identical to the draft it revised: the critics will read
		// the same document and object the same way. Two byte-identical
		// revisions cost 3 minutes and 9.5k tokens each (Neocapture plan,
		// 2026-08-29). The person decides at the gate with the verdict as is.
		if identicalRevision {
			break
		}

		// Nothing another round could change. The tree is untouched and the
		// gate is green, so the next implementer turn has nothing to write and
		// the next reviewer turn will read the same empty diff and object to it
		// again. T-007 burned three rounds this way: both ducklings agreed in
		// prose that the work was already present, and the reviewer returned
		// "request-changes" each time because its verdict contract has no way
		// to say "the code is right, the plan is wrong". The loop cannot
		// terminate on an objection the implementer cannot act on, so it is
		// terminated here instead.
		if !state.Changed && state.Gate == "green" {
			emit(params, "settled", map[string]interface{}{
				"round":  round,
				"detail": "no round changed the tree and the gate is green — further rounds cannot alter either",
			})
			break
		}
	}

	// The proposal is not the model's last bytes. Stage-owned folding and id
	// assignment can change section order and identity. Neocapture corrida 15
	// reviewed SPEC-005→SPEC-004, then offered a canonically renumbered
	// SPEC-004→SPEC-005 at the gate. Materialize once before review and return
	// that same body to proposal storage.
	if err := materialize("the stage-final candidate is frozen before final review and proposal storage"); err != nil {
		result.Error = err
		return result, err
	}

	// A two-round council used to end on an unreviewed architect revision:
	// reviewer requests one last change, architect applies it, gate receives
	// UNVERIFIED without knowing whether the real candidate fixed the issue or
	// introduced a new contradiction. One bounded, read-only critic pass does
	// not open a third repair loop; it gives the person evidence about exactly
	// what they are being asked to accept.
	if script.RevisionOpensNextRound && result.Rounds == maxRounds &&
		result.State.Verdict == "request-changes" && lastArchitect != nil {
		if err := finalDocumentReview(ctx, script, params, runner, registry, lastArchitect, findings, result); err != nil {
			result.Error = err
			return result, err
		}
	}
	if script.RequireApproval && result.State.Verdict != "approve" {
		result.Error = ErrReviewNotConverged
		return result, ErrReviewNotConverged
	}

	// The stage merger must receive the same cumulative candidate the critic
	// judged, not merely the final one-section patch emitted by the architect.
	if script.FragmentPrefix != "" && lastArchitect != nil {
		result.Outcome = lastArchitect
		result.Text = lastArchitect.Text
	}
	return result, nil
}

func completedAndVerified(report *DeliverablesReport, outcome *agent.Outcome) bool {
	if report == nil || report.Unreported || len(report.Undelivered()) > 0 || outcome == nil {
		return false
	}
	return outcomeVerifiedAfterMutation(outcome)
}

func outcomeVerifiedAfterMutation(outcome *agent.Outcome) bool {
	if outcome == nil {
		return false
	}
	lastVerify, lastMutation := -1, -1
	verifyGreen := false
	for i, call := range outcome.ToolCalls {
		switch call.Name {
		case "fs_write", "fs_write_lines", "fs_patch", "fs_delete":
			if call.Result != nil && !call.Result.IsError {
				lastMutation = i
			}
		case "verify_run":
			lastVerify = i
			verifyGreen = call.Result != nil && !call.Result.IsError
		}
	}
	return verifyGreen && lastVerify > lastMutation
}

func finalDocumentReview(ctx context.Context, script *Script, params *ExecuteParams, runner TurnRunner, registry *tools.Registry, candidate *agent.Outcome, openFindings []conv.Finding, result *ExecuteResult) error {
	digest := documentCandidateDigest(candidate.Text)
	result.CandidateDigest = digest
	emit(params, "final_review_started", map[string]interface{}{
		"round": result.Rounds, "candidate_digest": digest,
		"detail": "the last revision is being checked without opening another repair round",
	})
	verdict := "approve"
	var finalFindings []conv.Finding
	reviewers := 0
	for _, scripted := range script.Turns {
		if scripted.Persona != PersonaCritic {
			continue
		}
		turn := scripted
		toolbelt, err := turn.ResolveToolbelt(registry)
		if err != nil {
			return err
		}
		duckling := resolveDuckling(params, turn)
		promptTranscript := result.Transcript
		if script.MaterializeCandidate != nil {
			promptTranscript = transcriptWithoutRole(result.Transcript, config.RoleArchitect)
		}
		prompt, err := buildPrompt(&turn, params, promptTranscript, nil, nil, "", nil, nil, nil)
		if err != nil {
			return err
		}
		if script.FragmentPrefix != "" {
			prompt = fragmentCriticContext(prompt)
		}
		prompt += "\n\n## Final candidate under review\n\n" + candidate.Text +
			"\n\nThis is verification only. Return a verdict on this exact candidate; no architect turn follows automatically."
		if script.FragmentPrefix != "" {
			prompt += " The candidate is already POST-MERGE: explicit deletion tombstones have been applied and consumed. A deleted section's absence is correct; do not demand that `**Delete:** yes` appear in this persisted body."
		}
		if rendered := conv.RenderFindings(openFindings); rendered != "" {
			prompt += "\n\n## Open finding ledger from the preceding review\n\n" + rendered +
				"\nRe-check EACH ledger item against the exact candidate above. Approve only if every item is now resolved. " +
				"If any remains, return request-changes and repeat that unresolved item in findings; an approve must certify the whole ledger, not merely report that the latest revision changed text."
		}
		if params.ExecContext != nil {
			if kind := kindOfContract(turn.Contract, script); kind != "" {
				if params.ExecContext.DraftUnderReview == nil {
					params.ExecContext.DraftUnderReview = map[string]string{}
				}
				params.ExecContext.DraftUnderReview[kind] = candidate.Text
			}
		}

		index := script.TurnIndexBase + len(script.Turns) + reviewers
		emit(params, "turn_start", map[string]interface{}{
			"round": result.Rounds, "turn": index, "role": string(turn.Role),
			"duckling": string(duckling), "final_review": true,
		})
		outcome, err := runner(ctx, &turn, duckling, prompt, toolbelt, TurnContext{Round: result.Rounds, Index: index})
		if err != nil {
			return err
		}
		emitMessage(params, result.Rounds, index, turn.Role, duckling, outcome)
		emit(params, "turn_end", map[string]interface{}{
			"round": result.Rounds, "turn": index, "role": string(turn.Role), "final_review": true,
		})
		result.Transcript.Add(conv.Entry{
			Round: result.Rounds, Index: index, Role: turn.Role, Duckling: duckling, Text: transcriptText(outcome),
		})
		if result.RoleTexts == nil {
			result.RoleTexts = map[string][]string{}
		}
		result.RoleTexts[string(turn.Role)] = append(result.RoleTexts[string(turn.Role)], outcome.Text)
		reviewers++

		v, ok := outcome.Parsed.(*agent.Verdict)
		if !ok || v == nil {
			return fmt.Errorf("final document reviewer returned no verdict")
		}
		if v.Verdict != "approve" {
			verdict = v.Verdict
		}
		finalFindings = append(finalFindings, toConvFindings(v.Findings)...)
	}
	if reviewers == 0 {
		return nil
	}
	result.State.Verdict = verdict
	result.State.NoFindings = len(finalFindings) == 0
	if n := len(result.Records); n > 0 {
		result.Records[n-1].Verdict = verdict
	}
	emit(params, "final_review_completed", map[string]interface{}{
		"round": result.Rounds, "verdict": verdict, "findings": len(finalFindings), "candidate_digest": digest,
		"detail": "final evidence recorded; no additional revision was started",
	})
	return nil
}

// fragmentCriticContext removes the fragment author's wire protocol from a
// critic prompt. Corrida 28 showed the critic obeying the leading "Update this
// requirements" and "omitting does not delete" rules against the later
// post-merge candidate: it demanded consumed tombstones be persisted and sent
// the architect into malformed deletion loops. A critic needs the human's
// request and the materialized result, never the patch encoding.
func fragmentCriticContext(prompt string) string {
	start := strings.Index(prompt, "## Your task\n\nUpdate this ")
	draft := strings.Index(prompt, "## The draft under review")
	requestStart := strings.Index(prompt, "## The request\n\n")
	outline := strings.Index(prompt, "\n\n## The document today")
	if start < 0 || draft <= start || requestStart < start || outline <= requestStart || outline > draft {
		return prompt
	}
	requestStart += len("## The request\n\n")
	request := strings.TrimSpace(prompt[requestStart:outline])
	replacement := "## Amendment under review\n\nJudge whether the authoritative POST-MERGE candidate faithfully applies the human request. " +
		"Do not review or request author-side fragment syntax: placeholders and deletion tombstones have already been applied and consumed. " +
		"`Originates from` provenance is generated by Ducklab from the accepted intent records; additional intent ids on changed sections are expected and are not unauthorized content changes. " +
		"Enforce the smallest semantic delta: wording such as 'not required' removes a mandatory constraint but does not remove or forbid the capability. Reject deletion of that capability unless the human explicitly requested removal/forbiddance or it is necessary to resolve a direct contradiction with the requested behavior. " +
		"Preserve the force of the human's words: 'shall', 'must', and 'required' demand Priority: must; reject a candidate that weakens them to should or could. " +
		"Reject overlapping requirements created by the amendment: when an existing section already represented the capability (including as an exclusion or opposite decision), it should be transformed, not transformed AND accompanied by a new section for the same behavior. A new section is justified only for a distinct, independently testable behavior.\n\n" +
		"Reject fields copied from unrelated sections (for example, a display assumption moved onto a keyboard-shortcut requirement) unless the human request or that section's own behavior requires the field.\n\n" +
		"## Human request\n\n" + request + "\n\n"
	return prompt[:start] + replacement + prompt[draft:]
}

func documentCandidateDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", sum[:8])
}

func transcriptWithoutRole(in *conv.Transcript, role config.Role) *conv.Transcript {
	out := &conv.Transcript{}
	if in == nil {
		return out
	}
	for _, entry := range in.Entries {
		if entry.Role != role {
			out.Entries = append(out.Entries, entry)
		}
	}
	return out
}

// buildPrompt assembles the turn's user prompt: the task, the previous round's
// review if this is an implementer, and the diff if this is a reviewer.
func buildPrompt(turn *Turn, params *ExecuteParams, tr *conv.Transcript, findings []conv.Finding, correctiveNotes []string, operational string, report *DeliverablesReport, lastReview *reviewMemory, looked []string) (string, error) {
	var b strings.Builder
	b.WriteString(params.Prompt)

	switch turn.Role {
	case config.RoleArchitect:
		// A revision that cannot see the critique is just a second draft.
		if rendered := tr.Render(false, ""); rendered != "" {
			b.WriteString("\n\n")
			b.WriteString(rendered)
		}
		// And a revision that forgot what it read is a survey all over
		// again: two resumptions cost an architect 17 tool calls and 22
		// minutes re-reading the same documents (Neocapture, 2026-08-29).
		if memo := alreadyRead(looked); memo != "" {
			b.WriteString("\n\n" + memo)
		}
		if turn.Contract == "markdown_sections:M" {
			b.WriteString("\n\n## Validated topology\n\nThe earlier JSON plan manifest is the structural source of truth. Render it as milestones and H3 tasks without changing task ownership, producers, consumers, or verification. Ducklab derives `Owns` and `Depends on` from that graph; do not invent broad aggregate lanes such as `src/`.")
		}
	case config.RoleImplementer:
		if len(correctiveNotes) > 0 {
			b.WriteString("\n\n## Advisor corrective note\n\n" + strings.Join(correctiveNotes, "\n\n"))
		}
		if len(params.Deliverables) > 0 {
			b.WriteString("\n\n" + deliverablesContract(params.Deliverables))
		}
		// Put review feedback LAST. On a long task prompt, placing it before
		// the specification and deliverables made a small implementer read the
		// files, run a green compiler gate, and declare success without changing
		// any of the five defects the reviewer had just named. These findings
		// are the active work queue, not historical conversation.
		if rendered := conv.RenderFindings(findings); rendered != "" {
			b.WriteString("\n\n## Blocking review ledger — dispose every item this turn\n\n")
			b.WriteString("A green verification command does not resolve semantic review findings. For EACH numbered item below, either patch the code, or explicitly dispute it with concrete file-and-line evidence. Merely re-reading files or re-running the gate is not a disposition. Do not report the task complete while an item is unaddressed. In your final summary, list `Review dispositions: 1..N` and say `fixed` or `disputed` with the evidence for each.\n\n")
			b.WriteString(rendered)
		}
	case config.RoleReviewer:
		if len(params.InventoryUnaccounted) > 0 {
			b.WriteString("\n\n## Adoption survey gaps\nThe proposal does not account for these inventoried surfaces; critique the named gaps:\n")
			for _, item := range params.InventoryUnaccounted {
				fmt.Fprintf(&b, "- %s (%s) [%s]\n", item.Name, item.Kind, item.EvidencePath)
			}
		}
		if operational != "" {
			b.WriteString("\n\n## Operational summary\n\n```json\n" + operational + "\n```\n")
		}
		if section := deliverablesForReviewer(params.Deliverables, report); section != "" {
			b.WriteString("\n\n" + section)
		}
		// A document critic gets the draft under its own heading, with the
		// mechanism spelled out. Presented only as "Conversation so far", the
		// draft read as chat history and the reviewer went looking for the
		// real thing with tools — which truthfully reported a world without
		// it, since a proposal touches nothing until a person accepts it.
		if turn.Persona == PersonaCritic {
			b.WriteString("\n\n## The draft under review\n\n" +
				"The proposal you are critiquing is in the conversation below. " +
				"It exists only there — not in the tree, not in the artifact " +
				"store — until a person accepts it, so do not go looking for " +
				"it with tools.\n")
			// A critic that comes back after a pause is a fresh conversation
			// too; it gets its reads back like the architect does.
			if memo := alreadyRead(looked); memo != "" {
				b.WriteString("\n" + memo)
			}
		}
		// The reviewer gets the diff and the conversation with the author's
		// own turns removed (I7). Compacted per file: a tracked build
		// artifact once rode this prompt at 644KB and the reviewer re-read
		// it on all 22 calls of its loop — 4.7M tokens for one minified
		// bundle (T-067).
		if params.Diff != nil {
			diff, err := params.Diff()
			if err != nil {
				return "", fmt.Errorf("diff for reviewer: %w", err)
			}
			if memo := sinceLastReview(lastReview, diff); memo != "" {
				b.WriteString("\n\n" + memo)
			}
			b.WriteString("\n\n## The change under review\n\n```diff\n")
			b.WriteString(strings.TrimSpace(conv.CompactDiff(diff)))
			b.WriteString("\n```\n")
			if nativeCodeDiff(diff) {
				b.WriteString("\n## Native-code review sweep — required before verdict\n\n" +
					"Trace every changed success path and every early error/cleanup path. Explicitly verify: " +
					"(1) every path completes or signals its waiter and cannot deadlock; " +
					"(2) each allocation/handle uses the matching allocator-family release exactly once; " +
					"(3) shared state, thread lifetime, joins/unrefs and blocking API semantics are safe; " +
					"(4) external byte/pixel/wire representations are converted rather than assumed, including masks, channel widths, byte order, stride and alpha; " +
					"(5) null/failure cleanup never calls an API with an invalid handle; " +
					"(6) every public API parameter has observable semantics: callback user_data reaches that callback and documented results match the implementation; " +
					"(7) any pointer retained past the public call (idle source, callback, worker or queue) has an explicit safe lifetime through copying, ref-counting or documented ownership transfer; " +
					"and (8) comments do not claim work, persistence or acknowledgements the implementation does not perform. " +
					"Do not approve until you have performed this sweep over the final diff, even when compilation is green.\n")
			}
		}
		if rendered := tr.Render(turn.Anonymize, turn.OmitRole); rendered != "" {
			b.WriteString("\n")
			b.WriteString(rendered)
		}
	}
	return b.String(), nil
}

func nativeCodeDiff(diff string) bool {
	for _, line := range strings.Split(diff, "\n") {
		if !strings.HasPrefix(line, "diff --git ") {
			continue
		}
		for _, field := range strings.Fields(line)[2:] {
			path := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(field, "a/"), "b/"))
			for _, suffix := range []string{".c", ".h", ".cc", ".cpp", ".cxx", ".hh", ".hpp", ".hxx", ".m", ".mm"} {
				if strings.HasSuffix(path, suffix) {
					return true
				}
			}
		}
	}
	return false
}

// transcriptText renders a turn for the next reader.
//
// A verdict's raw JSON is the wire format, not the content. An architect asked
// to revise against `{"verdict":"request-changes"}` has been told nothing;
// against the rendered findings it has been told what to change.
func transcriptText(outcome *agent.Outcome) string {
	if v, ok := outcome.Parsed.(*agent.Verdict); ok && v != nil {
		var b strings.Builder
		fmt.Fprintf(&b, "Verdict: %s\n", v.Verdict)
		if rendered := conv.RenderFindings(toConvFindings(v.Findings)); rendered != "" {
			b.WriteString(rendered)
		}
		return strings.TrimSpace(b.String())
	}
	return outcome.Text
}

func resolveDuckling(params *ExecuteParams, turn Turn) config.DucklingID {
	if turn.Duckling != "" {
		return turn.Duckling
	}
	if params.Roster != nil {
		if id, ok := params.Roster[turn.Role]; ok && id != "" {
			return id
		}
	}
	if params.AgentLoop != nil {
		return params.AgentLoop.Duckling.ID
	}
	return ""
}

func registryFrom(params *ExecuteParams) *tools.Registry {
	if params.AgentLoop != nil && params.AgentLoop.Registry != nil {
		return params.AgentLoop.Registry
	}
	return tools.NewRegistry()
}

func defaultRunner(params *ExecuteParams) TurnRunner {
	return func(ctx context.Context, t *Turn, duckling config.DucklingID, prompt string, toolbelt []string, tc TurnContext) (*agent.Outcome, error) {
		return agent.RunTurn(ctx, params.AgentLoop, &agent.Turn{
			Round:     tc.Round,
			Index:     tc.Index,
			Role:      t.Role,
			Duckling:  duckling,
			Prompt:    prompt,
			Toolbelt:  toolbelt,
			Contract:  t.Contract,
			MaxTurns:  t.MaxTurns,
			Anonymize: t.Anonymize,
			Persona:   t.Persona,
		}, params.ExecContext)
	}
}

func toConvFindings(in []agent.Finding) []conv.Finding {
	out := make([]conv.Finding, len(in))
	for i, f := range in {
		out[i] = conv.Finding{Severity: f.Severity, File: f.File, Line: f.Line, Issue: f.Issue, Fix: f.Fix, Invariant: f.Invariant}
	}
	return out
}

func emit(params *ExecuteParams, kind string, data map[string]interface{}) {
	if params.OnEvent != nil {
		params.OnEvent(kind, data)
	}
}

// emitMessage records a turn's content and its tool calls.
//
// Tool calls are separate events rather than a field on the message: the
// timeline renders them in order, and a turn that made forty fs_read calls
// must not put forty payloads inside one record.
func emitMessage(params *ExecuteParams, round, turn int, role config.Role, duckling config.DucklingID, outcome *agent.Outcome) {
	EmitTurnRecord(func(kind string, data map[string]interface{}) {
		emit(params, kind, data)
	}, round, turn, role, duckling, outcome, params.LiveToolEvents)
}

// EmitTurnRecord writes what a turn said and did, through whatever writer the
// caller has.
//
// Exported because the operate loop drives its own turns: triage runs one per
// bug outside a script, and it wrote turn_start and turn_end around nothing at
// all. The run showed a participant with an empty bubble, and the model's
// reasoning — which is the whole content of a triage — never left the process.
// Duplicating the event shapes here would have been two places to keep in step.
// liveToolEvents says the runner already emitted each tool_call as it
// completed (agent.Loop.OnToolCall); the batch below would duplicate them.
func EmitTurnRecord(emitFn func(kind string, data map[string]interface{}), round, turn int, role config.Role, duckling config.DucklingID, outcome *agent.Outcome, liveToolEvents bool) {
	emit := func(_ *ExecuteParams, kind string, data map[string]interface{}) { emitFn(kind, data) }
	var params *ExecuteParams
	_ = params
	if outcome == nil {
		return
	}
	if text := strings.TrimSpace(outcome.Text); text != "" {
		data := map[string]interface{}{
			"round": round, "turn": turn,
			"role": string(role), "duckling": string(duckling),
			"content":    text,
			"tokens_in":  outcome.TokensIn,
			"tokens_out": outcome.TokensOut,
			"repairs":    outcome.Repairs,
		}
		// A reviewer's turn is a verdict, not prose. Sending only the raw text
		// left the lane showing `{"verdict":"approve", "findings":[]}` — the
		// one turn whose content is already structured, displayed as a blob.
		// The raw text stays: it is what the model actually returned.
		if v, ok := outcome.Parsed.(*agent.Verdict); ok && v != nil {
			data["verdict"] = v.Verdict
			data["findings"] = v.Findings
		}
		if outcome.Reasoning != "" {
			data["reasoning"] = outcome.Reasoning
		}
		emit(params, "message", data)
	}
	// Live emission (agent.Loop.OnToolCall) supersedes this batch: a
	// thirty-call turn showed an empty timeline for its whole length and
	// then every tick at once. The batch remains for callers that wire no
	// callback, so nothing loses its record.
	if liveToolEvents {
		return
	}
	for _, tc := range outcome.ToolCalls {
		data := map[string]interface{}{
			"round": round, "turn": turn,
			"role": string(role), "duckling": string(duckling),
			// "tool", not "name": both existing consumers — the CLI's stream
			// printer and the desktop's timeline — already read this key.
			"tool": tc.Name,
			"args": string(tc.Args),
		}
		if tc.Result != nil {
			data["ok"] = !tc.Result.IsError
			// The result is summarised, not stored whole: a single fs_read can
			// return an entire file, and forty of them would make the run log
			// larger than the repository it describes.
			data["result"] = SummariseToolResult(tc.Result.Content)
		}
		if tc.Digest != "" {
			data["digest"] = tc.Digest
		}
		emit(params, "tool_call", data)
	}
}

// partialTurnNotes preserves the useful, bounded checkpoint of a turn that
// stopped before producing a final answer. Tool calls are included because the
// worktree may contain their effects while the model's draft and reasoning are
// the only explanation of what remains to do.
func partialTurnNotes(outcome *agent.Outcome) string {
	if outcome == nil {
		return ""
	}
	type partialCall struct {
		Name   string `json:"name"`
		Args   string `json:"args,omitempty"`
		Result string `json:"result,omitempty"`
		Digest string `json:"digest,omitempty"`
	}
	checkpoint := struct {
		Draft     string        `json:"draft,omitempty"`
		Reasoning string        `json:"reasoning,omitempty"`
		ToolCalls []partialCall `json:"tool_calls,omitempty"`
	}{
		Draft: strings.TrimSpace(outcome.Text), Reasoning: strings.TrimSpace(outcome.Reasoning),
	}
	for _, call := range outcome.ToolCalls {
		pc := partialCall{Name: call.Name, Digest: call.Digest}
		if len(call.Args) > 2048 {
			pc.Args = string(call.Args[:2048]) + "…"
		} else {
			pc.Args = string(call.Args)
		}
		if call.Result != nil {
			pc.Result = SummariseToolResult(call.Result.Content)
		}
		checkpoint.ToolCalls = append(checkpoint.ToolCalls, pc)
	}
	const maxCheckpointBytes = 16384
	marshal := func() []byte {
		data, _ := json.Marshal(checkpoint)
		return data
	}
	data := marshal()
	// Keep the checkpoint valid JSON even when a model produced unusually large
	// drafts. Drop the oldest tool details first; the bounded summary still
	// retains the role's draft/reasoning and the most recent activity.
	for len(data) > maxCheckpointBytes && len(checkpoint.ToolCalls) > 1 {
		checkpoint.ToolCalls = checkpoint.ToolCalls[1:]
		data = marshal()
	}
	if len(data) > maxCheckpointBytes {
		checkpoint.Draft = truncateCheckpointText(checkpoint.Draft, 4096)
		checkpoint.Reasoning = truncateCheckpointText(checkpoint.Reasoning, 4096)
		for i := range checkpoint.ToolCalls {
			checkpoint.ToolCalls[i].Args = truncateCheckpointText(checkpoint.ToolCalls[i].Args, 512)
			checkpoint.ToolCalls[i].Result = truncateCheckpointText(checkpoint.ToolCalls[i].Result, 512)
		}
		data = marshal()
	}
	return string(data)
}

func truncateCheckpointText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// maxToolResultBytes bounds what a tool result contributes to the log (I3).
const maxToolResultBytes = 512

func SummariseToolResult(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxToolResultBytes {
		return s
	}
	return s[:maxToolResultBytes] + fmt.Sprintf("\n… %d bytes truncated", len(s)-maxToolResultBytes)
}

const maxResumeNoteRunes = 12000

// resumeCheckpointNotes turns the loop's durable interruption envelope back
// into material useful to the model. Providers persist a JSON object containing
// the partial draft, reasoning and tool records; pasting that whole envelope
// into the prompt exposed implementation detail, consumed context and invited
// the resumed seat to replay its investigation. The partial draft is the
// continuation point. Older/plain checkpoints remain supported.
func resumeCheckpointNotes(raw string) string {
	raw = strings.TrimSpace(raw)
	var checkpoint struct {
		Draft     string `json:"draft"`
		Reasoning string `json:"reasoning"`
	}
	if json.Unmarshal([]byte(raw), &checkpoint) == nil {
		switch {
		case strings.TrimSpace(checkpoint.Draft) != "":
			raw = strings.TrimSpace(checkpoint.Draft)
		case strings.TrimSpace(checkpoint.Reasoning) != "":
			raw = strings.TrimSpace(checkpoint.Reasoning)
		}
	}
	runes := []rune(raw)
	if len(runes) > maxResumeNoteRunes {
		return string(runes[:maxResumeNoteRunes]) + fmt.Sprintf("\n… %d checkpoint characters omitted", len(runes)-maxResumeNoteRunes)
	}
	return raw
}

// CapFor returns the call cap for a role: the configured one, else the fallback
// the caller was going to use.
func CapFor(caps map[config.Role]int, role config.Role, fallback int) int {
	if n, ok := caps[role]; ok && n > 0 {
		return n
	}
	return fallback
}
