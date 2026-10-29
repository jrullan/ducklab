// Package agenttoken holds the person's per-agent credentials (B-550).
//
// B-530 split the engine's one token in two: the person's and the agents'.
// The agents' was still one token for every agent — anything that read
// engine.json — so the engine could not tell OpenCode from Claude Code, and
// every agent reached every project. Here the person issues each agent its
// own token, names it, and grants it projects at a level. The engine derives
// the actor from the token and refuses whatever the grants do not cover.
//
// Only a hash of each secret is kept. The secret is shown once, when the
// token is created, and cannot be recovered: a lost token is revoked and
// issued again.
package agenttoken

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jrullan/ducklab/internal/xplat"
)

// Level is what a token may do in a granted project.
type Level string

const (
	// LevelRead reads: every GET, plus the few POSTs that only compute an
	// answer (an artifact lint).
	LevelRead Level = "read-only"
	// LevelFull is everything an agent may do there — still never what is
	// the person's alone (approving, lifting a budget, signing in).
	LevelFull Level = "full"
)

// Allows says whether a token holding l may act at need.
func (l Level) Allows(need Level) bool {
	switch l {
	case LevelFull:
		return need == LevelRead || need == LevelFull
	case LevelRead:
		return need == LevelRead
	}
	return false
}

// ParseLevel accepts the two levels, plus "read" as a short form.
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "read-only", "read", "readonly":
		return LevelRead, nil
	case "full":
		return LevelFull, nil
	}
	return "", fmt.Errorf("unknown access level %q: use read-only or full", s)
}

// Grant is one project a token may reach.
type Grant struct {
	Project string `json:"project"`
	Level   Level  `json:"level"`
}

// Token is a stored credential. Hash never leaves this package.
type Token struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Hint is the secret's first characters, so the person can tell which
	// token a configuration holds without the secret being stored.
	Hint       string     `json:"hint"`
	Hash       string     `json:"hash"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	Grants     []Grant    `json:"grants"`
}

// View is a token as the API shows it: everything but the hash.
type View struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Actor      string     `json:"actor"`
	Hint       string     `json:"hint"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	Grants     []Grant    `json:"grants"`
}

// Identity is who a request's token says it is.
type Identity struct {
	ID   string
	Name string
	// Actor is what the engine records: "agent:<slug of name>".
	Actor  string
	grants map[string]Level
}

// Level is the identity's access to project, if any.
func (i *Identity) Level(project string) (Level, bool) {
	if i == nil {
		return "", false
	}
	l, ok := i.grants[project]
	return l, ok
}

// Projects lists the granted project ids, sorted.
func (i *Identity) Projects() []string {
	if i == nil {
		return nil
	}
	out := make([]string, 0, len(i.grants))
	for p := range i.grants {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

type stored struct {
	// LegacyDisabled turns off the shared agent token in engine.json. The
	// zero value keeps it on: every integration that predates per-agent
	// tokens keeps working until the person moves it over and says so.
	LegacyDisabled bool    `json:"legacy_disabled"`
	Tokens         []Token `json:"tokens"`
}

// lastUsedFlush bounds how often recording a token's use rewrites the file.
const lastUsedFlush = time.Minute

// Store is the token file plus the engine's in-memory ephemeral tokens.
type Store struct {
	mu        sync.Mutex
	path      string
	data      stored
	byHash    map[string]int
	ephemeral map[string]*Identity
	flushed   time.Time
	now       func() time.Time
}

// ErrNotFound is a token id the store does not hold.
var ErrNotFound = errors.New("no such agent token")

// Open loads the store at path; a missing file is an empty store.
func Open(path string) (*Store, error) {
	s := &Store{path: path, ephemeral: map[string]*Identity{}, now: time.Now}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("read agent tokens: %w", err)
	default:
		if err := json.Unmarshal(raw, &s.data); err != nil {
			return nil, fmt.Errorf("parse agent tokens %s: %w", path, err)
		}
	}
	s.reindex()
	return s, nil
}

func (s *Store) reindex() {
	s.byHash = make(map[string]int, len(s.data.Tokens))
	for i, t := range s.data.Tokens {
		s.byHash[t.Hash] = i
	}
}

func (s *Store) saveLocked() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if err := xplat.AtomicWrite(s.path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write agent tokens: %w", err)
	}
	s.flushed = s.now()
	return nil
}

func hashOf(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// secretPrefix marks a per-agent token, so one pasted into the wrong place
// is recognisable.
const secretPrefix = "dlk_"

// Slug is the actor-safe form of a name: lower case, runs of anything but a
// letter or digit become one hyphen.
func Slug(name string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			hyphen = false
			continue
		}
		if !hyphen && b.Len() > 0 {
			b.WriteByte('-')
			hyphen = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

func actorOf(name string) string { return "agent:" + Slug(name) }

func view(t Token) View {
	grants := append([]Grant{}, t.Grants...)
	return View{ID: t.ID, Name: t.Name, Actor: actorOf(t.Name), Hint: t.Hint, CreatedAt: t.CreatedAt,
		LastUsedAt: t.LastUsedAt, RevokedAt: t.RevokedAt, Grants: grants}
}

// cleanGrants validates grants and returns them sorted by project. Whether a
// project exists is the caller's to check: the store does not know projects.
func cleanGrants(grants []Grant) ([]Grant, error) {
	out := make([]Grant, 0, len(grants))
	seen := map[string]bool{}
	for _, g := range grants {
		p := strings.TrimSpace(g.Project)
		if p == "" {
			return nil, errors.New("a grant needs a project")
		}
		if seen[p] {
			return nil, fmt.Errorf("project %q is granted twice", p)
		}
		seen[p] = true
		l, err := ParseLevel(string(g.Level))
		if err != nil {
			return nil, fmt.Errorf("project %q: %w", p, err)
		}
		out = append(out, Grant{Project: p, Level: l})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Project < out[j].Project })
	return out, nil
}

// checkNameLocked refuses an empty name and one whose actor another live
// token already records under: two agents written down as the same actor is
// the confusion per-agent tokens exist to end.
func (s *Store) checkNameLocked(name, exceptID string) (string, error) {
	name = strings.TrimSpace(name)
	if Slug(name) == "" {
		return "", errors.New("a token needs a name with at least one letter or digit, e.g. \"OpenCode ti36\"")
	}
	for _, t := range s.data.Tokens {
		if t.ID != exceptID && t.RevokedAt == nil && Slug(t.Name) == Slug(name) {
			return "", fmt.Errorf("token %s (%q) already records as %s; choose another name", t.ID, t.Name, actorOf(name))
		}
	}
	return name, nil
}

// List returns every token, live and revoked, oldest first.
func (s *Store) List() []View {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]View, 0, len(s.data.Tokens))
	for _, t := range s.data.Tokens {
		out = append(out, view(t))
	}
	return out
}

// Get returns one token.
func (s *Store) Get(id string) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexLocked(id)
	if i < 0 {
		return View{}, ErrNotFound
	}
	return view(s.data.Tokens[i]), nil
}

func (s *Store) indexLocked(id string) int {
	for i, t := range s.data.Tokens {
		if t.ID == id {
			return i
		}
	}
	return -1
}

// Create issues a token and returns its view and the secret — the only time
// the secret exists outside the agent that will hold it.
func (s *Store) Create(name string, grants []Grant) (View, string, error) {
	clean, err := cleanGrants(grants)
	if err != nil {
		return View{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	name, err = s.checkNameLocked(name, "")
	if err != nil {
		return View{}, "", err
	}
	raw, err := randomHex(24)
	if err != nil {
		return View{}, "", err
	}
	idHex, err := randomHex(4)
	if err != nil {
		return View{}, "", err
	}
	secret := secretPrefix + raw
	t := Token{ID: "at_" + idHex, Name: name, Hint: secret[:len(secretPrefix)+6], Hash: hashOf(secret),
		CreatedAt: s.now().UTC(), Grants: clean}
	s.data.Tokens = append(s.data.Tokens, t)
	if err := s.saveLocked(); err != nil {
		s.data.Tokens = s.data.Tokens[:len(s.data.Tokens)-1]
		return View{}, "", err
	}
	s.reindex()
	return view(t), secret, nil
}

// Update renames a live token and/or replaces its grants. A nil name or nil
// grants leaves that part alone; an empty grants list removes every grant.
func (s *Store) Update(id string, name *string, grants []Grant) (View, error) {
	var clean []Grant
	if grants != nil {
		var err error
		if clean, err = cleanGrants(grants); err != nil {
			return View{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexLocked(id)
	if i < 0 {
		return View{}, ErrNotFound
	}
	t := s.data.Tokens[i]
	if t.RevokedAt != nil {
		return View{}, fmt.Errorf("token %s is revoked; issue a new one", id)
	}
	if name != nil {
		n, err := s.checkNameLocked(*name, id)
		if err != nil {
			return View{}, err
		}
		t.Name = n
	}
	if grants != nil {
		t.Grants = clean
	}
	prev := s.data.Tokens[i]
	s.data.Tokens[i] = t
	if err := s.saveLocked(); err != nil {
		s.data.Tokens[i] = prev
		return View{}, err
	}
	return view(t), nil
}

// Revoke ends a token for good. Its record stays, so the history of who
// held what remains readable.
func (s *Store) Revoke(id string) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexLocked(id)
	if i < 0 {
		return View{}, ErrNotFound
	}
	if s.data.Tokens[i].RevokedAt != nil {
		return view(s.data.Tokens[i]), nil
	}
	prev := s.data.Tokens[i]
	now := s.now().UTC()
	s.data.Tokens[i].RevokedAt = &now
	if err := s.saveLocked(); err != nil {
		s.data.Tokens[i] = prev
		return View{}, err
	}
	return view(s.data.Tokens[i]), nil
}

// Lookup resolves a presented secret to the live token it is, recording the
// use. Revoked and unknown secrets resolve to nothing.
func (s *Store) Lookup(secret string) (*Identity, bool) {
	if !strings.HasPrefix(secret, secretPrefix) {
		return nil, false
	}
	h := hashOf(secret)
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.ephemeral[h]; ok {
		return id, true
	}
	i, ok := s.byHash[h]
	if !ok {
		return nil, false
	}
	t := &s.data.Tokens[i]
	if t.RevokedAt != nil {
		return nil, false
	}
	now := s.now().UTC()
	t.LastUsedAt = &now
	if now.Sub(s.flushed) >= lastUsedFlush {
		// Best effort: a lost last-used stamp is not worth failing a request.
		_ = s.saveLocked()
	}
	grants := make(map[string]Level, len(t.Grants))
	for _, g := range t.Grants {
		grants[g.Project] = g.Level
	}
	return &Identity{ID: t.ID, Name: t.Name, Actor: actorOf(t.Name), grants: grants}, true
}

// Issue mints an in-memory token for the engine's own helpers — a renderer
// pointed at a project's app — that never touches the file and dies with the
// returned revoke or the engine.
func (s *Store) Issue(name string, grants []Grant) (string, func(), error) {
	clean, err := cleanGrants(grants)
	if err != nil {
		return "", nil, err
	}
	raw, err := randomHex(24)
	if err != nil {
		return "", nil, err
	}
	secret := secretPrefix + raw
	m := make(map[string]Level, len(clean))
	for _, g := range clean {
		m[g.Project] = g.Level
	}
	h := hashOf(secret)
	s.mu.Lock()
	s.ephemeral[h] = &Identity{ID: "ephemeral", Name: name, Actor: actorOf(name), grants: m}
	s.mu.Unlock()
	return secret, func() {
		s.mu.Lock()
		delete(s.ephemeral, h)
		s.mu.Unlock()
	}, nil
}

// LegacyEnabled says whether the shared engine.json token still
// authenticates.
func (s *Store) LegacyEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.data.LegacyDisabled
}

// SetLegacyEnabled turns the shared agent token on or off.
func (s *Store) SetLegacyEnabled(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.data.LegacyDisabled
	s.data.LegacyDisabled = !enabled
	if err := s.saveLocked(); err != nil {
		s.data.LegacyDisabled = prev
		return err
	}
	return nil
}

// Flush writes any last-used stamps still only in memory.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data.Tokens) == 0 && !s.data.LegacyDisabled {
		if _, err := os.Stat(s.path); errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	return s.saveLocked()
}
