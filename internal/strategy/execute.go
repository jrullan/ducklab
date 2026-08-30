package strategy

import (
	"context"
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
	Error     error
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
	structureRetried := false
	pendingStructureNote := ""
	identicalRevision := false
	stuck := map[int]int{}
	redGateStreak := 0
	var evidence escalationEvidence

	for round := 1; round <= maxRounds; round++ {
		result.Rounds = round
		// One structure retry per ROUND: per run, a plan whose round-2
		// draft collided its lanes was only recorded (benchmark run 5).
		structureRetried = false
		state := conv.State{Round: round}
		verdictsThisRound := 0
		operational := ""
		// Consults that sent the implementer straight back to work this
		// round. Bounded: the duck is a counselor, not a judge, and only the
		// reviewer and the gate are the independent check.
		consultRetries := 0
		// The implementer's latest deliverables report this round: data for
		// the reviewer, evidence for the duck, a gap to flag on approve.
		var lastReport *DeliverablesReport

		for i := 0; i < len(script.Turns); i++ {
			turn := script.Turns[i]
			// The person's configured role cap beats the script's baked-in
			// number, as TurnCaps has always documented — applied HERE, on the
			// turn copy every consumer sees, not inside one runner. It used to
			// be consulted only by split/tournament/rubberduck (CapFor) while
			// builds were rescued by applyRoleTurns patching the script; a
			// test-first — which passes TurnCaps and patches nothing — ran its
			// implementer at TestFirstScript's hardcoded 24 while role_turns
			// said 100 and the Settings fallback said 40. A strong seat died
			// reading a 30-file project with every configured number decorative.
			if turn.Persona == PersonaCritic {
				// A document critic reads a draft that is in its prompt; the
				// script's six calls are the design. The configured reviewer
				// cap (for code reviews of large diffs) must not raise it: at
				// 100, a critic re-read the same sections for 29 calls of 41 s
				// (benchmark run 6). It may still lower it.
				if c := CapFor(params.TurnCaps, turn.Role, turn.MaxTurns); c < turn.MaxTurns {
					turn.MaxTurns = c
				}
			} else {
				turn.MaxTurns = CapFor(params.TurnCaps, turn.Role, turn.MaxTurns)
			}

			if params.ResumeFrom != nil && (round < params.ResumeFrom.Round || (round == params.ResumeFrom.Round && i < params.ResumeFrom.Index)) {
				continue
			}

			if turn.Role == config.RoleHuman {
				// A human turn is scheduled by the stage runner, not here.
				continue
			}
			// The previous round's revision IS this round's draft: the
			// critics judge it as it stands, and the architect speaks again
			// only after them.
			if script.RevisionOpensNextRound && round > 1 && i == 0 && lastArchitect != nil {
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

			prompt, err := buildPrompt(&turn, params, result.Transcript, findings, correctiveNotes, operational, lastReport, lastReview, seatLooked[turn.Role])
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
			if turn.Role == config.RoleArchitect && pendingStructureNote != "" {
				prompt += "\n\n" + pendingStructureNote
				pendingStructureNote = ""
			}
			if params.ResumeFrom != nil && round == params.ResumeFrom.Round && i == params.ResumeFrom.Index && params.ResumeFrom.Notes != "" {
				prompt += "\n\n## Resumed with partial notes\n\nThe " + string(params.ResumeFrom.Role) + " turn was interrupted. Continue from these notes:\n\n" + params.ResumeFrom.Notes
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
			emit(params, "turn_start", startData)

			outcome, err := runner(ctx, &turn, duckling, prompt, toolbelt, TurnContext{Round: round, Index: script.TurnIndexBase + i})
			if outcome != nil {
				result.Outcome = outcome
				if turn.Role == config.RoleArchitect && params.InventoryCoverage != nil {
					params.InventoryUnaccounted = params.InventoryCoverage(outcome.Text)
				}
			}
			if err != nil {
				notes := partialTurnNotes(outcome)
				emit(params, "turn_interrupted", map[string]interface{}{"round": round, "turn": i, "role": string(turn.Role), "notes": notes,
					"looked": mergeLooked(seatLooked[turn.Role], lookedFrom(outcome))})
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
					if problems := structureFindings(sectionsOf(lastArchitect), cur, turn.Contract, params.KnownIDs, params.SmallSeat, outcome.Text); len(problems) > 0 {
						emit(params, "structure_check", map[string]interface{}{
							"round": round, "turn": i, "findings": problems, "retried": !structureRetried,
						})
						if !structureRetried {
							structureRetried = true
							pendingStructureNote = structureNote(problems)
							emitMessage(params, round, i, turn.Role, duckling, outcome)
							i-- // the architect goes again, findings in hand
							continue
						}
					}
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
				// An approve over items the implementer itself reports
				// undelivered is a contradiction the record must show — the
				// T-119 ambiguity ("all already in the tree"?) made visible.
				if v.Verdict == "approve" && lastReport != nil {
					if gap := lastReport.Undelivered(); len(gap) > 0 {
						emit(params, "deliverables_gap", map[string]interface{}{
							"round": round, "undelivered": gap,
							"detail": "the reviewer approved while the implementer reports these deliverables undelivered",
						})
					}
				}
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
						if consultRetries < maxConsultRetries {
							consultRetries++
							emit(params, "advisor_retry", map[string]interface{}{
								"round": round, "retry": consultRetries, "of": maxConsultRetries,
							})
							i-- // run the implementer turn again, note in hand
							continue
						}
					}
				}
			}
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
			emit(params, "round_gate", map[string]interface{}{"result": gate, "round": round})
			if gate == "red" {
				redGateStreak++
			} else {
				redGateStreak = 0
			}
			evidence.RedGateStreak = redGateStreak
			_ = log
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

	return result, nil
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
	case config.RoleImplementer:
		if rendered := conv.RenderFindings(findings); rendered != "" {
			b.WriteString("\n\n")
			b.WriteString(rendered)
		}
		if len(correctiveNotes) > 0 {
			b.WriteString("\n\n## Advisor corrective note\n\n" + strings.Join(correctiveNotes, "\n\n"))
		}
		if len(params.Deliverables) > 0 {
			b.WriteString("\n\n" + deliverablesContract(params.Deliverables))
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
		}
		if rendered := tr.Render(turn.Anonymize, turn.OmitRole); rendered != "" {
			b.WriteString("\n")
			b.WriteString(rendered)
		}
	}
	return b.String(), nil
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

// CapFor returns the call cap for a role: the configured one, else the fallback
// the caller was going to use.
func CapFor(caps map[config.Role]int, role config.Role, fallback int) int {
	if n, ok := caps[role]; ok && n > 0 {
		return n
	}
	return fallback
}
