package service

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/jrullan/ducklab/internal/runlog"
)

// Reconstructing what a run spent, per duckling.
//
// Runs made before spend was recorded have none, and dropping them from the
// per-duckling table would quietly shrink the history the table exists to
// summarise. The information is not lost — llm.jsonl has every call with its
// duckling and its cost — it was simply never rolled up.
//
// Reading a file per run is not free, so it happens once per run and only for
// runs that lack the rollup.

// backfillSpend fills in a run's per-duckling spend from its call log.
//
// A no-op for runs that already have it, and for runs whose log is gone: a
// missing log means the numbers cannot be reconstructed, and inventing them
// would be worse than the row being absent.
func backfillSpend(projectRoot string, run *runlog.Run) {
	if run == nil {
		return
	}
	needsTotals := len(run.Spend) == 0
	needsSplit := needsTotals
	if !needsSplit {
		for _, d := range run.Spend {
			if d.ReasoningTokens == 0 && d.ContentTokens == 0 {
				needsSplit = true
				break
			}
		}
	}
	if !needsSplit {
		return
	}
	f, err := os.Open(filepath.Join(projectRoot, ".ducklab", "runs", run.ID, "llm.jsonl"))
	if err != nil {
		return
	}
	defer f.Close()

	spend := map[string]runlog.DucklingSpend{}
	sc := bufio.NewScanner(f)
	// A single call's record can be large: it carries the whole request.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var call runlog.LLMCall
		if err := json.Unmarshal(sc.Bytes(), &call); err != nil {
			continue // a truncated line is one call lost, not a reason to give up
		}
		if call.Duckling == "" {
			continue
		}
		d := spend[call.Duckling]
		d.Calls++
		d.Tokens += callTokens(call.Usage)
		reasoning, content := callCompletionSplit(call.Usage)
		d.ReasoningTokens += reasoning
		d.ContentTokens += content
		d.CostUSD += call.CostUSD
		if call.Estimated {
			d.Estimated = true
		}
		spend[call.Duckling] = d
	}
	if needsTotals && len(spend) > 0 {
		run.Spend = spend
	} else {
		for id, parsed := range spend {
			d, ok := run.Spend[id]
			if !ok {
				continue
			}
			d.ReasoningTokens = parsed.ReasoningTokens
			d.ContentTokens = parsed.ContentTokens
			run.Spend[id] = d
		}
	}
}
