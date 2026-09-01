package service

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/artifact"
)

var (
	numberedAmendmentMarker     = regexp.MustCompile(`(?i)(?:^|\s)\d+[.)]\s*`)
	coverageWord                = regexp.MustCompile(`[a-z0-9]+`)
	explicitRequirementOverride = regexp.MustCompile(`(?i)\boverrides?\s+(REQ-\d+)\b`)
)

// amendmentCoverageFindings catches a lossy post-merge amendment. Section-wise
// editing deliberately gives each small-model pass only one section; without a
// final whole-document check, deleting an old exclusion could be mistaken for
// implementing the requested positive behavior (Neocapture corrida 47).
//
// This is intentionally lexical and limited to explicitly numbered requests.
// It is a guard against total omission, not a semantic judge: reviewers still
// decide whether the surviving language is correct.
func amendmentCoverageFindings(request string, doc *artifact.Document) []string {
	clauses := numberedClauses(request)
	if len(clauses) < 2 || doc == nil {
		return nil
	}
	documentWords := wordSet(artifact.RenderBody(doc))
	var findings []string
	coveredBySection := map[string][]int{}
	for i, clause := range clauses {
		anchors := coverageAnchors(clause)
		if len(anchors) == 0 {
			continue
		}
		var missing []string
		for _, anchor := range anchors {
			if !containsWordFamily(documentWords, anchor) {
				missing = append(missing, anchor)
			}
		}
		if len(missing) > 0 {
			findings = append(findings, fmt.Sprintf("amendment clause %d left no explicit evidence for %s — preserve the requested behavior in the merged document", i+1, strings.Join(missing, ", ")))
			continue
		}
		for _, section := range doc.Sections {
			words := wordSet(section.Title + "\n" + section.Body)
			matches := true
			for _, anchor := range anchors {
				if !containsWordFamily(words, anchor) {
					matches = false
					break
				}
			}
			if matches {
				coveredBySection[section.ID] = append(coveredBySection[section.ID], i+1)
			}
		}
	}
	for id, covered := range coveredBySection {
		if len(covered) > 1 {
			findings = append(findings, fmt.Sprintf("%s combines amendment clauses %v — independently requested behaviors need independently traceable requirement sections", id, covered))
		}
	}
	for _, section := range doc.Sections {
		for _, match := range explicitRequirementOverride.FindAllStringSubmatch(section.Body, -1) {
			if match[1] != section.ID && requirementSectionExists(doc, strings.ToUpper(match[1])) {
				findings = append(findings, fmt.Sprintf("%s says it overrides %s while both requirements remain — transform the existing requirement instead of keeping conflicting sections", section.ID, strings.ToUpper(match[1])))
			}
		}
	}
	sort.Strings(findings)
	return findings
}

func requirementSectionExists(doc *artifact.Document, id string) bool {
	for _, section := range doc.Sections {
		if section.ID == id {
			return true
		}
	}
	return false
}

func numberedClauses(request string) []string {
	markers := numberedAmendmentMarker.FindAllStringIndex(request, -1)
	if len(markers) < 2 {
		return nil
	}
	clauses := make([]string, 0, len(markers))
	for i, marker := range markers {
		end := len(request)
		if i+1 < len(markers) {
			end = markers[i+1][0]
		}
		clauses = append(clauses, strings.TrimSpace(request[marker[1]:end]))
	}
	return clauses
}

func coverageAnchors(clause string) []string {
	stop := map[string]bool{
		"a": true, "an": true, "and": true, "are": true, "be": true,
		"following": true, "functionality": true, "implemented": true,
		"is": true, "no": true, "not": true, "of": true, "only": true,
		"may": true, "operation": true, "optional": true, "remain": true,
		"required": true, "shall": true, "should": true,
		"the": true, "to": true, "use": true, "uses": true, "using": true,
		"version": true, "versions": true, "new": true, "newer": true,
	}
	seen := map[string]bool{}
	for _, word := range coverageWord.FindAllString(strings.ToLower(clause), -1) {
		if len(word) < 4 || stop[word] {
			continue
		}
		seen[word] = true
	}
	anchors := make([]string, 0, len(seen))
	for word := range seen {
		anchors = append(anchors, word)
	}
	sort.Strings(anchors)
	return anchors
}

func wordSet(text string) map[string]bool {
	out := map[string]bool{}
	for _, word := range coverageWord.FindAllString(strings.ToLower(text), -1) {
		for _, variant := range wordFamily(word) {
			out[variant] = true
		}
	}
	return out
}

func containsWordFamily(words map[string]bool, word string) bool {
	for _, variant := range wordFamily(word) {
		if words[variant] {
			return true
		}
	}
	return false
}

func wordFamily(word string) []string {
	variants := []string{word}
	if len(word) > 5 && strings.HasSuffix(word, "ing") {
		root := strings.TrimSuffix(word, "ing")
		variants = append(variants, root, root+"e")
	}
	if len(word) > 4 && strings.HasSuffix(word, "ed") {
		root := strings.TrimSuffix(word, "ed")
		variants = append(variants, root, root+"e")
	}
	return variants
}
