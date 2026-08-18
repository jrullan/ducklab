package service

import (
	"context"
	"encoding/json"
	"fmt"
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
	ID       string                   `json:"id"`
	Provider string                   `json:"provider"`
	Model    string                   `json:"model"`
	Locality string                   `json:"locality"`
	Cost     config.Cost              `json:"cost"`
	Caps     config.Caps              `json:"caps"`
	Roles    []string                 `json:"roles,omitempty"`
	Notes    string                   `json:"notes,omitempty"`
	Measured *MeasuredEvidence        `json:"measured,omitempty"`
	Bench    map[string]BenchEvidence `json:"bench,omitempty"`
	Index    *config.ExternalIndex    `json:"index,omitempty"`
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

// Candidate is a non-binding recommendation for a roster seat.
type Candidate struct {
	ID  string `json:"id"`
	Why string `json:"why"`
}

// RankCandidates is the single seat-aware ordering rule. Every client
// (desktop board, MCP roster get) shows what it returns and never re-ranks:
// two rules would give two answers to "who should sit here?".
//
// The seat picks the metric. Implementer and architect: bench score first
// (ducklings with a bench outrank those without), then measured pass rate,
// then cost. Reviewer and the rest: pass rate, then cost. Advisor: cheapest
// and quickest — it speaks briefly and often. Triager, scribe and human are
// not ranked by evidence. Only measured ducklings (runs > 0) qualify, and
// local ones are left out: at $0/run they would top every cost ordering
// without saying anything about fitness. Three at most; a why line each.
func RankCandidates(role string, scorecards []Scorecard) []Candidate {
	switch role {
	case "triager", "scribe", "human":
		return []Candidate{}
	}
	benchFirst := role == "implementer" || role == "architect"
	type ranked struct {
		c                  Candidate
		primary, secondary float64
		bench              bool
	}
	rows := make([]ranked, 0, len(scorecards))
	for _, s := range scorecards {
		if s.Measured == nil || s.Measured.Runs <= 0 || s.Locality == "local" {
			continue
		}
		cost, wall, pass := s.Measured.AvgCostPerRun, s.Measured.AvgWallclock, s.Measured.PassRate
		bench, hasBench := 0.0, false
		for _, b := range s.Bench {
			if !hasBench || b.Score > bench {
				bench, hasBench = b.Score, true
			}
		}
		row := ranked{c: Candidate{ID: s.ID}, primary: pass, secondary: cost}
		row.c.Why = fmt.Sprintf("pass rate %.0f%% over %d runs · $%.2f/run", pass, s.Measured.Runs, cost)
		switch {
		case benchFirst && hasBench:
			row.primary, row.bench = bench, true
			row.c.Why = fmt.Sprintf("bench %.0f · $%.2f/run", bench*100, cost)
		case role == "advisor":
			row.primary, row.secondary = cost, wall
			row.c.Why = fmt.Sprintf("$%.2f/run · %.0fs avg", cost, wall)
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if benchFirst && rows[i].bench != rows[j].bench {
			return rows[i].bench
		}
		if rows[i].primary != rows[j].primary {
			if role == "advisor" {
				return rows[i].primary < rows[j].primary
			}
			return rows[i].primary > rows[j].primary
		}
		if rows[i].secondary != rows[j].secondary {
			return rows[i].secondary < rows[j].secondary
		}
		return rows[i].c.ID < rows[j].c.ID
	})
	if len(rows) > 3 {
		rows = rows[:3]
	}
	out := make([]Candidate, len(rows))
	for i := range rows {
		out[i] = rows[i].c
	}
	return out
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
	benchRows := loadLatestBench()
	out := make([]Scorecard, 0, len(s.cfg.Ducklings))
	for id, d := range s.cfg.Ducklings {
		p := s.cfg.Providers[d.Provider]
		c := Scorecard{ID: string(id), Provider: string(d.Provider), Model: d.Model, Cost: d.Cost, Caps: d.Caps, Notes: d.Notes, Index: d.Index}
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
