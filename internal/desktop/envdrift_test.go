package desktop

import "testing"

func TestMissingKeysNamesOnlyWhatTheDesktopCouldFix(t *testing.T) {
	providers := []map[string]interface{}{
		{"id": "openrouter", "api_key_env": "OPENROUTER_API_KEY", "key_present": false},
		{"id": "other", "api_key_env": "OTHER_KEY", "key_present": false},
		{"id": "local", "api_key_env": "", "key_present": true},
		{"id": "fine", "api_key_env": "FINE_KEY", "key_present": true},
		{"id": "dup", "api_key_env": "OPENROUTER_API_KEY", "key_present": false},
	}
	env := map[string]string{"OPENROUTER_API_KEY": "k", "FINE_KEY": "k"}
	got := MissingKeys(providers, func(k string) string { return env[k] })
	// OTHER_KEY is missing in the engine AND here: restarting fixes nothing,
	// so it is not reported. FINE_KEY is present in the engine. The dup
	// provider shares the variable and does not double it.
	if len(got) != 1 || got[0] != "OPENROUTER_API_KEY" {
		t.Fatalf("got %v, want [OPENROUTER_API_KEY]", got)
	}
	if out := MissingKeys(nil, func(string) string { return "" }); len(out) != 0 {
		t.Fatalf("empty providers gave %v", out)
	}
}
