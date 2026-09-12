package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The consultant's whole point is "why didn't T-097 pass?" — and it was
// blind to exactly that: run records live under .ducklab, which the fs
// denylist rightly protects. run_list and run_read are the front doors.
func TestRunHistoryTools(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".ducklab", "runs", "r-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"id":"r-1","stage":"test","task_id":"T-097","status":"done","verdict":"FAILED","started_at":"2026-08-11T22:00:00Z","failure":"the gate is still green"}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	events := strings.Join([]string{
		`{"type":"turn_start","data":{"round":1,"turn":0,"role":"implementer","duckling":"luna"}}`,
		`{"type":"message","data":{"round":1,"role":"reviewer","verdict":"request-changes","findings":[{"severity":"major","issue":"weak assertion"}]}}`,
		`{"type":"round_gate","data":{"round":1,"result":"red"}}`,
		`{"type":"gate","data":{"exit":1,"cmd":"go test ./..."}}`,
		`{"type":"run_end","data":{"verdict":"FAILED"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	ectx := &ExecContext{ProjectRoot: root}

	list := &RunListTool{}
	res, err := list.Execute(context.Background(), ectx, json.RawMessage(`{"task":"T-097"}`))
	if err != nil || res.IsError {
		t.Fatalf("run_list: %v %+v", err, res)
	}
	if !strings.Contains(res.Content, "r-1") || !strings.Contains(res.Content, "FAILED") {
		t.Errorf("list missing the run: %q", res.Content)
	}

	read := &RunReadTool{}
	res, err = read.Execute(context.Background(), ectx, json.RawMessage(`{"id":"r-1"}`))
	if err != nil || res.IsError {
		t.Fatalf("run_read: %v %+v", err, res)
	}
	for _, must := range []string{"still green", "request-changes", "weak assertion", "gate: red", "go test ./...", "luna"} {
		if !strings.Contains(res.Content, must) {
			t.Errorf("run_read lost %q:\n%s", must, res.Content)
		}
	}

	// A path in the id must not escape the record directory.
	res, _ = read.Execute(context.Background(), ectx, json.RawMessage(`{"id":"../../secret"}`))
	if !res.IsError {
		t.Error("a path-shaped id was accepted")
	}
}

func TestRunSummaryForPromptKeepsTheHeaderAndRecentEvidence(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".ducklab", "runs", "r-long")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	failure := strings.Repeat("compiler context that is no longer decisive\n", 300) + "the final compiler error"
	state, err := json.Marshal(map[string]interface{}{"id": "r-long", "stage": "plan", "status": "failed", "failure": failure})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), state, 0o644); err != nil {
		t.Fatal(err)
	}
	var events strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&events, `{"type":"error","data":{"error":"error-%02d %s"}}`+"\n", i, strings.Repeat("detail ", 12))
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(events.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, err := ReadRunSummaryForPrompt(root, "r-long", 900)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"r-long", "the final compiler error", "earlier bytes omitted", "earlier timeline entries omitted", "error-59"} {
		if !strings.Contains(summary, want) {
			t.Errorf("bounded summary lost %q:\n%s", want, summary)
		}
	}
	if strings.Contains(summary, "error-00") {
		t.Errorf("bounded summary retained stale evidence:\n%s", summary)
	}
	if len(summary) > 900 {
		t.Errorf("bounded summary = %d bytes, want at most 900", len(summary))
	}
}

func TestRunSummaryPromptCutKeepsAVerdictWithItsFindings(t *testing.T) {
	verdict := "- R2 reviewer verdict: request-changes (5 findings)\n" +
		"    - [major] one\n    - [major] two\n    - [major] three\n    - [major] four\n    - [major] five"
	entries := []string{verdict, "- gate exit 1: cargo test", "- ended: FAILED"}
	timeline := strings.Join(entries, "\n") + "\n"
	got := splitRunTimelineEntries(timeline)
	if len(got) != 3 || got[0] != verdict {
		t.Fatalf("timeline entries = %#v, want verdict and findings as one entry", got)
	}
}

func TestRunTimelineSummarizesToolProtocolInsteadOfQuotingItAsSpeech(t *testing.T) {
	cases := []struct {
		name    string
		content string
		tool    string
	}{
		{
			name:    "dsml",
			content: `<｜｜DSML｜｜ calls><｜｜DSML｜｜ invoke name="fs_read"><｜｜DSML｜｜ parameter name="path">src/main.go</｜｜DSML｜｜ parameter></｜｜DSML｜｜ invoke></｜｜DSML｜｜ calls>`,
			tool:    "fs_read",
		},
		{
			name:    "xml",
			content: `<tool_call>{"name":"verify_run","arguments":{}}</tool_call>`,
			tool:    "verify_run",
		},
		{
			name:    "ducklab dialect",
			content: "```ducklab\n{\"tool\":\"git_diff\",\"args\":{}}\n```",
			tool:    "git_diff",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runTimelineEntry("message", map[string]interface{}{
				"round":   1,
				"role":    "reviewer",
				"content": tc.content,
			})
			if strings.Contains(got, "said:") || strings.Contains(got, tc.content) {
				t.Fatalf("tool protocol was quoted as speech: %q", got)
			}
			for _, want := range []string{"reviewer emitted tool protocol instead of prose", tc.tool} {
				if !strings.Contains(got, want) {
					t.Errorf("summary lost %q: %q", want, got)
				}
			}
		})
	}
}
