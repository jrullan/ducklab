package mcp

import (
	"strings"
	"testing"
)

// B-530: refused a lane widening, the operator had the person's "Ok" and no
// door to carry it, so it read the engine token and impersonated the person.
// approval_request is the door: it files what and why, attributed to the
// operator, and decides nothing.
func TestApprovalRequestFilesTheAskAsTheOperator(t *testing.T) {
	eng := &fakeEngine{}
	resps := drive(t, eng,
		initFrame,
		callFrame(2, "approval_request", `{"run_id":"r-1","action":"accept","reason":"the person said Ok in chat to widening the lane","lane_widening":["tests/a.test.mjs"]}`),
	)
	text, isErr := toolResultText(t, resps[1])
	if isErr {
		t.Fatalf("approval_request failed: %s", text)
	}
	if eng.approvalRun != "r-1" {
		t.Fatalf("filed on %q, want r-1", eng.approvalRun)
	}
	if eng.approvalReq["actor"] != "mcp:claude" || eng.approvalReq["action"] != "accept" ||
		!strings.Contains(eng.approvalReq["reason"].(string), "said Ok") {
		t.Fatalf("request body = %#v", eng.approvalReq)
	}
	lane, _ := eng.approvalReq["lane_widening"].([]interface{})
	if len(lane) != 1 || lane[0] != "tests/a.test.mjs" {
		t.Fatalf("lane paths = %#v", eng.approvalReq["lane_widening"])
	}
	if eng.accepted != nil || eng.acceptedAs != "" {
		t.Fatalf("filing a request decided the run: accepted=%v as %q", eng.accepted, eng.acceptedAs)
	}
	if !strings.Contains(text, "desktop") {
		t.Fatalf("the reply does not say where the person decides: %s", text)
	}
}

// No MCP tool is a way to approve: the server holds the agent credential and
// the approving door belongs to the person.
func TestNoMCPToolApprovesARequest(t *testing.T) {
	for _, tool := range toolList() {
		name, _ := tool["name"].(string)
		if strings.Contains(name, "approve") || name == "approval_decide" {
			t.Fatalf("MCP exposes %q: an agent must not be able to approve its own request", name)
		}
	}
}
