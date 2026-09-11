package service

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/conv"
)

func taskCandidateInvariantFindings(projectRoot, taskID string, changed []string) []conv.Finding {
	findings := taskLaneFindings(projectRoot, taskID, changed)
	findings = append(findings, taskFixtureNarrowingFindings(projectRoot, taskID, changed)...)
	return findings
}

type laneClaim struct {
	path string
	tree bool
}

func taskLaneFindings(projectRoot, taskID string, changed []string) []conv.Finding {
	plan, err := artifact.Load(projectRoot, artifact.KindPlan)
	if err != nil || plan == nil || strings.TrimSpace(taskID) == "" {
		return nil
	}
	var allowed []laneClaim
	owners := map[string][]laneClaim{}
	for _, milestone := range plan.Sections {
		for _, task := range milestone.Children {
			claims := sectionLaneClaims(task)
			if len(claims) == 0 {
				claims = ownsClaims(milestone.Owns)
			}
			owners[task.ID] = claims
			if strings.EqualFold(task.ID, taskID) {
				allowed = claims
			}
		}
	}
	// Legacy and intentionally unpartitioned tasks declare no executable lane.
	if len(allowed) == 0 {
		return nil
	}
	var findings []conv.Finding
	for _, raw := range changed {
		path := cleanLanePath(raw)
		if path == "" || anyClaimContains(allowed, path) {
			continue
		}
		owner := "no task"
		for id, claims := range owners {
			if !strings.EqualFold(id, taskID) && anyClaimContains(claims, path) {
				owner = id
				break
			}
		}
		findings = append(findings, conv.Finding{
			Severity: "critical", File: path,
			Issue:     fmt.Sprintf("edit is outside %s's declared write lane (owned by %s)", taskID, owner),
			Fix:       "revert the edit, or amend and approve the plan so this task explicitly owns, produces, or modifies the path before accepting",
			Invariant: "a run may modify only paths in its task's Produces/Modifies/Owns lane",
		})
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].File < findings[j].File })
	return findings
}

// taskFixtureNarrowingFindings catches the cheap, high-confidence forms of a
// test making a named corpus pass by changing the test inputs rather than the
// implementation. It deliberately does not reject selecting the exact named
// cases from a multi-case document; focused acceptance probes need that. It
// rejects mutations below that boundary: filtering provider contributions,
// registries, or a case's selection before run_document sees it (B-385).
func taskFixtureNarrowingFindings(projectRoot, taskID string, changed []string) []conv.Finding {
	plan, err := artifact.Load(projectRoot, artifact.KindPlan)
	if err != nil || plan == nil || strings.TrimSpace(taskID) == "" {
		return nil
	}
	var fixtures []string
	for _, milestone := range plan.Sections {
		for _, task := range milestone.Children {
			if !strings.EqualFold(task.ID, taskID) {
				continue
			}
			for _, item := range strings.Split(task.Field("consumes"), ",") {
				item = strings.TrimSpace(strings.Trim(item, "`"))
				if !strings.HasPrefix(strings.ToLower(item), "file:") {
					continue
				}
				path := cleanLanePath(item[len("file:"):])
				if path != "" && pathHasSegment(path, "conformance") {
					fixtures = append(fixtures, path)
				}
			}
		}
	}
	if len(fixtures) == 0 {
		return nil
	}

	var findings []conv.Finding
	for _, raw := range changed {
		path := cleanLanePath(raw)
		if path == "" || !looksLikeTestSource(path) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(projectRoot, filepath.FromSlash(path)))
		if err != nil {
			continue
		}
		source := string(body)
		var named []string
		for _, fixture := range fixtures {
			if strings.Contains(source, fixture) || strings.Contains(source, filepath.Base(fixture)) {
				named = append(named, "file:"+fixture)
			}
		}
		if len(named) == 0 {
			continue
		}
		line, signal := fixtureNarrowingSignal(source)
		if signal == "" {
			continue
		}
		findings = append(findings, conv.Finding{
			Severity: "critical", File: path, Line: line,
			Issue:     fmt.Sprintf("test narrows the named fixture %s via %s before exercising it", strings.Join(named, ", "), signal),
			Fix:       "exercise the named fixture with the registry, selection, and inputs required by the task; if that cannot pass, report the blocker instead of reducing the fixture",
			Invariant: "an acceptance probe must exercise the resources named by the accepted task without substituting or narrowing them",
		})
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File == findings[j].File {
			return findings[i].Line < findings[j].Line
		}
		return findings[i].File < findings[j].File
	})
	return findings
}

func pathHasSegment(path, segment string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.EqualFold(part, segment) {
			return true
		}
	}
	return false
}

func looksLikeTestSource(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := filepath.Base(lower)
	return strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/") ||
		strings.HasPrefix(lower, "test/") || strings.HasPrefix(lower, "tests/") ||
		strings.Contains(base, "_test.") || strings.Contains(base, ".test.") || strings.Contains(base, "test_")
}

func fixtureNarrowingSignal(source string) (int, string) {
	lines := strings.Split(source, "\n")
	for index, line := range lines {
		compact := strings.ToLower(strings.Join(strings.Fields(line), ""))
		switch {
		case strings.Contains(compact, "payload.") && strings.Contains(compact, ".clear("):
			return index + 1, "clearing provider payload contributions"
		case strings.Contains(compact, "case[") && strings.Contains(compact, "[\"selection\"]") && strings.Contains(compact, "[\"disabled\"]"):
			return index + 1, "editing the corpus case selection"
		case strings.Contains(compact, "document[") && strings.Contains(compact, "[\"selection\"]") && strings.Contains(compact, "[\"disabled\"]"):
			return index + 1, "editing the corpus document selection"
		case strings.Contains(compact, ".retain(") &&
			(strings.Contains(compact, "provider") || strings.Contains(compact, "registry") || strings.Contains(compact, "constructor") || strings.Contains(compact, "capabilit")):
			return index + 1, "retaining only part of the named registry"
		}
	}
	return 0, ""
}

func sectionLaneClaims(section artifact.Section) []laneClaim {
	claims := ownsClaims(section.Owns)
	for _, field := range []string{"produces", "modifies"} {
		for _, item := range strings.Split(section.Field(field), ",") {
			item = strings.TrimSpace(strings.Trim(item, "`"))
			lower := strings.ToLower(item)
			switch {
			case strings.HasPrefix(lower, "file:"):
				claims = append(claims, laneClaim{path: cleanLanePath(item[len("file:"):])})
			case strings.HasPrefix(lower, "dir:"):
				claims = append(claims, laneClaim{path: cleanLanePath(item[len("dir:"):]), tree: true})
			}
		}
	}
	return claims
}

func ownsClaims(items []string) []laneClaim {
	var claims []laneClaim
	for _, raw := range items {
		raw = strings.TrimSpace(strings.Trim(raw, "`"))
		raw = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(raw, "/**"), "/*"), "/")
		if path := cleanLanePath(raw); path != "" {
			// Owns is a topology lane: the established collision checker treats a
			// path and descendants as one claim, including `internal/service`
			// without a trailing slash. Match that public grammar here.
			claims = append(claims, laneClaim{path: path, tree: true})
		}
	}
	return claims
}

func cleanLanePath(path string) string {
	path = strings.TrimSpace(strings.Trim(path, "`"))
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." || path == ".." || strings.HasPrefix(path, "../") || filepath.IsAbs(path) {
		return ""
	}
	return path
}

func anyClaimContains(claims []laneClaim, path string) bool {
	for _, claim := range claims {
		if path == claim.path || (claim.tree && strings.HasPrefix(path, claim.path+"/")) {
			return true
		}
	}
	return false
}
