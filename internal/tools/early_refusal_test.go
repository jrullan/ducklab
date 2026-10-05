package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// B-499: every refusal Execute returns before the tool runs counts toward the
// identical-failure brake. TI-36X T-005 (r-20261004-212715-5xxh, round 2):
// atom-local sent fs_read {} 22 times in a row, each refused "needs a path"
// before the brake could see it, and the reply's 24-call cap was spent.

// repeatUntilClosed sends the same call up to max times and returns the
// results and the 1-based call that closed tool use (0 if none did).
func repeatUntilClosed(t *testing.T, reg *Registry, ectx *ExecContext, tool, args string, max int) ([]*Result, int) {
	t.Helper()
	var results []*Result
	for i := 1; i <= max; i++ {
		res, err := reg.Execute(context.Background(), ectx, tool, json.RawMessage(args))
		if err != nil {
			t.Fatalf("%s %s call %d: %v", tool, args, i, err)
		}
		results = append(results, res)
		if res.EndTurn {
			return results, i
		}
	}
	return results, 0
}

// assertClosedByRepeatBrake checks that the identical refused call closed
// tool use at RepeatFailEndTurn, with the first RepeatFailLimit calls still
// carrying the refusal's own words, and that tools stay closed after it.
func assertClosedByRepeatBrake(t *testing.T, reg *Registry, ectx *ExecContext, tool, args, refusal string) {
	t.Helper()
	results, closedAt := repeatUntilClosed(t, reg, ectx, tool, args, 22)
	if closedAt != RepeatFailEndTurn {
		t.Fatalf("%s %s: tool use closed at call %d, want %d; last: %+v", tool, args, closedAt, RepeatFailEndTurn, results[len(results)-1])
	}
	for i := 0; i < RepeatFailLimit; i++ {
		if !results[i].IsError || results[i].EndTurn || !strings.Contains(results[i].Content, refusal) {
			t.Fatalf("%s %s call %d should be the refusal %q itself: %+v", tool, args, i+1, refusal, results[i])
		}
	}
	for i := RepeatFailLimit; i < RepeatFailEndTurn-1; i++ {
		if !strings.Contains(results[i].Content, "CHANGE the arguments") || results[i].EndTurn {
			t.Fatalf("%s %s call %d should be refused with orders to change: %+v", tool, args, i+1, results[i])
		}
	}
	last := results[closedAt-1]
	if !last.IsError || !ectx.ToolsClosed || !strings.Contains(last.Content, "tool use is now CLOSED for this reply") {
		t.Fatalf("%s %s: the closing result does not close: %+v", tool, args, last)
	}
	if ectx.ToolAvailable("fs_write") {
		t.Fatalf("%s %s: tools stayed available after the close", tool, args)
	}
}

// The real case: 22 identical fs_read {} calls close tool use at the sixth,
// leaving the reply room to answer.
func TestTwentyTwoReadsWithNoPathCloseToolUse(t *testing.T) {
	reg, ectx, _ := spellingProject(t)
	assertClosedByRepeatBrake(t, reg, ectx, "fs_read", `{}`, `needs a "path"`)
}

// Every file tool that needs a path: the missing-path refusal is counted.
func TestEveryMissingPathRefusalCountsTowardTheBrake(t *testing.T) {
	for _, call := range []struct{ tool, args string }{
		{"fs_write", `{"content": "x"}`},
		{"fs_write_lines", `{"start": 1, "end": 2, "content": "x"}`},
		{"fs_patch", `{"edits": [{"search": "old", "replace": "new"}]}`},
		{"fs_delete", `{"path": "   "}`},
	} {
		t.Run(call.tool, func(t *testing.T) {
			reg, ectx, _ := spellingProject(t)
			assertClosedByRepeatBrake(t, reg, ectx, call.tool, call.args, `needs a "path"`)
		})
	}
}

// Past the research boundary, missing paths are counted too (the check runs
// before the boundary, B-497).
func TestMissingPathsPastTheBoundaryCountTowardTheBrake(t *testing.T) {
	reg, ectx, _ := researchBoundaryReached(t)
	assertClosedByRepeatBrake(t, reg, ectx, "fs_read", `{}`, `needs a "path"`)
}

// An unknown tool name refused six times closes tool use.
func TestRepeatedUnknownToolClosesToolUse(t *testing.T) {
	reg, ectx, _ := spellingProject(t)
	assertClosedByRepeatBrake(t, reg, ectx, "fs_raed", `{"path":"logic.mjs"}`, `unknown tool "fs_raed"`)
}

// Once tool use is closed, an unknown tool name is told so too, instead of
// "unknown tool" with the turn left open.
func TestAnUnknownToolAfterTheCloseIsToldToolsAreClosed(t *testing.T) {
	reg, ectx, _ := spellingProject(t)
	ectx.ToolsClosed = true
	res, _ := reg.Execute(context.Background(), ectx, "fs_raed", json.RawMessage(`{}`))
	if !res.EndTurn || !strings.Contains(res.Content, "CLOSED") {
		t.Fatalf("an unknown tool after the close did not end the turn: %+v", res)
	}
}

// The jail pre-check past the research boundary (B-492) does not count as a
// research refusal, but it is a failure: repeating the same bad path closes
// tool use at the identical-failure limit.
func TestRepeatedBadPathPastTheBoundaryClosesToolUse(t *testing.T) {
	reg, ectx, _ := researchBoundaryReached(t)
	assertClosedByRepeatBrake(t, reg, ectx, "fs_read", `{"path":"../outside.txt"}`, "path escapes root")
	if ectx.researchRefusals != 0 {
		t.Fatalf("path errors were counted as research refusals: %d", ectx.researchRefusals)
	}
}

// Research boundary refusals are recorded as failures of their call, while
// the boundary's own brake (ResearchRefusalLimit, 5) still closes first and
// keeps its wording (TestRepeatedReadsAfterTheResearchBoundaryCloseTools).
func TestResearchRefusalsAreRecordedAsFailures(t *testing.T) {
	reg, ectx, _ := researchBoundaryReached(t)
	for i := 1; i <= 3; i++ {
		res, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"a.txt"}`))
		if !strings.Contains(res.Content, "RESEARCH BUDGET EXHAUSTED") {
			t.Fatalf("refusal %d: %+v", i, res)
		}
		if ectx.lastFailSig != "fs_read\x00"+`{"path":"a.txt"}` || ectx.lastFailCount != i {
			t.Fatalf("refusal %d was not recorded: sig %q count %d", i, ectx.lastFailSig, ectx.lastFailCount)
		}
	}
}

// fs_patch's per-file refusal said "end your reply" and never closed: the
// same refused patch is now closed by the identical-failure brake.
func TestRepeatedRefusedPatchClosesToolUse(t *testing.T) {
	reg, ectx, _ := spellingProject(t)
	ectx.fsPatchFailStreak = map[string]int{"logic.mjs": FSPatchFailLimit}
	assertClosedByRepeatBrake(t, reg, ectx, "fs_patch",
		`{"path":"logic.mjs","edits":[{"search":"old","replace":"new"}]}`, "REFUSED: fs_patch has failed")
}

// fs_read of a project document is redirected to artifact_read; the same
// redirected read repeated closes tool use.
func TestRepeatedDocumentReadClosesToolUse(t *testing.T) {
	reg, ectx, root := spellingProject(t)
	if err := os.MkdirAll(filepath.Join(root, ".ducklab", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ducklab", "docs", "plan.md"), []byte("# Plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertClosedByRepeatBrake(t, reg, ectx, "fs_read", `{"path":".ducklab/docs/plan.md"}`, "is a project document")
}

// REPEATED READ refusals are recorded as failures of their call. The
// sequence still ends at the research brake (the third identical read closes
// reads, and the boundary closes everything at its fifth refusal), which is
// sooner than RepeatFailEndTurn for one call, so the record is checked here.
func TestRepeatedReadRefusalsAreRecordedAsFailures(t *testing.T) {
	reg, ectx, _ := spellingProject(t)
	sig := "fs_read\x00" + `{"path":"logic.mjs"}`
	results, closedAt := repeatUntilClosed(t, reg, ectx, "fs_read", `{"path":"logic.mjs"}`, 22)
	if !strings.Contains(results[1].Content, "REPEATED READ") {
		t.Fatalf("the second read was not refused: %+v", results[1])
	}
	if !strings.Contains(results[3].Content, "read-only tools are now CLOSED") {
		t.Fatalf("the fourth read did not close reads: %+v", results[3])
	}
	if closedAt == 0 || closedAt > 4+ResearchRefusalLimit {
		t.Fatalf("identical reads never closed tool use (closed at %d)", closedAt)
	}
	// Replay to the refusals and check the record after each.
	ectx.BeginTurn()
	reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"logic.mjs"}`))
	reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"logic.mjs"}`))
	if ectx.lastFailSig != sig || ectx.lastFailCount != 1 {
		t.Fatalf("REPEATED READ was not recorded: %q %d", ectx.lastFailSig, ectx.lastFailCount)
	}
	reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"logic.mjs"}`)) // served again
	reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"logic.mjs"}`))
	if ectx.lastFailSig != sig || ectx.lastFailCount != 1 {
		t.Fatalf("the read-closing refusal was not recorded: %q %d", ectx.lastFailSig, ectx.lastFailCount)
	}
}

// STOP SEARCHING is recorded as a failure of its call. It resets the miss
// count, so the next identical search runs (and its "no matches" is a
// success), which is why it cannot close by itself; the record is checked.
func TestStopSearchingIsRecordedAsAFailure(t *testing.T) {
	reg, ectx, _ := spellingProject(t)
	reg.Register(&FSSearch{})
	ectx.searchMisses = SearchMissLimit
	args := `{"pattern":"nothing-here-xyz"}`
	res, _ := reg.Execute(context.Background(), ectx, "fs_search", json.RawMessage(args))
	if !strings.Contains(res.Content, "STOP SEARCHING") {
		t.Fatalf("expected the search refusal: %+v", res)
	}
	if ectx.lastFailSig != "fs_search\x00"+args || ectx.lastFailCount != 1 {
		t.Fatalf("STOP SEARCHING was not recorded: %q %d", ectx.lastFailSig, ectx.lastFailCount)
	}
}

// A changed call still resets the count: three refused fs_read {} then a
// good read, then fs_read {} again starts from one.
func TestAGoodCallBetweenEarlyRefusalsResetsTheCount(t *testing.T) {
	reg, ectx, _ := spellingProject(t)
	for i := 0; i < RepeatFailLimit; i++ {
		reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{}`))
	}
	if res, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"logic.mjs"}`)); res.IsError {
		t.Fatalf("good read refused: %+v", res)
	}
	res, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{}`))
	if !strings.Contains(res.Content, `needs a "path"`) || ectx.lastFailCount != 1 {
		t.Fatalf("the count did not restart after a good call: %+v (count %d)", res, ectx.lastFailCount)
	}
}
