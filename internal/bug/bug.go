// Package bug is the operate loop's state machine (05 §6).
//
// A bug is the only thing in ducklab that starts outside it: everything else
// descends from a requirement someone wrote here. So its lifecycle is the one
// place where the tool has to be careful about accepting claims from elsewhere,
// and the transitions are checked rather than assumed.
package bug

import (
	"fmt"
	"sort"
	"strings"
)

// Status is where a bug is in the loop.
type Status string

const (
	Open       Status = "open"
	Triaged    Status = "triaged"
	Duplicate  Status = "duplicate"
	WontFix    Status = "wontfix"
	InProgress Status = "in_progress"
	Fixed      Status = "fixed"
	Verified   Status = "verified"
	Closed     Status = "closed"
)

// Severity ranks a bug. Ordered here so a list can be sorted by urgency
// rather than by the order things were reported.
type Severity string

const (
	Critical Severity = "critical"
	High     Severity = "high"
	Normal   Severity = "normal"
	Low      Severity = "low"
)

var severityRank = map[Severity]int{Critical: 0, High: 1, Normal: 2, Low: 3}

// ValidSeverity reports whether a severity is one we recognise.
func ValidSeverity(s string) bool {
	_, ok := severityRank[Severity(strings.ToLower(strings.TrimSpace(s)))]
	return ok
}

// transitions is the closed set of legal moves.
//
// Written as data rather than as a chain of ifs, because the interesting
// question about a bug tracker is never "can I get from open to fixed" — it is
// which moves are NOT allowed, and a table answers that by being read.
//
// Two rules worth naming:
//
//   - Nothing returns to open. A bug that turns out not to be fixed is
//     reopened as its own record, so the history of what was claimed and when
//     survives. Rewinding a status erases the claim.
//   - fixed does not go straight to closed. Closing is what verified is for,
//     and a fix nobody re-ran the gate on is a fix nobody checked.
var transitions = map[Status][]Status{
	Open:       {Triaged, Duplicate, WontFix},
	Triaged:    {InProgress, Duplicate, WontFix},
	InProgress: {Fixed, Triaged},
	Fixed:      {Verified, InProgress},
	Verified:   {Closed},
	Duplicate:  {Closed},
	WontFix:    {Closed},
	Closed:     {},
}

// CanMove reports whether a bug may go from one status to another.
func CanMove(from, to Status) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Move returns the new status, or an error naming what was allowed.
//
// The error lists the legal moves because the caller is usually a person
// typing a command, and "cannot move from fixed to open" without saying what
// they can do turns a mistake into a guessing game.
func Move(from, to Status) (Status, error) {
	if from == to {
		return from, nil
	}
	if !Valid(string(to)) {
		return from, fmt.Errorf("unknown status %q", to)
	}
	if CanMove(from, to) {
		return to, nil
	}
	allowed := transitions[from]
	if len(allowed) == 0 {
		return from, fmt.Errorf("a %s bug is finished; it cannot move to %s", from, to)
	}
	words := make([]string, len(allowed))
	for i, s := range allowed {
		words[i] = string(s)
	}
	sort.Strings(words)
	return from, fmt.Errorf("a %s bug cannot become %s; it can become %s",
		from, to, strings.Join(words, ", "))
}

// Valid reports whether a string names a status.
func Valid(s string) bool {
	_, ok := transitions[Status(s)]
	return ok
}

// Bug is one report.
type Bug struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	Severity    Severity `json:"severity"`
	Status      Status   `json:"status"`
	DuplicateOf string   `json:"duplicate_of,omitempty"`
	TaskID      string   `json:"task_id,omitempty"`
	Source      string   `json:"source"`
	Reporter    string   `json:"reporter,omitempty"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// SortByUrgency orders bugs as someone deciding what to do next would want
// them: worst first, and oldest first within a severity, so a critical bug
// reported on Monday is not buried by one reported today.
func SortByUrgency(bugs []Bug) {
	sort.SliceStable(bugs, func(i, j int) bool {
		ri, rj := rank(bugs[i].Severity), rank(bugs[j].Severity)
		if ri != rj {
			return ri < rj
		}
		return bugs[i].CreatedAt < bugs[j].CreatedAt
	})
}

func rank(s Severity) int {
	if r, ok := severityRank[s]; ok {
		return r
	}
	// An unrecognised severity sorts last. Inventing urgency for a word we do
	// not know would bury the ones we do.
	return len(severityRank)
}

// Open reports whether a bug still needs someone's attention.
func (b Bug) IsOpen() bool {
	switch b.Status {
	case Closed, Duplicate, WontFix, Verified:
		return false
	}
	return true
}
