package service

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/bench"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/report"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/xplat"
)

type Scorecard struct {
	ID       string            `json:"id"`
	Provider string            `json:"provider"`
	Model    string            `json:"model"`
	Locality string            `json:"locality"`
	Cost     config.Cost       `json:"cost"`
	Caps     config.Caps       `json:"caps"`
	Roles    []string          `json:"roles,omitempty"`
	Notes    string            `json:"notes,omitempty"`
	Measured *MeasuredEvidence `json:"measured,omitempty"`
	// MeasuredByRole is the same evidence split by the seat the duckling
	// held: a reviewer's pass rate is not its implementer's.
	MeasuredByRole map[string]MeasuredEvidence `json:"measured_by_role,omitempty"`
	Bench          map[string]BenchEvidence    `json:"bench,omitempty"`
	Index          *config.ExternalIndex       `json:"index,omitempty"`
}
type MeasuredEvidence struct {
	Runs          int     `json:"runs"`
	PassRate      float64 `json:"pass_rate"`
	AvgCostPerRun float64 `json:"avg_cost_usd"`
	AvgWallclock  float64 `json:"avg_wallclock_s"`
	Tokens        int64   `json:"tokens"`
	Estimated     bool    `json:"estimated"`
}
type BenchEvidence struct {
	Score        float64 `json:"score,omitempty"`
	Suite        string  `json:"suite"`
	SuiteVersion int     `json:"suite_version"`
	StartedAt    string  `json:"started_at"`
	Verdict      string  `json:"verdict,omitempty"`
	Tokens       int64   `json:"tokens,omitempty"`
	Cost         float64 `json:"cost,omitempty"`
	Wallclock    float64 `json:"wallclock,omitempty"`
	Estimated    bool    `json:"estimated,omitempty"`
}

// IsLocalHost classifies provider endpoints without consulting the network.
func IsLocalHost(base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	h := strings.Trim(u.Hostname(), "[]")
	if h == "localhost" || h == "::1" || net.ParseIP(h).IsLoopback() {
		return true
	}
	ip := net.ParseIP(h)
	if ip != nil {
		return ip.IsPrivate()
	}
	return strings.HasSuffix(strings.ToLower(h), ".local")
}

func (s *Service) Scorecards(ctx context.Context) ([]Scorecard, error) {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	runs := make([]*runlog.Run, 0)
	s.runsMu.RLock()
	for _, r := range s.runs {
		if r != nil {
			runs = append(runs, r.run)
		}
	}
	s.runsMu.RUnlock()
	rep := report.Build(runs, report.Options{By: "duckling"})
	measured := map[string]report.Row{}
	for _, r := range rep.Rows {
		measured[r.Key] = r
	}
	byRole := map[string]map[string]MeasuredEvidence{}
	for _, r := range report.Build(runs, report.Options{By: "duckling_role"}).Rows {
		id, role, ok := strings.Cut(r.Key, "/")
		if !ok {
			continue
		}
		if byRole[id] == nil {
			byRole[id] = map[string]MeasuredEvidence{}
		}
		byRole[id][role] = MeasuredEvidence{Runs: r.Runs, PassRate: r.PassRate(), AvgCostPerRun: r.AvgCost(), AvgWallclock: r.AvgWall().Seconds(), Tokens: r.Tokens, Estimated: r.Estimated}
	}
	benchRows := loadLatestBench()
	// Fetched indices fill the ducklings nobody declared one for; only
	// ducklings on the provider that serves the endpoint are looked up —
	// a local model has no permaslug there and must not borrow one.
	var fetched *indexCache
	if p := openRouterProvider(s.cfg); p != nil {
		fetched = s.indexes.current(ctx, p)
	}
	out := make([]Scorecard, 0, len(s.cfg.Ducklings))
	for id, d := range s.cfg.Ducklings {
		p := s.cfg.Providers[d.Provider]
		c := Scorecard{ID: string(id), Provider: string(d.Provider), Model: d.Model, Cost: d.Cost, Caps: d.Caps, Notes: d.Notes, Index: d.Index}
		if c.Index == nil && isOpenRouter(p) {
			c.Index = externalIndexFor(fetched, d.Model)
		}
		c.Locality = "remote"
		if IsLocalHost(p.BaseURL) {
			c.Locality = "local"
		}
		for _, r := range d.Roles {
			c.Roles = append(c.Roles, string(r))
		}
		if row, ok := measured[string(id)]; ok {
			c.Measured = &MeasuredEvidence{Runs: row.Runs, PassRate: row.PassRate(), AvgCostPerRun: row.AvgCost(), AvgWallclock: row.AvgWall().Seconds(), Tokens: row.Tokens, Estimated: row.Estimated}
		}
		if m, ok := byRole[string(id)]; ok {
			c.MeasuredByRole = m
		}
		if b, ok := benchRows[string(id)]; ok {
			c.Bench = b
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func loadLatestBench() map[string]map[string]BenchEvidence {
	out := map[string]map[string]BenchEvidence{}
	dir, err := xplat.DataDir()
	if err != nil {
		return out
	}
	root := filepath.Join(dir, "bench")
	suites, _ := os.ReadDir(root)
	for _, sd := range suites {
		if !sd.IsDir() {
			continue
		}
		fs, _ := os.ReadDir(filepath.Join(root, sd.Name()))
		for _, f := range fs {
			if filepath.Ext(f.Name()) != ".json" {
				continue
			}
			raw, e := os.ReadFile(filepath.Join(root, sd.Name(), f.Name()))
			if e != nil {
				continue
			}
			var r bench.Result
			if json.Unmarshal(raw, &r) != nil {
				continue
			}
			for _, cell := range r.Cells {
				old, ok := out[cell.Duckling][r.Suite]
				if !ok || r.StartedAt > old.StartedAt {
					if out[cell.Duckling] == nil {
						out[cell.Duckling] = map[string]BenchEvidence{}
					}
					out[cell.Duckling][r.Suite] = BenchEvidence{Score: func() float64 {
						if cell.Verdict == "PASSED" {
							return 1
						}
						return 0
					}(), Suite: r.Suite, SuiteVersion: r.SuiteVersion, StartedAt: r.StartedAt, Verdict: cell.Verdict, Tokens: cell.Tokens, Cost: cell.CostUSD, Wallclock: float64(cell.WallMs) / 1000, Estimated: cell.Estimated}
				}
			}
		}
	}
	return out
}
