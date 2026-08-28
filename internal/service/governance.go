package service

import (
	"sort"
	"strconv"
	"strings"
)

const desktopStaleMessage = "this diff changes the frontend source but not the shipped bundle; run make desktop, or cut a release"

// governanceCallouts describes changes to settings that control how a project
// is run. It reads only project.toml hunks, so similarly named source settings
// cannot create a gate warning.
func governanceCallouts(diff string) []string {
	var callouts []string
	inProject := false
	section := ""
	old, added := map[string]string{}, map[string]string{}
	flush := func() {
		keys := make(map[string]bool, len(old)+len(added))
		for key := range old {
			keys[key] = true
		}
		for key := range added {
			keys[key] = true
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			before, hadBefore := old[key]
			after, hadAfter := added[key]
			if hadBefore && hadAfter && before == after {
				continue
			}
			if !hadBefore {
				before = "unset"
			}
			if !hadAfter {
				after = "unset"
			}
			callouts = append(callouts, "this diff changes project "+key+" from "+before+" to "+after)
		}
		old, added = map[string]string{}, map[string]string{}
	}
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			inProject = strings.Contains(line, " a/.ducklab/project.toml b/.ducklab/project.toml")
			section = ""
			continue
		}
		if !inProject || len(line) < 2 || strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}
		// Context lines carry table headings when a hunk changes a key inside a
		// TOML table. They establish scope but are not themselves a change.
		prefix := line[0]
		if prefix != '+' && prefix != '-' && prefix != ' ' {
			continue
		}
		body := strings.TrimSpace(line[1:])
		if strings.HasPrefix(body, "[") && strings.HasSuffix(body, "]") {
			section = strings.Trim(body, "[]")
			continue
		}
		if prefix == ' ' {
			continue
		}
		key, value, ok := strings.Cut(body, "=")
		if !ok {
			continue
		}
		key = governanceKey(section, strings.TrimSpace(key))
		if key == "" {
			continue
		}
		value = strings.TrimSpace(value)
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}
		if prefix == '-' {
			old[key] = value
		} else {
			added[key] = value
		}
	}
	flush()
	return callouts
}

// desktopStale reports whether a candidate changes desktop source without its
// shipped bundle. It examines diff headers so content mentioning these paths
// cannot create a false warning.
func desktopStale(diff string) bool {
	var source, bundle bool
	for _, line := range strings.Split(diff, "\n") {
		if !strings.HasPrefix(line, "diff --git ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		for _, path := range fields[2:4] {
			path = strings.TrimPrefix(path, "a/")
			path = strings.TrimPrefix(path, "b/")
			if strings.HasPrefix(path, "frontend/src/") {
				source = true
			}
			if strings.HasPrefix(path, "cmd/ducklab-desktop/frontend/dist/") {
				bundle = true
			}
		}
	}
	return source && !bundle
}

func governanceKey(section, key string) string {
	if section == "" && key == "autonomy" {
		return "autonomy"
	}
	if section == "verify" || strings.HasPrefix(key, "verify.") {
		return "verify"
	}
	if section == "budget" || strings.HasPrefix(key, "budget.") {
		return "budgets"
	}
	if section == "roster" || section == "roster_seats" || section == "mode_seats" || key == "roster" || key == "roster_seats" || key == "mode_seats" {
		return "seats"
	}
	return ""
}
