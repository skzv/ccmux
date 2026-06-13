package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// MCP protocol version this server implements. The spec lets server
// and client negotiate; we advertise the version we've tested against
// and accept whatever the client requests in initialize (the spec
// requires servers to pick the highest mutually-supported version,
// but in practice most clients accept the server's choice).
const protocolVersion = "2025-06-18"

// rpcRequest is one JSON-RPC 2.0 request frame. Method is always set;
// ID is nil for notifications (no response expected) and a string or
// number for requests.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse is one JSON-RPC 2.0 response frame. Exactly one of
// Result / Error is set.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// JSON-RPC 2.0 error codes from the spec, plus MCP's convention of
// using -32602 (Invalid params) for tool-argument validation errors.
const (
	errParseError     = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternal       = -32603
)

// Server holds the long-lived state for one ccmux-mcp process: the
// daemon client it proxies to, the tool registry, and whether
// mutating tools are exposed. Run() drives the stdio loop until the
// client disconnects or ctx cancels.
type Server struct {
	client      DaemonClient
	tools       map[string]Tool
	allowMutate bool
	version     string

	// writeMu serializes writes to stdout so concurrent tool calls
	// don't interleave JSON frames. MCP is request/response so this
	// is mostly defensive — the spec does allow batched/concurrent
	// requests on a single transport.
	writeMu sync.Mutex
}

// NewServer wires a server with the read-only tools always exposed
// and the mutating tools gated on allowMutate. The DaemonClient
// interface keeps tests independent of the live daemon.Client struct.
func NewServer(client DaemonClient, allowMutate bool, version string) *Server {
	s := &Server{client: client, allowMutate: allowMutate, version: version}
	s.tools = buildTools(s)
	return s
}

// Run reads JSON-RPC frames (newline-delimited JSON, per MCP's stdio
// transport) from `in`, dispatches each, and writes responses to
// `out`. Returns nil on EOF, error on unrecoverable I/O failure.
func (s *Server) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	// MCP responses can be large (full pane previews, project lists);
	// the default bufio buffer is 64 KiB which clips on real workloads.
	// 4 MiB is generous and matches the daemon's inbound JSON cap.
	const maxLine = 4 << 20
	buf := make([]byte, 0, 64<<10)
	scanner.Buffer(buf, maxLine)

	enc := json.NewEncoder(out)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeFrame(enc, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: errParseError, Message: "parse error: " + err.Error()}})
			continue
		}
		if req.JSONRPC != "2.0" {
			s.writeFrame(enc, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: errInvalidRequest, Message: `jsonrpc must be "2.0"`}})
			continue
		}
		resp, isNotification := s.handle(ctx, &req)
		if isNotification {
			continue
		}
		s.writeFrame(enc, resp)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	return nil
}

// writeFrame serializes a response and writes it to the encoder.
// Errors are dropped to stderr — there's no recovery path when stdout
// is broken.
func (s *Server) writeFrame(enc *json.Encoder, resp rpcResponse) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = enc.Encode(resp)
}

// handle dispatches one request. Returns (response, isNotification).
// Notifications (request without an ID per JSON-RPC) get no response.
func (s *Server) handle(ctx context.Context, req *rpcRequest) (rpcResponse, bool) {
	isNotification := len(req.ID) == 0
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {
	case "initialize":
		resp.Result = s.handleInitialize()
	case "notifications/initialized":
		// Client signaled it's ready — no response per spec.
		return resp, true
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = s.handleToolsList()
	case "tools/call":
		out, rerr := s.handleToolsCall(ctx, req.Params)
		if rerr != nil {
			resp.Error = rerr
		} else {
			resp.Result = out
		}
	case "resources/list", "prompts/list":
		// We declare neither capability in initialize, but some
		// clients still probe. Return empty rather than -32601 so
		// they don't log a noisy error.
		resp.Result = map[string]any{"resources": []any{}, "prompts": []any{}}
	default:
		if isNotification {
			// Unknown notifications are silently dropped per spec.
			return resp, true
		}
		resp.Error = &rpcError{Code: errMethodNotFound, Message: "unknown method: " + req.Method}
	}
	return resp, isNotification
}

// initializeResult is what `initialize` returns to the client. We
// advertise the `tools` capability only.
type initializeResult struct {
	ProtocolVersion string            `json:"protocolVersion"`
	Capabilities    map[string]any    `json:"capabilities"`
	ServerInfo      map[string]string `json:"serverInfo"`
	Instructions    string            `json:"instructions,omitempty"`
}

func (s *Server) handleInitialize() initializeResult {
	return initializeResult{
		ProtocolVersion: protocolVersion,
		Capabilities:    map[string]any{"tools": map[string]any{}},
		ServerInfo:      map[string]string{"name": "ccmux-mcp", "version": s.version},
		Instructions: "ccmux exposes its session/project/agent state through these tools. " +
			"Use list_sessions to see what's running, read_pane to inspect a session without attaching, " +
			"and list_conversations / read_note to recover past work. Mutating tools (spawn_session, " +
			"send_keys, kill_session) are only available when ccmux-mcp was started with --allow-mutate.",
	}
}

// toolDescriptor is the shape advertised by tools/list.
type toolDescriptor struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func (s *Server) handleToolsList() map[string]any {
	out := make([]toolDescriptor, 0, len(s.tools))
	for name, t := range s.tools {
		out = append(out, toolDescriptor{Name: name, Description: t.Description, InputSchema: t.InputSchema})
	}
	// Stable order — tools/list response order isn't spec-mandated
	// but a deterministic listing makes UIs and tests stable.
	sortTools(out)
	return map[string]any{"tools": out}
}

// toolsCallParams is the JSON-RPC params shape for tools/call.
type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// toolResult is what tools/call returns. The MCP spec wraps tool
// output as a list of content blocks; we use one `text` block whose
// body is the JSON-encoded tool result. Agents (and humans inspecting
// the wire log) read the JSON directly.
type toolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (s *Server) handleToolsCall(ctx context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var p toolsCallParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return toolResult{}, &rpcError{Code: errInvalidParams, Message: "invalid tools/call params: " + err.Error()}
	}
	tool, ok := s.tools[p.Name]
	if !ok {
		return toolResult{}, &rpcError{Code: errMethodNotFound, Message: "unknown tool: " + p.Name}
	}
	// Backstop: a tool handler can't be allowed to hang forever and
	// block the stdio loop. 30s matches the daemon's per-call budget
	// and is well above any legitimate read/write.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	out, err := tool.Handler(ctx, p.Arguments)
	if err != nil {
		// Distinguish "wrong arguments" from "ccmuxd failed."
		// The former is a protocol error the agent should retry
		// with different args; the latter is a tool execution
		// failure that should be reported via isError=true so the
		// agent sees the message.
		var argErr *invalidArgs
		if errors.As(err, &argErr) {
			return toolResult{}, &rpcError{Code: errInvalidParams, Message: argErr.Error()}
		}
		body, _ := json.Marshal(map[string]string{"error": err.Error()})
		return toolResult{Content: []toolContent{{Type: "text", Text: string(body)}}, IsError: true}, nil
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return toolResult{}, &rpcError{Code: errInternal, Message: "marshal tool result: " + err.Error()}
	}
	return toolResult{Content: []toolContent{{Type: "text", Text: string(body)}}}, nil
}

// invalidArgs is a sentinel error type for handlers to signal "the
// caller gave me bad arguments" — server.go converts it to a
// JSON-RPC -32602 instead of an isError=true result.
type invalidArgs struct{ msg string }

func (e *invalidArgs) Error() string { return e.msg }

// sortTools sorts the slice in place by Name. Pulled out so server.go
// stays sort-package-free at the top level.
func sortTools(ts []toolDescriptor) {
	// Insertion sort — tools count is small (~12), this avoids the
	// import and keeps the call site obvious.
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && ts[j-1].Name > ts[j].Name; j-- {
			ts[j-1], ts[j] = ts[j], ts[j-1]
		}
	}
}
