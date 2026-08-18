package desktop

import "sort"

// MissingKeys names the provider API-key variables the running engine lacks
// that THIS process has.
//
// The engine is a daemon on purpose — closing the desktop must never kill a
// running tournament — so a desktop launched with a freshly unlocked keyring
// can adopt an engine that was started without it. Twice now that read as
// "ducklab is broken": every OpenRouter run 401'd while the UI showed
// nothing wrong. The one remedy is the Restart-engine door the version
// banner already owns; this computes when to open it.
//
// providers is the engine's own /v1/providers answer; getenv is the
// desktop's environment. Only NAMES travel — never values.
func MissingKeys(providers []map[string]interface{}, getenv func(string) string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range providers {
		name, _ := p["api_key_env"].(string)
		if name == "" || seen[name] {
			continue
		}
		present, _ := p["key_present"].(bool)
		if !present && getenv(name) != "" {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
