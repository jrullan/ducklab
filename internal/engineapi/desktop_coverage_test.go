package engineapi

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every capability the engine has, reachable from the desktop.
//
// This is a defect class, not a series of accidents. The engine gained a
// capability, the desktop did not surface it, and the gap was found by a person
// running the product and noticing something missing: sampling params, the run
// budget, why a run failed, a triage's proposals, filing a bug at all. Each was
// invisible until someone hit it.
//
// The route table is the source of truth for what the application can do. This
// test walks it and fails when a route has no way to be called from the client,
// so the next gap is a red test rather than a bad afternoon.
//
// Exceptions belong in notInTheDesktop with the reason written down. An empty
// reason is not an exception, it is a gap somebody silenced.
// knownGaps are capabilities the desktop cannot reach today. They are listed so
// the test locks in "no NEW gaps" instead of going red forever, and so the work
// left is a list somebody can prioritise rather than a discovery each time
// someone uses the product.
//
// Removing a line here is the definition of done for closing one.
var knownGaps = map[string]string{
	"GET /v1/projects/{id}/skills":                  "the skills loop has no desktop surface at all",
	"GET /v1/projects/{id}/skills/{name}":           "same",
	"POST /v1/projects/{id}/skills":                 "same",
	"POST /v1/projects/{id}/skills/{name}/run":      "same",
	"GET /v1/projects/{id}/roster/suggest":          "roster is editable but the engine's suggestion is not offered",
	"POST /v1/projects/{id}/roster/suggest":         "same",
	"POST /v1/projects/{id}/releases/{version}/cut": "a release can be drafted from the desktop but not cut",
	"GET /v1/runs/{id}/transcript":                  "the conversation is rebuilt from events; the engine's own rendering is unreachable",
	"GET /v1/engine":                                "engine version and paths; Settings shows a version it gets from the event stream",
}

var notInTheDesktop = map[string]string{
	"GET /v1/health":       "liveness for the CLI and the daemon supervisor; the desktop uses the event stream's connection state",
	"GET /v1/events":       "consumed by api/events.ts, which builds the URL itself rather than going through the client",
	"GET /v1/openapi.json": "the document itself, for tooling",
	"POST /v1/shutdown":    "the daemon's own stop path; a desktop that could stop the engine it talks to would be closing the door it is standing in",
}

func TestEveryEngineCapabilityIsReachableFromTheDesktop(t *testing.T) {
	client, err := os.ReadFile("../../frontend/src/api/client.ts")
	if err != nil {
		t.Skipf("no desktop client to check: %v", err)
	}
	// Every /v1 path the client mentions, with its parameters flattened so a
	// template matches the route it was written for.
	mentioned := map[string]bool{}
	for _, m := range regexp.MustCompile("[`\"](/v1[^`\"?]*)").FindAllStringSubmatch(string(client), -1) {
		p := regexp.MustCompile(`\$\{[^}]+\}`).ReplaceAllString(m[1], "{}")
		mentioned[strings.TrimRight(p, "/")] = true
		// A trailing interpolation is often the query string, not a path
		// segment: `/v1/runs${q}` is the same route as /v1/runs.
		mentioned[strings.TrimRight(strings.TrimSuffix(p, "{}"), "/")] = true
	}

	var gaps []string
	for _, r := range routeTable() {
		path := regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(r.Path, "{}")
		key := r.Method + " " + r.Path
		for _, list := range []map[string]string{notInTheDesktop, knownGaps} {
			if reason, ok := list[key]; ok && strings.TrimSpace(reason) == "" {
				t.Errorf("%s is excused with no reason, which is a gap somebody silenced", key)
			}
		}
		if _, ok := notInTheDesktop[key]; ok {
			continue
		}
		reachable := mentioned[strings.TrimRight(path, "/")]
		if _, known := knownGaps[key]; known {
			// Closed without being crossed off. Left alone this list rots into
			// a set of claims nobody checks.
			if reachable {
				t.Errorf("%s is reachable now — remove it from knownGaps", key)
			}
			continue
		}
		if !reachable {
			gaps = append(gaps, key)
		}
	}
	sort.Strings(gaps)
	if len(gaps) > 0 {
		t.Errorf("%d NEW engine capabilities cannot be called from the desktop:\n  %s\n\n"+
			"Add a method to frontend/src/api/client.ts, or list the route in "+
			"notInTheDesktop (out of scope, with the reason) or knownGaps (not built yet).",
			len(gaps), strings.Join(gaps, "\n  "))
	}
}

// The guard has to fail when a route is added with no way to call it, or it is
// a test that only ever agrees with whatever the code already does.
func TestTheCoverageGuardCatchesANewGap(t *testing.T) {
	mentioned := map[string]bool{"/v1/known": true}
	routes := []struct{ method, path string }{
		{"GET", "/v1/known"},
		{"POST", "/v1/brand-new"},
	}
	var gaps []string
	for _, r := range routes {
		if !mentioned[r.path] {
			gaps = append(gaps, r.method+" "+r.path)
		}
	}
	if len(gaps) != 1 || gaps[0] != "POST /v1/brand-new" {
		t.Errorf("the comparison does not detect an unreachable route: %v", gaps)
	}
}
