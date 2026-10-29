package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
)

// A subscription provider needs no address and no key: its sign-in stands
// where the key would, and the list says whether it is there — never a token.
func TestCodexProviderSetAndSignInStatus(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, path := fleetService(t)
	if err := s.ProviderSet("chatgpt", ProviderView{Kind: "codex"}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.LoadGlobal(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Providers["chatgpt"]; got.Kind != config.ProviderKindCodex || got.BaseURL != provider.CodexDefaultURL {
		t.Fatalf("persisted = %+v", got)
	}
	if _, ok := s.providers["chatgpt"].(*provider.Codex); !ok {
		t.Fatalf("provider is %T, want *provider.Codex", s.providers["chatgpt"])
	}

	view := codexView(t, s)
	if view.SignIn == nil || view.SignIn.SignedIn {
		t.Fatalf("sign-in before login = %+v", view.SignIn)
	}

	creds, err := codexCredentials("chatgpt")
	if err != nil {
		t.Fatal(err)
	}
	// A JWT-shaped token whose claims name the plan.
	token := "e30.eyJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGgiOnsiY2hhdGdwdF9wbGFuX3R5cGUiOiJwcm8ifX0.sig"
	if err := creds.Save(provider.CodexTokens{AccessToken: token, RefreshToken: "rt"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(creds.Path(), os.Getenv("XDG_CONFIG_HOME")) || filepath.Base(creds.Path()) != "chatgpt.json" {
		t.Fatalf("credentials at %s", creds.Path())
	}
	view = codexView(t, s)
	if !view.SignIn.SignedIn || view.SignIn.Plan != "pro" {
		t.Fatalf("sign-in after login = %+v", view.SignIn)
	}
	for _, p := range s.ProviderList() {
		if p.Kind != "codex" && p.SignIn != nil {
			t.Errorf("keyed provider %s reports a sign-in", p.ID)
		}
	}
}

func TestProviderSetRefusesAnUnknownKind(t *testing.T) {
	s, _ := fleetService(t)
	if err := s.ProviderSet("odd", ProviderView{Kind: "gemini", BaseURL: "https://example.com"}); err == nil {
		t.Fatal("saved a provider of an unknown kind")
	}
}

func codexView(t *testing.T, s *Service) ProviderView {
	t.Helper()
	for _, p := range s.ProviderList() {
		if p.ID == "chatgpt" {
			return p
		}
	}
	t.Fatal("codex provider not listed")
	return ProviderView{}
}

// The engine runs the device sign-in: it hands out the code, collects the
// tokens in the background, and the status flips to signed in. Logout forgets.
func TestCodexProviderLoginThroughTheEngine(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, _ := fleetService(t)
	if err := s.ProviderSet("chatgpt", ProviderView{Kind: "codex"}); err != nil {
		t.Fatal(err)
	}
	token := "e30.eyJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGgiOnsiY2hhdGdwdF9wbGFuX3R5cGUiOiJwcm8ifX0.sig"
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			io.WriteString(w, `{"device_auth_id":"dev-1","user_code":"WXYZ-0000","interval":3}`)
		case "/api/accounts/deviceauth/token":
			io.WriteString(w, `{"authorization_code":"a","code_verifier":"v"}`)
		case "/oauth/token":
			json.NewEncoder(w).Encode(map[string]string{"access_token": token, "refresh_token": "rt"})
		}
	}))
	defer issuer.Close()
	s.providers["chatgpt"].(*provider.Codex).Credentials().WithIssuer(issuer.URL)

	started, err := s.ProviderLoginStart(context.Background(), "chatgpt")
	if err != nil || started.Status != "pending" || started.UserCode != "WXYZ-0000" {
		t.Fatalf("start = %+v, %v", started, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, err := s.ProviderLoginStatus("chatgpt")
		if err != nil {
			t.Fatal(err)
		}
		if st.Status == "signed_in" {
			if st.SignIn == nil || !st.SignIn.SignedIn || st.SignIn.Plan != "pro" {
				t.Fatalf("status = %+v", st.SignIn)
			}
			break
		}
		if st.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("status = %+v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := s.ProviderLogout("chatgpt"); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.ProviderLoginStatus("chatgpt"); st.Status != "none" || st.SignIn.SignedIn {
		t.Fatalf("after logout = %+v", st)
	}
	// A keyed provider has nothing to sign in to.
	if _, err := s.ProviderLoginStart(context.Background(), "local"); err == nil || !strings.Contains(err.Error(), "only a codex provider") {
		t.Fatalf("keyed provider login err = %v", err)
	}
}

// Review: a call made before signing in marks the provider's ducklings as
// unable to chat, which keeps them out of automatic seating. Signing in
// clears that mark.
func TestSigningInClearsTheCannotChatMark(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, _ := fleetService(t)
	if err := s.ProviderSet("chatgpt", ProviderView{Kind: "codex"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DucklingSet("sol", DucklingView{Provider: "chatgpt", Model: "gpt-6.1-sol", Roles: []string{"architect"}}); err != nil {
		t.Fatal(err)
	}
	s.ducklings.RecordProviderResult("sol", provider.ErrAuth)
	if !s.ducklings.LastProbeFailed("sol") {
		t.Fatal("precondition: an auth failure marks the duckling")
	}
	s.notifyProviderSignedIn("chatgpt")
	if s.ducklings.LastProbeFailed("sol") {
		t.Fatal("signing in left the duckling marked as unable to chat")
	}
}

// A second sign-in that times out does not undo a good first one.
func TestAFailedSecondSignInKeepsTheStoredOne(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, _ := fleetService(t)
	if err := s.ProviderSet("chatgpt", ProviderView{Kind: "codex"}); err != nil {
		t.Fatal(err)
	}
	creds, _ := codexCredentials("chatgpt")
	if err := creds.Save(provider.CodexTokens{AccessToken: "e30.e30.sig", RefreshToken: "rt"}); err != nil {
		t.Fatal(err)
	}
	s.codexLogins.byID = map[config.ProviderID]*codexLogin{
		"chatgpt": {view: ProviderLoginView{Status: "failed", Error: "timed out"}, cancel: func() {}},
	}
	st, err := s.ProviderLoginStatus("chatgpt")
	if err != nil || st.Status != "signed_in" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	_ = creds.Remove()
	if st, _ := s.ProviderLoginStatus("chatgpt"); st.Status != "failed" || st.Error != "timed out" {
		t.Fatalf("status with nothing stored = %+v", st)
	}
}
