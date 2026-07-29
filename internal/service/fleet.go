package service

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/duckling"
	"github.com/jrullan/ducklab/internal/provider"
)

// This file is the fleet: which models exist and how to reach them.
//
// Until now the engine could list ducklings and nothing else, so combining a
// local model with a hosted one meant hand-editing config.toml and restarting.
// That is the difference between a harness someone can use and one they have
// to maintain.
//
// A key is never written here and never returned. A provider stores the *name*
// of an environment variable; the value is read from the environment at call
// time (I10). The API can therefore say "this provider needs OPENROUTER_API_KEY
// and it is not set" without ever holding the secret.

// ProviderView is a provider as a client sees it.
type ProviderView struct {
	ID      string            `json:"id"`
	Kind    string            `json:"kind"`
	BaseURL string            `json:"base_url"`
	Headers map[string]string `json:"headers,omitempty"`
	// APIKeyEnv is the name of the variable the key is read from. Never the
	// key.
	APIKeyEnv string `json:"api_key_env,omitempty"`
	// KeyPresent says whether that variable is set in the engine's
	// environment. A provider configured correctly and missing its key is the
	// commonest reason a duckling fails, and it is invisible otherwise.
	KeyPresent bool `json:"key_present"`
	// InUse lists the ducklings that would break if this provider went away.
	InUse []string `json:"in_use,omitempty"`
}

// DucklingView is a duckling as a client sees it, for editing.
type DucklingView struct {
	ID       string                `json:"id"`
	Provider string                `json:"provider"`
	Model    string                `json:"model"`
	Roles    []string              `json:"roles,omitempty"`
	Notes    string                `json:"notes,omitempty"`
	Params   config.SamplingParams `json:"params"`
	Caps     config.Caps           `json:"caps"`
	Cost     config.Cost           `json:"cost"`
}

// ProviderList returns every configured provider.
func (s *Service) ProviderList() []ProviderView {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()

	used := map[config.ProviderID][]string{}
	for id, d := range s.cfg.Ducklings {
		used[d.Provider] = append(used[d.Provider], string(id))
	}

	out := make([]ProviderView, 0, len(s.cfg.Providers))
	for id, p := range s.cfg.Providers {
		names := used[id]
		sort.Strings(names)
		out = append(out, ProviderView{
			ID: string(id), Kind: string(p.Kind), BaseURL: p.BaseURL,
			Headers: p.Headers, APIKeyEnv: p.APIKeyEnv,
			KeyPresent: p.APIKeyEnv == "" || os.Getenv(p.APIKeyEnv) != "",
			InUse:      names,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ProviderSet adds or replaces a provider and rebuilds what depends on it.
func (s *Service) ProviderSet(id string, view ProviderView) error {
	if err := s.canWriteConfig(); err != nil {
		return err
	}
	if err := validID(id); err != nil {
		return err
	}
	if view.BaseURL == "" {
		return fmt.Errorf("provider %q needs a base_url", id)
	}
	kind := config.ProviderKind(view.Kind)
	if kind == "" {
		kind = config.ProviderKindOpenAI
	}

	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()

	p := config.Provider{
		Kind: kind, BaseURL: view.BaseURL,
		APIKeyEnv: view.APIKeyEnv, Headers: view.Headers,
	}
	// Built before it is saved. A provider whose base URL cannot produce a
	// client is a provider that will fail on the first run instead of here,
	// where the person is looking at the form they just filled in.
	prov, err := createProvider(config.ProviderID(id), p)
	if err != nil {
		return fmt.Errorf("provider %q: %w", id, err)
	}

	if s.cfg.Providers == nil {
		s.cfg.Providers = map[config.ProviderID]config.Provider{}
	}
	s.cfg.Providers[config.ProviderID(id)] = p
	if err := s.saveConfig(); err != nil {
		delete(s.cfg.Providers, config.ProviderID(id))
		return err
	}
	s.providers[config.ProviderID(id)] = prov
	s.ducklings.RegisterProvider(prov)
	return nil
}

// ProviderRemove deletes a provider.
//
// Refused while a duckling still points at it: removing it would leave a
// duckling that lists fine and fails the moment it is used, which is a worse
// state than the one being fixed.
func (s *Service) ProviderRemove(id string) error {
	if err := s.canWriteConfig(); err != nil {
		return err
	}
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()

	if _, ok := s.cfg.Providers[config.ProviderID(id)]; !ok {
		return fmt.Errorf("no provider %q", id)
	}
	var users []string
	for did, d := range s.cfg.Ducklings {
		if d.Provider == config.ProviderID(id) {
			users = append(users, string(did))
		}
	}
	if len(users) > 0 {
		sort.Strings(users)
		return fmt.Errorf("provider %q is used by %v; remove or repoint those ducklings first", id, users)
	}

	saved := s.cfg.Providers[config.ProviderID(id)]
	delete(s.cfg.Providers, config.ProviderID(id))
	if err := s.saveConfig(); err != nil {
		s.cfg.Providers[config.ProviderID(id)] = saved
		return err
	}
	delete(s.providers, config.ProviderID(id))
	return nil
}

// DucklingSet adds or replaces a duckling.
func (s *Service) DucklingSet(id string, view DucklingView) error {
	if err := s.canWriteConfig(); err != nil {
		return err
	}
	if err := validID(id); err != nil {
		return err
	}
	if view.Model == "" {
		return fmt.Errorf("duckling %q needs a model", id)
	}

	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()

	if _, ok := s.cfg.Providers[config.ProviderID(view.Provider)]; !ok {
		// Named, with what does exist. A duckling pointing at a provider that
		// is not there is the same failure as a typo, and the fix is the same.
		have := make([]string, 0, len(s.cfg.Providers))
		for pid := range s.cfg.Providers {
			have = append(have, string(pid))
		}
		sort.Strings(have)
		return fmt.Errorf("duckling %q names provider %q, which does not exist; configured: %v",
			id, view.Provider, have)
	}

	roles := make([]config.Role, 0, len(view.Roles))
	for _, r := range view.Roles {
		roles = append(roles, config.Role(r))
	}
	d := config.Duckling{
		Provider: config.ProviderID(view.Provider), Model: view.Model,
		Roles: roles, Notes: view.Notes,
		Params: view.Params, Caps: view.Caps, Cost: view.Cost,
	}

	if s.cfg.Ducklings == nil {
		s.cfg.Ducklings = map[config.DucklingID]config.Duckling{}
	}
	previous, existed := s.cfg.Ducklings[config.DucklingID(id)]
	s.cfg.Ducklings[config.DucklingID(id)] = d
	if err := s.saveConfig(); err != nil {
		if existed {
			s.cfg.Ducklings[config.DucklingID(id)] = previous
		} else {
			delete(s.cfg.Ducklings, config.DucklingID(id))
		}
		return err
	}
	if err := s.ducklings.Register(duckling.FromConfig(config.DucklingID(id), d)); err != nil {
		return fmt.Errorf("register %q: %w", id, err)
	}
	return nil
}

// DucklingRemove deletes a duckling.
func (s *Service) DucklingRemove(id string) error {
	if err := s.canWriteConfig(); err != nil {
		return err
	}
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()

	previous, ok := s.cfg.Ducklings[config.DucklingID(id)]
	if !ok {
		return fmt.Errorf("no duckling %q", id)
	}
	delete(s.cfg.Ducklings, config.DucklingID(id))
	if err := s.saveConfig(); err != nil {
		s.cfg.Ducklings[config.DucklingID(id)] = previous
		return err
	}
	s.ducklings.Unregister(config.DucklingID(id))
	return nil
}

// DucklingGet returns one duckling's editable form.
func (s *Service) DucklingGet(ctx context.Context, id string) (*DucklingView, error) {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()

	d, ok := s.cfg.Ducklings[config.DucklingID(id)]
	if !ok {
		return nil, fmt.Errorf("no duckling %q", id)
	}
	roles := make([]string, 0, len(d.Roles))
	for _, r := range d.Roles {
		roles = append(roles, string(r))
	}
	return &DucklingView{
		ID: id, Provider: string(d.Provider), Model: d.Model,
		Roles: roles, Notes: d.Notes,
		Params: d.Params, Caps: d.Caps, Cost: d.Cost,
	}, nil
}

// saveConfig writes the whole global config back.
//
// The caller holds cfgMu and restores what it changed if this fails, so a
// rejected write leaves memory and disk saying the same thing. A config the
// engine believes and the file contradicts is the worst outcome available.
func (s *Service) saveConfig() error {
	if err := s.canWriteConfig(); err != nil {
		return err
	}
	if err := config.SaveGlobal(s.configPath, s.cfg); err != nil {
		return fmt.Errorf("write %s: %w", s.configPath, err)
	}
	return nil
}

// canWriteConfig reports whether changes can be persisted at all.
//
// Checked before validating anything else: an engine with nowhere to write is
// the fundamental problem, and telling someone their provider id is malformed
// when the real answer is "nothing you type can be saved" wastes their time.
func (s *Service) canWriteConfig() error {
	if s.configPath == "" {
		return fmt.Errorf("this engine was started without a config file, so ducklings and providers cannot be changed")
	}
	return nil
}

// validID rejects names that cannot be typed as a flag or a TOML key.
func validID(id string) error {
	if id == "" {
		return fmt.Errorf("an id is required")
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return fmt.Errorf("id %q must be lowercase letters, digits, dashes and underscores", id)
		}
	}
	return nil
}

// ProviderKinds are the kinds a provider may be, for a client's form.
func ProviderKinds() []string {
	return []string{
		string(config.ProviderKindOpenAI),
		string(config.ProviderKindAnthropic),
	}
}

var _ = provider.Provider(nil)
