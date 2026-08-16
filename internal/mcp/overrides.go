package mcp

// copyRunOverrides forwards the desktop's per-run controls without inventing
// defaults: omitted fields remain engine-owned.
func copyRunOverrides(dst map[string]interface{}, a args) {
	if seats, ok := a["ducklings"].([]interface{}); ok {
		dst["ducklings"] = seats
	}
	if mode := a.str("mode"); mode != "" {
		dst["mode"] = mode
	}
	if turns, ok := a["agent_turns"]; ok {
		dst["agent_turns"] = turns
	}
}

