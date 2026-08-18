package service

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/duckling"
)

// RosterEntry is one role assignment, with where it came from.
type RosterEntry struct {
	Role      string   `json:"role"`
	Duckling  string   `json:"duckling"`
	Ducklings []string `json:"ducklings"`
	// Default is the all-projects choice when this project overrides the role.
	// It lets clients explain both values instead of presenting the project value
	// as if it were the global default.
	Default string `json:"default,omitempty"`
	// Source is "project" when project.toml declares it, "default" when the
	// engine picked it. A user needs to know which assignments are theirs.
	Source string `json:"source"`
}

// RosterView is the resolved roster for a project.
type RosterView struct {
	Entries []RosterEntry `json:"entries"`
	Warning string        `json:"warning,omitempty"`
}

// RosterGet returns the roster as it will actually be used, not just what the
// file declares — an undeclared role still gets a duckling, and hiding that
// would make the roster look emptier than the runs behave.
// RosterGet answers who would play each role — for the given mode, when one is
// named.
//
// The mode matters because a saved line-up overrides the roster at run time,
// and this answer is what the desktop shows beside the start button. Without
// the mode it described a different run than the one about to start: a person
// saved k3-then-sonnet for council, and the preview went on warning that
// sonnet would critique its own draft — reading the roster while the run
// would read the line-up. A preview that lies about who will run is worse
// than none: it tells the person their setting did not take.
func (s *Service) RosterGet(ctx context.Context, projectID, mode string) (*RosterView, error) {
	projCfg, err := s.projectConfig(projectID)
	if err != nil {
		return nil, err
	}
	resolved, sources := s.resolveCanonicalRoster(projCfg, mode)
	warning := ""
	if mode == "" || mode == "pair" {
		warning = bothSidesWarning(resolved)
	}
	// The board's per-mode note: what a launch of this mode would refuse
	// today (a tournament with one contestant, a split with one worker).
	if mode != "" {
		if err := s.validateRosterMode(mode, projCfg); err != nil {
			note := fmt.Sprintf("not runnable yet: %v", err)
			if warning == "" {
				warning = note
			} else {
				warning += "; " + note
			}
		}
	}
	view := &RosterView{Warning: warning}

	for _, role := range config.ValidRoles() {
		if role == config.RoleHuman {
			continue
		}
		canonicalSource := sources[role]
		entry := RosterEntry{Role: string(role), Duckling: string(resolved[role]), Ducklings: s.rosterIDs(projCfg, mode, role), Source: canonicalSource}
		if canonicalSource == "project pin" || canonicalSource == "project mode seat" {
			withoutPin := *projCfg
			withoutPin.Roster = make(config.Roster, len(projCfg.Roster))
			for r, id := range projCfg.Roster {
				if r != role {
					withoutPin.Roster[r] = id
				}
			}
			withoutPin.ModeSeats = map[string]map[string][]string{}
			for m, seats := range projCfg.ModeSeats {
				withoutPin.ModeSeats[m] = map[string][]string{}
				for r, ids := range seats {
					if !(m == mode && r == string(role)) {
						withoutPin.ModeSeats[m][r] = append([]string{}, ids...)
					}
				}
			}
			withoutPin.RosterSeats = make(map[config.Role][]config.DucklingID, len(projCfg.RosterSeats))
			for r, ids := range projCfg.RosterSeats {
				if r != role {
					withoutPin.RosterSeats[r] = append([]config.DucklingID{}, ids...)
				}
			}
			defaults, _ := s.resolveCanonicalRoster(&withoutPin, mode)
			entry.Default = string(defaults[role])
		}
		view.Entries = append(view.Entries, entry)
	}
	sort.SliceStable(view.Entries, func(i, j int) bool { return view.Entries[i].Role < view.Entries[j].Role })
	return view, nil
}

// GlobalRosterGet returns effective global seats with canonical provenance.
func (s *Service) GlobalRosterGet(ctx context.Context, mode string) (*RosterView, error) {
	resolved, sources := s.resolveCanonicalRoster(&config.Project{}, mode)
	view := &RosterView{Warning: bothSidesWarning(resolved)}
	// The same per-mode note the project board gets: what a launch of this
	// mode would refuse today, from the global seats alone.
	if mode != "" {
		if err := s.validateRosterMode(mode, &config.Project{}); err != nil {
			note := fmt.Sprintf("not runnable yet: %v", err)
			if view.Warning == "" {
				view.Warning = note
			} else {
				view.Warning += "; " + note
			}
		}
	}
	for _, role := range config.ValidRoles() {
		if role == config.RoleHuman {
			continue
		}
		ids := []string{}
		s.cfgMu.RLock()
		if v := s.cfg.Defaults.ModeSeats[mode][string(role)]; len(v) > 0 {
			ids = append(ids, v...)
		} else {
			ids = append(ids, s.cfg.Defaults.RolePins[string(role)]...)
		}
		s.cfgMu.RUnlock()
		view.Entries = append(view.Entries, RosterEntry{Role: string(role), Duckling: string(resolved[role]), Ducklings: ids, Source: sources[role]})
	}
	return view, nil
}

// GlobalRosterSet replaces one complete global seat list.
func (s *Service) GlobalRosterSet(ctx context.Context, mode, role string, ids []string) (*RosterView, error) {
	if !validRole(config.Role(role)) || role == string(config.RoleHuman) {
		return nil, fmt.Errorf("field role invalid for roster: %q; next: choose a board role", role)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("field ducklings must be a non-empty ordered list; next: provide duckling IDs")
	}
	for _, id := range ids {
		if _, err := s.ducklings.Get(config.DucklingID(id)); err != nil {
			return nil, fmt.Errorf("no duckling %q — registered: %s; next: choose a registered duckling", id, registeredDucklings(s))
		}
	}
	v := s.ModeDefaults()
	if v.ModeSeats == nil {
		v.ModeSeats = map[string]map[string][]string{}
	}
	if v.ModeSeats[mode] == nil {
		v.ModeSeats[mode] = map[string][]string{}
	}
	if role == "triager" || role == "scribe" {
		if v.RolePins == nil {
			v.RolePins = map[string][]string{}
		}
		v.RolePins[role] = append([]string{}, ids...)
	} else {
		v.ModeSeats[mode][role] = append([]string{}, ids...)
	}
	if err := s.ModeDefaultsSet(v); err != nil {
		return nil, fmt.Errorf("field mode/ducklings: %v; next: provide a valid complete roster", err)
	}
	return s.GlobalRosterGet(ctx, mode)
}

// ProjectAutonomy reports the project's declared autonomy, empty when it
// defers to the global default.
func (s *Service) ProjectAutonomy(projectID string) (string, error) {
	projCfg, err := s.projectConfig(projectID)
	if err != nil {
		return "", err
	}
	return string(projCfg.Autonomy), nil
}

// ProjectAutonomySet persists the project's autonomy to project.toml — the
// level the triage resolver and run defaults consult FIRST. It had no
// control anywhere: the harness's own guidance was "edit the TOML", the
// exact reflex this product exists to remove. Empty clears the override
// back to the global default.
func (s *Service) ProjectAutonomySet(projectID, autonomy string) error {
	if autonomy != "" {
		if err := config.ValidateAutonomy(config.Autonomy(autonomy)); err != nil {
			return err
		}
	}
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return err
	}
	projCfg, err := s.projectConfig(projectID)
	if err != nil {
		return err
	}
	projCfg.Autonomy = config.Autonomy(autonomy)
	path := filepath.Join(entry.Path, ".ducklab", "project.toml")
	if err := writeProjectTOML(path, projCfg); err != nil {
		return fmt.Errorf("write project.toml: %w", err)
	}
	return nil
}

// RosterSet assigns a duckling to a role and persists it to project.toml.
func (s *Service) RosterSet(ctx context.Context, projectID, role, ducklingID string) (*RosterView, error) {
	return s.RosterSetMany(ctx, projectID, role, []string{ducklingID})
}

// RosterSetMany replaces the complete ordered project pin for a role.
func (s *Service) RosterSetMany(ctx context.Context, projectID, role string, ducklingIDs []string) (*RosterView, error) {
	return s.RosterSetManyMode(ctx, projectID, "", role, ducklingIDs)
}

// RosterSetManyMode replaces a project pin and validates the complete mode roster.
func (s *Service) RosterSetManyMode(ctx context.Context, projectID, mode, role string, ducklingIDs []string) (*RosterView, error) {
	if !validRole(config.Role(role)) || role == string(config.RoleHuman) {
		return nil, fmt.Errorf("field role invalid for roster: %q (valid: %s); next: choose a board role", role, rolesList())
	}
	if len(ducklingIDs) == 0 {
		return nil, fmt.Errorf("field ducklings must be a non-empty ordered list; next: provide duckling IDs")
	}
	for _, ducklingID := range ducklingIDs {
		if _, err := s.ducklings.Get(config.DucklingID(ducklingID)); err != nil {
			return nil, fmt.Errorf("no duckling %q — registered: %s; next: choose a registered duckling", ducklingID, registeredDucklings(s))
		}
	}

	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	projCfg, err := s.projectConfig(projectID)
	if err != nil {
		return nil, err
	}
	if projCfg.Roster == nil {
		projCfg.Roster = config.Roster{}
	}
	ids := make([]config.DucklingID, len(ducklingIDs))
	for i, id := range ducklingIDs {
		ids[i] = config.DucklingID(id)
	}
	if mode != "" {
		// A pin made for ONE mode lands in the project's per-mode seats —
		// the same shape as the global defaults — and touches no other mode.
		// It used to become a role pin, so seating atom-local for solo's
		// implementer seated it for pair, split and tournament too (B-068).
		if projCfg.ModeSeats == nil {
			projCfg.ModeSeats = map[string]map[string][]string{}
		}
		if projCfg.ModeSeats[mode] == nil {
			projCfg.ModeSeats[mode] = map[string][]string{}
		}
		projCfg.ModeSeats[mode][role] = append([]string{}, ducklingIDs...)
	} else {
		// No mode: a role pin, mode-independent (triager, scribe, or a
		// deliberate "this project always implements with X"). Keep the
		// legacy scalar mirror for readers that still understand it.
		projCfg.Roster[config.Role(role)] = config.DucklingID(ducklingIDs[0])
		if projCfg.RosterSeats == nil {
			projCfg.RosterSeats = map[config.Role][]config.DucklingID{}
		}
		projCfg.RosterSeats[config.Role(role)] = ids
	}

	// Cardinality is a LAUNCH rule, not a write rule: a tournament is built
	// one contestant at a time, and refusing the first pin because there is
	// no second yet made the seat impossible to fill from the board. The
	// write lands; the board is told what the launch will still refuse; the
	// launch itself (RunStart) keeps the hard check.
	path := filepath.Join(entry.Path, ".ducklab", "project.toml")
	if err := writeProjectTOML(path, projCfg); err != nil {
		return nil, fmt.Errorf("write project.toml: %w", err)
	}
	return s.RosterGet(ctx, projectID, mode)
}

func (s *Service) validateRosterMode(mode string, proj *config.Project) error {
	if mode == "" {
		return nil
	}
	count := 0
	role := ""
	switch mode {
	case "council":
		role = "reviewer"
	case "split", "tournament":
		role = "implementer"
	default:
		return nil
	}
	if proj.ModeSeats != nil && len(proj.ModeSeats[mode][role]) > 0 {
		count = len(proj.ModeSeats[mode][role])
	} else if ids := proj.RosterSeats[config.Role(role)]; len(ids) > 0 {
		count = len(ids)
	} else if id := proj.Roster[config.Role(role)]; id != "" {
		count = 1
	} else {
		s.cfgMu.RLock()
		count = len(s.cfg.Defaults.ModeSeats[mode][role])
		s.cfgMu.RUnlock()
	}
	if mode == "council" && count < 1 {
		return fmt.Errorf("council requires at least one critic")
	}
	if (mode == "split" || mode == "tournament") && count < 2 {
		return fmt.Errorf("%s requires at least two %s", mode, map[string]string{"split": "workers", "tournament": "contestants"}[mode])
	}
	return nil
}

func (s *Service) RosterUnpin(ctx context.Context, projectID, mode, role string) (*RosterView, error) {
	if !validRole(config.Role(role)) || role == string(config.RoleHuman) {
		return nil, fmt.Errorf("field role invalid for roster: %q; next: choose a valid role", role)
	}
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	projCfg, err := s.projectConfig(projectID)
	if err != nil {
		return nil, err
	}
	// Unpinning on a mode's column removes that mode's seat; when the only
	// pin behind the card is a role pin (mode-independent), that is what
	// goes — and it goes for every mode, because that is what it was.
	removed := false
	if mode != "" && projCfg.ModeSeats != nil && projCfg.ModeSeats[mode] != nil {
		if _, ok := projCfg.ModeSeats[mode][role]; ok {
			delete(projCfg.ModeSeats[mode], role)
			if len(projCfg.ModeSeats[mode]) == 0 {
				delete(projCfg.ModeSeats, mode)
			}
			removed = true
		}
	}
	if !removed {
		delete(projCfg.Roster, config.Role(role))
		delete(projCfg.RosterSeats, config.Role(role))
	}
	if err := writeProjectTOML(filepath.Join(entry.Path, ".ducklab", "project.toml"), projCfg); err != nil {
		return nil, fmt.Errorf("write project.toml: %w", err)
	}
	return s.RosterGet(ctx, projectID, mode)
}

func (s *Service) rosterIDs(proj *config.Project, mode string, role config.Role) []string {
	if proj.ModeSeats != nil && len(proj.ModeSeats[mode][string(role)]) > 0 {
		return append([]string{}, proj.ModeSeats[mode][string(role)]...)
	}
	if ids := proj.RosterSeats[role]; len(ids) > 0 {
		out := make([]string, len(ids))
		for i, id := range ids {
			out[i] = string(id)
		}
		return out
	}
	if id := proj.Roster[role]; id != "" {
		return []string{string(id)}
	}
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	if ids := s.cfg.Defaults.ModeSeats[mode][string(role)]; len(ids) > 0 {
		return append([]string{}, ids...)
	}
	return append([]string{}, s.cfg.Defaults.RolePins[string(role)]...)
}

func registeredDucklings(s *Service) string {
	ids := make([]string, 0)
	for _, d := range s.ducklings.List() {
		ids = append(ids, string(d.ID))
	}
	sort.Strings(ids)
	return strings.Join(ids, ", ")
}

// Suggestion is one ranked candidate for a role.
type Suggestion struct {
	Role     string  `json:"role"`
	Duckling string  `json:"duckling"`
	Runs     int     `json:"runs"`
	PassRate float64 `json:"pass_rate"`
	CostPerM float64 `json:"cost_per_mtok"`
	Evidence string  `json:"evidence"`
}

// RosterSuggest ranks ducklings per role from recorded results.
//
// Deterministic and model-free by design (03-CLI.md §3.3): a recommendation
// produced by asking a model which model to use would be exactly the kind of
// unfalsifiable claim this project exists to avoid. Ranking is by recorded
// pass rate, ties broken by cost, then by id so the output never reorders
// between identical runs.
func (s *Service) RosterSuggest(ctx context.Context, projectID string) ([]Suggestion, error) {
	runs, err := s.RunList(ctx, RunFilter{ProjectID: projectID})
	if err != nil {
		return nil, err
	}

	type stat struct{ runs, passed int }
	// role -> duckling -> stat
	byRole := map[string]map[string]*stat{}
	for _, r := range runs {
		if r == nil || r.Verdict == "" {
			continue
		}
		for role, id := range r.Roster {
			if id == "" {
				continue
			}
			if byRole[role] == nil {
				byRole[role] = map[string]*stat{}
			}
			st := byRole[role][id]
			if st == nil {
				st = &stat{}
				byRole[role][id] = st
			}
			st.runs++
			if r.Verdict == "PASSED" {
				st.passed++
			}
		}
	}

	var out []Suggestion
	for _, role := range config.ValidRoles() {
		if role == config.RoleHuman {
			continue
		}
		var cands []Suggestion
		for _, d := range s.ducklings.List() {
			st := byRole[string(role)][string(d.ID)]
			sg := Suggestion{
				Role: string(role), Duckling: string(d.ID),
				CostPerM: d.Cost.OutputPerMTok,
			}
			if st != nil && st.runs > 0 {
				sg.Runs = st.runs
				sg.PassRate = float64(st.passed) / float64(st.runs) * 100
				sg.Evidence = fmt.Sprintf("%d/%d passed in this role", st.passed, st.runs)
			} else {
				sg.Evidence = "no recorded runs in this role"
			}
			cands = append(cands, sg)
		}
		sort.Slice(cands, func(i, j int) bool {
			// A duckling with no history must not outrank one with a proven
			// record just because 0 sorts somewhere convenient.
			if (cands[i].Runs == 0) != (cands[j].Runs == 0) {
				return cands[j].Runs == 0
			}
			if cands[i].PassRate != cands[j].PassRate {
				return cands[i].PassRate > cands[j].PassRate
			}
			if cands[i].CostPerM != cands[j].CostPerM {
				return cands[i].CostPerM < cands[j].CostPerM
			}
			return cands[i].Duckling < cands[j].Duckling
		})
		if len(cands) > 0 {
			out = append(out, cands[0])
		}
	}
	return out, nil
}

// RosterApply writes a suggestion set to project.toml.
func (s *Service) RosterApply(ctx context.Context, projectID string, sugg []Suggestion) (*RosterView, error) {
	for _, sg := range sugg {
		if _, err := s.RosterSet(ctx, projectID, sg.Role, sg.Duckling); err != nil {
			return nil, err
		}
	}
	return s.RosterGet(ctx, projectID, "")
}

// DucklingProbeForce re-probes a duckling, ignoring the cache.
func (s *Service) DucklingProbeForce(ctx context.Context, id string) (*duckling.Capabilities, error) {
	return s.ducklings.ProbeForce(ctx, config.DucklingID(id))
}

// projectConfig loads a project's config by id.
func (s *Service) projectConfig(projectID string) (*config.Project, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	return config.LoadProject(filepath.Join(entry.Path, ".ducklab", "project.toml"))
}

func validRole(r config.Role) bool {
	for _, valid := range config.ValidRoles() {
		if r == valid {
			return true
		}
	}
	return false
}

func rolesList() string {
	var names []string
	for _, r := range config.ValidRoles() {
		if r != config.RoleHuman {
			names = append(names, string(r))
		}
	}
	return joinComma(names)
}

func joinComma(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}
