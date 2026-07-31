package strategy

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
)

func councilParams(rec *recorder, outcomes ...*agent.Outcome) *ExecuteParams {
	return &ExecuteParams{
		Prompt: "Write the requirements for a timesheet app.",
		Runner: rec.runner(outcomes...),
		Roster: map[config.Role]config.DucklingID{
			config.RoleArchitect: "pato-atom",
			config.RoleReviewer:  "pato-local",
		},
	}
}

func TestCouncilScriptValidates(t *testing.T) {
	if err := CouncilScript("REQ", nil).Validate(testRegistry(t)); err != nil {
		t.Fatalf("council does not validate: %v", err)
	}
}

// The reviewer must see the draft. Anonymize controls WHO is shown, not
// whether the transcript appears — conflating them left council's reviewer
// reviewing nothing at all.
func TestCouncilReviewerSeesTheDraft(t *testing.T) {
	rec := &recorder{}
	draft := "## REQ-001 — Users can log time\n\nBody of the draft."
	params := councilParams(rec,
		&agent.Outcome{Text: draft},
		verdictOutcome("approve"),
		&agent.Outcome{Text: draft},
	)
	if _, err := ExecuteScript(context.Background(), CouncilScript("REQ", nil), params); err != nil {
		t.Fatal(err)
	}
	if len(rec.prompts) < 2 {
		t.Fatalf("only %d turns ran", len(rec.prompts))
	}
	reviewerPrompt := rec.prompts[1]
	if !strings.Contains(reviewerPrompt, "Body of the draft.") {
		t.Errorf("the reviewer was not shown the draft:\n%s", reviewerPrompt)
	}
}

// A revision that cannot see the critique is just a second draft.
func TestCouncilArchitectSeesTheCritique(t *testing.T) {
	rec := &recorder{}
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-001 — Draft\n"},
		verdictOutcome("request-changes", agent.Finding{
			Severity: "major", File: "requirements.md",
			Issue: "REQ-001 does not say what is out of scope", Fix: "add a scope line",
		}),
		&agent.Outcome{Text: "## REQ-001 — Revised\n"},
	)
	if _, err := ExecuteScript(context.Background(), CouncilScript("REQ", nil), params); err != nil {
		t.Fatal(err)
	}
	if len(rec.prompts) < 3 {
		t.Fatalf("the revision turn never ran (%d turns)", len(rec.prompts))
	}
	revision := rec.prompts[2]
	if !strings.Contains(revision, "out of scope") {
		t.Errorf("the architect's revision could not see the critique:\n%s", revision)
	}
}

// pair keeps the opposite rule: its reviewer must NOT read the author's
// reasoning, or the second model stops being decorrelated.
func TestPairReviewerStillCannotSeeTheAuthorsReasoning(t *testing.T) {
	rec := &recorder{}
	params := pairParams(rec, "green",
		editsOutcome("I changed it because the operator was inverted"),
		verdictOutcome("approve"),
	)
	if _, err := ExecutePair(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.prompts[1], "because the operator was inverted") {
		t.Error("pair's reviewer was shown the author's reasoning")
	}
}

func TestCouncilSkipsTheHumanTurnWhenUnattended(t *testing.T) {
	rec := &recorder{}
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-001 — Draft\n"},
		verdictOutcome("approve"),
		&agent.Outcome{Text: "## REQ-001 — Draft\n"},
	)
	if _, err := ExecuteScript(context.Background(), CouncilScript("REQ", nil), params); err != nil {
		t.Fatal(err)
	}
	for _, role := range rec.roles {
		if role == config.RoleHuman {
			t.Error("a human turn ran with no human present")
		}
	}
}

// Two rounds at most: an artifact that has not converged after a draft, a
// critique and a revision needs a person, not another lap.
func TestCouncilStopsAtTwoRounds(t *testing.T) {
	rec := &recorder{}
	params := councilParams(rec)
	res, err := ExecuteScript(context.Background(), CouncilScript("REQ", nil), params)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds > 2 {
		t.Errorf("ran %d rounds", res.Rounds)
	}
}

func TestCouncilContractFollowsThePrefix(t *testing.T) {
	for prefix, want := range map[string]string{"REQ": "markdown_sections:REQ", "SPEC": "markdown_sections:SPEC", "M": "markdown_sections:M"} {
		if got := CouncilScript(prefix, nil).Turns[0].Contract; got != want {
			t.Errorf("prefix %q: contract = %q, want %q", prefix, got, want)
		}
	}
}

// A council with three ticked boxes seats three ducklings. For as long as the
// council had exactly two chairs, the third box saved fine and did nothing —
// a person ticked k3, sonnet and luna, and luna watched from the gallery.
func TestCouncilSeatsOneCritiqueTurnPerCritic(t *testing.T) {
	script := CouncilScript("REQ", []config.DucklingID{"pato-local", "pato-luna"})
	var critics []config.DucklingID
	for _, turn := range script.Turns {
		if turn.Role == config.RoleReviewer {
			critics = append(critics, turn.Duckling)
		}
	}
	if len(critics) != 2 || critics[0] != "pato-local" || critics[1] != "pato-luna" {
		t.Fatalf("critique turns pinned to %v, want [pato-local pato-luna]", critics)
	}
	if err := script.Validate(testRegistry(t)); err != nil {
		t.Fatalf("multi-critic council does not validate: %v", err)
	}
	// The line-up order runs in order: drafter first, then each critic, then
	// the revision.
	rec := &recorder{}
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-001 — Draft\n"},
		verdictOutcome("approve"),
		verdictOutcome("approve"),
		&agent.Outcome{Text: "## REQ-001 — Final\n"},
	)
	if _, err := ExecuteScript(context.Background(), script, params); err != nil {
		t.Fatal(err)
	}
	want := []config.DucklingID{"pato-atom", "pato-local", "pato-luna", "pato-atom"}
	if len(rec.ducklings) != len(want) {
		t.Fatalf("%d turns ran with %v, want %v", len(rec.ducklings), rec.ducklings, want)
	}
	for i, d := range want {
		if rec.ducklings[i] != d {
			t.Errorf("turn %d ran on %s, want %s", i, rec.ducklings[i], d)
		}
	}
}

// Each critic reads the draft, not the other critics. A critic shown a fellow
// critic's findings anchors on them, and N critics become one critique read N
// times — which is the decorrelation the extra seats exist for, undone.
func TestCouncilCriticsDoNotSeeEachOther(t *testing.T) {
	rec := &recorder{}
	script := CouncilScript("REQ", []config.DucklingID{"pato-local", "pato-luna"})
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-001 — Draft\n\nBody of the draft."},
		verdictOutcome("request-changes", agent.Finding{
			Severity: "major", File: "requirements.md",
			Issue: "the scope line is missing", Fix: "add one",
		}),
		verdictOutcome("approve"),
		&agent.Outcome{Text: "## REQ-001 — Revised\n"},
	)
	if _, err := ExecuteScript(context.Background(), script, params); err != nil {
		t.Fatal(err)
	}
	second := rec.prompts[2]
	if strings.Contains(second, "the scope line is missing") {
		t.Error("the second critic was shown the first critic's findings")
	}
	if !strings.Contains(second, "Body of the draft.") {
		t.Errorf("the second critic was not shown the draft:\n%s", second)
	}
	// The revision, by contrast, must see every critique — that is its input.
	revision := rec.prompts[3]
	if !strings.Contains(revision, "the scope line is missing") {
		t.Errorf("the revision could not see the first critic's findings:\n%s", revision)
	}
}

// The round's verdict is the WORST across its critics. Folding by overwrite
// meant the last critic to speak decided for everyone: request-changes then
// approve settled the round as approved, and the objection evaporated.
func TestCouncilOneRequestChangesOutvotesTheApprovals(t *testing.T) {
	rec := &recorder{}
	script := CouncilScript("REQ", []config.DucklingID{"pato-local", "pato-luna"})
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-001 — Draft\n"},
		verdictOutcome("request-changes", agent.Finding{
			Severity: "major", File: "requirements.md", Issue: "no scope", Fix: "add one",
		}),
		verdictOutcome("approve"), // the LAST critic approves
		&agent.Outcome{Text: "## REQ-001 — Revised\n"},
	)
	res, err := ExecuteScript(context.Background(), script, params)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 2 {
		t.Errorf("ran %d rounds — an unresolved request-changes must force the second round", res.Rounds)
	}
}

// And the converse: unanimous approval settles the round.
func TestCouncilUnanimousApprovalSettlesTheRound(t *testing.T) {
	rec := &recorder{}
	script := CouncilScript("REQ", []config.DucklingID{"pato-local", "pato-luna"})
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-001 — Draft\n"},
		verdictOutcome("approve"),
		verdictOutcome("approve"),
		&agent.Outcome{Text: "## REQ-001 — Final\n"},
	)
	res, err := ExecuteScript(context.Background(), script, params)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 1 {
		t.Errorf("ran %d rounds on a unanimous approval", res.Rounds)
	}
}

// No critics is the original council: one unpinned reviewer, the roster's own.
func TestCouncilWithNoCriticsKeepsTheOriginalShape(t *testing.T) {
	script := CouncilScript("REQ", nil)
	var reviewers int
	for _, turn := range script.Turns {
		if turn.Role == config.RoleReviewer {
			reviewers++
			if turn.Duckling != "" {
				t.Errorf("the fallback reviewer is pinned to %q; it must come from the roster", turn.Duckling)
			}
		}
	}
	if reviewers != 1 {
		t.Errorf("%d reviewer turns, want 1", reviewers)
	}
}

// The code-review framing sent a real critic hunting for a diff that by design
// does not exist: it called git_diff (empty — a proposal never touches the
// tree), artifact_read (the OLD approved document) and fs_read (no such file),
// and its tools truthfully corroborated "there is no draft anywhere". Three of
// its six turns went to archaeology. The critique turn now presents the draft
// under its own heading with the mechanism spelled out.
func TestACriticIsToldTheDraftLivesInTheConversation(t *testing.T) {
	rec := &recorder{}
	script := CouncilScript("REQ", []config.DucklingID{"pato-local"})
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-016 — Zoom\n\nScroll to zoom."},
		verdictOutcome("approve"),
		&agent.Outcome{Text: "## REQ-016 — Zoom\n"},
	)
	if _, err := ExecuteScript(context.Background(), script, params); err != nil {
		t.Fatal(err)
	}
	critique := rec.prompts[1]
	if !strings.Contains(critique, "The draft under review") {
		t.Errorf("the critique prompt never names the draft as the thing under review:\n%s", critique)
	}
	if !strings.Contains(critique, "do not go looking for it with tools") {
		t.Errorf("the critique prompt does not warn off the tool hunt:\n%s", critique)
	}
	if !strings.Contains(critique, "Scroll to zoom.") {
		t.Errorf("the draft itself is missing:\n%s", critique)
	}
}

// Every critique turn of a council carries the critic persona; a task-mode
// reviewer (pair) keeps the code framing, because there a diff IS the thing
// under review.
func TestOnlyCouncilCritiquesCarryTheCriticPersona(t *testing.T) {
	for _, turn := range CouncilScript("REQ", []config.DucklingID{"a", "b"}).Turns {
		if turn.Role == config.RoleReviewer && turn.Persona != PersonaCritic {
			t.Errorf("council critique turn without the critic persona: %+v", turn)
		}
	}
	for _, turn := range PairScript().Turns {
		if turn.Persona != "" {
			t.Errorf("pair turn carries persona %q", turn.Persona)
		}
	}
}
