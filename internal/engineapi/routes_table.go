package engineapi

import (
	"net/http"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/duckling"
	"github.com/jrullan/ducklab/internal/report"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/service"
)

// Route describes one endpoint.
//
// This table is the SINGLE source for both the mux and the OpenAPI document.
// A hand-written spec alongside hand-written handlers drifts the moment
// someone adds a route and forgets the other file; deriving both from one
// table makes that impossible by construction (07 §7.3).
type Route struct {
	Method string
	Path   string
	// Auth is false only for /v1/health, which a client must reach before it
	// has read the token file.
	Auth bool
	// Request and Response are zero values of the DTOs. Their Go types are
	// reflected into JSON Schema, so the schema can never disagree with what
	// the handler actually encodes.
	Request  any
	Response any
	Summary  string
	// ClientMethod names the generated client method. Empty means the endpoint
	// is not exposed on the generated clients.
	ClientMethod string
	handler      func(*Server) http.HandlerFunc
}

// listOf documents a `{items, total}` envelope.
type listOf struct {
	Items any `json:"items"`
	Total int `json:"total"`
}

type healthResponse struct {
	OK         bool   `json:"ok"`
	Version    string `json:"version"`
	UptimeS    int    `json:"uptime_s"`
	ActiveRuns int    `json:"active_runs"`
}

type acceptRequest struct {
	Message string `json:"message"`
}

type rejectRequest struct {
	Reason string `json:"reason"`
}

type answerRequest struct {
	QuestionID string `json:"question_id"`
	Answer     string `json:"answer"`
}

type rosterSetRequest struct {
	Role     string `json:"role"`
	Duckling string `json:"duckling"`
}

type ducklingTestRequest struct {
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type ducklingTestResponse struct {
	Text      string  `json:"text"`
	TokensIn  int     `json:"tokens_in"`
	TokensOut int     `json:"tokens_out"`
	CostUSD   float64 `json:"cost_usd"`
}

type diffResponse struct {
	Diff string `json:"diff"`
}

type verifyResponse struct {
	Output string `json:"output"`
}

type transcriptResponse struct {
	Markdown string `json:"markdown"`
}

type traceCheckResponse struct {
	Errors []map[string]string `json:"errors"`
}

// routeTable is the complete API surface.
func routeTable() []Route {
	return []Route{
		{Method: "GET", Path: "/v1/health", Auth: false,
			Response: healthResponse{}, Summary: "Engine liveness. No auth: a client must reach it before reading the token file.",
			ClientMethod: "Health", handler: func(s *Server) http.HandlerFunc { return s.handleHealth }},

		{Method: "GET", Path: "/v1/engine", Auth: true,
			Summary: "Engine info", ClientMethod: "Engine",
			handler: func(s *Server) http.HandlerFunc { return s.handleEngine }},

		{Method: "POST", Path: "/v1/shutdown", Auth: true,
			Summary: "Request a graceful stop",
			handler: func(s *Server) http.HandlerFunc { return s.handleShutdown }},

		// Projects
		{Method: "GET", Path: "/v1/projects", Auth: true,
			Response: listOf{Items: []service.Project{}}, Summary: "List registered projects",
			ClientMethod: "ProjectList", handler: func(s *Server) http.HandlerFunc { return s.handleProjectList }},
		{Method: "POST", Path: "/v1/projects", Auth: true,
			Request: service.InitRequest{}, Response: service.Project{},
			Summary: "Open or initialise a project", ClientMethod: "ProjectInit",
			handler: func(s *Server) http.HandlerFunc { return s.handleProjectCreate }},
		{Method: "GET", Path: "/v1/projects/{id}", Auth: true,
			Response: service.Project{}, Summary: "Get a project", ClientMethod: "ProjectGet",
			handler: func(s *Server) http.HandlerFunc { return s.handleProjectGet }},
		{Method: "PATCH", Path: "/v1/projects/{id}", Auth: true,
			Request: map[string]string{}, Response: service.Project{},
			Summary: "Update project config keys", ClientMethod: "ProjectUpdate",
			handler: func(s *Server) http.HandlerFunc { return s.handleProjectUpdate }},
		{Method: "DELETE", Path: "/v1/projects/{id}", Auth: true,
			Summary: "Unregister a project", ClientMethod: "ProjectForget",
			handler: func(s *Server) http.HandlerFunc { return s.handleProjectForget }},
		{Method: "GET", Path: "/v1/projects/{id}/status", Auth: true,
			Response: service.Status{}, Summary: "Project status", ClientMethod: "ProjectStatus",
			handler: func(s *Server) http.HandlerFunc { return s.handleProjectStatus }},

		// Ducklings
		{Method: "GET", Path: "/v1/ducklings", Auth: true,
			Response: listOf{Items: []duckling.Duckling{}}, Summary: "List ducklings",
			ClientMethod: "DucklingList", handler: func(s *Server) http.HandlerFunc { return s.handleDucklingList }},
		{Method: "POST", Path: "/v1/ducklings/{id}/test", Auth: true,
			Request: ducklingTestRequest{}, Response: ducklingTestResponse{},
			Summary: "One round-trip against a duckling", ClientMethod: "DucklingTest",
			handler: func(s *Server) http.HandlerFunc { return s.handleDucklingTest }},
		{Method: "POST", Path: "/v1/ducklings/{id}/probe", Auth: true,
			Response: duckling.Capabilities{}, Summary: "Force a capability re-probe",
			ClientMethod: "DucklingProbe", handler: func(s *Server) http.HandlerFunc { return s.handleDucklingProbe }},

		// Roster
		{Method: "GET", Path: "/v1/projects/{id}/roster", Auth: true,
			Response: service.RosterView{}, Summary: "Resolved roster", ClientMethod: "RosterGet",
			handler: func(s *Server) http.HandlerFunc { return s.handleRosterGet }},
		{Method: "PUT", Path: "/v1/projects/{id}/roster", Auth: true,
			Request: rosterSetRequest{}, Response: service.RosterView{},
			Summary: "Assign a duckling to a role", ClientMethod: "RosterSet",
			handler: func(s *Server) http.HandlerFunc { return s.handleRosterSet }},
		{Method: "GET", Path: "/v1/projects/{id}/roster/suggest", Auth: true,
			Response: listOf{Items: []service.Suggestion{}}, Summary: "Ranked roster suggestion",
			ClientMethod: "RosterSuggest", handler: func(s *Server) http.HandlerFunc { return s.handleRosterSuggest }},
		{Method: "POST", Path: "/v1/projects/{id}/roster/suggest", Auth: true,
			Response: service.RosterView{}, Summary: "Apply the suggestion",
			ClientMethod: "RosterApply", handler: func(s *Server) http.HandlerFunc { return s.handleRosterApply }},

		// Runs
		{Method: "POST", Path: "/v1/projects/{id}/runs", Auth: true,
			Request: service.RunRequest{}, Response: runlog.Run{},
			Summary: "Start a run", ClientMethod: "RunStart",
			handler: func(s *Server) http.HandlerFunc { return s.handleRunStart }},
		{Method: "GET", Path: "/v1/runs", Auth: true,
			Response: listOf{Items: []runlog.Run{}}, Summary: "List runs", ClientMethod: "RunList",
			handler: func(s *Server) http.HandlerFunc { return s.handleRunList }},
		{Method: "GET", Path: "/v1/runs/{id}", Auth: true,
			Response: service.RunDetail{}, Summary: "Run detail with events", ClientMethod: "RunGet",
			handler: func(s *Server) http.HandlerFunc { return s.handleRunGet }},
		{Method: "GET", Path: "/v1/runs/{id}/diff", Auth: true,
			Response: diffResponse{}, Summary: "Working diff", ClientMethod: "RunDiff",
			handler: func(s *Server) http.HandlerFunc { return s.handleRunDiff }},
		{Method: "GET", Path: "/v1/runs/{id}/candidates", Auth: true,
			Response:     listOf{Items: []service.CandidateView{}},
			Summary:      "Anonymised candidates. Carries no authorship, by design (I7).",
			ClientMethod: "RunCandidates", handler: func(s *Server) http.HandlerFunc { return s.handleRunCandidates }},
		{Method: "GET", Path: "/v1/runs/{id}/verify", Auth: true,
			Response: verifyResponse{}, Summary: "Gate output", ClientMethod: "RunVerify",
			handler: func(s *Server) http.HandlerFunc { return s.handleRunVerify }},
		{Method: "GET", Path: "/v1/runs/{id}/transcript", Auth: true,
			Response: transcriptResponse{}, Summary: "Rendered transcript", ClientMethod: "RunTranscript",
			handler: func(s *Server) http.HandlerFunc { return s.handleRunTranscript }},
		{Method: "GET", Path: "/v1/runs/{id}/llm", Auth: true,
			Summary: "Recorded model calls, redacted", ClientMethod: "RunLLM",
			handler: func(s *Server) http.HandlerFunc { return s.handleRunLLM }},
		{Method: "POST", Path: "/v1/runs/{id}/accept", Auth: true,
			Request: acceptRequest{}, Response: service.AcceptResult{},
			Summary: "Accept and commit", ClientMethod: "RunAccept",
			handler: func(s *Server) http.HandlerFunc { return s.handleRunAccept }},
		{Method: "POST", Path: "/v1/runs/{id}/reject", Auth: true,
			Request: rejectRequest{}, Summary: "Reject", ClientMethod: "RunReject",
			handler: func(s *Server) http.HandlerFunc { return s.handleRunReject }},
		{Method: "POST", Path: "/v1/runs/{id}/abort", Auth: true,
			Summary: "Abort", ClientMethod: "RunAbort",
			handler: func(s *Server) http.HandlerFunc { return s.handleRunAbort }},
		{Method: "POST", Path: "/v1/runs/{id}/resume", Auth: true,
			Response: runlog.Run{}, Summary: "Resume a paused run", ClientMethod: "RunResume",
			handler: func(s *Server) http.HandlerFunc { return s.handleRunResume }},
		{Method: "POST", Path: "/v1/runs/{id}/answer", Auth: true,
			Request: answerRequest{}, Summary: "Answer a pending question", ClientMethod: "RunAnswer",
			handler: func(s *Server) http.HandlerFunc { return s.handleRunAnswer }},

		// The cycle
		{Method: "POST", Path: "/v1/projects/{id}/reviews", Auth: true,
			Request: service.ReviewRequest{}, Response: runlog.Run{},
			Summary: "Review an accepted task", ClientMethod: "ReviewStart",
			handler: func(s *Server) http.HandlerFunc { return s.handleReviewStart }},
		{Method: "POST", Path: "/v1/projects/{id}/stages/{stage}", Auth: true,
			Request: service.StageRequest{}, Response: runlog.Run{},
			Summary: "Run intake, spec or plan", ClientMethod: "StageStart",
			handler: func(s *Server) http.HandlerFunc { return s.handleStageStart }},
		{Method: "GET", Path: "/v1/projects/{id}/artifacts/{kind}", Auth: true,
			Summary: "Read an artifact and any pending proposal", ClientMethod: "ArtifactGet",
			handler: func(s *Server) http.HandlerFunc { return s.handleArtifactGet }},
		{Method: "POST", Path: "/v1/projects/{id}/artifacts/{kind}/promote", Auth: true,
			Summary: "Accept a pending proposal", ClientMethod: "ArtifactPromote",
			handler: func(s *Server) http.HandlerFunc { return s.handleArtifactPromote }},
		{Method: "GET", Path: "/v1/projects/{id}/tasks", Auth: true,
			Response: listOf{Items: []service.TaskView{}}, Summary: "Tasks from the plan",
			ClientMethod: "TaskList", handler: func(s *Server) http.HandlerFunc { return s.handleTaskList }},
		{Method: "GET", Path: "/v1/projects/{id}/tasks/next", Auth: true,
			Response: service.TaskView{}, Summary: "First task whose dependencies are met",
			ClientMethod: "TaskNext", handler: func(s *Server) http.HandlerFunc { return s.handleTaskNext }},
		{Method: "GET", Path: "/v1/projects/{id}/trace/{anyID}", Auth: true,
			Response: artifact.Node{}, Summary: "Walk the spine from an id",
			ClientMethod: "TraceShow", handler: func(s *Server) http.HandlerFunc { return s.handleTraceShow }},

		// Reporting
		{Method: "GET", Path: "/v1/projects/{id}/report", Auth: true,
			Response: report.Report{}, Summary: "Solo-baseline comparison", ClientMethod: "Report",
			handler: func(s *Server) http.HandlerFunc { return s.handleReport }},
		{Method: "GET", Path: "/v1/projects/{id}/trace/check", Auth: true,
			Response: traceCheckResponse{}, Summary: "Traceability check (deterministic, model-free)",
			ClientMethod: "TraceCheck",
			handler:      func(s *Server) http.HandlerFunc { return s.handleTraceCheck }},

		// Stream
		{Method: "GET", Path: "/v1/events", Auth: true,
			Summary: "SSE event stream. Accepts ?token= because EventSource cannot set headers.",
			handler: func(s *Server) http.HandlerFunc { return s.handleEvents }},
	}
}
