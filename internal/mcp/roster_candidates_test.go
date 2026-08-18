package mcp

import "testing"

// Scorecards is deliberately an extra fake method until roster get consumes
// scorecard evidence. It documents the engine data available to candidates.
func (f *fakeEngine) Scorecards() ([]map[string]interface{}, error) {
	return []map[string]interface{}{
		{"id": "bench-best", "measured": map[string]interface{}{"runs": 14, "pass_rate": .92, "avg_cost_usd": .31}, "bench": map[string]interface{}{"suite": map[string]interface{}{"score": .91}}},
		{"id": "pass-cheap", "measured": map[string]interface{}{"runs": 14, "pass_rate": .92, "avg_cost_usd": .20}},
		{"id": "no-runs", "measured": map[string]interface{}{"runs": 0}},
	}, nil
}

func TestRosterMCPGetIncludesSeatCandidatesFromScorecards(t *testing.T) {
	f := &fakeEngine{rosterViews: map[string]map[string]interface{}{
		"global:pair": {"entries": []interface{}{
			map[string]interface{}{"role": "implementer", "ducklings": []interface{}{"bench-best"}, "source": "global mode seat"},
			map[string]interface{}{"role": "advisor", "ducklings": []interface{}{"pass-cheap"}, "source": "global mode seat"},
		}},
	}}
	res, rpcErr := rosterCall(t, NewServer(f), map[string]interface{}{"action": "get", "scope": "global", "mode": "pair"})
	if rpcErr != nil { t.Fatal(rpcErr) }
	entries, _ := unwrap(t, res)["entries"].([]interface{})
	if len(entries) != 2 { t.Fatalf("entries = %#v", entries) }
	for _, raw := range entries {
		entry := raw.(map[string]interface{})
		candidates, ok := entry["candidates"].([]interface{})
		if !ok || len(candidates) == 0 { t.Fatalf("%s candidates missing: %#v", entry["role"], entry) }
		first := candidates[0].(map[string]interface{})
		if first["id"] != "bench-best" { t.Errorf("%s first candidate = %#v, want bench-best", entry["role"], first) }
		if why, _ := first["why"].(string); why == "" { t.Errorf("candidate needs desktop-parity why: %#v", first) }
	}
}

func TestRosterMCPGetOmitsCandidatesWithoutEvidence(t *testing.T) {
	f := &fakeEngine{rosterViews: map[string]map[string]interface{}{
		"p:pair": {"entries": []interface{}{map[string]interface{}{"role": "reviewer", "ducklings": []interface{}{"no-runs"}, "source": "project pin"}}},
	}}
	res, rpcErr := rosterCall(t, NewServer(f), map[string]interface{}{"action": "get", "scope": "project", "project_id": "p", "mode": "pair"})
	if rpcErr != nil { t.Fatal(rpcErr) }
	entry := unwrap(t, res)["entries"].([]interface{})[0].(map[string]interface{})
	if candidates, exists := entry["candidates"]; exists && len(candidates.([]interface{})) != 0 { t.Fatalf("no-evidence seat candidates = %#v, want empty", candidates) }
}
