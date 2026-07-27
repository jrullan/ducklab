package artifact

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/xplat"
)

// MaxMemoryBytes caps project.md (02 §5.5).
//
// The file is injected into the system prompt of EVERY turn, so it is not
// storage — it is a standing cost paid on every model call. An uncapped log of
// accepted work would quietly consume the context window the actual task needs.
const MaxMemoryBytes = 8192

const (
	memoryDescHeading = "# What we are building"
	memoryConvHeading = "# Conventions"
	memoryWorkHeading = "# Accepted work"
	foldedLinePrefix  = "- … and "
	foldedLineSuffix  = " earlier tasks"
)

// Memory is the rolling project note.
type Memory struct {
	Description string
	Conventions string
	// Accepted is newest-last, matching the file.
	Accepted []string
	// Folded counts entries collapsed into the summary line.
	Folded int
}

var foldedRe = regexp.MustCompile(`^- … and (\d+) earlier tasks`)

// LoadMemory reads project.md. A missing file is an empty memory.
func LoadMemory(projectRoot string) (*Memory, error) {
	data, err := os.ReadFile(Path(projectRoot, KindProject))
	if err != nil {
		if os.IsNotExist(err) {
			return &Memory{}, nil
		}
		return nil, err
	}
	return ParseMemory(string(data)), nil
}

// ParseMemory reads the note's three sections.
func ParseMemory(content string) *Memory {
	m := &Memory{}
	if _, rest, ok := splitFrontmatter(content); ok {
		content = rest
	}

	section := ""
	var desc, conv []string
	for _, line := range strings.Split(content, "\n") {
		switch strings.TrimSpace(line) {
		case memoryDescHeading:
			section = "desc"
			continue
		case memoryConvHeading:
			section = "conv"
			continue
		case memoryWorkHeading:
			section = "work"
			continue
		}
		switch section {
		case "desc":
			desc = append(desc, line)
		case "conv":
			conv = append(conv, line)
		case "work":
			t := strings.TrimSpace(line)
			if t == "" {
				continue
			}
			if match := foldedRe.FindStringSubmatch(t); match != nil {
				n, _ := strconv.Atoi(match[1])
				m.Folded += n
				continue
			}
			if strings.HasPrefix(t, "- ") {
				m.Accepted = append(m.Accepted, strings.TrimPrefix(t, "- "))
			}
		}
	}
	m.Description = strings.TrimSpace(strings.Join(desc, "\n"))
	m.Conventions = strings.TrimSpace(strings.Join(conv, "\n"))
	return m
}

// RecordAccepted appends a task to the log and folds if needed.
func (m *Memory) RecordAccepted(taskID, title, runID string, when time.Time) {
	entry := fmt.Sprintf("%s %s %s (%s)", when.UTC().Format("2006-01-02"), taskID, title, runID)
	m.Accepted = append(m.Accepted, entry)
	m.fold()
}

// fold collapses the oldest entries until the rendered note fits.
//
// Oldest first: recent work is what a follow-up task needs context for, and the
// count preserves the fact that earlier work existed rather than pretending the
// project started last week.
func (m *Memory) fold() {
	for len(RenderMemory(m)) > MaxMemoryBytes && len(m.Accepted) > 1 {
		m.Folded++
		m.Accepted = m.Accepted[1:]
	}
}

// RenderMemory writes the note. fold() calls it to measure as it goes, so
// the cap is enforced against the real rendered size rather than an estimate.
func RenderMemory(m *Memory) string {
	var b strings.Builder
	b.WriteString("---\nkind: project\n---\n\n")

	b.WriteString(memoryDescHeading + "\n")
	if m.Description != "" {
		b.WriteString(m.Description + "\n")
	}
	b.WriteString("\n" + memoryConvHeading + "\n")
	if m.Conventions != "" {
		b.WriteString(m.Conventions + "\n")
	}
	b.WriteString("\n" + memoryWorkHeading + "\n")
	if m.Folded > 0 {
		fmt.Fprintf(&b, "%s%d%s\n", foldedLinePrefix, m.Folded, foldedLineSuffix)
	}
	for _, a := range m.Accepted {
		fmt.Fprintf(&b, "- %s\n", a)
	}
	return b.String()
}

// SaveMemory writes project.md.
func SaveMemory(projectRoot string, m *Memory) error {
	m.fold()
	if err := os.MkdirAll(DocsDir(projectRoot), 0o755); err != nil {
		return err
	}
	return xplat.AtomicWrite(Path(projectRoot, KindProject), []byte(RenderMemory(m)), 0o644)
}

// PromptContext is what gets injected into every turn.
//
// Returns "" when there is nothing worth saying: an empty scaffold in every
// prompt is noise a model has to read past on every call.
func (m *Memory) PromptContext() string {
	if m.Description == "" && m.Conventions == "" && len(m.Accepted) == 0 {
		return ""
	}
	var b strings.Builder
	if m.Description != "" {
		b.WriteString("## What we are building\n" + m.Description + "\n\n")
	}
	if m.Conventions != "" {
		b.WriteString("## Conventions\n" + m.Conventions + "\n\n")
	}
	if len(m.Accepted) > 0 {
		b.WriteString("## Accepted work\n")
		if m.Folded > 0 {
			fmt.Fprintf(&b, "(%d earlier tasks omitted)\n", m.Folded)
		}
		for _, a := range m.Accepted {
			fmt.Fprintf(&b, "- %s\n", a)
		}
	}
	return strings.TrimSpace(b.String())
}

// FailedAttempt summarises a run that did not work (04 §1.5).
type FailedAttempt struct {
	RunID   string
	Mode    string
	Summary string
	Gate    string
}

// RenderFailedAttempts formats prior failures for an implementer prompt.
//
// A small model re-run on the same task reliably repeats the same dead end.
// Naming what was already tried is the cheapest correction available, and it
// is generated from the run record rather than by asking a model to recall.
func RenderFailedAttempts(attempts []FailedAttempt) string {
	if len(attempts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Approaches already tried and failed for this task\n")
	for _, a := range attempts {
		fmt.Fprintf(&b, "- %s (%s): %s", a.RunID, a.Mode, strings.TrimSpace(a.Summary))
		if a.Gate != "" {
			fmt.Fprintf(&b, "; gate: %s", strings.TrimSpace(a.Gate))
		}
		b.WriteString("\n")
	}
	return b.String()
}
