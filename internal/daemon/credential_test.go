package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// B-530: the token was the start time in hex nanoseconds, which engine.json's
// started_at narrows to a guessable range. Two credentials are only two if
// neither can be computed.
func TestTokensAreRandomAndDistinct(t *testing.T) {
	a, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if a == b || len(a) != 64 || len(b) != 64 {
		t.Fatalf("tokens %q and %q: want two distinct 256-bit hex values", a, b)
	}
}

// The person's credential lives in its own 0600 file, never in engine.json,
// and AsHuman reads it; an engine from before the split has no such file and
// its single token is kept.
func TestThePersonsCredentialIsAFileOfItsOwn(t *testing.T) {
	enginePath := isolateDaemonState(t)
	info := &EngineInfo{PID: 999999999, Port: 4242, Token: "agent-token"}
	if got := AsHuman(info); got.Token != "agent-token" {
		t.Fatalf("AsHuman with no human.token = %q, want the only token there is", got.Token)
	}
	if err := WriteEngineFiles(info, "human-token"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(enginePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" || strings.Contains(string(raw), "human-token") {
		t.Fatalf("engine.json carries the person's credential: %s", raw)
	}
	path, _ := HumanTokenPath()
	if filepath.Dir(path) != filepath.Dir(enginePath) || filepath.Base(path) != "human.token" {
		t.Fatalf("human.token at %s, want beside engine.json", path)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("human.token mode = %v, want 0600", st.Mode().Perm())
	}
	got := AsHuman(info)
	if got.Token != "human-token" || info.Token != "agent-token" {
		t.Fatalf("AsHuman = %q (input now %q), want the person's token on a copy", got.Token, info.Token)
	}
}

// An engine that loses the start race must not overwrite the running
// engine's human.token on its way to being refused engine.json.
func TestALosingEngineLeavesTheLiveCredentialAlone(t *testing.T) {
	enginePath := isolateDaemonState(t)
	if err := os.MkdirAll(filepath.Dir(enginePath), 0o755); err != nil {
		t.Fatal(err)
	}
	live := `{"pid":` + strconv.Itoa(os.Getpid()) + `,"port":1,"token":"live-agent"}`
	if err := os.WriteFile(enginePath, []byte(live), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteHumanToken("live-human"); err != nil {
		t.Fatal(err)
	}
	if err := WriteEngineFiles(&EngineInfo{PID: 1, Token: "new-agent"}, "new-human"); err == nil {
		t.Fatal("a second engine published over a live one")
	}
	if got, _ := ReadHumanToken(); got != "live-human" {
		t.Fatalf("human.token = %q, want the live engine's", got)
	}
}
