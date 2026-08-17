// Package mcp makes ducklab an MCP server: an external model takes the
// user's seat — reads each stage and task result, decides gates, answers
// questions, starts work.
//
// It is the THIRD client, beside the CLI and the desktop, and everything hard
// about "a model operates the app" was already solved for the second one: the
// engine states the legal actions on every run (`next`), verdicts stay gates
// (I2 — what an operator takes over is the HUMAN gate, which is precisely
// what the person connecting one is choosing to delegate), and every decision
// is recorded with the operator's name. The record must never say a human
// decided what a model decided.
//
// Out of scope, deliberately: configuring providers, ducklings or budgets —
// those stay human-owned — and anything that returns a secret, which I10
// already guarantees does not exist.
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"

	"github.com/jrullan/ducklab/internal/build"
)

// Engine is the slice of the engine client the operator surface needs.
type Engine interface {
	ProjectList() ([]map[string]interface{}, error)
	RunList(projectID string) ([]map[string]interface{}, error)
	RunGet(id string) (map[string]interface{}, error)
	RunDiff(id string) (diff, tests, warning string, err error)
	RunAcceptAs(id, message, actor string) (map[string]interface{}, error)
	RunReject(id, reason string) error
	RunAbort(id string) error
	RunResume(id string) (map[string]interface{}, error)
	RunBudgetLift(id, kind, actor string) (map[string]interface{}, error)
	RunAnswer(id, questionID, answer string) error
	RunFileFindings(id string) ([]map[string]interface{}, error)
	RunStart(projectID string, req map[string]interface{}) (map[string]interface{}, error)
	StageStart(projectID, stage string, req map[string]interface{}) (map[string]interface{}, error)
	ArtifactGet(projectID, kind string) (map[string]interface{}, error)
	TaskList(projectID string) ([]map[string]interface{}, error)
	BugAdd(projectID string, req map[string]string) (map[string]interface{}, error)
	BugList(projectID string, openOnly bool) ([]map[string]interface{}, error)
	BugAttach(projectID, bugID, filename, dataB64 string) (map[string]interface{}, error)
	BugTriage(projectID, bugID string, req map[string]interface{}) (map[string]interface{}, error)
	BugPromote(projectID, bugID, actor string) (map[string]interface{}, error)
	BugMove(projectID, bugID, status, actor string) (map[string]interface{}, error)
	TestStart(projectID string, req map[string]interface{}) (map[string]interface{}, error)
	AppStatus(projectID string) (map[string]interface{}, error)
	AppStart(projectID string) (map[string]interface{}, error)
	AppStop(projectID string) error
	ProjectNext(projectID string) ([]map[string]interface{}, error)
	ProjectStatus(projectID string) (map[string]interface{}, error)
}

// Server speaks MCP (JSON-RPC 2.0, newline-delimited) over a reader/writer
// pair — stdio in production, buffers in tests.
type Server struct {
	eng Engine
	// client is the connected operator's name from initialize, used to
	// attribute every decision: approved_by "mcp:claude" is auditable;
	// "human" would be a lie.
	client string
}

func NewServer(eng Engine) *Server { return &Server{eng: eng} }

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve reads requests until the reader closes. Notifications (no id) get no
// reply, per JSON-RPC.
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	scan := bufio.NewScanner(r)
	// A tools/call result can carry a whole document; the default 64k line
	// limit is not a protocol limit.
	scan.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	enc := json.NewEncoder(w)
	for scan.Scan() {
		line := scan.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue // a malformed frame is dropped, never guessed at
		}
		result, rpcErr := s.dispatch(&req)
		if req.ID == nil {
			continue
		}
		resp := map[string]interface{}{"jsonrpc": "2.0", "id": req.ID}
		if rpcErr != nil {
			resp["error"] = rpcErr
		} else {
			resp["result"] = result
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scan.Err()
}

func (s *Server) dispatch(req *rpcRequest) (interface{}, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ClientInfo struct {
				Name string `json:"name"`
			} `json:"clientInfo"`
		}
		_ = json.Unmarshal(req.Params, &p)
		s.client = p.ClientInfo.Name
		if s.client == "" {
			s.client = "operator"
		}
		return map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"serverInfo":      map[string]string{"name": "ducklab", "version": build.Version, "commit": build.Commit, "provenance": build.Provenance()},
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
		}, nil
	case "notifications/initialized", "initialized":
		return nil, nil
	case "ping":
		return map[string]interface{}{}, nil
	case "tools/list":
		return map[string]interface{}{"tools": toolList()}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid params"}
		}
		out, err := s.call(p.Name, p.Arguments)
		if err != nil {
			// Tool failures are results, not protocol errors: the operator
			// reads them and adjusts, same as a person reading a red banner.
			return toolText(fmt.Sprintf("error: %v", err), true), nil
		}
		return out, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
}

// toolText wraps a string as an MCP tool result.
func toolText(text string, isErr bool) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": text}},
		"isError": isErr,
	}
}

func toolJSON(v interface{}) map[string]interface{} {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return toolText(fmt.Sprintf("error: %v", err), true)
	}
	return toolText(string(b), false)
}
