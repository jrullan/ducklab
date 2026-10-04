package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/tools"
)

// Review of #145: the tool acted on tests/parser.test.mjs, so the record must
// say so. The run's written-path list, its restore scope and the retry
// evidence are read from these records; "/tests/parser.test.mjs" matched none
// of them.
func TestARecordedCallNamesTheCanonicalProjectPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tests", "parser.test.mjs"), []byte("expect 0.015625\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &countingProvider{replies: []string{
		"```ducklab\n{\"tool\":\"fs_write\",\"args\":{\"path\":\"/tests/parser.test.mjs\",\"content\":\"expect 0.001953125\\n\"}}\n```",
		"Fixed the expected value.",
	}}
	loop := testLoop(p, 0)
	loop.Registry.Register(&tools.FSWrite{})
	turn := &Turn{Role: config.RoleImplementer, Prompt: "fix the test", Contract: "freeform", Toolbelt: []string{"fs_write"}, MaxTurns: 3}
	out, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: dir, Role: config.RoleImplementer})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 1 || out.ToolCalls[0].Result.IsError {
		t.Fatalf("write did not run: %+v", out.ToolCalls)
	}
	args := string(out.ToolCalls[0].Args)
	if !strings.Contains(args, `"path":"tests/parser.test.mjs"`) || strings.Contains(args, `"/tests/`) {
		t.Errorf("record args = %s, want the canonical project path", args)
	}
}

// The native-tools path (atom-local's) records the canonical path too.
func TestANativeRecordedCallNamesTheCanonicalProjectPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := provider.NewFake("f")
	var call int
	fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
		call++
		if call == 1 {
			tc := provider.ToolCall{ID: "c1", Type: "function"}
			tc.Function.Name = "fs_write"
			tc.Function.Arguments = `{"path":"/tests/parser.test.mjs","content":"expect 0.001953125\n"}`
			return &provider.ChatResponse{Choices: []provider.Choice{{
				Message: provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{tc}}, FinishReason: provider.FinishToolCalls,
			}}}
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{
			Message: provider.Message{Role: "assistant", Content: "Fixed."}, FinishReason: provider.FinishStop,
		}}}
	}
	loop := &Loop{
		Provider: fake,
		Duckling: &DucklingConfig{ID: "pato", Model: "m", Caps: provider.Capabilities{NativeTools: true}},
		Registry: tools.NewRegistry(),
		Budget:   budget.NewTracker(&budget.Budget{MaxUSD: 10, MaxTokens: 1e6, MaxTurns: 50, MaxWallclockS: 600}),
		MaxTurns: 4,
	}
	loop.Registry.Register(&tools.FSWrite{})
	turn := &Turn{Role: config.RoleImplementer, Prompt: "fix the test", Toolbelt: []string{"fs_write"}, Contract: "freeform", MaxTurns: 4}
	out, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: dir, Role: config.RoleImplementer})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 1 || out.ToolCalls[0].Result.IsError {
		t.Fatalf("write did not run: %+v", out.ToolCalls)
	}
	if args := string(out.ToolCalls[0].Args); !strings.Contains(args, `"path":"tests/parser.test.mjs"`) {
		t.Errorf("native record args = %s, want the canonical project path", args)
	}
}

// Review of #145: the canonical path was computed after execution, and a
// delete removes the file that lets "/obsolete.txt" be read as a project
// path. The record kept "/obsolete.txt", so the run's written paths, which
// the restore uses to put the file back, never named it.
func TestADeletedTopLevelFileIsRecordedByItsProjectPath(t *testing.T) {
	for _, native := range []bool{false, true} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "obsolete.txt"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		var loop *Loop
		if native {
			fake := provider.NewFake("f")
			var call int
			fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
				call++
				if call == 1 {
					tc := provider.ToolCall{ID: "c1", Type: "function"}
					tc.Function.Name = "fs_delete"
					tc.Function.Arguments = `{"path":"/obsolete.txt"}`
					return &provider.ChatResponse{Choices: []provider.Choice{{
						Message: provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{tc}}, FinishReason: provider.FinishToolCalls,
					}}}
				}
				return &provider.ChatResponse{Choices: []provider.Choice{{
					Message: provider.Message{Role: "assistant", Content: "Deleted."}, FinishReason: provider.FinishStop,
				}}}
			}
			loop = &Loop{Provider: fake, Duckling: &DucklingConfig{ID: "pato", Model: "m", Caps: provider.Capabilities{NativeTools: true}},
				Registry: tools.NewRegistry(), MaxTurns: 4,
				Budget: budget.NewTracker(&budget.Budget{MaxUSD: 10, MaxTokens: 1e6, MaxTurns: 50, MaxWallclockS: 600})}
		} else {
			loop = testLoop(&countingProvider{replies: []string{
				"```ducklab\n{\"tool\":\"fs_delete\",\"args\":{\"path\":\"/obsolete.txt\"}}\n```",
				"Deleted.",
			}}, 0)
		}
		loop.Registry.Register(&tools.FSDelete{})
		turn := &Turn{Role: config.RoleImplementer, Prompt: "remove the obsolete file", Contract: "freeform", Toolbelt: []string{"fs_delete"}, MaxTurns: 3}
		out, err := RunTurn(context.Background(), loop, turn, &tools.ExecContext{ProjectRoot: dir, Role: config.RoleImplementer})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.ToolCalls) != 1 || out.ToolCalls[0].Result.IsError {
			t.Fatalf("native=%v: delete did not run: %+v", native, out.ToolCalls)
		}
		if _, err := os.Stat(filepath.Join(dir, "obsolete.txt")); !os.IsNotExist(err) {
			t.Errorf("native=%v: the file was not deleted: %v", native, err)
		}
		if args := string(out.ToolCalls[0].Args); !strings.Contains(args, `"path":"obsolete.txt"`) {
			t.Errorf("native=%v: record args = %s, want obsolete.txt", native, args)
		}
	}
}
