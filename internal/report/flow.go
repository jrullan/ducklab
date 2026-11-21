package report

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/bug"
	"github.com/jrullan/ducklab/internal/runlog"
)

// Flow is where delivery time goes (B-556): how long runs spend in model
// turns and gates versus waiting for the owner, what the owner is waited on
// for, how long a bug takes from report to fixed and how many owner actions
// that took, and how often a task lands on its first build.
//
// The thing the whole project wants faster is the time from a report to an
// accepted commit, and the solo-baseline comparison (Build) cannot say where
// that time goes: on TI-36X, 92% of run time was a run waiting for a person.
// Arithmetic over recorded events only — no model is consulted.
type Flow struct {
	Runs      int           `json:"runs"`
	Wall      time.Duration `json:"wall_ns"`
	Model     time.Duration `json:"model_ns"`
	Gate      time.Duration `json:"gate_ns"`
	OwnerWait time.Duration `json:"owner_wait_ns"`
	Stages    []FlowStage   `json:"stages"`
	Waits     []FlowWait    `json:"waits"`
	// Longest are the longest single waits. A handful of pauses left for
	// days can make the totals say "the owner is slow" when the owner
	// answers in seconds: on TI-36X one budget pause sat 418 h of the 475 h
	// of owner wait, while gate pauses were answered in minutes.
	Longest []FlowLongWait `json:"longest"`
	// Open are runs waiting on the owner right now. They are named, not
	// timed: the log has no end for the wait, and the report does not read
	// the clock (review of B-556).
	Open         []FlowOpenWait    `json:"open"`
	Bugs         FlowBugs          `json:"bugs"`
	Tasks        FlowTasks         `json:"tasks"`
	Implementers []FlowImplementer `json:"implementers"`
}

// FlowStage is one stage's share of the time.
type FlowStage struct {
	Stage      string        `json:"stage"`
	Runs       int           `json:"runs"`
	Model      time.Duration `json:"model_ns"`
	Gate       time.Duration `json:"gate_ns"`
	OwnerWait  time.Duration `json:"owner_wait_ns"`
	MedianWall time.Duration `json:"median_wall_ns"`
}

// FlowWait is one kind of owner pause.
type FlowWait struct {
	Kind   string        `json:"kind"`
	Count  int           `json:"count"`
	Total  time.Duration `json:"total_ns"`
	Median time.Duration `json:"median_ns"`
}

// FlowLongWait is one long pause, named so a person can find it.
type FlowLongWait struct {
	RunID    string        `json:"run_id"`
	TaskID   string        `json:"task_id,omitempty"`
	Kind     string        `json:"kind"`
	Duration time.Duration `json:"duration_ns"`
}

// FlowOpenWait is a run still waiting on the owner.
type FlowOpenWait struct {
	RunID  string `json:"run_id"`
	TaskID string `json:"task_id,omitempty"`
	Kind   string `json:"kind"`
	Since  string `json:"since"`
}

// FlowBugs is the report-to-fixed path.
type FlowBugs struct {
	Reported int `json:"reported"`
	Fixed    int `json:"fixed"`
	// LeadMedian and LeadP90 run from the report to its first move to fixed.
	LeadMedian time.Duration `json:"lead_median_ns"`
	LeadP90    time.Duration `json:"lead_p90_ns"`
	// OwnerActionsMedian counts, per fixed bug, the owner's signed status
	// moves and promotions plus the pauses its runs waited on the owner for.
	OwnerActionsMedian float64 `json:"owner_actions_median"`
	RunsMedian         float64 `json:"runs_median"`
}

// FlowTasks is how build attempts landed.
type FlowTasks struct {
	Built     int `json:"built"`
	Accepted  int `json:"accepted"`
	FirstPass int `json:"first_pass"`
}

// FlowImplementer is one implementer's builds.
type FlowImplementer struct {
	Duckling     string        `json:"duckling"`
	Builds       int           `json:"builds"`
	Passed       int           `json:"passed"`
	MedianCost   float64       `json:"median_cost_usd"`
	MedianActive time.Duration `json:"median_active_ns"`
}

const longestShown = 5

// FlowRun is one run with its event log.
type FlowRun struct {
	Run    *runlog.Run
	Events []*runlog.Event
}

// endsWait reports whether e is the owner acting on a waiting run, or the
// run ending. Checked against real TI-36X logs (review of B-556):
//   - turn_start is NOT a response: a safe-point pause cancels in-flight
//     turns that still write turn_start within milliseconds, which closed
//     history_duration waits at 0 s while the owner resumed 19 min later;
//   - accept_refused is NOT a response: a failed reproduction writes a fresh
//     human_needed and the deferred accept_refused landed right after it,
//     19 phantom 0 s gate waits;
//   - some resumes write only checkpoint{reason: resume}, a chat reply is a
//     message from the human, and older logs show an accept only as
//     gate_started{phase: accept}.
func endsWait(e *runlog.Event) bool {
	switch e.Type {
	case "decision", "human", "budget_lifted", "accept_started", "run_end":
		return true
	case "checkpoint":
		return e.Data["reason"] == "resume"
	case "message":
		return e.Data["role"] == "human"
	case "gate_started":
		return e.Data["phase"] == "accept"
	}
	return false
}

// reopensWait reports an accept that did not land: the run is back at its
// gate, waiting on the owner again, whether or not a new human_needed says so.
func reopensWait(e *runlog.Event) bool {
	switch e.Type {
	case "accept_refused":
		return true
	case "accept_settled":
		return e.Data["outcome"] == "refused" || e.Data["outcome"] == "interrupted"
	}
	return false
}

func eventTime(e *runlog.Event) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, e.TS)
	return t, err == nil
}

type runTimes struct {
	wall, model, gate, wait time.Duration
	waits                   map[string][]time.Duration
	each                    []kindWait
	// open is a wait the log never closed; openKind and openAt describe it.
	openKind string
	openAt   time.Time
}

type kindWait struct {
	kind string
	d    time.Duration
}

// timesOf splits one run's events into model turns, gates and owner waits.
// A turn or gate the log never closed contributes nothing: an interval
// without its end is not a measurement. An open wait is returned on its own,
// so a run still waiting can be named without inventing how long it waited.
func timesOf(events []*runlog.Event) runTimes {
	rt := runTimes{waits: map[string][]time.Duration{}}
	var first, last, turnAt, gateAt, waitAt time.Time
	waitKind := ""
	closeWait := func(at time.Time) {
		if waitAt.IsZero() {
			return
		}
		d := at.Sub(waitAt)
		if d < 0 {
			d = 0
		}
		rt.wait += d
		rt.waits[waitKind] = append(rt.waits[waitKind], d)
		rt.each = append(rt.each, kindWait{waitKind, d})
		waitAt = time.Time{}
	}
	for _, e := range events {
		at, ok := eventTime(e)
		if !ok {
			continue
		}
		if first.IsZero() {
			first = at
		}
		last = at
		if endsWait(e) {
			closeWait(at)
		}
		switch e.Type {
		case "turn_start":
			turnAt = at
		case "turn_end", "turn_interrupted":
			if !turnAt.IsZero() {
				rt.model += at.Sub(turnAt)
				turnAt = time.Time{}
			}
		case "gate_started":
			gateAt = at
		case "round_gate", "gate", "gate_reproduced":
			if !gateAt.IsZero() {
				rt.gate += at.Sub(gateAt)
				gateAt = time.Time{}
			}
		case "human_needed":
			closeWait(at)
			waitAt = at
			waitKind, _ = e.Data["kind"].(string)
			if waitKind == "" {
				waitKind = "other"
			}
		}
		if reopensWait(e) && waitAt.IsZero() {
			waitAt, waitKind = at, "gate"
		}
	}
	if !first.IsZero() {
		rt.wall = last.Sub(first)
	}
	if !waitAt.IsZero() {
		rt.openKind, rt.openAt = waitKind, waitAt
	}
	return rt
}

// ownerActor is the owner acting, by hand or through an agent they run
// (signed mcp:<client>). The engine, autopilot and auto:* are not the owner.
// On TI-36X 64 of 69 promotions were signed mcp:opencode (review of B-556).
func ownerActor(actor string) bool {
	return actor == "human" || strings.HasPrefix(actor, "mcp:")
}

func stageOf(r *runlog.Run) string {
	if r.Stage != "" {
		return r.Stage
	}
	if r.TaskID != "" {
		return "build"
	}
	return "other"
}

func medianDur(v []time.Duration) time.Duration {
	if len(v) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

func p90Dur(v []time.Duration) time.Duration {
	if len(v) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	// Nearest rank: the smallest value at or above 90% of the sample.
	return s[(9*len(s)+9)/10-1]
}

func medianFloat(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

// runBugs names the bugs a run worked on: a build's bug, or a triage's batch.
func runBugs(fr FlowRun) []string {
	if fr.Run.BugID != "" {
		return []string{fr.Run.BugID}
	}
	for _, e := range fr.Events {
		if e.Type != "run_start" {
			continue
		}
		if ids, ok := e.Data["bug_ids"].([]interface{}); ok {
			var out []string
			for _, id := range ids {
				if s, ok := id.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
		break
	}
	return nil
}

// BuildFlow computes the flow report.
func BuildFlow(runs []FlowRun, bugs []bug.Bug) *Flow {
	f := &Flow{}
	stages := map[string]*FlowStage{}
	stageWalls := map[string][]time.Duration{}
	waits := map[string][]time.Duration{}
	type bugRun struct {
		started string
		pauses  int
	}
	bugRunsOf := map[string][]bugRun{}
	taskBugs := map[string]string{}
	for _, b := range bugs {
		if b.TaskID != "" {
			taskBugs[b.TaskID] = b.ID
		}
	}
	type attempt struct {
		started  string
		id       string
		accepted bool
	}
	taskAttempts := map[string][]attempt{}
	impl := map[string]*FlowImplementer{}
	implCost := map[string][]float64{}
	implActive := map[string][]time.Duration{}

	for _, fr := range runs {
		if fr.Run == nil {
			continue
		}
		f.Runs++
		rt := timesOf(fr.Events)
		f.Wall += rt.wall
		f.Model += rt.model
		f.Gate += rt.gate
		f.OwnerWait += rt.wait
		st := stageOf(fr.Run)
		row := stages[st]
		if row == nil {
			row = &FlowStage{Stage: st}
			stages[st] = row
		}
		row.Runs++
		row.Model += rt.model
		row.Gate += rt.gate
		row.OwnerWait += rt.wait
		stageWalls[st] = append(stageWalls[st], rt.wall)
		pauses := 0
		for kind, ds := range rt.waits {
			waits[kind] = append(waits[kind], ds...)
			pauses += len(ds)
		}
		if !rt.openAt.IsZero() && fr.Run.Status != "done" && fr.Run.Status != "failed" {
			f.Open = append(f.Open, FlowOpenWait{RunID: fr.Run.ID, TaskID: fr.Run.TaskID, Kind: rt.openKind, Since: rt.openAt.UTC().Format(time.RFC3339)})
		}
		for _, w := range rt.each {
			f.Longest = append(f.Longest, FlowLongWait{RunID: fr.Run.ID, TaskID: fr.Run.TaskID, Kind: w.kind, Duration: w.d})
		}
		ids := runBugs(fr)
		if len(ids) == 0 && fr.Run.TaskID != "" {
			if id := taskBugs[fr.Run.TaskID]; id != "" {
				ids = []string{id}
			}
		}
		for _, id := range ids {
			bugRunsOf[id] = append(bugRunsOf[id], bugRun{fr.Run.StartedAt, pauses})
		}
		if st == "build" && fr.Run.TaskID != "" {
			taskAttempts[fr.Run.TaskID] = append(taskAttempts[fr.Run.TaskID], attempt{fr.Run.StartedAt, fr.Run.ID, fr.Run.Accepted})
			if d := fr.Run.Roster["implementer"]; d != "" {
				ir := impl[d]
				if ir == nil {
					ir = &FlowImplementer{Duckling: d}
					impl[d] = ir
				}
				ir.Builds++
				if fr.Run.Verdict == "PASSED" {
					ir.Passed++
				}
				var cost float64
				for _, sp := range fr.Run.Spend {
					cost += sp.CostUSD
				}
				implCost[d] = append(implCost[d], cost)
				active := fr.Run.ActiveWallclockMs
				if active == 0 {
					active = fr.Run.WallclockMs
				}
				implActive[d] = append(implActive[d], time.Duration(active)*time.Millisecond)
			}
		}
	}

	for st, row := range stages {
		row.MedianWall = medianDur(stageWalls[st])
		f.Stages = append(f.Stages, *row)
	}
	sort.Slice(f.Stages, func(i, j int) bool {
		a, b := f.Stages[i], f.Stages[j]
		if a.Model+a.Gate != b.Model+b.Gate {
			return a.Model+a.Gate > b.Model+b.Gate
		}
		return a.Stage < b.Stage
	})
	for kind, ds := range waits {
		var total time.Duration
		for _, d := range ds {
			total += d
		}
		f.Waits = append(f.Waits, FlowWait{Kind: kind, Count: len(ds), Total: total, Median: medianDur(ds)})
	}
	sort.Slice(f.Waits, func(i, j int) bool {
		if f.Waits[i].Total != f.Waits[j].Total {
			return f.Waits[i].Total > f.Waits[j].Total
		}
		return f.Waits[i].Kind < f.Waits[j].Kind
	})

	sort.SliceStable(f.Longest, func(i, j int) bool { return f.Longest[i].Duration > f.Longest[j].Duration })
	if len(f.Longest) > longestShown {
		f.Longest = f.Longest[:longestShown]
	}

	sort.Slice(f.Open, func(i, j int) bool { return f.Open[i].Since < f.Open[j].Since })

	var leads []time.Duration
	var actions, runsPer []float64
	for _, b := range bugs {
		f.Bugs.Reported++
		created, err := time.Parse(time.RFC3339Nano, b.CreatedAt)
		hist := append([]bug.AuditEntry(nil), b.History...)
		sort.SliceStable(hist, func(i, j int) bool { return hist[i].TS < hist[j].TS })
		var fixedAt time.Time
		owner := 0
		for _, h := range hist {
			if h.To == string(bug.Fixed) {
				if t, terr := time.Parse(time.RFC3339Nano, h.TS); terr == nil {
					fixedAt = t
				}
				break
			}
			if ownerActor(h.Actor) {
				owner++
			}
		}
		if fixedAt.IsZero() {
			continue
		}
		f.Bugs.Fixed++
		if err == nil && !fixedAt.Before(created) {
			leads = append(leads, fixedAt.Sub(created))
		}
		// Only the runs that led to the first fix: a re-fix after a reopen
		// is a second delivery, not this one's cost.
		runs, pauses := 0, 0
		for _, r := range bugRunsOf[b.ID] {
			if st, perr := time.Parse(time.RFC3339Nano, r.started); perr == nil && st.After(fixedAt) {
				continue
			}
			runs++
			pauses += r.pauses
		}
		actions = append(actions, float64(owner+pauses))
		runsPer = append(runsPer, float64(runs))
	}
	f.Bugs.LeadMedian = medianDur(leads)
	f.Bugs.LeadP90 = p90Dur(leads)
	f.Bugs.OwnerActionsMedian = medianFloat(actions)
	f.Bugs.RunsMedian = medianFloat(runsPer)

	for _, atts := range taskAttempts {
		f.Tasks.Built++
		// Two runs can start in the same second; the id breaks the tie, as
		// RunList does.
		sort.Slice(atts, func(i, j int) bool {
			if atts[i].started != atts[j].started {
				return atts[i].started < atts[j].started
			}
			return atts[i].id < atts[j].id
		})
		for i, a := range atts {
			if a.accepted {
				f.Tasks.Accepted++
				if i == 0 {
					f.Tasks.FirstPass++
				}
				break
			}
		}
	}

	for d, ir := range impl {
		ir.MedianCost = medianFloat(implCost[d])
		ir.MedianActive = medianDur(implActive[d])
		f.Implementers = append(f.Implementers, *ir)
	}
	sort.Slice(f.Implementers, func(i, j int) bool {
		if f.Implementers[i].Builds != f.Implementers[j].Builds {
			return f.Implementers[i].Builds > f.Implementers[j].Builds
		}
		return f.Implementers[i].Duckling < f.Implementers[j].Duckling
	})
	return f
}

// longDuration is formatDuration with hours, for waits that run to days.
func longDuration(d time.Duration) string {
	if d < time.Hour {
		return formatDuration(d)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func hours(d time.Duration) string { return fmt.Sprintf("%.1fh", d.Hours()) }

func pct(part, whole time.Duration) string {
	if whole <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", 100*float64(part)/float64(whole))
}

// RenderFlow renders the flow report as Markdown.
func RenderFlow(f *Flow) string {
	var b strings.Builder
	b.WriteString("# Flow report\n\nWhere delivery time goes, from recorded events only — no model was consulted.\n\n")
	fmt.Fprintf(&b, "## Time across %d runs\n\n", f.Runs)
	b.WriteString("| | Total | Share of run time |\n|---|---|---|\n")
	fmt.Fprintf(&b, "| Run time | %s | 100%% |\n", hours(f.Wall))
	fmt.Fprintf(&b, "| Waiting for the owner | %s | %s |\n", hours(f.OwnerWait), pct(f.OwnerWait, f.Wall))
	fmt.Fprintf(&b, "| Model turns | %s | %s |\n", hours(f.Model), pct(f.Model, f.Wall))
	fmt.Fprintf(&b, "| Gates | %s | %s |\n\n", hours(f.Gate), pct(f.Gate, f.Wall))

	b.WriteString("## By stage\n\n| Stage | Runs | Model | Gates | Owner wait | Median run |\n|---|---|---|---|---|---|\n")
	for _, s := range f.Stages {
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s | %s |\n", s.Stage, s.Runs, hours(s.Model), hours(s.Gate), hours(s.OwnerWait), longDuration(s.MedianWall))
	}
	b.WriteString("\n## What the owner was waited on for\n\n")
	if len(f.Waits) == 0 {
		b.WriteString("No run waited on the owner.\n")
	} else {
		b.WriteString("| Pause | Count | Total | Median |\n|---|---|---|---|\n")
		for _, w := range f.Waits {
			fmt.Fprintf(&b, "| %s | %d | %s | %s |\n", w.Kind, w.Count, hours(w.Total), longDuration(w.Median))
		}
	}
	if len(f.Longest) > 0 {
		b.WriteString("\nLongest single waits (a few of these can dominate the totals):\n\n| Run | Task | Pause | Waited |\n|---|---|---|---|\n")
		for _, w := range f.Longest {
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", w.RunID, w.TaskID, w.Kind, longDuration(w.Duration))
		}
	}
	if len(f.Open) > 0 {
		b.WriteString("\nWaiting on the owner now (not in the totals — the wait has no end yet):\n\n| Run | Task | Pause | Since |\n|---|---|---|---|\n")
		for _, w := range f.Open {
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", w.RunID, w.TaskID, w.Kind, w.Since)
		}
	}
	b.WriteString("\n## Bugs\n\n")
	fmt.Fprintf(&b, "%d reported, %d reached fixed.\n\n", f.Bugs.Reported, f.Bugs.Fixed)
	if f.Bugs.Fixed > 0 {
		fmt.Fprintf(&b, "- Report to fixed: median %s, 90th percentile %s.\n", longDuration(f.Bugs.LeadMedian), longDuration(f.Bugs.LeadP90))
		fmt.Fprintf(&b, "- Owner actions per fixed bug (moves and promotions the owner signed, by hand or through their agent, plus the pauses of the runs that led to its first fix; a triage batch's pause counts for every bug in it): median %.1f.\n", f.Bugs.OwnerActionsMedian)
		fmt.Fprintf(&b, "- Runs per fixed bug: median %.1f.\n", f.Bugs.RunsMedian)
	}
	b.WriteString("\n## Builds\n\n")
	fmt.Fprintf(&b, "%d tasks built, %d accepted, %d on their first build.\n", f.Tasks.Built, f.Tasks.Accepted, f.Tasks.FirstPass)
	if len(f.Implementers) > 0 {
		b.WriteString("\n| Implementer | Builds | Passed | Median cost | Median active time |\n|---|---|---|---|---|\n")
		for _, ir := range f.Implementers {
			fmt.Fprintf(&b, "| %s | %d | %d | $%.3f | %s |\n", ir.Duckling, ir.Builds, ir.Passed, ir.MedianCost, longDuration(ir.MedianActive))
		}
		b.WriteString("\nImplementers did not build the same tasks; the table describes what happened, it is not a controlled comparison.\n")
	}
	b.WriteString("\nAll recorded runs. Owner wait runs from each pause until the owner acts on the run (a decision, an answer, an accept, a budget lift, a resume) or the run ends. Engine work during a pause, such as a pre-reproduction or a cancelled turn winding down, does not end it; an accept that did not land puts the run back to waiting.\n")
	return b.String()
}
