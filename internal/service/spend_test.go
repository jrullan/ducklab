package service

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
)

func writeCallLog(t *testing.T, root, runID string, calls []runlog.LLMCall) {
	t.Helper()
	dir := filepath.Join(root, ".ducklab", "runs", runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, c := range calls {
		raw, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "llm.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Runs made before spend was recorded have none. Dropping them from the
// per-duckling table would quietly shrink the history the table exists to
// summarise — and the information was never lost, only never rolled up.
func TestSpendIsRebuiltFromTheCallLog(t *testing.T) {
	root := t.TempDir()
	writeCallLog(t, root, "r-1", []runlog.LLMCall{
		{Duckling: "pato-atom", CostUSD: 0.10, Usage: map[string]interface{}{"total_tokens": float64(1000)}},
		{Duckling: "pato-atom", CostUSD: 0.20, Usage: map[string]interface{}{"total_tokens": float64(2000)}},
		{Duckling: "pato-sonnet", CostUSD: 1.50, Usage: map[string]interface{}{
			"prompt_tokens": float64(400), "completion_tokens": float64(100),
		}},
	})

	run := &runlog.Run{ID: "r-1"}
	backfillSpend(root, run)

	atom := run.Spend["pato-atom"]
	// Money is summed in float64, so 0.10 + 0.20 is 0.30000000000000004.
	// Comparing exactly asserts something about IEEE 754 rather than about
	// this code.
	if atom.Calls != 2 || atom.Tokens != 3000 || !closeTo(atom.CostUSD, 0.30) {
		t.Errorf("pato-atom = %+v", atom)
	}
	// Providers disagree about the names; prompt+completion is the other one.
	sonnet := run.Spend["pato-sonnet"]
	if sonnet.Calls != 1 || sonnet.Tokens != 500 || !closeTo(sonnet.CostUSD, 1.50) {
		t.Errorf("pato-sonnet = %+v", sonnet)
	}
}

// closeTo compares money to well under a cent.
func closeTo(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

// A run that already has its rollup is not read again: the file is only worth
// opening when there is nothing else to go on.
func TestBackfillLeavesRecordedSpendAlone(t *testing.T) {
	root := t.TempDir()
	writeCallLog(t, root, "r-1", []runlog.LLMCall{
		{Duckling: "someone-else", CostUSD: 99, Usage: map[string]interface{}{"total_tokens": float64(1)}},
	})
	run := &runlog.Run{
		ID:    "r-1",
		Spend: map[string]runlog.DucklingSpend{"pato-atom": {Calls: 1, Tokens: 10, CostUSD: 1}},
	}
	backfillSpend(root, run)
	if _, ok := run.Spend["someone-else"]; ok {
		t.Error("an existing rollup was overwritten from the log")
	}
}

// A missing log means the numbers cannot be reconstructed. Inventing them
// would be worse than the row being absent.
func TestBackfillIsSilentWithoutALog(t *testing.T) {
	run := &runlog.Run{ID: "r-gone"}
	backfillSpend(t.TempDir(), run)
	if len(run.Spend) != 0 {
		t.Errorf("spend = %+v, want none", run.Spend)
	}
}

// A truncated last line is one call lost, not a reason to abandon the rest —
// and a killed engine leaves exactly that.
func TestBackfillSurvivesATruncatedLog(t *testing.T) {
	root := t.TempDir()
	writeCallLog(t, root, "r-1", []runlog.LLMCall{
		{Duckling: "pato-atom", CostUSD: 0.10, Usage: map[string]interface{}{"total_tokens": float64(1000)}},
	})
	path := filepath.Join(root, ".ducklab", "runs", "r-1", "llm.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, []byte(`{"duckling":"pato-atom","cost`)...), 0o644); err != nil {
		t.Fatal(err)
	}

	run := &runlog.Run{ID: "r-1"}
	backfillSpend(root, run)
	if got := run.Spend["pato-atom"]; got.Calls != 1 || got.Tokens != 1000 {
		t.Errorf("the whole log was abandoned over one bad line: %+v", got)
	}
}

// Every provider spells it differently, and a call nobody counted is not a
// call that used nothing — but Estimated already carries that distinction.
func TestCallTokensReadsTheUsualNames(t *testing.T) {
	for _, c := range []struct {
		usage map[string]interface{}
		want  int64
	}{
		{map[string]interface{}{"total_tokens": float64(500)}, 500},
		{map[string]interface{}{"prompt_tokens": float64(100), "completion_tokens": float64(50)}, 150},
		{map[string]interface{}{"input_tokens": float64(10), "output_tokens": float64(5)}, 15},
		{map[string]interface{}{}, 0},
		{nil, 0},
	} {
		if got := callTokens(c.usage); got != c.want {
			t.Errorf("callTokens(%v) = %d, want %d", c.usage, got, c.want)
		}
	}
}
