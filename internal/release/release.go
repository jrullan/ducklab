// Package release collects what shipped and renders the notes (05 §9.1).
//
// The collection is deterministic: which work is in a release is a fact about
// accepted runs and tags, not a judgement. Only the prose is written by a
// model, and only after the facts are fixed.
package release

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Version is a semantic version.
type Version struct{ Major, Minor, Patch int }

func (v Version) String() string { return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch) }

var tagRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// ParseVersion reads a vX.Y.Z tag.
func ParseVersion(tag string) (Version, bool) {
	m := tagRe.FindStringSubmatch(strings.TrimSpace(tag))
	if m == nil {
		return Version{}, false
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	return Version{maj, min, pat}, true
}

// Bump returns the next version for a bump kind.
func Bump(v Version, kind string) (Version, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "major":
		return Version{v.Major + 1, 0, 0}, nil
	case "minor", "":
		return Version{v.Major, v.Minor + 1, 0}, nil
	case "patch":
		return Version{v.Major, v.Minor, v.Patch + 1}, nil
	}
	return Version{}, fmt.Errorf("unknown bump %q, want major, minor or patch", kind)
}

// Latest returns the highest version among tags, and whether any was found.
//
// Ordered by version, not by tag date. A patch cut after a minor is still
// older, and reading the release history by when someone happened to tag would
// put it last.
func Latest(tags []string) (Version, bool) {
	var out Version
	found := false
	for _, t := range tags {
		v, ok := ParseVersion(t)
		if !ok {
			continue // a tag that is not a release is not our business
		}
		if !found || less(out, v) {
			out, found = v, true
		}
	}
	return out, found
}

func less(a, b Version) bool {
	if a.Major != b.Major {
		return a.Major < b.Major
	}
	if a.Minor != b.Minor {
		return a.Minor < b.Minor
	}
	return a.Patch < b.Patch
}

// Item is one piece of accepted work in a release.
type Item struct {
	TaskID    string
	Title     string
	Milestone string
	CommitSHA string
}

// Notes is a release's contents, grouped as the document will present them.
type Notes struct {
	Version    Version
	Since      string // the previous tag, or "" for the first release
	Milestones []Milestone
	// Unverified counts accepted work whose gate never ran. Reported rather
	// than hidden: a release note that reads the same whether or not anything
	// was tested is a release note that cannot be trusted (P3).
	Unverified int
}

// Milestone groups items under the milestone they belong to.
type Milestone struct {
	ID    string
	Items []Item
}

// Group orders items into milestones, and both deterministically.
//
// Work with no milestone is collected under "unassigned" rather than dropped:
// a task built from a hand-written spec is still something that shipped.
func Group(items []Item) []Milestone {
	byID := map[string][]Item{}
	for _, it := range items {
		id := it.Milestone
		if id == "" {
			id = "unassigned"
		}
		byID[id] = append(byID[id], it)
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]Milestone, 0, len(ids))
	for _, id := range ids {
		group := byID[id]
		sort.Slice(group, func(i, j int) bool { return group[i].TaskID < group[j].TaskID })
		out = append(out, Milestone{ID: id, Items: group})
	}
	return out
}

// Path is the release document for a version.
func Path(projectRoot string, v Version) string {
	return filepath.Join(projectRoot, ".ducklab", "docs", "releases", v.String()+".md")
}

// Dir is where release documents live.
func Dir(projectRoot string) string {
	return filepath.Join(projectRoot, ".ducklab", "docs", "releases")
}

// Render writes the release document: the facts, then the scribe's prose.
//
// The inventory is written by this function and never by a model. A model
// writes the notes a user reads; what actually shipped is a matter of record,
// and a release whose contents were paraphrased is a release nobody can audit.
func Render(n Notes, prose string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("kind: release\n")
	fmt.Fprintf(&b, "version: %s\n", n.Version)
	if n.Since != "" {
		fmt.Fprintf(&b, "since: %s\n", n.Since)
	}
	fmt.Fprintf(&b, "tasks: %d\n", count(n))
	if n.Unverified > 0 {
		fmt.Fprintf(&b, "unverified: %d\n", n.Unverified)
	}
	b.WriteString("---\n\n")

	fmt.Fprintf(&b, "# %s\n\n", n.Version)

	if count(n) == 0 {
		b.WriteString("No accepted work since the last release.\n")
		return b.String()
	}

	if strings.TrimSpace(prose) != "" {
		b.WriteString(strings.TrimSpace(prose))
		b.WriteString("\n\n")
	}

	if n.Unverified > 0 {
		fmt.Fprintf(&b, "> %d of these changes were accepted with no gate that could run, "+
			"so nothing was verified about them beyond a person reading the diff.\n\n", n.Unverified)
	}

	b.WriteString("## What shipped\n\n")
	for _, m := range n.Milestones {
		fmt.Fprintf(&b, "### %s\n\n", m.ID)
		for _, it := range m.Items {
			title := it.Title
			if title == "" {
				title = it.TaskID
			}
			fmt.Fprintf(&b, "- **%s** %s", it.TaskID, title)
			if it.CommitSHA != "" {
				fmt.Fprintf(&b, " (`%s`)", short(it.CommitSHA))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func count(n Notes) int {
	total := 0
	for _, m := range n.Milestones {
		total += len(m.Items)
	}
	return total
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
