package service

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/conv"
)

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
			Fix:       "revert the edit, or amend and approve the plan so this task explicitly owns or produces the path before accepting",
			Invariant: "a run may modify only paths in its task's Produces/Owns lane",
		})
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].File < findings[j].File })
	return findings
}

func sectionLaneClaims(section artifact.Section) []laneClaim {
	claims := ownsClaims(section.Owns)
	for _, item := range strings.Split(section.Field("produces"), ",") {
		item = strings.TrimSpace(strings.Trim(item, "`"))
		lower := strings.ToLower(item)
		switch {
		case strings.HasPrefix(lower, "file:"):
			claims = append(claims, laneClaim{path: cleanLanePath(item[len("file:"):])})
		case strings.HasPrefix(lower, "dir:"):
			claims = append(claims, laneClaim{path: cleanLanePath(item[len("dir:"):]), tree: true})
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
