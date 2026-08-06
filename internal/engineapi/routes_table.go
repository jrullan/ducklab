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

type skillNewRequest struct {
	Name string `json:"name"`
	// Runnable scaffolds a run.sh too. Documentation-only is the default,
	// because it is the cheap and safe form (05 §7).
	Runnable bool `json:"runnable"`
}

type skillRunRequest struct {
	Args map[string]interface{} `json:"args"`
}

type benchRequest struct {
	Suite     string   `json:"suite"`
	Ducklings []string `json:"ducklings"`
	Modes     []string `json:"modes"`
	Keep      bool     `json:"keep"`
}

type diffResponse struct {
	Diff string `json:"diff"`
	// Tests is the part of Diff that touches test files, when the run was
	// flagged for editing tests the task never asked about (05 §5.3). Sent
	// separately so a client shows it first rather than hoping the reader
	// scrolls to it.
	Tests string `json:"tests,omitempty"`
	// Warning is what to tell the reader about Tests. Sent by the engine
	// rather than composed by each client: the CLI may not import verify
	// (AC-16), and three clients inventing three wordings for the same
	// finding is three things to keep in step.
	Warning string `json:"warning,omitempty"`
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
		{Method: "GET", Path: "/v1/projects/{id}/gate", Auth: true,
			Response:     service.GateStatus{},
			Summary:      "The configured gate beside the one detection finds today",
			ClientMethod: "ProjectGate",
			handler:      func(s *Server) http.HandlerFunc { return s.handleProjectGate }},
		{Method: "POST", Path: "/v1/projects/{id}/gate", Auth: true,
			Response:     service.GateStatus{},
			Summary:      "Adopt the detected gate. Never automatic: a gate decides what a verdict means.",
			ClientMethod: "ProjectGateAdopt",
			handler:      func(s *Server) http.HandlerFunc { return s.handleProjectGateAdopt }},
		{Method: "POST", Path: "/v1/projects/{id}/gate/run", Auth: true,
			Response:     service.GateResult{},
			Summary:      "Run the gate now and report what happened. On demand: a gate can be a whole test suite.",
			ClientMethod: "GateRun",
			handler:      func(s *Server) http.HandlerFunc { return s.handleGateRun }},
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
		{Method: "PUT", Path: "/v1/ducklings/{id}", Auth: true,
			Request: service.DucklingView{}, Summary: "Add or replace a duckling",
			ClientMethod: "DucklingSet",
			handler:      func(s *Server) http.HandlerFunc { return s.handleDucklingSet }},
		{Method: "DELETE", Path: "/v1/ducklings/{id}", Auth: true,
			Summary: "Remove a duckling", ClientMethod: "DucklingRemove",
			handler: func(s *Server) http.HandlerFunc { return s.handleDucklingRemove }},
		{Method: "GET", Path: "/v1/defaults/budget", Auth: true,
			Response:     service.BudgetView{},
			Summary:      "The budget every run starts with. It was invisible and immutable, so a run that hit the ceiling failed with a number nobody had chosen.",
			ClientMethod: "BudgetDefaults",
			handler:      func(s *Server) http.HandlerFunc { return s.handleBudgetDefaults }},
		{Method: "PUT", Path: "/v1/defaults/budget", Auth: true,
			Request: service.BudgetView{}, Summary: "Replace the default run budget",
			ClientMethod: "BudgetDefaultsSet",
			handler:      func(s *Server) http.HandlerFunc { return s.handleBudgetDefaultsSet }},
		{Method: "GET", Path: "/v1/defaults/modes", Auth: true,
			Response:     service.ModeDefaultsView{},
			Summary:      "Per-mode defaults: rounds, the duckling line-up, and the per-turn model-call cap. All lived only in the scripts and the config.",
			ClientMethod: "ModeDefaults",
			handler:      func(s *Server) http.HandlerFunc { return s.handleModeDefaults }},
		{Method: "PUT", Path: "/v1/defaults/modes", Auth: true,
			Request: service.ModeDefaultsView{}, Summary: "Replace the per-mode defaults",
			ClientMethod: "ModeDefaultsSet",
			handler:      func(s *Server) http.HandlerFunc { return s.handleModeDefaultsSet }},
		{Method: "GET", Path: "/v1/providers", Auth: true,
			Response:     listOf{Items: []service.ProviderView{}},
			Summary:      "Configured providers. Carries the name of the key's environment variable, never a key (I10).",
			ClientMethod: "ProviderList",
			handler:      func(s *Server) http.HandlerFunc { return s.handleProviderList }},
		{Method: "PUT", Path: "/v1/providers/{id}", Auth: true,
			Request: service.ProviderView{}, Summary: "Add or replace a provider",
			ClientMethod: "ProviderSet",
			handler:      func(s *Server) http.HandlerFunc { return s.handleProviderSet }},
		{Method: "DELETE", Path: "/v1/providers/{id}", Auth: true,
			Summary: "Remove a provider", ClientMethod: "ProviderRemove",
			handler: func(s *Server) http.HandlerFunc { return s.handleProviderRemove }},
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
		{Method: "GET", Path: "/v1/projects/{id}/skills", Auth: true,
			Response:     listOf{Items: []service.SkillSummary{}},
			Summary:      "Skills available to a project, project shadowing global",
			ClientMethod: "SkillList",
			handler:      func(s *Server) http.HandlerFunc { return s.handleSkillList }},
		{Method: "GET", Path: "/v1/projects/{id}/skills/{name}", Auth: true,
			Summary: "One skill, with its body and any validation problems", ClientMethod: "SkillGet",
			handler: func(s *Server) http.HandlerFunc { return s.handleSkillGet }},
		{Method: "POST", Path: "/v1/projects/{id}/skills", Auth: true,
			Request: skillNewRequest{}, Summary: "Scaffold a skill directory",
			ClientMethod: "SkillNew",
			handler:      func(s *Server) http.HandlerFunc { return s.handleSkillNew }},
		{Method: "POST", Path: "/v1/projects/{id}/skills/{name}/run", Auth: true,
			Request: skillRunRequest{}, Summary: "Run a skill. No model is involved.",
			ClientMethod: "SkillRun",
			handler:      func(s *Server) http.HandlerFunc { return s.handleSkillRun }},
		{Method: "POST", Path: "/v1/bench/start", Auth: true,
			Request:      benchRequest{},
			Summary:      "Start a bench without holding the request open: cells run as ordinary runs and the result appears in the list when done.",
			ClientMethod: "BenchStart",
			handler:      func(s *Server) http.HandlerFunc { return s.handleBenchStart }},
		{Method: "POST", Path: "/v1/bench", Auth: true,
			Request:      benchRequest{},
			Summary:      "Run a benchmark suite. No project; every cell is a throwaway.",
			ClientMethod: "Bench",
			handler:      func(s *Server) http.HandlerFunc { return s.handleBench }},
		{Method: "GET", Path: "/v1/bench", Auth: true,
			Response: listOf{Items: []service.BenchSummary{}},
			Summary:  "Past bench results, newest first", ClientMethod: "BenchList",
			handler: func(s *Server) http.HandlerFunc { return s.handleBenchList }},
		{Method: "GET", Path: "/v1/bench/{suite}/{stamp}", Auth: true,
			Summary: "One bench result", ClientMethod: "BenchGet",
			handler: func(s *Server) http.HandlerFunc { return s.handleBenchGet }},
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
		{Method: "GET", Path: "/v1/runs/{id}/brief", Auth: true,
			Summary: "What a person asked this run for, verbatim", ClientMethod: "RunBrief",
			handler: func(s *Server) http.HandlerFunc { return s.handleRunBrief }},
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
		{Method: "GET", Path: "/v1/projects/{id}/bugs", Auth: true,
			Summary: "List bugs", ClientMethod: "BugList",
			handler: func(s *Server) http.HandlerFunc { return s.handleBugList }},
		{Method: "POST", Path: "/v1/projects/{id}/bugs", Auth: true,
			Request: service.BugRequest{}, Summary: "Report a bug", ClientMethod: "BugAdd",
			handler: func(s *Server) http.HandlerFunc { return s.handleBugAdd }},
		{Method: "PUT", Path: "/v1/projects/{id}/bugs/{bug}", Auth: true,
			Request: service.BugRequest{},
			Summary:      "Correct what a report says. A bug could be moved, triaged and promoted but never edited.",
			ClientMethod: "BugEdit",
			handler:      func(s *Server) http.HandlerFunc { return s.handleBugEdit }},
		{Method: "DELETE", Path: "/v1/projects/{id}/tasks/{task}", Auth: true,
			Summary:      "Remove a task from the plan. Refused once a run has named it.",
			ClientMethod: "TaskRemove",
			handler:      func(s *Server) http.HandlerFunc { return s.handleTaskRemove }},
		{Method: "DELETE", Path: "/v1/projects/{id}/artifacts/{kind}/proposal", Auth: true,
			Summary:      "Discard a pending proposal. A rejected one stays on disk by design (05 §1.1); this is the person's explicit act of letting it go.",
			ClientMethod: "ArtifactDiscard",
			handler:      func(s *Server) http.HandlerFunc { return s.handleArtifactDiscard }},
		{Method: "POST", Path: "/v1/projects/{id}/bugs/triage", Auth: true,
			Response: runlog.Run{}, Summary: "Triage the open bugs", ClientMethod: "BugTriage",
			handler: func(s *Server) http.HandlerFunc { return s.handleBugTriage }},
		{Method: "POST", Path: "/v1/projects/{id}/bugs/{bug}/promote", Auth: true,
			Summary: "Promote a bug to a task", ClientMethod: "BugPromote",
			handler: func(s *Server) http.HandlerFunc { return s.handleBugPromote }},
		{Method: "POST", Path: "/v1/projects/{id}/bugs/{bug}/status", Auth: true,
			Summary: "Move a bug", ClientMethod: "BugMove",
			handler: func(s *Server) http.HandlerFunc { return s.handleBugMove }},
		{Method: "GET", Path: "/v1/projects/{id}/reviews", Auth: true,
			Summary: "List reviews", ClientMethod: "ReviewList",
			handler: func(s *Server) http.HandlerFunc { return s.handleReviewList }},
		{Method: "GET", Path: "/v1/projects/{id}/reviews/{task}", Auth: true,
			Summary: "Read a review", ClientMethod: "ReviewGet",
			handler: func(s *Server) http.HandlerFunc { return s.handleReviewGet }},
		{Method: "GET", Path: "/v1/projects/{id}/releases", Auth: true,
			Summary: "List releases", ClientMethod: "ReleaseList",
			handler: func(s *Server) http.HandlerFunc { return s.handleReleaseList }},
		{Method: "GET", Path: "/v1/projects/{id}/releases/{version}", Auth: true,
			Summary: "Read a release", ClientMethod: "ReleaseGet",
			handler: func(s *Server) http.HandlerFunc { return s.handleReleaseGet }},
		{Method: "POST", Path: "/v1/projects/{id}/releases", Auth: true,
			Request: service.ReleaseRequest{}, Response: runlog.Run{},
			Summary: "Plan a release", ClientMethod: "ReleasePlan",
			handler: func(s *Server) http.HandlerFunc { return s.handleReleasePlan }},
		{Method: "POST", Path: "/v1/projects/{id}/releases/{version}/cut", Auth: true,
			Summary: "Cut a planned release", ClientMethod: "ReleaseCut",
			handler: func(s *Server) http.HandlerFunc { return s.handleReleaseCut }},
		{Method: "POST", Path: "/v1/projects/{id}/reviews", Auth: true,
			Request: service.ReviewRequest{}, Response: runlog.Run{},
			Summary: "Review an accepted task", ClientMethod: "ReviewStart",
			handler: func(s *Server) http.HandlerFunc { return s.handleReviewStart }},
		{Method: "POST", Path: "/v1/projects/{id}/tests", Auth: true,
			Request: service.TestFirstRequest{}, Response: runlog.Run{},
			Summary:      "Write the failing test for a task, before the code exists",
			ClientMethod: "TestStart",
			handler:      func(s *Server) http.HandlerFunc { return s.handleTestStart }},
		{Method: "POST", Path: "/v1/projects/{id}/tasks/{task}/retire-test", Auth: true,
			Response:     runlog.Run{},
			Summary:      "Withdraw a committed failing test: revert its commit, free the task and the queue",
			ClientMethod: "TestRetire",
			handler:      func(s *Server) http.HandlerFunc { return s.handleTestRetire }},
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
		{Method: "GET", Path: "/v1/projects/{id}/trace/report", Auth: true,
			Response: renderedResponse{},
			Summary:  "The development report: narrative from the approved requirements, the requirement→spec→task matrix with statuses, bug fixes, releases, spine health. Deterministic — no model writes the record.",
			ClientMethod: "TraceReport",
			handler:      func(s *Server) http.HandlerFunc { return s.handleTraceReport }},

		// Stream
		{Method: "GET", Path: "/v1/events", Auth: true,
			Summary: "SSE event stream. Accepts ?token= because EventSource cannot set headers.",
			handler: func(s *Server) http.HandlerFunc { return s.handleEvents }},
	}
}
