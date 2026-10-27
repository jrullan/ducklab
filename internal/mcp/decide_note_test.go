package mcp

import (
	"strings"
	"testing"
)

// B-524: every decide action × the gates it answers. A note is carried only
// by resume; on every other action it is refused before the engine is asked
// to decide anything, with a message naming the door that does carry the
// words. The required reason reaches the engine on every action — it was
// dropped without a word on resume and abort.
func TestEveryDecideActionAppliesOrRefusesItsWords(t *testing.T) {
	const reason = "the person wants no jsdom"
	const note = "build without jsdom"
	cases := []struct {
		action, stage string
		// refusal must appear in the error when a note rides this action;
		// empty means the note is carried.
		refusal string
		// reached reports what the engine received of the reason (and, for
		// resume, of the note).
		reached func(f *fakeEngine) bool
	}{
		{"accept", "build", "edit the bug's proposal with bug_edit before bug_promote",
			func(f *fakeEngine) bool { return f.acceptedMsg == reason }},
		{"accept", "triage", "edit the bug's proposal with bug_edit before bug_promote",
			func(f *fakeEngine) bool { return f.acceptedMsg == reason }},
		{"accept", "spec", "use request_changes with the change as its reason",
			func(f *fakeEngine) bool { return f.acceptedMsg == reason }},
		{"reject", "build", "it is recorded with the reject",
			func(f *fakeEngine) bool { return len(f.rejected) == 1 && f.rejected[0] == "r-x: "+reason && f.rejectedAs == "mcp:elena" }},
		{"reject", "triage", "it is recorded with the reject",
			func(f *fakeEngine) bool { return len(f.rejected) == 1 && f.rejected[0] == "r-x: "+reason && f.rejectedAs == "mcp:elena" }},
		{"request_changes", "plan", "Its reason IS the change sent to the next draft",
			func(f *fakeEngine) bool { return f.lastStageReq != nil && f.lastStageReq["revise"] == reason }},
		{"request_changes", "release", "Its reason IS the change sent to the next draft",
			func(f *fakeEngine) bool { return f.lastReleaseRevise == reason }},
		{"abort", "build", "it is recorded with the abort",
			func(f *fakeEngine) bool { return len(f.aborted) == 1 && f.aborted[0] == "r-x: "+reason+" (mcp:elena)" }},
		{"abort", "triage", "it is recorded with the abort",
			func(f *fakeEngine) bool { return len(f.aborted) == 1 && f.aborted[0] == "r-x: "+reason+" (mcp:elena)" }},
		{"resume", "build", "",
			func(f *fakeEngine) bool { return f.resumeReason == reason && f.resumeActor == "mcp:elena" }},
		{"resume", "test", "",
			func(f *fakeEngine) bool { return f.resumeReason == reason && f.resumeActor == "mcp:elena" }},
	}
	for _, tc := range cases {
		for _, withNote := range []bool{false, true} {
			name := tc.action + "/" + tc.stage
			if withNote {
				name += "/note"
			}
			t.Run(name, func(t *testing.T) {
				eng := &fakeEngine{budgetLifted: "n/a", runs: map[string]map[string]interface{}{
					"r-x": {"id": "r-x", "status": "paused", "stage": tc.stage, "project_id": "p",
						"next": []interface{}{tc.action}},
				}}
				args := `{"run_id":"r-x","action":"` + tc.action + `","reason":"` + reason + `"`
				if withNote {
					args += `,"note":"` + note + `"`
				}
				resps := drive(t, eng,
					`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"elena"}}}`,
					callFrame(2, "decide", args+"}"))
				text, isErr := toolResultText(t, resps[1])
				if withNote && tc.refusal != "" {
					if !isErr || !strings.Contains(text, "only resume carries a note") || !strings.Contains(text, tc.refusal) {
						t.Fatalf("a note on %s was not refused with its door: %q", tc.action, text)
					}
					if tc.reached(eng) || len(eng.accepted) != 0 || eng.resumeCount != 0 {
						t.Fatalf("the engine was asked to %s although the note would be dropped", tc.action)
					}
					return
				}
				if isErr {
					t.Fatalf("%s failed: %q", tc.action, text)
				}
				if !tc.reached(eng) {
					t.Fatalf("%s: the reason did not reach the engine (%+v)", tc.action, eng)
				}
				if withNote && eng.resumeNote != note {
					t.Fatalf("resume note reached the engine as %q", eng.resumeNote)
				}
				if tc.action == "accept" {
					// The reply says the reason changed nothing — the claim
					// "accepted with your adjustment" has nothing to stand on.
					if !strings.Contains(text, "changed nothing") {
						t.Errorf("accept reply does not say what the reason did: %q", text)
					}
					if tc.stage == "triage" && !strings.Contains(text, "bug_edit") {
						t.Errorf("triage accept reply does not name bug_edit: %q", text)
					}
				}
			})
		}
	}
}

// The schema says the same thing the refusals do, so a model reading only
// the tool list is not told a note can ride an accept.
func TestDecideSchemaSaysTheReasonChangesNothing(t *testing.T) {
	resps := drive(t, &fakeEngine{}, initFrame, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools, _ := resps[1]["result"].(map[string]interface{})["tools"].([]interface{})
	for _, tl := range tools {
		tool := tl.(map[string]interface{})
		if tool["name"] != "decide" {
			continue
		}
		desc, _ := tool["description"].(string)
		props := tool["inputSchema"].(map[string]interface{})["properties"].(map[string]interface{})
		noteDesc, _ := props["note"].(map[string]interface{})["description"].(string)
		reasonDesc, _ := props["reason"].(map[string]interface{})["description"].(string)
		if !strings.Contains(desc, "bug_edit") || !strings.Contains(desc, "exactly as shown") {
			t.Errorf("decide description does not say accept takes the proposal as shown: %q", desc)
		}
		if !strings.Contains(noteDesc, "Refused on every other action") {
			t.Errorf("note description: %q", noteDesc)
		}
		if !strings.Contains(reasonDesc, "changes nothing") {
			t.Errorf("reason description: %q", reasonDesc)
		}
		return
	}
	t.Fatal("no decide tool")
}

// B-529: an accept whose gate replay outlasted the engine's answer comes back
// in_progress. ti36xpro's operator read the cut-off accepts as intermittent
// and retried seven times; decide must say nothing was decided yet, that a
// retry starts nothing, and where the outcome appears — and must not claim
// "accepted exactly as proposed".
func TestDecideAcceptInProgressSaysWaitAndWhere(t *testing.T) {
	eng := &fakeEngine{
		acceptResult: map[string]interface{}{"commit_sha": "", "in_progress": true, "info": "accept in progress"},
		runs: map[string]map[string]interface{}{
			"r-x": {"id": "r-x", "status": "paused", "stage": "build", "project_id": "p", "next": []interface{}{"accept"}},
		},
	}
	resps := drive(t, eng,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"elena"}}}`,
		callFrame(2, "decide", `{"run_id":"r-x","action":"accept","reason":"suite passes"}`))
	text, isErr := toolResultText(t, resps[1])
	if isErr {
		t.Fatalf("an accept in progress was reported as an error: %q", text)
	}
	for _, want := range []string{"NOT YET DECIDED", "Do not call decide again", "run_get", "accept_in_progress", "last_accept"} {
		if !strings.Contains(text, want) {
			t.Errorf("decide's in-progress reply lacks %q: %s", want, text)
		}
	}
	if strings.Contains(text, "accepted exactly as proposed") {
		t.Errorf("decide claimed an accept that has not happened: %s", text)
	}
}
