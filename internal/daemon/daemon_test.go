package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func isolateDaemonState(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	state := filepath.Join(root, "state")
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("LocalAppData", filepath.Join(root, "local"))
	t.Setenv("AppData", filepath.Join(root, "roaming"))
	return filepath.Join(state, "ducklab", "engine.json")
}

func TestWriteEngineJSONRefusesLiveEngineIdentity(t *testing.T) {
	path := isolateDaemonState(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"pid":` + strconv.Itoa(os.Getpid()) + `,"port":43123,"token":"live-token","version":"live","started_at":"now","state_dir":"live-state"}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}

	err := WriteEngineJSON(&EngineInfo{PID: os.Getpid(), Port: 43124, Token: "new-token"})
	if err == nil {
		t.Fatal("WriteEngineJSON overwrote an engine.json whose recorded process is alive")
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != string(original) {
		t.Fatalf("engine.json changed after refused write: got %q, want %q", got, original)
	}
}
