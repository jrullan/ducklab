// Package config resolves the set of model sources ducklab can talk to. Sources
// come from built-in defaults, an optional JSON file, and environment
// overrides. API keys live ONLY in the environment, never in the file.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/source"
)

// SourceDef is the file/default shape of a source (no secrets).
type SourceDef struct {
	BaseURL string `json:"base_url"`
	Model   string `json:"model,omitempty"`
}

// Defaults are the sources ducklab knows out of the box: José's local Beelink
// (model autodetected) and the AiTopAtom GB10 box.
var Defaults = map[string]SourceDef{
	"beelink":   {BaseURL: "http://localhost:8081/v1"},
	"aitopatom": {BaseURL: "http://10.0.0.5:8000/v1", Model: "qwopus-coder"},
}

// ConfigPath is the optional JSON override file (XDG-friendly).
func ConfigPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "ducklab", "config.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "ducklab", "config.json")
}

type fileShape struct {
	Sources map[string]SourceDef `json:"sources"`
}

// Load merges defaults, the config file, and environment overrides into a map
// of ready-to-use sources.
func Load() (map[string]*source.HTTPSource, error) {
	defs := map[string]SourceDef{}
	for k, v := range Defaults {
		defs[k] = v
	}
	if data, err := os.ReadFile(ConfigPath()); err == nil {
		var fs fileShape
		if err := json.Unmarshal(data, &fs); err != nil {
			return nil, err
		}
		for k, v := range fs.Sources {
			defs[k] = v
		}
	}

	out := map[string]*source.HTTPSource{}
	for name, def := range defs {
		env := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		baseURL := envOr("DUCKLAB_"+env+"_BASE_URL", def.BaseURL)
		model := envOr("DUCKLAB_"+env+"_MODEL", def.Model)
		out[name] = &source.HTTPSource{
			SrcName: name,
			BaseURL: baseURL,
			ModelID: model,
			APIKey:  os.Getenv("DUCKLAB_" + env + "_API_KEY"),
			Timeout: 10 * time.Minute,
		}
	}
	return out, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
