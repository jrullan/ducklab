package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/duck"
	"github.com/jrullan/ducklab/internal/prim"
	"github.com/jrullan/ducklab/internal/run"
)

// callStat aggregates one source's model calls within a run.
type callStat struct {
	source string
	calls  int
	tokens int
	secs   float64
}

// parseLLMLog folds llm_log.jsonl into per-source totals — the honest record of
// which models actually ran, how much they were asked, and how long they took.
func parseLLMLog(path string) []callStat {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	agg := map[string]*callStat{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		var e struct {
			Source     string  `json:"source"`
			Prompt     int     `json:"prompt_tokens"`
			Completion int     `json:"completion_tokens"`
			Elapsed    float64 `json:"elapsed_s"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Source == "" {
			continue
		}
		s := agg[e.Source]
		if s == nil {
			s = &callStat{source: e.Source}
			agg[e.Source] = s
		}
		s.calls++
		s.tokens += e.Prompt + e.Completion
		s.secs += e.Elapsed
	}
	var out []callStat
	for _, s := range agg {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].source < out[j].source })
	return out
}

// runReport renders a human-useful summary of a run: what happened, which
// models ran and at what cost, whether tests passed, what changed, and the
// reviewer's verdict. Shared by `ducklab show` and the REPL's /show.
func runReport(repo, taskID string) string {
	r, err := run.Open(filepath.Join(repo, "runs", taskID))
	if err != nil || r.State.State == "INTAKE" && len(r.State.History) == 0 {
		return duck.Dim.Render("  no run named " + taskID + " under " + filepath.Join(repo, "runs"))
	}
	var b strings.Builder
	line := func(s string) { b.WriteString(s + "\n") }
	kv := func(k, v string) { line("  " + duck.Key.Render(pad(k, 12)) + v) }

	mode := dataString(r, "mode")
	state := r.State.State
	stateStyled := duck.OK.Render(state)
	if state != "HUMAN_GATE" {
		stateStyled = duck.Warns.Render(state)
	}

	line(duck.Title.Render("  "+taskID) + duck.Dim.Render("  ("+orNone(mode)+")"))
	kv("state", stateStyled)
	if res := dataString(r, "resolution"); res != "" {
		kv("resolution", res)
	}

	// tests
	if tf, ok := r.State.Data["tests_final"].(map[string]any); ok {
		if okv, _ := tf["ok"].(bool); okv {
			kv("tests", duck.OK.Render("green"))
		} else {
			kv("tests", duck.Bad.Render("red"))
		}
	}

	// models actually used + cost (answers "was the second model even called?")
	stats := parseLLMLog(r.LogPath())
	if len(stats) > 0 {
		line("  " + duck.Key.Render("models"))
		for _, s := range stats {
			line(fmt.Sprintf("    %s %s",
				duck.Value.Render(pad(s.source, 12)),
				duck.Dim.Render(fmt.Sprintf("%d call(s) · %d tokens · %.1fs", s.calls, s.tokens, s.secs))))
		}
	}

	// what changed (diffstat of the result branch vs base)
	base := dataString(r, "base_branch")
	if base == "" {
		base = detectBase(repo)
	}
	branch := "ducklab/" + taskID + "/final"
	if ok, _ := prim.Git("rev-parse --verify -q "+branch, repo); ok {
		if okStat, stat := prim.Git(
			fmt.Sprintf("diff --stat %s %s -- . ':(exclude)runs'", base, branch), repo); okStat && strings.TrimSpace(stat) != "" {
			line("  " + duck.Key.Render("changes") + duck.Dim.Render("  ("+branch+")"))
			for _, l := range strings.Split(strings.TrimSpace(stat), "\n") {
				line("    " + duck.Dim.Render(strings.TrimSpace(l)))
			}
		}
	}

	// verdict — mode-specific
	switch mode {
	case "tournament":
		if j, ok := r.Read("judge.md"); ok {
			rep := prim.ParseJudge(j)
			v := "  " + duck.Key.Render("judge") + " decision " + rep.Decision
			if len(rep.Blocking) > 0 {
				var bl []string
				for k := range rep.Blocking {
					bl = append(bl, k)
				}
				sort.Strings(bl)
				v += duck.Bad.Render("  blocking: " + strings.Join(bl, ","))
			}
			line(v)
		}
	case "driver":
		if rv, name := lastReview(r); rv != "" {
			line("  " + duck.Key.Render("review") + duck.Dim.Render("  ("+name+")"))
			line("    " + duck.Dim.Render(prim.TruncateMiddle(oneLine(rv), 300)))
		}
	}

	if state == "HUMAN_GATE" {
		line(duck.Dim.Render("  ▸ /accept to merge · /reject to discard · /diff for full patch"))
	} else {
		line(duck.Dim.Render("  ▸ artifacts in " + filepath.Join(repo, "runs", taskID)))
	}
	return strings.TrimRight(b.String(), "\n")
}

// lastReview returns the most recent observer review body and its file name.
func lastReview(r *run.Run) (string, string) {
	for i := 5; i >= 1; i-- {
		name := fmt.Sprintf("review_%d.md", i)
		if v, ok := r.Read(name); ok {
			return v, name
		}
	}
	return "", ""
}

func dataString(r *run.Run, key string) string {
	if v, ok := r.State.Data[key].(string); ok {
		return v
	}
	return ""
}

func detectBase(repo string) string {
	for _, b := range []string{"main", "master"} {
		if ok, _ := prim.Git("rev-parse --verify -q "+b, repo); ok {
			return b
		}
	}
	return prim.CurrentBranch(repo)
}
