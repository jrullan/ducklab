package engineapi

import "testing"

// Scorecards are the Roster's single comparable view of every registered
// duckling, so the public route and generated-client method must be declared
// together in the route table.
func TestScorecardsRouteIsExposedToTypedClients(t *testing.T) {
	for _, route := range routeTable() {
		if route.Method == "GET" && route.Path == "/v1/ducklings/scorecards" {
			if route.ClientMethod != "Scorecards" {
				t.Errorf("scorecards ClientMethod = %q, want Scorecards", route.ClientMethod)
			}
			return
		}
	}
	t.Fatal("route table does not contain GET /v1/ducklings/scorecards")
}
