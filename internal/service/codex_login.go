package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
)

// ProviderLoginView is a subscription sign-in in progress or done. The code
// is what the person types at the page; it is single-use and short-lived, and
// it is not a credential — the tokens never leave the engine.
type ProviderLoginView struct {
	// Status is pending (waiting for the person to approve), signed_in,
	// failed, or none (no sign-in started and none stored).
	Status          string      `json:"status"`
	VerificationURL string      `json:"verification_url,omitempty"`
	UserCode        string      `json:"user_code,omitempty"`
	Error           string      `json:"error,omitempty"`
	SignIn          *SignInView `json:"sign_in,omitempty"`
}

// codexLogin is one device sign-in the engine is waiting on.
type codexLogin struct {
	mu     sync.Mutex
	view   ProviderLoginView
	cancel context.CancelFunc
}

type codexLogins struct {
	mu   sync.Mutex
	byID map[config.ProviderID]*codexLogin
}

// codexLoginWindow is how long the engine waits for the approval.
const codexLoginWindow = 15 * time.Minute

// ProviderLoginStart begins a device sign-in for a codex provider: the engine
// asks for a code, returns it for the person to approve in a browser, and
// collects the tokens in the background. A sign-in already waiting is
// replaced — its code may have been lost with the window that showed it.
func (s *Service) ProviderLoginStart(ctx context.Context, id string) (ProviderLoginView, error) {
	pid := config.ProviderID(id)
	creds, err := s.codexProvider(pid)
	if err != nil {
		return ProviderLoginView{}, err
	}
	code, err := creds.StartDeviceLogin(ctx)
	if err != nil {
		return ProviderLoginView{}, err
	}
	loginCtx, cancel := context.WithTimeout(context.Background(), codexLoginWindow)
	login := &codexLogin{
		view:   ProviderLoginView{Status: "pending", VerificationURL: code.VerificationURL, UserCode: code.UserCode},
		cancel: cancel,
	}
	s.codexLogins.mu.Lock()
	if s.codexLogins.byID == nil {
		s.codexLogins.byID = map[config.ProviderID]*codexLogin{}
	}
	if prev := s.codexLogins.byID[pid]; prev != nil {
		prev.cancel()
	}
	s.codexLogins.byID[pid] = login
	s.codexLogins.mu.Unlock()

	go func() {
		defer cancel()
		_, err := creds.PollDeviceLogin(loginCtx, code)
		login.mu.Lock()
		defer login.mu.Unlock()
		if err != nil {
			if loginCtx.Err() == context.Canceled {
				return // replaced by a newer sign-in, or the engine is stopping
			}
			login.view = ProviderLoginView{Status: "failed", Error: err.Error()}
			return
		}
		login.view = ProviderLoginView{Status: "signed_in"}
		s.notifyProviderSignedIn(pid)
	}()
	return login.view, nil
}

// ProviderLoginStatus reports the sign-in waiting for approval, or the stored
// one when none is waiting.
func (s *Service) ProviderLoginStatus(id string) (ProviderLoginView, error) {
	pid := config.ProviderID(id)
	if _, err := s.codexProvider(pid); err != nil {
		return ProviderLoginView{}, err
	}
	s.codexLogins.mu.Lock()
	login := s.codexLogins.byID[pid]
	s.codexLogins.mu.Unlock()
	var attempt ProviderLoginView
	if login != nil {
		login.mu.Lock()
		attempt = login.view
		login.mu.Unlock()
	}
	// Only a sign-in still waiting speaks for itself; otherwise the stored
	// sign-in is the truth — a timed-out second attempt does not undo a good
	// first one, and a sign-out or a dead grant undoes an old success.
	signIn := codexSignIn(pid)
	if attempt.Status == "pending" {
		attempt.SignIn = signIn
		return attempt, nil
	}
	view := ProviderLoginView{Status: "none", SignIn: signIn}
	if signIn.SignedIn {
		view.Status = "signed_in"
	} else if attempt.Status == "failed" {
		view.Status, view.Error = "failed", attempt.Error
	}
	return view, nil
}

// ProviderLogout forgets a codex provider's sign-in and stops one waiting.
func (s *Service) ProviderLogout(id string) error {
	pid := config.ProviderID(id)
	creds, err := s.codexProvider(pid)
	if err != nil {
		return err
	}
	s.codexLogins.mu.Lock()
	if login := s.codexLogins.byID[pid]; login != nil {
		login.cancel()
		delete(s.codexLogins.byID, pid)
	}
	s.codexLogins.mu.Unlock()
	return creds.Remove()
}

// codexProvider is the sign-in store of a configured codex provider. Any
// other provider is refused: it has a key, not a sign-in.
func (s *Service) codexProvider(id config.ProviderID) (*provider.CodexCredentials, error) {
	s.cfgMu.RLock()
	p, ok := s.cfg.Providers[id]
	live := s.providers[id]
	s.cfgMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("provider %q does not exist", id)
	}
	if p.Kind != config.ProviderKindCodex {
		return nil, fmt.Errorf("provider %q is kind %s; only a codex provider signs in", id, p.Kind)
	}
	// The live provider's own store, so its cached tokens see the change.
	if c, ok := live.(*provider.Codex); ok && c.Credentials() != nil {
		return c.Credentials(), nil
	}
	return codexCredentials(id)
}

// notifyProviderSignedIn clears the "cannot chat" mark a call without a
// sign-in left on the provider's ducklings — it kept them out of automatic
// seating — and wakes queued work. A run that failed or paused for want of a
// sign-in is not resumed: that stays the person's call.
func (s *Service) notifyProviderSignedIn(pid config.ProviderID) {
	s.cfgMu.RLock()
	var ids []config.DucklingID
	for id, d := range s.cfg.Ducklings {
		if d.Provider == pid {
			ids = append(ids, id)
		}
	}
	s.cfgMu.RUnlock()
	for _, id := range ids {
		s.ducklings.RecordProviderResult(id, nil)
	}
	if s.queue != nil {
		s.queue.poke(s)
	}
}
