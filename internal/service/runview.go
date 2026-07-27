package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/runlog"
)

// CandidateView is one tournament candidate as a client may see it.
//
// There is deliberately no author field. I7 is a property of the product, not
// only of the prompt: if the mapping from label to duckling reached the API,
// a UI could render it, a screenshot could leak it, and the anonymity would be
// cosmetic. The mapping stays in the run's own records, which the judge never
// reads.
type CandidateView struct {
	Label string `json:"label"`
	Diff  string `json:"diff"`
	Gate  string `json:"gate,omitempty"`
}

// RunDiff returns the run's captured diff.
func (s *Service) RunDiff(ctx context.Context, id string) (string, error) {
	return s.readRunFile(id, "diff.patch")
}

// RunTranscript returns the human-readable conversation.
func (s *Service) RunTranscript(ctx context.Context, id string) (string, error) {
	// Built from the event log rather than a second file.
	//
	// It used to read transcript.md, which nothing ever wrote, so the endpoint
	// answered an empty document for every run that has ever existed. The
	// events are the record; deriving the transcript from them means there is
	// one source of truth instead of two that can disagree.
	if md, err := s.readRunFile(id, "transcript.md"); err == nil && strings.TrimSpace(md) != "" {
		return md, nil
	}
	events, err := runlog.ReadEvents(s.RunDir(id))
	if err != nil {
		return "", err
	}
	return renderTranscript(events), nil
}

// renderTranscript turns a run's events into readable markdown.
func renderTranscript(events []*runlog.Event) string {
	var b strings.Builder
	for _, e := range events {
		switch e.Type {
		case "message":
			role, _ := e.Data["role"].(string)
			duckling, _ := e.Data["duckling"].(string)
			content, _ := e.Data["content"].(string)
			if content == "" {
				continue
			}
			fmt.Fprintf(&b, "## %s", role)
			if duckling != "" {
				fmt.Fprintf(&b, " · %s", duckling)
			}
			b.WriteString("\n\n")
			b.WriteString(strings.TrimSpace(content))
			b.WriteString("\n\n")
		case "tool_call":
			name, _ := e.Data["tool"].(string)
			ok, present := e.Data["ok"].(bool)
			status := ""
			switch {
			case !present:
				status = ""
			case ok:
				status = " · ok"
			default:
				status = " · failed"
			}
			fmt.Fprintf(&b, "`%s`%s\n\n", name, status)
		}
	}
	return b.String()
}

// RunVerify returns the gate output, tail-limited.
func (s *Service) RunVerify(ctx context.Context, id string, tailLines int) (string, error) {
	out, err := s.readRunFile(id, "verify.log")
	if err != nil {
		return "", err
	}
	if tailLines <= 0 {
		return out, nil
	}
	lines := strings.Split(out, "\n")
	if len(lines) <= tailLines {
		return out, nil
	}
	return strings.Join(lines[len(lines)-tailLines:], "\n"), nil
}

// RunCandidates returns the anonymised candidates of a tournament run.
func (s *Service) RunCandidates(ctx context.Context, id string) ([]CandidateView, error) {
	dir := s.RunDir(id)
	if dir == "" {
		return nil, fmt.Errorf("run %q not found", id)
	}
	candDir := filepath.Join(dir, "candidates")
	entries, err := os.ReadDir(candDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // not a tournament run
		}
		return nil, err
	}

	var out []CandidateView
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".patch") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(candDir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, CandidateView{
			Label: strings.TrimSuffix(e.Name(), ".patch"),
			Diff:  string(data),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, nil
}

// RunLLMCalls returns the recorded model calls, already redacted on write.
func (s *Service) RunLLMCalls(ctx context.Context, id string, fromSeq int) ([]map[string]interface{}, error) {
	dir := s.RunDir(id)
	if dir == "" {
		return nil, fmt.Errorf("run %q not found", id)
	}
	return runlog.ReadJSONL(filepath.Join(dir, "llm.jsonl"), fromSeq)
}

func (s *Service) readRunFile(id, name string) (string, error) {
	dir := s.RunDir(id)
	if dir == "" {
		return "", fmt.Errorf("run %q not found", id)
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		if os.IsNotExist(err) {
			// A run that has not reached this artefact yet is not an error:
			// the Run view opens before the diff exists.
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}
