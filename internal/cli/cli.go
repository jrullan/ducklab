// Package cli is ducklab's terminal front-end: a light stdlib-flag dispatcher
// over the strategy engine, plus the interactive REPL. Every action available
// in the REPL is also a scriptable subcommand.
package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/duck"
	"github.com/jrullan/ducklab/internal/prim"
	"github.com/jrullan/ducklab/internal/run"
	"github.com/jrullan/ducklab/internal/source"
	"github.com/jrullan/ducklab/internal/strategy"
)

// Version is stamped at build time (-ldflags) or defaults here.
var Version = "0.1.0"

// Main is the process entry point. It returns an exit code.
func Main(args []string) int {
	if len(args) == 0 {
		usage()
		return 1
	}
	switch args[0] {
	case "sources":
		return cmdSources()
	case "run":
		return cmdRun(args[1:])
	case "resume":
		return cmdResume(args[1:])
	case "show":
		return cmdShow(args[1:])
	case "diff":
		return cmdDiff(args[1:])
	case "chat":
		return cmdChat(args[1:])
	case "version", "--version", "-v":
		fmt.Println("ducklab", Version)
		return 0
	case "help", "--help", "-h":
		usage()
		return 0
	default:
		fmt.Fprintln(os.Stderr, duck.Bad.Render("unknown command: ")+args[0])
		usage()
		return 1
	}
}

func usage() {
	fmt.Println(duck.Banner("a multi-model harness for local LLMs"))
	fmt.Println()
	fmt.Println(duck.Title.Render("Usage"))
	fmt.Println("  ducklab chat [--repo PATH] [--verify CMD]     interactive REPL")
	fmt.Println("  ducklab run <id> <req-file> --repo PATH [--verify CMD | --no-verify] [--mode M] [--a S --b S --judge S]")
	fmt.Println(duck.Dim.Render("      verification auto-detects (tests > build > unverified) when --verify is omitted"))
	fmt.Println("  ducklab resume <id> --repo PATH               resume a run from state.json")
	fmt.Println("  ducklab show <id> --repo PATH                 summarize a run (models, cost, diff, verdict)")
	fmt.Println("  ducklab diff <id> --repo PATH                 the full result patch")
	fmt.Println("  ducklab sources                               list sources and reachability")
	fmt.Println("  ducklab version")
	fmt.Println()
	fmt.Println(duck.Dim.Render("  modes: " + strings.Join(strategy.Names(), ", ")))
}

func cmdSources() int {
	srcs, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, duck.Bad.Render("config error: ")+err.Error())
		return 1
	}
	var names []string
	for n := range srcs {
		names = append(names, n)
	}
	sort.Strings(names)
	ctx := context.Background()
	for _, n := range names {
		s := srcs[n]
		model, err := s.ResolveModel(ctx)
		status := duck.OK.Render(model)
		if err != nil {
			status = duck.Bad.Render("unreachable: " + firstLine(err.Error()))
		}
		fmt.Printf("%s  %s  %s\n",
			duck.Key.Render(pad(n, 12)), duck.Dim.Render(pad(s.BaseURL, 34)), status)
	}
	return 0
}

// runFlags is the shared flag set for run/resume.
type runFlags struct {
	repo, tests, mode, a, b, judge string
	noVerify                       bool
}

func parseRunFlags(args []string) (pos []string, f runFlags) {
	f = runFlags{mode: "driver", a: "beelink", b: "aitopatom", judge: "aitopatom"}
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--repo":
			i++
			f.repo = get(args, i)
		case "--tests", "--verify":
			i++
			f.tests = get(args, i)
		case "--no-verify":
			f.noVerify = true
		case "--mode":
			i++
			f.mode = get(args, i)
		case "--a":
			i++
			f.a = get(args, i)
		case "--b":
			i++
			f.b = get(args, i)
		case "--judge":
			i++
			f.judge = get(args, i)
		default:
			pos = append(pos, args[i])
		}
		i++
	}
	return pos, f
}

func cmdRun(args []string) int {
	pos, f := parseRunFlags(args)
	if len(pos) < 2 {
		fmt.Fprintln(os.Stderr, duck.Bad.Render("usage: ducklab run <task_id> <req-file> --repo PATH --tests CMD"))
		return 1
	}
	taskID := prim.Slugify(pos[0])
	reqData, err := os.ReadFile(pos[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, duck.Bad.Render("cannot read requirement: ")+err.Error())
		return 1
	}
	if f.repo == "" {
		f.repo, _ = os.Getwd()
	}
	repo, _ := filepath.Abs(f.repo)
	strat, ok := strategy.Get(f.mode)
	if !ok {
		fmt.Fprintln(os.Stderr, duck.Bad.Render("unknown mode: ")+f.mode+
			duck.Dim.Render(" (have: "+strings.Join(strategy.Names(), ", ")+")"))
		return 1
	}
	if initialized, err := prim.EnsureRepo(repo); err != nil {
		fmt.Fprintln(os.Stderr, duck.Bad.Render("cannot initialize repo: ")+err.Error())
		return 1
	} else if initialized {
		fmt.Println(duck.Hunk.Render("  ◌ no git repo — ran git init + initial commit so ducklab can branch"))
	}
	env, code := buildEnv(taskID, string(reqData), repo, f)
	if code != 0 {
		return code
	}
	return execute(strat, env)
}

func cmdShow(args []string) int {
	pos, f := parseRunFlags(args)
	if len(pos) < 1 {
		fmt.Fprintln(os.Stderr, duck.Bad.Render("usage: ducklab show <task_id> --repo PATH"))
		return 1
	}
	if f.repo == "" {
		f.repo, _ = os.Getwd()
	}
	repo, _ := filepath.Abs(f.repo)
	fmt.Println(runReport(repo, prim.Slugify(pos[0])))
	return 0
}

func cmdDiff(args []string) int {
	pos, f := parseRunFlags(args)
	if len(pos) < 1 {
		fmt.Fprintln(os.Stderr, duck.Bad.Render("usage: ducklab diff <task_id> --repo PATH"))
		return 1
	}
	if f.repo == "" {
		f.repo, _ = os.Getwd()
	}
	repo, _ := filepath.Abs(f.repo)
	fmt.Println(runDiff(repo, prim.Slugify(pos[0])))
	return 0
}

func cmdResume(args []string) int {
	pos, f := parseRunFlags(args)
	if len(pos) < 1 {
		fmt.Fprintln(os.Stderr, duck.Bad.Render("usage: ducklab resume <task_id> --repo PATH"))
		return 1
	}
	if f.repo == "" {
		f.repo, _ = os.Getwd()
	}
	repo, _ := filepath.Abs(f.repo)
	taskID := prim.Slugify(pos[0])
	r, err := run.Open(filepath.Join(repo, "runs", taskID))
	if err != nil {
		fmt.Fprintln(os.Stderr, duck.Bad.Render(err.Error()))
		return 1
	}
	mode, _ := r.Get("mode")
	req, _ := r.Get("requirement")
	if s, ok := mode.(string); ok {
		f.mode = s
	}
	strat, ok := strategy.Get(f.mode)
	if !ok {
		fmt.Fprintln(os.Stderr, duck.Bad.Render("run has unknown mode: ")+f.mode)
		return 1
	}
	env, code := buildEnv(taskID, asString(req), repo, f)
	if code != 0 {
		return code
	}
	return execute(strat, env)
}

// buildEnv loads sources and assembles the strategy Env.
func buildEnv(taskID, requirement, repo string, f runFlags) (strategy.Env, int) {
	srcs, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, duck.Bad.Render("config error: ")+err.Error())
		return strategy.Env{}, 1
	}
	pick := func(name string) (source.Client, bool) {
		s, ok := srcs[name]
		if !ok {
			fmt.Fprintln(os.Stderr, duck.Bad.Render("unknown source: ")+name)
			return nil, false
		}
		return s, true
	}
	a, ok := pick(f.a)
	if !ok {
		return strategy.Env{}, 1
	}
	b, ok := pick(f.b)
	if !ok {
		return strategy.Env{}, 1
	}
	judge, ok := pick(f.judge)
	if !ok {
		return strategy.Env{}, 1
	}
	r, err := run.Open(filepath.Join(repo, "runs", taskID))
	if err != nil {
		fmt.Fprintln(os.Stderr, duck.Bad.Render(err.Error()))
		return strategy.Env{}, 1
	}
	gate := resolveGate(repo, f)
	_ = r.Set("mode", f.mode)
	_ = r.Set("requirement", requirement)
	fmt.Println(duck.Dim.Render("  gate: " + gate.Label()))
	return strategy.Env{
		Ctx: context.Background(), TaskID: taskID, Requirement: requirement,
		Repo: repo, Gate: gate, Contestants: []source.Client{a, b},
		Judge: judge, Run: r,
		OnStage: func(stage, src string) {
			if src != "" {
				fmt.Println(duck.Dim.Render("  · " + stage + " — " + src))
			} else {
				fmt.Println(duck.Dim.Render("  · " + stage))
			}
		},
	}, 0
}

// resolveGate picks the verification gate: an explicit --verify/--tests command
// (unless --no-verify), else auto-detection (tests > build > none).
func resolveGate(repo string, f runFlags) prim.Gate {
	if f.noVerify {
		return prim.Gate{Kind: "none"}
	}
	if strings.TrimSpace(f.tests) != "" {
		return prim.GateFromCmd(f.tests)
	}
	return prim.DetectGate(repo)
}

func execute(strat strategy.Strategy, env strategy.Env) int {
	fmt.Println(duck.Quack.Render("  quack — " + strat.Name() + " on [" + env.TaskID + "]"))
	out, err := strat.Run(env)
	if err != nil {
		fmt.Fprintln(os.Stderr, duck.Bad.Render("  ✗ "+err.Error()))
		return 1
	}
	printOutcome(out)
	// HUMAN_GATE and UNVERIFIED are both awaiting-a-human, not failures.
	if out.State == "ESCALATED" {
		return 2
	}
	return 0
}

func printOutcome(o strategy.Outcome) {
	var head string
	switch o.State {
	case "HUMAN_GATE":
		head = duck.OK.Render("  ✓ " + o.State)
	case "UNVERIFIED":
		head = duck.Hunk.Render("  ◌ " + o.State)
	default:
		head = duck.Warns.Render("  ⚠ " + o.State)
	}
	fmt.Println(head + duck.Dim.Render("  "+o.Message))
	if o.Resolution != "" {
		fmt.Printf("    %s %s", duck.Key.Render("resolution"), o.Resolution)
		if o.Winner != "" {
			fmt.Printf("  %s %s", duck.Key.Render("winner"), o.Winner)
		}
		fmt.Println()
	}
	if o.Branch != "" {
		fmt.Printf("    %s %s\n", duck.Key.Render("branch"), o.Branch)
	}
}

// helpers
func get(a []string, i int) string {
	if i < len(a) {
		return a[i]
	}
	return ""
}
func pad(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
