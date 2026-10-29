package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The ChatGPT sign-in a Codex subscription uses. The client id is the Codex
// CLI's public OAuth client — the one every third-party harness that speaks to
// a subscription presents (OpenCode and Hermes do the same).
const (
	CodexIssuer        = "https://auth.openai.com"
	CodexDefaultURL    = "https://chatgpt.com/backend-api/codex"
	codexClientID      = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexRefreshMargin = 5 * time.Minute
)

// CodexTokens is what a sign-in leaves on disk. Only ducklab writes this
// file: the Codex CLI's own ~/.codex/auth.json is never read or written,
// because a refresh token rotates on use — two programs sharing one grant log
// each other out.
type CodexTokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	IDToken      string    `json:"id_token,omitempty"`
	LastRefresh  time.Time `json:"last_refresh"`
	// Invalid is why the grant stopped working (revoked, expired, spent
	// elsewhere), set when a refresh is refused. A dead grant reads as not
	// signed in, so the person is offered a sign-in rather than a sign-out.
	Invalid string `json:"invalid,omitempty"`
}

// CodexAccount is what the access token says about the subscription.
type CodexAccount struct {
	AccountID string
	Plan      string
	Email     string
	Expires   time.Time
	Residency string
}

// Account decodes the access token's claims. It does not verify the
// signature: the token is only ever presented back to the server that signed
// it, and the claims here pick headers and print status, nothing more.
func (t CodexTokens) Account() (CodexAccount, error) {
	parts := strings.Split(t.AccessToken, ".")
	if len(parts) < 2 {
		return CodexAccount{}, errors.New("access token is not a JWT")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return CodexAccount{}, fmt.Errorf("access token claims: %w", err)
	}
	var claims struct {
		Exp     int64 `json:"exp"`
		Profile struct {
			Email string `json:"email"`
		} `json:"https://api.openai.com/profile"`
		Auth struct {
			AccountID        string `json:"chatgpt_account_id"`
			Plan             string `json:"chatgpt_plan_type"`
			DataResidency    string `json:"chatgpt_data_residency"`
			ComputeResidency string `json:"chatgpt_compute_residency"`
		} `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		return CodexAccount{}, fmt.Errorf("access token claims: %w", err)
	}
	var expires time.Time // zero: the token does not say
	if claims.Exp > 0 {
		expires = time.Unix(claims.Exp, 0)
	}
	return CodexAccount{
		AccountID: claims.Auth.AccountID,
		Plan:      claims.Auth.Plan,
		Email:     claims.Profile.Email,
		Expires:   expires,
		Residency: firstNonEmpty(claims.Auth.DataResidency, claims.Auth.ComputeResidency),
	}, nil
}

// CodexCredentials is one provider's sign-in, kept in a file of its own.
// The engine reads it lazily and refreshes it in place; `ducklab provider
// login` writes it from another process, so a changed file is re-read.
type CodexCredentials struct {
	path       string
	issuer     string
	httpClient *http.Client

	mu      sync.Mutex
	tokens  *CodexTokens
	modTime time.Time
}

// CodexCredentialsPath is where provider id's sign-in lives under ducklab's
// config directory. One file per provider, so two subscriptions can coexist.
func CodexCredentialsPath(configDir, id string) string {
	return filepath.Join(configDir, "codex", id+".json")
}

// codexStores makes one store per file. A refresh token rotates on use, so
// two stores refreshing the same file at once — a provider rebuilt by a
// config edit while a run still holds the old one — would spend it twice, and
// the second is refused as reused.
var codexStores = struct {
	mu sync.Mutex
	m  map[string]*CodexCredentials
}{m: map[string]*CodexCredentials{}}

// NewCodexCredentials is the sign-in stored at path, shared by everyone who
// asks for the same path.
func NewCodexCredentials(path string) *CodexCredentials {
	path = filepath.Clean(path)
	codexStores.mu.Lock()
	defer codexStores.mu.Unlock()
	if c := codexStores.m[path]; c != nil {
		return c
	}
	c := &CodexCredentials{path: path, issuer: CodexIssuer, httpClient: &http.Client{Timeout: 30 * time.Second}}
	codexStores.m[path] = c
	return c
}

// WithIssuer points the sign-in at another OAuth issuer (tests).
func (c *CodexCredentials) WithIssuer(issuer string) *CodexCredentials {
	c.issuer = strings.TrimSuffix(issuer, "/")
	return c
}

// Path is where the sign-in lives.
func (c *CodexCredentials) Path() string { return c.path }

// ErrCodexNotLoggedIn means no sign-in is stored for the provider.
var ErrCodexNotLoggedIn = errors.New("not signed in to the Codex subscription")

// Load returns the stored tokens, re-reading the file when it changed.
func (c *CodexCredentials) Load() (CodexTokens, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loadLocked()
}

func (c *CodexCredentials) loadLocked() (CodexTokens, error) {
	st, err := os.Stat(c.path)
	if errors.Is(err, os.ErrNotExist) {
		c.tokens = nil
		return CodexTokens{}, ErrCodexNotLoggedIn
	}
	if err != nil {
		return CodexTokens{}, err
	}
	if c.tokens != nil && st.ModTime().Equal(c.modTime) {
		return usable(*c.tokens)
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		return CodexTokens{}, err
	}
	var t CodexTokens
	if err := json.Unmarshal(data, &t); err != nil {
		return CodexTokens{}, fmt.Errorf("codex credentials %s: %w", c.path, err)
	}
	if t.AccessToken == "" || t.RefreshToken == "" {
		return CodexTokens{}, ErrCodexNotLoggedIn
	}
	c.tokens, c.modTime = &t, st.ModTime()
	return usable(t)
}

// usable refuses a grant marked dead.
func usable(t CodexTokens) (CodexTokens, error) {
	if t.Invalid != "" {
		return CodexTokens{}, fmt.Errorf("%w: the sign-in stopped working (%s)", ErrCodexNotLoggedIn, t.Invalid)
	}
	return t, nil
}

// Save writes the tokens owner-only, atomically: a reader never sees half a
// file, and a crash mid-write leaves the previous sign-in intact.
func (c *CodexCredentials) Save(t CodexTokens) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.saveLocked(t)
}

func (c *CodexCredentials) saveLocked(t CodexTokens) error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.path), ".codex-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), c.path); err != nil {
		return err
	}
	st, err := os.Stat(c.path)
	if err != nil {
		return err
	}
	c.tokens, c.modTime = &t, st.ModTime()
	return nil
}

// Remove forgets the sign-in.
func (c *CodexCredentials) Remove() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokens = nil
	if err := os.Remove(c.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Fresh returns tokens good for at least the refresh margin, refreshing them
// first when they are not. Serialized: two turns refreshing at once would
// spend the same refresh token twice, and the second is refused as reused.
func (c *CodexCredentials) Fresh(ctx context.Context) (CodexTokens, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, err := c.loadLocked()
	if err != nil {
		return CodexTokens{}, err
	}
	// A token whose expiry cannot be read is used as is; the server's 401
	// says when it is stale (ForceRefresh). Refreshing it on every call would
	// rotate the grant for nothing.
	if acct, err := t.Account(); err != nil || acct.Expires.IsZero() || time.Until(acct.Expires) > codexRefreshMargin {
		return t, nil
	}
	return c.refreshLocked(ctx, t)
}

// ForceRefresh replaces an access token the server refused before its
// expiry. stale is the token that was refused: when another caller already
// replaced it, that newer token is returned instead of spending the refresh
// token a second time.
func (c *CodexCredentials) ForceRefresh(ctx context.Context, stale CodexTokens) (CodexTokens, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, err := c.loadLocked()
	if err != nil {
		return CodexTokens{}, err
	}
	if t.AccessToken != stale.AccessToken {
		return t, nil
	}
	return c.refreshLocked(ctx, t)
}

func (c *CodexCredentials) refreshLocked(ctx context.Context, t CodexTokens) (CodexTokens, error) {
	refreshed, err := c.refresh(ctx, t)
	if errors.Is(err, ErrAuth) {
		// Dead grant: remember it, so the status says "sign in again"
		// instead of "signed in" until the next call fails the same way.
		t.Invalid = err.Error()
		_ = c.saveLocked(t)
		return CodexTokens{}, err
	}
	if err != nil {
		return CodexTokens{}, err
	}
	if err := c.saveLocked(refreshed); err != nil {
		return CodexTokens{}, fmt.Errorf("save refreshed codex credentials: %w", err)
	}
	return refreshed, nil
}

func (c *CodexCredentials) refresh(ctx context.Context, t CodexTokens) (CodexTokens, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {t.RefreshToken},
		"client_id":     {codexClientID},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.issuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return CodexTokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return CodexTokens{}, fmt.Errorf("%w: codex token refresh: %v", ErrProviderUnavailable, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return CodexTokens{}, fmt.Errorf("%w: codex token refresh: %s", ErrRateLimit, resp.Status)
	case resp.StatusCode >= 500:
		return CodexTokens{}, fmt.Errorf("%w: codex token refresh: %s", ErrProviderUnavailable, resp.Status)
	case resp.StatusCode != http.StatusOK:
		// A dead grant — revoked, expired, or spent by another program —
		// only a new sign-in mends.
		return CodexTokens{}, fmt.Errorf("%w: codex sign-in expired or revoked (%s: %s) — sign in again", ErrAuth, resp.Status, strings.TrimSpace(string(body)))
	}
	var got struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &got); err != nil || got.AccessToken == "" {
		return CodexTokens{}, fmt.Errorf("%w: codex token refresh returned no access token", ErrInvalidResponse)
	}
	out := CodexTokens{
		AccessToken:  got.AccessToken,
		RefreshToken: firstNonEmpty(got.RefreshToken, t.RefreshToken),
		IDToken:      firstNonEmpty(got.IDToken, t.IDToken),
		LastRefresh:  time.Now().UTC(),
	}
	return out, nil
}

// CodexDeviceCode is the code a person types at the verification page.
type CodexDeviceCode struct {
	VerificationURL string
	UserCode        string
	deviceAuthID    string
	interval        time.Duration
}

// StartDeviceLogin asks for a device code: the person opens the URL, types
// the code, and PollDeviceLogin collects the tokens. No browser redirect and
// no local listener, so it works over SSH and from a headless engine host.
func (c *CodexCredentials) StartDeviceLogin(ctx context.Context) (CodexDeviceCode, error) {
	body, _ := json.Marshal(map[string]string{"client_id": codexClientID})
	req, err := http.NewRequestWithContext(ctx, "POST", c.issuer+"/api/accounts/deviceauth/usercode", strings.NewReader(string(body)))
	if err != nil {
		return CodexDeviceCode{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return CodexDeviceCode{}, fmt.Errorf("request a device code: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusNotFound {
		return CodexDeviceCode{}, fmt.Errorf("device-code sign-in is not available (%s) — enable \"device code authorization for Codex\" in ChatGPT's security settings", resp.Status)
	}
	if resp.StatusCode != http.StatusOK {
		return CodexDeviceCode{}, fmt.Errorf("request a device code: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var got struct {
		DeviceAuthID string          `json:"device_auth_id"`
		UserCode     string          `json:"user_code"`
		Interval     json.RawMessage `json:"interval"`
	}
	if err := json.Unmarshal(raw, &got); err != nil || got.DeviceAuthID == "" || got.UserCode == "" {
		return CodexDeviceCode{}, fmt.Errorf("device code response is missing its code")
	}
	// The interval arrives as a number or as a numeric string.
	interval := 5
	if s := strings.Trim(string(got.Interval), `" `); s != "" {
		fmt.Sscanf(s, "%d", &interval)
	}
	if interval < 3 {
		interval = 3
	}
	return CodexDeviceCode{
		VerificationURL: c.issuer + "/codex/device",
		UserCode:        got.UserCode,
		deviceAuthID:    got.DeviceAuthID,
		interval:        time.Duration(interval) * time.Second,
	}, nil
}

// PollDeviceLogin waits for the person to approve the code, exchanges the
// authorization for tokens, and stores them. Bounded by ctx.
func (c *CodexCredentials) PollDeviceLogin(ctx context.Context, code CodexDeviceCode) (CodexTokens, error) {
	body, _ := json.Marshal(map[string]string{"device_auth_id": code.deviceAuthID, "user_code": code.UserCode})
	var grant struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
	}
	for {
		select {
		case <-ctx.Done():
			return CodexTokens{}, fmt.Errorf("sign-in not completed: %w", ctx.Err())
		case <-time.After(code.interval):
		}
		req, err := http.NewRequestWithContext(ctx, "POST", c.issuer+"/api/accounts/deviceauth/token", strings.NewReader(string(body)))
		if err != nil {
			return CodexTokens{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return CodexTokens{}, fmt.Errorf("sign-in not completed: %w", ctx.Err())
			}
			continue // a network blip while the person is still typing
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
			continue // not approved yet
		}
		if resp.StatusCode != http.StatusOK {
			return CodexTokens{}, fmt.Errorf("device sign-in: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
		}
		if err := json.Unmarshal(raw, &grant); err != nil || grant.AuthorizationCode == "" || grant.CodeVerifier == "" {
			return CodexTokens{}, fmt.Errorf("device sign-in response is missing its authorization")
		}
		break
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {grant.AuthorizationCode},
		"redirect_uri":  {c.issuer + "/deviceauth/callback"},
		"client_id":     {codexClientID},
		"code_verifier": {grant.CodeVerifier},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.issuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return CodexTokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return CodexTokens{}, fmt.Errorf("exchange the sign-in for tokens: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return CodexTokens{}, fmt.Errorf("exchange the sign-in for tokens: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var got struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
	}
	if err := json.Unmarshal(raw, &got); err != nil || got.AccessToken == "" || got.RefreshToken == "" {
		return CodexTokens{}, fmt.Errorf("token exchange returned no tokens")
	}
	t := CodexTokens{AccessToken: got.AccessToken, RefreshToken: got.RefreshToken, IDToken: got.IDToken, LastRefresh: time.Now().UTC()}
	// Checked under the lock: a sign-out that cancelled this login while the
	// exchange was in flight must not be undone by the save behind it.
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return CodexTokens{}, fmt.Errorf("sign-in not completed: %w", err)
	}
	if err := c.saveLocked(t); err != nil {
		return CodexTokens{}, err
	}
	return t, nil
}
