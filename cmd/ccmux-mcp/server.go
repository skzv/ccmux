package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// MCP protocol version this server implements — the latest revision
// we've tested against, and what we answer with when the client
// requests a version we don't know. See supportedProtocolVersions
// for the negotiation set.
const protocolVersion = "2025-06-18"

// supportedProtocolVersions are the MCP revisions this server can
// serve. Everything we implement (tools over newline-delimited stdio)
// has an identical wire shape across these revisions, so when the
// client requests one of them in initialize we echo it back per spec;
// anything else gets protocolVersion and the client decides.
var supportedProtocolVersions = []string{"2024-11-05", "2025-03-26", protocolVersion}

// nullID is the JSON-RPC id for responses whose request id could not
// be determined (parse errors, oversized frames). The spec requires
// the id member to be present and literal null in that case — ID is
// a json.RawMessage with omitempty, so leaving it unset would drop
// the member entirely and strict clients would discard the response.
var nullID = json.RawMessage("null")

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
	// MCP requests/responses can be large (full pane previews, project
	// lists); 4 MiB is generous and matches the daemon's inbound JSON
	// cap. Lines over the cap are drained and answered with a JSON-RPC
	// error instead of aborting the loop — bufio.Scanner's ErrTooLong
	// is unrecoverable, so one oversized request used to kill every
	// in-flight and future call on this transport.
	const maxLine = 4 << 20
	r := bufio.NewReaderSize(in, 64<<10)
	enc := json.NewEncoder(out)

	for {
		line, tooLong, err := readLimitedLine(r, maxLine)
		if tooLong {
			s.writeFrame(enc, rpcResponse{JSONRPC: "2.0", ID: nullID, Error: &rpcError{Code: errParseError, Message: fmt.Sprintf("parse error: request exceeds %d bytes", maxLine)}})
		} else if len(line) > 0 {
			s.dispatchLine(ctx, enc, line)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
	}
}

// dispatchLine parses and handles one raw frame, writing the response
// (if any) to enc.
func (s *Server) dispatchLine(ctx context.Context, enc *json.Encoder, line []byte) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		// The id couldn't be determined, so per spec it must be
		// literal null — not absent.
		s.writeFrame(enc, rpcResponse{JSONRPC: "2.0", ID: nullID, Error: &rpcError{Code: errParseError, Message: "parse error: " + err.Error()}})
		return
	}
	if req.JSONRPC != "2.0" {
		id := req.ID
		if len(id) == 0 {
			id = nullID
		}
		s.writeFrame(enc, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: errInvalidRequest, Message: `jsonrpc must be "2.0"`}})
		return
	}
	resp, isNotification := s.handle(ctx, &req)
	if isNotification {
		return
	}
	s.writeFrame(enc, resp)
}

// readLimitedLine reads one newline-terminated line from r, capped at
// max bytes. Oversized lines are drained through their newline (or
// EOF) and reported via tooLong=true so the caller can answer with a
// JSON-RPC error and keep serving — unlike bufio.Scanner, whose
// ErrTooLong poisons the scanner. err is io.EOF at end of input; a
// final unterminated line is returned alongside it. The returned line
// has its \n / \r\n terminator stripped.
func readLimitedLine(r *bufio.Reader, max int) (line []byte, tooLong bool, err error) {
	var buf []byte
	for {
		chunk, rerr := r.ReadSlice('\n')
		if !tooLong {
			buf = append(buf, chunk...)
		}
		switch {
		case rerr == nil: // hit the newline
			if tooLong {
				return nil, true, nil
			}
			line = trimLineEnding(buf)
			if len(line) > max {
				return nil, true, nil
			}
			return line, false, nil
		case errors.Is(rerr, bufio.ErrBufferFull):
			// Mid-line. Flip to draining mode once over budget so an
			// arbitrarily long line can't grow the buffer unbounded.
			if !tooLong && len(buf) > max {
				tooLong = true
				buf = nil
			}
		default: // io.EOF or a real read error
			if tooLong {
				return nil, true, rerr
			}
			line = trimLineEnding(buf)
			if len(line) > max {
				return nil, true, rerr
			}
			return line, false, rerr
		}
	}
}

// trimLineEnding strips one trailing \n or \r\n.
func trimLineEnding(b []byte) []byte {
	b = bytes.TrimSuffix(b, []byte("\n"))
	return bytes.TrimSuffix(b, []byte("\r"))
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
		resp.Result = s.handleInitialize(req.Params)
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

// initializeParams is the slice of the client's initialize params we
// act on: the protocol version it wants to speak.
type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

// negotiateProtocolVersion implements the spec's version handshake:
// echo the client's requested version when we support it; otherwise
// answer with our latest and let the client decide whether to
// proceed or disconnect.
func negotiateProtocolVersion(requested string) string {
	for _, v := range supportedProtocolVersions {
		if v == requested {
			return requested
		}
	}
	return protocolVersion
}

func (s *Server) handleInitialize(raw json.RawMessage) initializeResult {
	var p initializeParams
	if len(raw) > 0 {
		// Best-effort: malformed params just fall back to our latest.
		_ = json.Unmarshal(raw, &p)
	}
	return initializeResult{
		ProtocolVersion: negotiateProtocolVersion(p.ProtocolVersion),
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
