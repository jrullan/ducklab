package agenttoken

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent-tokens.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestAnIssuedTokenResolvesToItsGrantsAndSurvivesARestart(t *testing.T) {
	s, path := openTemp(t)
	view, secret, err := s.Create("OpenCode ti36", []Grant{{Project: "ti36", Level: "full"}, {Project: "ducklab", Level: "read"}})
	if err != nil {
		t.Fatal(err)
	}
	if view.Actor != "agent:opencode-ti36" {
		t.Errorf("actor = %q", view.Actor)
	}
	if !strings.HasPrefix(secret, "dlk_") || !strings.HasPrefix(secret, view.Hint) {
		t.Errorf("secret %q does not start with its hint %q", secret, view.Hint)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	who, ok := reopened.Lookup(secret)
	if !ok {
		t.Fatal("token lost across a reopen")
	}
	if l, _ := who.Level("ti36"); l != LevelFull {
		t.Errorf("ti36 = %q", l)
	}
	if l, _ := who.Level("ducklab"); l != LevelRead {
		t.Errorf("ducklab = %q (\"read\" should normalise to read-only)", l)
	}
	if _, ok := who.Level("mitimesheet"); ok {
		t.Error("an ungranted project resolved")
	}
}

func TestOnlyTheHashIsStored(t *testing.T) {
	s, path := openTemp(t)
	_, secret, err := s.Create("Claude Code", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret[len(secretPrefix)+6:]) {
		t.Error("the token file holds the secret")
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("token file mode = %v, want 0600", st.Mode().Perm())
	}
	for _, v := range s.List() {
		if v.ID == "" || v.Hint == "" {
			t.Errorf("view missing id or hint: %+v", v)
		}
	}
}

func TestARevokedTokenIsRefusedButStillListed(t *testing.T) {
	s, _ := openTemp(t)
	view, secret, _ := s.Create("Hermes", []Grant{{Project: "p", Level: LevelFull}})
	if _, err := s.Revoke(view.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Lookup(secret); ok {
		t.Error("revoked token still resolves")
	}
	if got := s.List(); len(got) != 1 || got[0].RevokedAt == nil {
		t.Errorf("list = %+v, want the revoked record", got)
	}
	if _, err := s.Update(view.ID, nil, []Grant{}); err == nil {
		t.Error("a revoked token's grants changed")
	}
	// Its name is free again.
	if _, _, err := s.Create("hermes", nil); err != nil {
		t.Errorf("name of a revoked token not reusable: %v", err)
	}
}

func TestTwoLiveTokensCannotRecordAsOneActor(t *testing.T) {
	s, _ := openTemp(t)
	if _, _, err := s.Create("OpenCode ti36", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Create("opencode  TI36!", nil); err == nil {
		t.Error("a second token recording as agent:opencode-ti36 was issued")
	}
	if _, _, err := s.Create("  ", nil); err == nil {
		t.Error("a nameless token was issued")
	}
}

func TestGrantsAreValidated(t *testing.T) {
	s, _ := openTemp(t)
	for _, grants := range [][]Grant{
		{{Project: "p", Level: "admin"}},
		{{Project: "", Level: LevelFull}},
		{{Project: "p", Level: LevelFull}, {Project: "p", Level: LevelRead}},
	} {
		if _, _, err := s.Create("x", grants); err == nil {
			t.Errorf("grants %+v accepted", grants)
		}
	}
}

func TestUpdateReplacesGrantsAndLookupSeesIt(t *testing.T) {
	s, _ := openTemp(t)
	view, secret, _ := s.Create("a", []Grant{{Project: "p", Level: LevelFull}})
	if _, err := s.Update(view.ID, nil, []Grant{{Project: "q", Level: LevelRead}}); err != nil {
		t.Fatal(err)
	}
	who, _ := s.Lookup(secret)
	if _, ok := who.Level("p"); ok {
		t.Error("the replaced grant still applies")
	}
	if l, _ := who.Level("q"); l != LevelRead {
		t.Errorf("q = %q", l)
	}
	// nil grants leaves them alone.
	name := "renamed"
	if _, err := s.Update(view.ID, &name, nil); err != nil {
		t.Fatal(err)
	}
	who, _ = s.Lookup(secret)
	if who.Actor != "agent:renamed" {
		t.Errorf("actor = %q", who.Actor)
	}
	if _, ok := who.Level("q"); !ok {
		t.Error("a rename dropped the grants")
	}
}

func TestLevels(t *testing.T) {
	if !LevelFull.Allows(LevelRead) || !LevelFull.Allows(LevelFull) || !LevelRead.Allows(LevelRead) {
		t.Error("a level refuses what it covers")
	}
	if LevelRead.Allows(LevelFull) || Level("").Allows(LevelRead) {
		t.Error("a level allows what it does not cover")
	}
}

func TestTheLegacySwitchPersistsAndDefaultsOn(t *testing.T) {
	s, path := openTemp(t)
	if !s.LegacyEnabled() {
		t.Fatal("a fresh store turned the shared token off")
	}
	if err := s.SetLegacyEnabled(false); err != nil {
		t.Fatal(err)
	}
	reopened, _ := Open(path)
	if reopened.LegacyEnabled() {
		t.Error("the switch did not survive a reopen")
	}
}

func TestAnIssuedEphemeralTokenDiesWithItsRevokeAndNeverTouchesTheFile(t *testing.T) {
	s, path := openTemp(t)
	secret, revoke, err := s.Issue("renderer", []Grant{{Project: "p", Level: LevelRead}})
	if err != nil {
		t.Fatal(err)
	}
	who, ok := s.Lookup(secret)
	if !ok || who.Actor != "agent:renderer" {
		t.Fatalf("ephemeral lookup = %+v, %v", who, ok)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("an ephemeral token wrote the file")
	}
	revoke()
	if _, ok := s.Lookup(secret); ok {
		t.Error("ephemeral token outlived its revoke")
	}
}

func TestLastUseIsRecordedWithoutRewritingTheFileEachTime(t *testing.T) {
	s, path := openTemp(t)
	_, secret, _ := s.Create("a", nil)
	clock := time.Now()
	s.now = func() time.Time { return clock }
	before, _ := os.Stat(path)
	s.Lookup(secret)
	if got := s.List()[0].LastUsedAt; got == nil {
		t.Fatal("last use not recorded")
	}
	after, _ := os.Stat(path)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("a use within the flush window rewrote the file")
	}
	clock = clock.Add(2 * lastUsedFlush)
	s.Lookup(secret)
	reopened, _ := Open(path)
	if reopened.List()[0].LastUsedAt == nil {
		t.Error("last use never reached the file")
	}
}

func TestUnknownSecretsResolveToNothing(t *testing.T) {
	s, _ := openTemp(t)
	for _, secret := range []string{"", "dlk_", "dlk_0000", "not-a-token"} {
		if _, ok := s.Lookup(secret); ok {
			t.Errorf("%q resolved", secret)
		}
	}
}
