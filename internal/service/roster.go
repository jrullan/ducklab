package service

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/duckling"
)

// RosterEntry is one role assignment, with where it came from.
type RosterEntry struct {
	Role     string `json:"role"`
	Duckling string `json:"duckling"`
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
	resolved, _ := s.resolveRoster(projCfg)

	lineup := map[config.Role]bool{}
	if mode != "" {
		for _, role := range applyStageLineup(resolved, s.ducklingsFor(mode, nil)) {
			lineup[role] = true
		}
	}
	view := &RosterView{Warning: bothSidesWarning(resolved)}

	for _, role := range config.ValidRoles() {
		if role == config.RoleHuman {
			continue
		}
		source := "default"
		if id, ok := projCfg.Roster[role]; ok && id != "" {
			source = "project"
		}
		if lineup[role] {
			source = mode + " line-up"
		}
		view.Entries = append(view.Entries, RosterEntry{
			Role: string(role), Duckling: string(resolved[role]), Source: source,
		})
	}
	sort.Slice(view.Entries, func(i, j int) bool { return view.Entries[i].Role < view.Entries[j].Role })
	return view, nil
}

// RosterSet assigns a duckling to a role and persists it to project.toml.
func (s *Service) RosterSet(ctx context.Context, projectID, role, ducklingID string) (*RosterView, error) {
	if !validRole(config.Role(role)) {
		return nil, fmt.Errorf("unknown role %q (valid: %s)", role, rolesList())
	}
	if _, err := s.ducklings.Get(config.DucklingID(ducklingID)); err != nil {
		return nil, fmt.Errorf("unknown duckling %q", ducklingID)
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
	projCfg.Roster[config.Role(role)] = config.DucklingID(ducklingID)

	path := filepath.Join(entry.Path, ".ducklab", "project.toml")
	if err := writeProjectTOML(path, projCfg); err != nil {
		return nil, fmt.Errorf("write project.toml: %w", err)
	}
	return s.RosterGet(ctx, projectID, "")
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
