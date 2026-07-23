// Package strategy holds ducklab's collaboration recipes. Each strategy is a
// deterministic state machine that wires model roles (solver, driver, observer,
// judge) over the shared primitives. Models only ever produce text; git, tests,
// and control flow belong to the orchestrator.
package strategy

import (
	"context"
	"sort"

	"github.com/jrullan/ducklab/internal/prim"
	"github.com/jrullan/ducklab/internal/run"
	"github.com/jrullan/ducklab/internal/source"
)

// StageFunc is an optional progress sink: (stage label, active source name).
type StageFunc func(stage, sourceName string)

// Env is everything a strategy needs to execute one task.
type Env struct {
	Ctx         context.Context
	TaskID      string
	Requirement string
	Repo        string
	Gate        prim.Gate // verification tier (tests / build / custom / none)
	Contestants []source.Client
	Judge       source.Client
	Run         *run.Run
	OnStage     StageFunc
	// OnCall, if set, receives each completion's Result for per-phase telemetry.
	OnCall func(source.Result)
	// OnRetry, if set, is notified when a transient failure is being retried.
	OnRetry func(attempt int, reason string)
}

func (e Env) stage(stage, src string) {
	if e.OnStage != nil {
		e.OnStage(stage, src)
	}
}

// Outcome is the terminal result of a run.
type Outcome struct {
	// State is the terminal state:
	//   HUMAN_GATE — a gate passed (tests/build/custom green); ready to accept
	//   UNVERIFIED — changes produced but no automated gate ran; you are the gate
	//   ESCALATED  — a gate failed or the run could not converge
	State      string
	Resolution string
	// Winner names the model whose solution won a competition. It is set ONLY
	// by tournament, where models genuinely compete. Collaborative modes (solo,
	// driver, plan) leave it empty — nobody wins or loses there but the user.
	Winner    string
	Branch    string
	TestsPass bool
	Message   string
}

// Strategy is one collaboration recipe.
type Strategy interface {
	Name() string
	// MinContestants reports how many contestant sources the recipe needs
	// (solo=1, driver=1 plus a judge/observer, tournament=2 plus a judge).
	MinContestants() int
	Run(env Env) (Outcome, error)
}

var registry = map[string]Strategy{}

// Register adds a strategy to the global registry.
func Register(s Strategy) { registry[s.Name()] = s }

// Get returns a registered strategy.
func Get(name string) (Strategy, bool) {
	s, ok := registry[name]
	return s, ok
}

// Names lists registered strategies, sorted.
func Names() []string {
	var out []string
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func init() {
	Register(Solo{})
	Register(Driver{})
	Register(Tournament{})
	Register(Plan{})
}
