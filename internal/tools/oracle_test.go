package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

func oracleProject(t *testing.T) (*Registry, *ExecContext, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"tests/parser.test.mjs": "assert 0.015625\n",
		"logic.mjs":             "export const x = 1\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg := NewRegistry()
	reg.Register(&FSWrite{})
	reg.Register(&FSPatch{})
	reg.Register(&FSDelete{})
	reg.Register(&OracleDispute{})
	ectx := &ExecContext{ProjectRoot: root, Role: config.RoleImplementer, OracleTests: []string{"tests/parser.test.mjs"}}
	ectx.BeginTurn()
	return reg, ectx, root
}

// B-490: the implementer may not rewrite the test it is judged by — through
// any write tool or spelling — until the person allows a correction.
func TestTheOracleIsGuardedUntilThePersonAllowsACorrection(t *testing.T) {
	reg, ectx, root := oracleProject(t)
	for _, call := range []struct{ tool, args string }{
		{"fs_write", `{"path":"tests/parser.test.mjs","content":"assert 0.001953125\n"}`},
		{"fs_write", `{"path":"/tests/parser.test.mjs","content":"assert 0.001953125\n"}`},
		{"fs_patch", `{"path":"tests/parser.test.mjs","edits":[{"search":"0.015625","replace":"0.001953125"}]}`},
		{"fs_delete", `{"path":"tests/parser.test.mjs"}`},
	} {
		res, _ := reg.Execute(context.Background(), ectx, call.tool, json.RawMessage(call.args))
		if !res.IsError || !strings.Contains(res.Content, "oracle_dispute") {
			t.Errorf("%s %s was not refused with the dispute route: %+v", call.tool, call.args, res)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(root, "tests", "parser.test.mjs")); string(data) != "assert 0.015625\n" {
		t.Fatalf("the oracle changed: %q", data)
	}
	if res, _ := reg.Execute(context.Background(), ectx, "fs_write", json.RawMessage(`{"path":"logic.mjs","content":"export const x = 2\n"}`)); res.IsError {
		t.Errorf("an implementation write was refused: %s", res.Content)
	}
	ectx.Answers = map[string]string{OracleQuestionPrefix + "abc": OracleCorrectAnswer}
	if res, _ := reg.Execute(context.Background(), ectx, "fs_write", json.RawMessage(`{"path":"tests/parser.test.mjs","content":"assert 0.001953125\n"}`)); res.IsError {
		t.Errorf("the oracle stayed locked after the person allowed a correction: %s", res.Content)
	}
}

func TestOracleDisputePausesForThePersonWithTheReason(t *testing.T) {
	reg, ectx, _ := oracleProject(t)
	args := json.RawMessage(`{"test":"tests/parser.test.mjs","assertion":"2^-3^2 = 0.015625","why":"slice 2 requires right associativity: 2^(-(3^2)) = 2^-9 = 0.001953125"}`)
	res, err := reg.Execute(context.Background(), ectx, "oracle_dispute", args)
	if !errors.Is(err, ErrHumanNeeded) || res != nil {
		t.Fatalf("dispute did not pause: %+v, %v", res, err)
	}
	q := ectx.Pending
	if q == nil || !strings.HasPrefix(q.ID, OracleQuestionPrefix) || len(q.Options) != 2 ||
		!strings.Contains(q.Question, "2^-9 = 0.001953125") || !strings.Contains(q.Question, "tests/parser.test.mjs") {
		t.Fatalf("pending question = %+v", q)
	}
	ectx.Pending = nil
	ectx.Answers = map[string]string{q.ID: OracleKeepAnswer}
	if res, err := reg.Execute(context.Background(), ectx, "oracle_dispute", args); err != nil || !strings.Contains(res.Content, "keeps the test") {
		t.Errorf("a kept test did not resolve the replayed dispute: %+v, %v", res, err)
	}
	if res, _ := reg.Execute(context.Background(), ectx, "fs_write", json.RawMessage(`{"path":"tests/parser.test.mjs","content":"x\n"}`)); !res.IsError {
		t.Error("keeping the test unlocked it")
	}
}

func TestOracleDisputeRefusesWhatIsNotADispute(t *testing.T) {
	reg, ectx, _ := oracleProject(t)
	for name, args := range map[string]string{
		"not an oracle test": `{"test":"tests/other.test.mjs","assertion":"a","why":"b"}`,
		"no reason":          `{"test":"tests/parser.test.mjs","assertion":"a","why":""}`,
	} {
		res, err := reg.Execute(context.Background(), ectx, "oracle_dispute", json.RawMessage(args))
		if err != nil || !res.IsError {
			t.Errorf("%s: %+v, %v", name, res, err)
		}
	}
	ectx.NoHuman = true
	res, err := reg.Execute(context.Background(), ectx, "oracle_dispute", json.RawMessage(`{"test":"tests/parser.test.mjs","assertion":"a","why":"b"}`))
	if err != nil || !res.IsError || !strings.Contains(res.Content, "keep the test") {
		t.Errorf("with nobody to ask the dispute should keep the test: %+v, %v", res, err)
	}
	none := &ExecContext{ProjectRoot: ectx.ProjectRoot, Role: config.RoleImplementer}
	if none.ToolAvailable("oracle_dispute") || !ectx.ToolAvailable("oracle_dispute") {
		t.Error("oracle_dispute should be offered only to a build with an oracle")
	}
	if res, _ := reg.Execute(context.Background(), none, "oracle_dispute", json.RawMessage(`{"test":"tests/parser.test.mjs","assertion":"a","why":"b"}`)); !res.IsError {
		t.Error("a build without an oracle accepted a dispute")
	}
}
