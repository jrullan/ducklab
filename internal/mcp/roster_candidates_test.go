package mcp

import "testing"

// Seat candidates are computed once, in the engine (service.RankCandidates),
// and travel on each roster entry. This surface is a client: it must hand
// them to the model untouched — id, why, and their order — and must not
// invent any when the engine sent none (a seat without evidence).
func TestRosterMCPGetPassesSeatCandidatesThrough(t *testing.T) {
	f := &fakeEngine{rosterViews: map[string]map[string]interface{}{
		"global:pair": {"entries": []interface{}{
			map[string]interface{}{"role": "implementer", "ducklings": []interface{}{"bench-best"}, "source": "global mode seat",
				"candidates": []interface{}{
					map[string]interface{}{"id": "bench-best", "why": "bench 91 · $0.31/run"},
					map[string]interface{}{"id": "pass-cheap", "why": "pass rate 92% over 14 runs · $0.20/run"},
				}},
			map[string]interface{}{"role": "advisor", "ducklings": []interface{}{"pass-cheap"}, "source": "global mode seat",
				"candidates": []interface{}{map[string]interface{}{"id": "pass-cheap", "why": "$0.20/run · 40s avg"}}},
		}},
	}}
	res, rpcErr := rosterCall(t, NewServer(f), map[string]interface{}{"action": "get", "scope": "global", "mode": "pair"})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	entries, _ := unwrap(t, res)["entries"].([]interface{})
	if len(entries) != 2 {
		t.Fatalf("entries = %#v", entries)
	}
	want := map[string]string{"implementer": "bench-best", "advisor": "pass-cheap"}
	for _, raw := range entries {
		entry := raw.(map[string]interface{})
		role, _ := entry["role"].(string)
		candidates, ok := entry["candidates"].([]interface{})
		if !ok || len(candidates) == 0 {
			t.Fatalf("%s candidates missing: %#v", role, entry)
		}
		first := candidates[0].(map[string]interface{})
		if first["id"] != want[role] {
			t.Errorf("%s first candidate = %#v, want %s", role, first, want[role])
		}
		if why, _ := first["why"].(string); why == "" {
			t.Errorf("candidate needs desktop-parity why: %#v", first)
		}
	}
}

func TestRosterMCPGetOmitsCandidatesWithoutEvidence(t *testing.T) {
	f := &fakeEngine{rosterViews: map[string]map[string]interface{}{
		"p:pair": {"entries": []interface{}{map[string]interface{}{"role": "reviewer", "ducklings": []interface{}{"no-runs"}, "source": "project pin"}}},
	}}
	res, rpcErr := rosterCall(t, NewServer(f), map[string]interface{}{"action": "get", "scope": "project", "project_id": "p", "mode": "pair"})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	entry := unwrap(t, res)["entries"].([]interface{})[0].(map[string]interface{})
	if candidates, exists := entry["candidates"]; exists && len(candidates.([]interface{})) != 0 {
		t.Fatalf("no-evidence seat candidates = %#v, want none", candidates)
	}
}
