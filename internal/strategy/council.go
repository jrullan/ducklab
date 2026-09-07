package strategy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
)

// CouncilScript returns the artifact mode used by intake, spec and plan
// (05 §4.4).
//
// No code is written here. The architect drafts, a reviewer critiques the
// draft, and the architect revises. The human turn between them is where the
// rubber-duck premise becomes literal: the user is one of the ducks, not an
// audience for them.
//
// prefix is the section id prefix the artifact expects (REQ, SPEC, M), which
// fixes the architect's output contract.
// PersonaCritic marks a reviewer turn as a document critic: what it reviews
// is a draft that exists only in the conversation, and its system prompt must
// say so or the model spends its turns hunting for a diff.
const PersonaCritic = "critic"
const PersonaPlanManifest = "plan_manifest"
const PersonaPlanManifestCritic = "plan_manifest_critic"

const planManifestSemanticReview = `## Compact plan manifest audit — required

Review the JSON manifest below before Ducklab freezes its topology. This is a
semantic review; deterministic parsing, ids and graph checks have already run.

The candidate's complete schema is exactly: milestone fields id, title and
tasks; task fields id, title, implements, work_unit, acceptance_slices,
acceptance_probes, produces, consumes and verification. Do not request
Markdown rendering fields such as Owns, Toolchain, Depends on, Exercises, Out
of scope or Assumption. Ducklab derives or validates those after this compact
manifest is approved. Do not request any key outside this schema.

- Account for every accepted must and in-scope should SPEC obligation. An
  Implements id alone is not coverage: the work unit, observable slice and
  corresponding probe must jointly deliver the behavior and its polarity.
- Include could work only when the accepted scope selects it. Treat wont as a
  boundary, never as positive implementation work.
- Each task is one cohesive concern with 1–3 independently observable slices.
  Do not approve bundled concerns merely because they fit in three bullets.
  Multiple operations may share one task when they have the same actor,
  selection policy, change boundary and joint probe; do not split merely by
  counting endpoint names. Different selection or authority rules are distinct
  concerns.
- Every probe must actually observe its same-index slice. A generic build,
  grep, or count is not evidence for unrelated runtime or authority behavior.
- For every task, audit every slice/probe pair separately. State why the exact
  command observes that exact outcome with the required success/failure
  polarity; do not let a task-level summary stand in for this accounting.
- Preserve actor/action/object authority: validation, proposal and execution
  are different responsibilities and must not silently change owners.
- Implements is a many-to-many trace link, not artifact ownership. Several
  tasks may implement different obligations of one SPEC. Judge ownership from
  Produces/Consumes and the work unit; do not reject duplicate Implements links
  by themselves.
- Produces must name every file or bounded directory the task will create or
  edit, as well as any output target/capability another task consumes. A
  build-target names an output but does not grant ownership of an omitted build
  definition such as Cargo.toml, meson.build or a project file. Audit that the
  declared lanes are sufficient for the work unit.
- Review the whole compact manifest before approving. If it is defective,
  identify the exact SPEC obligation and the smallest repartition needed, but
  do not allocate milestone or task ids in the fix; the architect regenerates
  the complete manifest.

Approve only when this manifest is a sound topology to freeze.`

func planManifestSemanticReviewFor(small bool) string {
	if small {
		return planManifestSemanticReview
	}
	review := strings.Replace(planManifestSemanticReview,
		"- Each task is one cohesive concern with 1–3 independently observable slices.",
		"- Each task is one cohesive concern with one or more independently observable slices.", 1)
	return strings.Replace(review,
		"  Do not approve bundled concerns merely because they fit in three bullets.\n  Multiple operations may share one task when they have the same actor,",
		"  Do not split or merge tasks solely because of their slice count. Multiple\n  operations may share one task when they have the same actor,", 1)
}

func planManifestReviewContract(params *ExecuteParams, outcome *agent.Outcome) string {
	var specs []string
	if len(params.PlanSeed) > 0 {
		// PlanSeed is the accepted coverage ledger. KnownIDs is deliberately
		// broader: it also contains deferred and excluded sections so parsers can
		// distinguish a valid reference from a typo. H3a made the reviewer audit
		// both sets, including wont/could SPECs the seed had excluded.
		for _, spec := range params.PlanSeed {
			if planSeedInScope(spec) {
				specs = append(specs, spec.ID)
			}
		}
	} else {
		for id := range params.KnownIDs {
			if strings.HasPrefix(id, "SPEC-") {
				specs = append(specs, id)
			}
		}
	}
	var tasks []string
	if outcome != nil {
		if manifest, ok := outcome.Parsed.(*agent.PlanManifest); ok && manifest != nil {
			for _, milestone := range manifest.Milestones {
				for _, task := range milestone.Tasks {
					tasks = append(tasks, task.ID)
				}
			}
		}
	}
	sort.Strings(specs)
	sort.Strings(tasks)
	return "verdict:plan_manifest:" + strings.Join(specs, ",") + "|" + strings.Join(tasks, ",")
}

// planCoverageReview is semantic on purpose. Implements links, graph edges and
// field shapes are mechanical and belong to structureFindings; deciding whether
// a task's accepted outcomes actually deliver a specification obligation needs
// a critic. Keeping the instruction here makes that boundary explicit instead
// of disguising keyword overlap as proof of coverage.
const planCoverageReview = `## Plan obligation audit — required

An **Implements:** id is an index pointer, never evidence that the task delivers
the section. Before your verdict, read every accepted, in-scope SPEC section —
including sections absent from all Implements lines — and audit its obligations
against the tasks' **Work unit:** and top-level **Acceptance slices:**.

- Account for independently testable behavior, authority/boundary rules, and
  named error or exclusion cases. A title, explanatory paragraph, Produces,
  Exercises, or a broad project gate does not count as an accepted outcome.
- Respect each section's explicit priority. ` + "`must`" + ` and in-scope ` + "`should`" + `
  obligations need coverage. A ` + "`could`" + ` section needs work only when the accepted
  scope explicitly selects it. A ` + "`wont`" + ` section is a boundary to preserve, not a
  task that must be implemented merely to prove absence.
- A mandatory obligation is not implemented when its behavior appears only in
  an Assumption, Out of scope clause, title, explanatory prose, or as input
  injected by an unnamed upstream actor. Require an in-scope Work unit and a
  top-level Acceptance slice that observes the promised behavior.
- Preserve named authority boundaries as actor/action/object relations. A task
  that validates or returns evidence must not silently become the actor that
  decides, installs, activates, executes, or otherwise owns an action reserved
  by the specification to somebody else.
- A milestone's Owns lane is the permitted aggregate boundary for its child
  tasks' Produces entries. Do not report that parent/child containment as a
  second owner; report collisions only between competing tasks or milestones.
- A specification may be covered by several tasks and one task may cover
  several related specifications; do not demand a syntactic one-to-one split.
- If an obligation has no acceptance slice, request changes. Name the exact
  SPEC id and omitted obligation in one class-level finding, and ask for the
  smallest task/slice correction rather than rewriting unrelated topology.
- Approve only after this obligation-level sweep. Do not infer coverage merely
  because every SPEC id appears somewhere in Implements.`

// SoloArtifactScript is one architect, drafting alone.
//
// A deviation from 05 §4.4, which names council as the artifact mode. It is
// offered because the choice is real: council's value is a second model
// critiquing the draft, and that is worth its cost on a first draft of
// requirements and often not worth it on a small revision. Council stays the
// default; this is the cheaper answer for someone who knows they want it.
//
// No reviewer turn, so no verdict to wait on: one round, one draft.
func SoloArtifactScript(prefix string) *Script {
	return &Script{
		Name: "solo",
		Turns: []Turn{
			{
				Role:     config.RoleArchitect,
				Toolbelt: "full",
				Contract: fmt.Sprintf("markdown_sections:%s", prefix),
				MaxTurns: 12,
			},
		},
		// MaxRounds is 1, so the loop stops after the first round whatever this
		// says. It has to compile, and it must not wait on a verdict: there is
		// no reviewer to produce one.
		Until:     `round == 1`,
		MaxRounds: 1,
	}
}

// InventoryScript is the mandatory first pass of an adoption survey.
func InventoryScript() *Script {
	return &Script{
		Name:  "survey-inventory",
		Turns: []Turn{{Role: config.RoleArchitect, Toolbelt: "full", Contract: "json:inventory", MaxTurns: 12}},
		Until: `round == 1`, MaxRounds: 1,
	}
}

// CompositionReviewScript is one bounded semantic reading of a document that
// has already been assembled from isolated section passes. Local reviewers can
// prove each section is coherent in isolation; only this pass can see that two
// sections own the same concern, or that the composition omitted one entirely.
// It never repairs the document and has no tools: its verdict is evidence for
// the human gate, kept distinct from deterministic graph checks.
func CompositionReviewScript() *Script {
	return &Script{
		Name: "composition-review",
		Turns: []Turn{{
			Role:            config.RoleReviewer,
			Toolbelt:        "none",
			Contract:        "verdict",
			MaxTurns:        4,
			MaxTurnsCeiling: 4,
			Persona:         PersonaCritic,
		}},
		Until:     "round == 1",
		MaxRounds: 1,
	}
}

// ArtifactScript returns the script a stage should run for a mode.
//
// Unknown modes fall back to council rather than failing: the default is the
// spec's, and a typo should not stop someone drafting.
func ArtifactScript(prefix, mode string, critics []config.DucklingID) *Script {
	if mode == "solo" {
		return SoloArtifactScript(prefix)
	}
	return CouncilScript(prefix, critics)
}

// CouncilScript builds a council: one drafts, the others critique, the first
// revises.
//
// critics pins each critique turn to its own duckling, in line-up order. For a
// long time the council seated exactly two — which made it a council in name
// only, and made the third model a person ticked in Settings silently a
// spectator. The product's whole thesis is decorrelation between cheap models;
// a draft read by N different models with N different blind spots is that
// thesis applied to documents. Empty critics seats one unpinned reviewer, the
// roster's own, which is the original shape.
func CouncilScript(prefix string, critics []config.DucklingID) *Script {
	contract := fmt.Sprintf("markdown_sections:%s", prefix)
	var turns []Turn
	if prefix == "M" {
		turns = append(turns, Turn{
			Role:     config.RoleArchitect,
			Toolbelt: "none",
			Contract: "json:plan_manifest",
			MaxTurns: 2,
			Persona:  PersonaPlanManifest,
		})
		turns = append(turns, Turn{
			Role:            config.RoleReviewer,
			Toolbelt:        "none",
			Contract:        "verdict",
			MaxTurns:        4,
			MaxTurnsCeiling: 4,
			Persona:         PersonaPlanManifestCritic,
			Anonymize:       true,
			OmitRole:        config.RoleReviewer,
		})
	}
	turns = append(turns,
		Turn{
			Role:     config.RoleArchitect,
			Toolbelt: "document",
			Contract: contract,
			MaxTurns: 12,
		},
	)
	if len(critics) == 0 {
		critics = []config.DucklingID{""}
	}
	for _, c := range critics {
		turns = append(turns, Turn{
			Role:     config.RoleReviewer,
			Duckling: c,
			// A document council is a closed review. Ducklab supplies the
			// candidate, accepted lifecycle documents and explicit references in
			// the prompt; workspace tools let a small critic spend its whole turn
			// rediscovering that same input instead of returning a verdict.
			Toolbelt: "none",
			Contract: "verdict",
			MaxTurns: 6,
			Persona:  PersonaCritic,
			// Each critic reads the DRAFT, not the other critics. A critic
			// shown a fellow critic's findings anchors on them, and N critics
			// become one critique read N times — the decorrelation the extra
			// seats exist for, undone by the transcript (I7). The architect's
			// revision turn still sees every critique.
			Anonymize: true,
			OmitRole:  config.RoleReviewer,
		})
	}
	turns = append(turns,
		Turn{
			// Conditional: the scheduler skips it unless a human is
			// available and the stage asked for one (05 §4.4).
			Role:     config.RoleHuman,
			Contract: "freeform",
			MaxTurns: 1,
		},
		Turn{
			Role:     config.RoleArchitect,
			Toolbelt: "document",
			Contract: contract,
			MaxTurns: 12,
		},
	)
	return &Script{
		Name:  "council",
		Turns: turns,
		// Four rounds at most. Later rounds are dormant when a reviewer approves
		// early. Neocapture corrida 31 reached the final read-only review with one
		// localized ownership defect after steadily reducing 6 findings to 2 to
		// 5 to 1; a fourth reviewed repair lets that converging document finish
		// without making successful councils longer. The round's verdict is the WORST across
		// critics — one request-changes among approvals is a request for changes.
		Until:     `verdict == "approve"`,
		MaxRounds: 4,
		// Round 2 opens on the revision round 1 closed with: re-drafting it
		// first cost every council an architect turn per extra round
		// (benchmark run 6: draft → critique → revision → draft again).
		RevisionOpensNextRound: true,
	}
}
