package strategy

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The deliverables checklist — the implementer's work contract.
//
// Jose's design: the task's bullets are WHAT must be delivered (the plan's or
// the promotion's words, never the implementer's own — a model that writes
// its own list grades itself against a target it can quietly narrow, and I2
// says the model does not define its success criterion). HOW it gets there
// stays the implementer's. At the end of its turn it reports per NUMBER —
// what a model never re-types it cannot mangle — and anything not delivered
// summons the rubber duck with the exact question: why not #4?
//
// The reviewer receives the statuses as data (ids and states, never the
// implementer's notes); the duck receives everything.

var (
	bulletRe     = regexp.MustCompile(`^\s*(?:[-*•]|\d+[.)])\s+(.+?)\s*$`)
	outOfScopeRe = regexp.MustCompile(`(?i)^\s*(?:\*\*|__)?\s*(?:out of scope|non-goals?|not in scope|no incluye|fuera de alcance)\b`)
	headingRe    = regexp.MustCompile(`^\s*#{1,6}\s+`)
)

// ExtractDeliverables numbers the task body's top-level bullets, stopping at
// an out-of-scope marker. A body with no bullets yields the title as the one
// deliverable — a task is always at least itself.
func ExtractDeliverables(title, body string) []string {
	var out []string
	inScope := true
	for _, line := range strings.Split(body, "\n") {
		if outOfScopeRe.MatchString(line) {
			inScope = false
			continue
		}
		if headingRe.MatchString(line) {
			// A new heading ends an out-of-scope block; bullets under it are
			// deliverables again unless it is itself out-of-scope.
			inScope = !outOfScopeRe.MatchString(headingRe.ReplaceAllString(line, ""))
			continue
		}
		if !inScope {
			continue
		}
		if m := bulletRe.FindStringSubmatch(line); m != nil {
			// Top-level only: an indented sub-bullet elaborates its parent.
			if strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t") {
				continue
			}
			text := strings.TrimSpace(m[1])
			// Bold-only bullets ("**Constraints:**") are labels, not work.
			if strings.HasSuffix(text, ":") && strings.Count(text, " ") <= 2 {
				continue
			}
			out = append(out, text)
		}
	}
	if len(out) == 0 && strings.TrimSpace(title) != "" {
		out = []string{strings.TrimSpace(title)}
	}
	return out
}

// DeliverableStatus is one line of the report.
type DeliverableStatus struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// DeliverablesReport is what the implementer said it delivered.
type DeliverablesReport struct {
	Items []DeliverableStatus `json:"deliverables"`
	// Unreported is true when the turn carried no parseable report: the
	// contract was asked for and not honoured. Data for the reviewer, not a
	// reason to fail the turn.
	Unreported bool `json:"unreported,omitempty"`
}

var validDeliverableStatus = map[string]bool{"done": true, "partial": true, "not_done": true, "blocked": true}

// Undelivered lists the ids the implementer itself says are not done.
func (r *DeliverablesReport) Undelivered() []int {
	if r == nil {
		return nil
	}
	var ids []int
	for _, it := range r.Items {
		if it.Status != "done" {
			ids = append(ids, it.ID)
		}
	}
	sort.Ints(ids)
	return ids
}

// Missing lists deliverable ids (1..n) the report did not mention at all.
func (r *DeliverablesReport) Missing(n int) []int {
	seen := map[int]bool{}
	if r != nil {
		for _, it := range r.Items {
			seen[it.ID] = true
		}
	}
	var ids []int
	for i := 1; i <= n; i++ {
		if !seen[i] {
			ids = append(ids, i)
		}
	}
	return ids
}

// ParseDeliverablesReport finds the report object in the implementer's
// reply. Tolerant by design: fenced or bare, anywhere in the text; unknown
// ids and statuses are dropped rather than refused. No report at all is
// recorded as Unreported.
func ParseDeliverablesReport(text string, n int) *DeliverablesReport {
	rep := &DeliverablesReport{}
	idx := strings.LastIndex(text, `"deliverables"`)
	if idx < 0 {
		rep.Unreported = true
		return rep
	}
	// Walk back to the object's opening brace, then find its matching close.
	start := strings.LastIndex(text[:idx], "{")
	if start < 0 {
		rep.Unreported = true
		return rep
	}
	depth, end := 0, -1
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i + 1
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		rep.Unreported = true
		return rep
	}
	var raw struct {
		Items []struct {
			ID     interface{} `json:"id"`
			Status string      `json:"status"`
			Note   string      `json:"note"`
		} `json:"deliverables"`
	}
	if err := json.Unmarshal([]byte(text[start:end]), &raw); err != nil {
		rep.Unreported = true
		return rep
	}
	seen := map[int]bool{}
	for _, it := range raw.Items {
		id := 0
		switch v := it.ID.(type) {
		case float64:
			id = int(v)
		case string:
			fmt.Sscanf(strings.TrimSpace(v), "%d", &id)
		}
		if id < 1 || (n > 0 && id > n) || seen[id] {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(it.Status))
		status = strings.ReplaceAll(status, "-", "_")
		status = strings.ReplaceAll(status, " ", "_")
		if !validDeliverableStatus[status] {
			continue
		}
		seen[id] = true
		rep.Items = append(rep.Items, DeliverableStatus{ID: id, Status: status, Note: strings.TrimSpace(it.Note)})
	}
	sort.Slice(rep.Items, func(i, j int) bool { return rep.Items[i].ID < rep.Items[j].ID })
	if len(rep.Items) == 0 {
		rep.Unreported = true
	}
	return rep
}

// renderDeliverables numbers the list for a prompt.
func renderDeliverables(items []string) string {
	var b strings.Builder
	for i, d := range items {
		fmt.Fprintf(&b, "%d. %s\n", i+1, d)
	}
	return b.String()
}

// deliverablesContract is the implementer's closing instruction.
func deliverablesContract(items []string) string {
	return "## Deliverables — your work contract\n\n" +
		"These are the task's deliverables, numbered. Do them; how is up to you. " +
		"When you finish, END your reply with exactly one JSON object reporting each by number:\n\n" +
		renderDeliverables(items) + "\n" +
		"```json\n{\"deliverables\":[{\"id\":1,\"status\":\"done\"},{\"id\":2,\"status\":\"blocked\",\"note\":\"why, in one sentence\"}]}\n```\n\n" +
		"status is one of: done | partial | not_done | blocked. Report honestly — an item you " +
		"could not finish is not a failure, it is the signal that brings you help; an item " +
		"marked done that is not will be found by the reviewer.\n"
}

// deliverablesForReviewer renders the statuses as data — ids and states,
// none of the implementer's notes (I7: the reviewer must not read the
// author's rationalisation).
func deliverablesForReviewer(items []string, rep *DeliverablesReport) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Deliverables and the implementer's own report\n\n")
	b.WriteString(renderDeliverables(items))
	if rep == nil || rep.Unreported {
		b.WriteString("\nThe implementer filed no report on these. Judge each against the diff.\n")
		return b.String()
	}
	type line struct {
		ID     int    `json:"id"`
		Status string `json:"status"`
	}
	var lines []line
	for _, it := range rep.Items {
		lines = append(lines, line{it.ID, it.Status})
	}
	data, _ := json.Marshal(map[string]interface{}{"reported": lines, "not_reported": rep.Missing(len(items))})
	b.WriteString("\n```json\n" + string(data) + "\n```\n\n")
	b.WriteString("Verify each id against the diff — a \"done\" is a claim, not a fact. " +
		"Anything the implementer itself reports undelivered is not yours to excuse: an approve " +
		"with undelivered items must say why the task is nonetheless satisfied.\n")
	return b.String()
}
