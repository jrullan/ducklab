package strategy

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
)

// Later council rounds open on the revision the previous round closed with:
// the architect revises once per rejected round and never re-drafts at the
// top of the next round.
func TestACouncilsSecondRoundOpensOnTheRevision(t *testing.T) {
	architectTurns := 0
	var events []string
	params := &ExecuteParams{
		OnEvent: func(kind string, data map[string]interface{}) { events = append(events, kind) },
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, _ string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			if turn.Role == config.RoleReviewer {
				return verdictOutcome("request-changes"), nil
			}
			architectTurns++
			return &agent.Outcome{Text: "## REQ-001 — Draft\n\nBody v" + string(rune('0'+architectTurns)), Parsed: []agent.Section{{ID: "REQ-001", Title: "Draft", Body: "Body"}}}, nil
		},
		Roster: map[config.Role]config.DucklingID{config.RoleArchitect: "arch", config.RoleReviewer: "crit"},
	}
	if _, err := ExecuteScript(context.Background(), CouncilScript("REQ", nil), params); err != nil {
		t.Fatal(err)
	}
	if architectTurns != 4 {
		t.Fatalf("architect turns = %d, want 4 (draft plus three revisions) — not a re-draft at the top of a later round", architectTurns)
	}
	carried := false
	for _, e := range events {
		if e == "draft_carried" {
			carried = true
		}
	}
	if !carried {
		t.Fatalf("no draft_carried event; events = %v", events)
	}
}

// A fragment revision returns only the sections it changes. The next critic
// must judge the accumulated amendment, not mistake that latest patch for the
// whole candidate (Neocapture corrida 9: REQ-006 and REQ-009 looked deleted).
func TestAFragmentCouncilCarriesTheMaterializedCandidate(t *testing.T) {
	script := CouncilScript("REQ", nil)
	script.FragmentPrefix = "REQ"
	for i := range script.Turns {
		if script.Turns[i].Role == config.RoleArchitect {
			script.Turns[i].Contract = ""
		}
	}
	architectTurns, reviewerTurns := 0, 0
	params := &ExecuteParams{
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, prompt string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			if turn.Role == config.RoleReviewer {
				reviewerTurns++
				if reviewerTurns == 2 {
					for _, want := range []string{"REQ-001", "REQ-006", "REQ-009"} {
						if !strings.Contains(prompt, want) {
							t.Errorf("round-2 critic lost %s:\n%s", want, prompt)
						}
					}
					return verdictOutcome("approve"), nil
				}
				return verdictOutcome("request-changes"), nil
			}
			architectTurns++
			switch architectTurns {
			case 1:
				return &agent.Outcome{Text: "## REQ-001 — Trigger\n\nOld.\n\n## REQ-006 — Keyboard\n\nOut.\n\n## REQ-009 — Saving\n\nRequired."}, nil
			default:
				return &agent.Outcome{Text: "## REQ-001 — User action\n\nRevised."}, nil
			}
		},
		Roster: map[config.Role]config.DucklingID{config.RoleArchitect: "arch", config.RoleReviewer: "crit"},
	}
	res, err := ExecuteScript(context.Background(), script, params)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"REQ-001 — User action", "REQ-006 — Keyboard", "REQ-009 — Saving"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("final materialized candidate lost %q:\n%s", want, res.Text)
		}
	}
}

// New fragment sections deliberately share the -900 wire placeholder. The
// scheduler must give them stable temporary ids before review and must not
// append them again when a later repair uses sequential placeholder ids.
func TestAFragmentCouncilCanonicalizesNewSectionsAcrossRounds(t *testing.T) {
	script := CouncilScript("SPEC", nil)
	script.FragmentPrefix = "SPEC"
	for i := range script.Turns {
		if script.Turns[i].Role == config.RoleArchitect {
			script.Turns[i].Contract = ""
		}
	}
	architectTurns, reviewerTurns := 0, 0
	params := &ExecuteParams{
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, prompt string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			if turn.Role == config.RoleReviewer {
				reviewerTurns++
				if reviewerTurns == 1 {
					for _, want := range []string{"## SPEC-900 — Capture", "## SPEC-901 — Save", "authoritative"} {
						if !strings.Contains(prompt, want) {
							t.Errorf("first critic lacks %q:\n%s", want, prompt)
						}
					}
					return verdictOutcome("request-changes"), nil
				}
				authoritative := prompt[strings.LastIndex(prompt, "## Materialized fragment candidate — authoritative"):]
				if strings.Count(authoritative, "## SPEC-900 — Capture") != 1 || strings.Count(authoritative, "## SPEC-901 — Save") != 1 {
					t.Errorf("second critic received duplicated authoritative additions:\n%s", authoritative)
				}
				return verdictOutcome("approve"), nil
			}
			architectTurns++
			if architectTurns == 1 {
				return &agent.Outcome{Text: "## SPEC-900 — Capture\n\nold\n\n## SPEC-900 — Save\n\nold\n"}, nil
			}
			return &agent.Outcome{Text: "## SPEC-900 — Capture\n\nfixed\n\n## SPEC-901 — Save\n\nfixed\n"}, nil
		},
		Roster: map[config.Role]config.DucklingID{config.RoleArchitect: "arch", config.RoleReviewer: "crit"},
	}
	res, err := ExecuteScript(context.Background(), script, params)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(res.Text, "## SPEC-") != 2 || !strings.Contains(res.Text, "## SPEC-901 — Save") {
		t.Fatalf("materialized result duplicated or lost sections:\n%s", res.Text)
	}
}

// The candidate a person accepts is the closing architect revision, not the
// draft the last round critic saw immediately before it. Record one bounded
// verdict on that exact text without opening an unbounded repair lap.
func TestCouncilFinallyReviewsTheLastRevision(t *testing.T) {
	script := CouncilScript("REQ", nil)
	architectTurns, reviewerTurns := 0, 0
	var finalPrompt string
	params := &ExecuteParams{
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, prompt string, _ []string, tc TurnContext) (*agent.Outcome, error) {
			if turn.Role == config.RoleReviewer {
				reviewerTurns++
				if tc.Index >= len(script.Turns) {
					finalPrompt = prompt
					return verdictOutcome("approve"), nil
				}
				return verdictOutcome("request-changes", agent.Finding{
					Severity: "major", File: "requirements.md", Issue: "REQ-011 still contradicts saving", Fix: "delete REQ-011",
				}), nil
			}
			architectTurns++
			return &agent.Outcome{Text: "## REQ-001 — Candidate\n\nRevision " + string(rune('0'+architectTurns)) + ".", Parsed: []agent.Section{{ID: "REQ-001", Title: "Candidate"}}}, nil
		},
		Roster: map[config.Role]config.DucklingID{config.RoleArchitect: "arch", config.RoleReviewer: "crit"},
	}
	res, err := ExecuteScript(context.Background(), script, params)
	if err != nil {
		t.Fatal(err)
	}
	if reviewerTurns != 4 {
		t.Fatalf("reviewer turns = %d, want three round reviews plus final verification", reviewerTurns)
	}
	if !strings.Contains(finalPrompt, "Revision 4.") || !strings.Contains(finalPrompt, "verification only") {
		t.Errorf("final reviewer did not receive the closing revision:\n%s", finalPrompt)
	}
	if !strings.Contains(finalPrompt, "Open finding ledger") || !strings.Contains(finalPrompt, "REQ-011 still contradicts saving") ||
		!strings.Contains(finalPrompt, "Re-check EACH ledger item") {
		t.Errorf("final reviewer did not receive the open finding ledger:\n%s", finalPrompt)
	}
	if res.State.Verdict != "approve" {
		t.Errorf("final verdict = %q, want approve", res.State.Verdict)
	}
}

// The final reviewer and the proposal gate must share one materialized body.
// In Neocapture corrida 15 the reviewer saw SPEC-005 before SPEC-004, while
// stage id assignment later renumbered the persisted proposal into the
// opposite order and left a red verdict attached to a defect no longer shown.
func TestCouncilMaterializesCandidateBeforeFinalReview(t *testing.T) {
	script := CouncilScript("REQ", nil)
	const canonical = "## REQ-001 — Candidate\n\ncanonical body\n"
	script.MaterializeCandidate = func(_ []string, candidate *agent.Outcome) (*agent.Outcome, error) {
		out := *candidate
		out.Text = canonical
		return &out, nil
	}
	reviewerTurns := 0
	architectTurns := 0
	var finalPrompt string
	params := &ExecuteParams{
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, prompt string, _ []string, tc TurnContext) (*agent.Outcome, error) {
			if turn.Role == config.RoleReviewer {
				reviewerTurns++
				if tc.Index >= len(script.Turns) {
					finalPrompt = prompt
					return verdictOutcome("approve"), nil
				}
				return verdictOutcome("request-changes"), nil
			}
			architectTurns++
			body := "uncanonical body " + string(rune('0'+architectTurns))
			return &agent.Outcome{Text: "## REQ-001 — Candidate\n\n" + body + "\n", Parsed: []agent.Section{{ID: "REQ-001", Title: "Candidate", Body: body}}}, nil
		},
		Roster: map[config.Role]config.DucklingID{config.RoleArchitect: "arch", config.RoleReviewer: "crit"},
	}
	res, err := ExecuteScript(context.Background(), script, params)
	if err != nil {
		t.Fatal(err)
	}
	finalCandidate := finalPrompt[strings.LastIndex(finalPrompt, "## Final candidate under review"):]
	if !strings.Contains(finalCandidate, canonical) || strings.Contains(finalCandidate, "uncanonical body") {
		t.Fatalf("final reviewer did not receive the materialized candidate (reviewers=%d rounds=%d verdict=%s):\n%s", reviewerTurns, res.Rounds, res.State.Verdict, finalPrompt)
	}
	if strings.Contains(finalPrompt, "uncanonical body") {
		t.Fatalf("final reviewer also received superseded architect drafts:\n%s", finalPrompt)
	}
	if res.Text != canonical || res.CandidateDigest != documentCandidateDigest(canonical) {
		t.Fatalf("result text/digest = %q/%q", res.Text, res.CandidateDigest)
	}
}

func TestCouncilMaterializesCandidateBeforeFindingFreeFirstReview(t *testing.T) {
	script := CouncilScript("REQ", nil)
	const canonical = "## REQ-001 — Candidate\n\ncanonical body\n"
	script.MaterializeCandidate = func(_ []string, candidate *agent.Outcome) (*agent.Outcome, error) {
		out := *candidate
		out.Text = canonical
		return &out, nil
	}
	var reviewPrompt string
	params := &ExecuteParams{
		Runner: func(_ context.Context, turn *Turn, _ config.DucklingID, prompt string, _ []string, _ TurnContext) (*agent.Outcome, error) {
			if turn.Role == config.RoleReviewer {
				reviewPrompt = prompt
				return verdictOutcome("approve"), nil
			}
			return &agent.Outcome{Text: "## REQ-001 — Candidate\n\nraw body\n", Parsed: []agent.Section{{ID: "REQ-001", Title: "Candidate", Body: "raw body"}}}, nil
		},
		Roster: map[config.Role]config.DucklingID{config.RoleArchitect: "arch", config.RoleReviewer: "crit"},
	}
	res, err := ExecuteScript(context.Background(), script, params)
	if err != nil {
		t.Fatal(err)
	}
	authoritative := reviewPrompt[strings.LastIndex(reviewPrompt, "## Materialized candidate — authoritative"):]
	if !strings.Contains(authoritative, canonical) || strings.Contains(authoritative, "raw body") {
		t.Fatalf("first reviewer did not receive the materialized candidate:\n%s", reviewPrompt)
	}
	if strings.Contains(reviewPrompt, "### Turn 1 — architect") {
		t.Fatalf("reviewer also received the superseded architect wire response:\n%s", reviewPrompt)
	}
	if res.Text != canonical || res.CandidateDigest != documentCandidateDigest(canonical) {
		t.Fatalf("fast-path result text/digest = %q/%q", res.Text, res.CandidateDigest)
	}
}
