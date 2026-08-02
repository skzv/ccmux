package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// newTestServer wires a Server with the canned fake DaemonClient and
// any test-time overrides. Mutating tools are off unless the caller
// passes allowMutate=true.
func newTestServer(allowMutate bool, fake *fakeClient) *Server {
	if fake == nil {
		fake = &fakeClient{}
	}
	return NewServer(fake, allowMutate, "test")
}

// runOnce feeds one JSON-RPC frame in and returns the decoded
// response. Helper for the protocol-level tests below.
func runOnce(t *testing.T, srv *Server, frame any) rpcResponse {
	t.Helper()
	in := encodeFrame(t, frame)
	var out bytes.Buffer
	if err := srv.Run(context.Background(), in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var resp rpcResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (raw=%q)", err, out.String())
	}
	return resp
}

// runMany feeds a list of frames in and returns all decoded responses
// (notifications produce no response, so the count can be < len(frames)).
func runMany(t *testing.T, srv *Server, frames []any) []rpcResponse {
	t.Helper()
	var inBuf bytes.Buffer
	for _, f := range frames {
		b, err := json.Marshal(f)
		if err != nil {
			t.Fatalf("marshal frame: %v", err)
		}
		inBuf.Write(b)
		inBuf.WriteByte('\n')
	}
	var out bytes.Buffer
	if err := srv.Run(context.Background(), &inBuf, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	dec := json.NewDecoder(&out)
	var responses []rpcResponse
	for dec.More() {
		var r rpcResponse
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		responses = append(responses, r)
	}
	return responses
}

func encodeFrame(t *testing.T, frame any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	buf := bytes.NewBuffer(nil)
	buf.Write(b)
	buf.WriteByte('\n')
	return buf
}

// TestInitialize pins the handshake response shape: protocol version,
// the tools capability, and the server name. Agents make decisions
// off this so it can't quietly drift.
func TestInitialize(t *testing.T) {
	srv := newTestServer(false, nil)
	resp := runOnce(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{"protocolVersion": "2025-06-18"},
	})
	if resp.Error != nil {
		t.Fatalf("initialize errored: %+v", resp.Error)
	}
	body, _ := json.Marshal(resp.Result)
	var got initializeResult
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if got.ProtocolVersion != protocolVersion {
		t.Errorf("protocolVersion = %q, want %q", got.ProtocolVersion, protocolVersion)
	}
	if _, ok := got.Capabilities["tools"]; !ok {
		t.Error("capabilities.tools missing — agents won't discover any tool")
	}
	if got.ServerInfo["name"] != "ccmux-mcp" {
		t.Errorf("serverInfo.name = %q, want ccmux-mcp", got.ServerInfo["name"])
	}
}

// TestPing — JSON-RPC heartbeat must return an empty object result.
// Some MCP clients ping every N seconds; if we don't reply they tear
// down the connection.
func TestPing(t *testing.T) {
	srv := newTestServer(false, nil)
	resp := runOnce(t, srv, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "ping"})
	if resp.Error != nil {
		t.Fatalf("ping errored: %+v", resp.Error)
	}
	if resp.Result == nil {
		t.Error("ping must return an object result, got nil")
	}
}

// TestUnknownMethod — anything we don't recognize should respond with
// JSON-RPC -32601 (Method not found), not crash the loop.
func TestUnknownMethod(t *testing.T) {
	srv := newTestServer(false, nil)
	resp := runOnce(t, srv, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "ghost"})
	if resp.Error == nil {
		t.Fatal("unknown method must return JSON-RPC error")
	}
	if resp.Error.Code != errMethodNotFound {
		t.Errorf("code = %d, want %d", resp.Error.Code, errMethodNotFound)
	}
}

// TestNotificationGetsNoResponse — a JSON-RPC notification (no id)
// must NOT produce a response. Sending one back would confuse clients.
func TestNotificationGetsNoResponse(t *testing.T) {
	srv := newTestServer(false, nil)
	responses := runMany(t, srv, []any{
		map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"},
		map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}, // sentinel so we know the loop survived
	})
	if len(responses) != 1 {
		t.Fatalf("got %d responses, want 1 (notification + 1 request)", len(responses))
	}
	if responses[0].Result == nil {
		t.Error("the surviving request (ping) lost its response")
	}
}

// TestParseError — a malformed line must produce a -32700 parse error
// AND the loop must keep running on subsequent lines. Otherwise one
// stray byte takes the whole server down.
func TestParseError(t *testing.T) {
	srv := newTestServer(false, nil)
	var in bytes.Buffer
	in.WriteString("not valid json\n")
	in.WriteString(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")
	var out bytes.Buffer
	if err := srv.Run(context.Background(), &in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	dec := json.NewDecoder(&out)
	var first rpcResponse
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("decode first: %v", err)
	}
	if first.Error == nil || first.Error.Code != errParseError {
		t.Errorf("first response wasn't a parse error: %+v", first)
	}
	var second rpcResponse
	if err := dec.Decode(&second); err != nil {
		t.Fatalf("decode second (loop didn't recover): %v", err)
	}
	if second.Error != nil {
		t.Errorf("second response errored — loop didn't recover: %+v", second.Error)
	}
}

// TestToolsListReadOnly — without --allow-mutate, only read-only tools
// must be listed. A safe default: an agent can't accidentally invoke
// send_keys on a server the user didn't explicitly opt into.
func TestToolsListReadOnly(t *testing.T) {
	srv := newTestServer(false, nil)
	resp := runOnce(t, srv, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	if resp.Error != nil {
		t.Fatalf("tools/list errored: %+v", resp.Error)
	}
	body, _ := json.Marshal(resp.Result)
	var got struct {
		Tools []toolDescriptor `json:"tools"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	names := names(got.Tools)
	mustHave := []string{"list_sessions", "read_pane", "list_projects", "list_conversations", "get_usage", "list_machines", "list_notes", "read_note", "search_notes", "get_daemon_health"}
	for _, n := range mustHave {
		if !contains(names, n) {
			t.Errorf("read-only listing missing %q (got %v)", n, names)
		}
	}
	mustNotHave := []string{"spawn_session", "spawn_bare_session", "send_keys", "kill_session"}
	for _, n := range mustNotHave {
		if contains(names, n) {
			t.Errorf("read-only listing exposed mutating tool %q", n)
		}
	}
}

// TestToolsListMutating — with --allow-mutate, the mutating tools
// must appear alongside the read-only set. This is the explicit
// opt-in path.
func TestToolsListMutating(t *testing.T) {
	srv := newTestServer(true, nil)
	resp := runOnce(t, srv, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	if resp.Error != nil {
		t.Fatalf("tools/list errored: %+v", resp.Error)
	}
	body, _ := json.Marshal(resp.Result)
	var got struct {
		Tools []toolDescriptor `json:"tools"`
	}
	_ = json.Unmarshal(body, &got)
	names := names(got.Tools)
	for _, n := range []string{"spawn_session", "spawn_bare_session", "send_keys", "kill_session"} {
		if !contains(names, n) {
			t.Errorf("mutating listing missing %q (got %v)", n, names)
		}
	}
}

// TestToolsListOrderedAlphabetically — sortTools should produce a
// stable, alphabetized order. Pin so a refactor can't make tool
// listings non-deterministic (which breaks any client cache and any
// snapshot test downstream).
func TestToolsListOrderedAlphabetically(t *testing.T) {
	srv := newTestServer(true, nil)
	resp := runOnce(t, srv, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	body, _ := json.Marshal(resp.Result)
	var got struct {
		Tools []toolDescriptor `json:"tools"`
	}
	_ = json.Unmarshal(body, &got)
	for i := 1; i < len(got.Tools); i++ {
		if got.Tools[i-1].Name > got.Tools[i].Name {
			t.Errorf("tools out of order at %d: %q > %q", i, got.Tools[i-1].Name, got.Tools[i].Name)
		}
	}
}

// TestToolsCallUnknownTool — calling tools/call with a name we don't
// know must return -32601 (method not found), not crash.
func TestToolsCallUnknownTool(t *testing.T) {
	srv := newTestServer(false, nil)
	resp := runOnce(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "ghost", "arguments": map[string]any{}},
	})
	if resp.Error == nil || resp.Error.Code != errMethodNotFound {
		t.Errorf("expected -32601 method not found, got %+v", resp.Error)
	}
}

// TestToolsCallSucceeds — the happy path: list_sessions returns the
// fake's canned data, wrapped in MCP's content/text envelope.
func TestToolsCallSucceeds(t *testing.T) {
	fake := &fakeClient{sessions: []daemonSessionShorthand{{Name: "ccmux", State: "active"}}}
	srv := newTestServer(false, fake)
	resp := runOnce(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "list_sessions", "arguments": map[string]any{}},
	})
	if resp.Error != nil {
		t.Fatalf("tools/call errored: %+v", resp.Error)
	}
	body, _ := json.Marshal(resp.Result)
	var got toolResult
	_ = json.Unmarshal(body, &got)
	if got.IsError {
		t.Errorf("isError=true, want false; content=%s", got.Content[0].Text)
	}
	if len(got.Content) != 1 || got.Content[0].Type != "text" {
		t.Fatalf("expected one text content block, got %+v", got.Content)
	}
	if !strings.Contains(got.Content[0].Text, `"name": "ccmux"`) {
		t.Errorf("result missing session name; got %s", got.Content[0].Text)
	}
}

// TestToolsCallDaemonError — when the daemon client fails (e.g.
// daemon not running), the call must come back as isError=true with
// the error in the text body, NOT as a JSON-RPC -32603. That's the
// MCP convention: tool execution failures are tool results, not
// transport errors.
func TestToolsCallDaemonError(t *testing.T) {
	fake := &fakeClient{sessionsErr: errFake("daemon offline")}
	srv := newTestServer(false, fake)
	resp := runOnce(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "list_sessions", "arguments": map[string]any{}},
	})
	if resp.Error != nil {
		t.Fatalf("daemon error must NOT be a JSON-RPC error: %+v", resp.Error)
	}
	body, _ := json.Marshal(resp.Result)
	var got toolResult
	_ = json.Unmarshal(body, &got)
	if !got.IsError {
		t.Error("isError must be true for tool execution failure")
	}
	if !strings.Contains(got.Content[0].Text, "daemon offline") {
		t.Errorf("error message must be propagated to the agent; got %s", got.Content[0].Text)
	}
}

// TestToolsCallInvalidArguments — when the handler returns invalidArgs,
// the dispatcher must convert it to a JSON-RPC -32602 (Invalid params)
// so the agent's MCP client surfaces it as a protocol error, not a
// silently-failed tool call.
func TestToolsCallInvalidArguments(t *testing.T) {
	srv := newTestServer(false, nil)
	resp := runOnce(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "read_pane", "arguments": map[string]any{}},
	})
	if resp.Error == nil || resp.Error.Code != errInvalidParams {
		t.Errorf("expected -32602 invalid params, got %+v", resp.Error)
	}
}

// TestEmptyLinesIgnored — the stdio loop must skip empty lines
// silently. Some MCP clients send `\n\n` as a keep-alive heartbeat.
func TestEmptyLinesIgnored(t *testing.T) {
	srv := newTestServer(false, nil)
	in := bytes.NewBufferString("\n\n" + `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")
	var out bytes.Buffer
	if err := srv.Run(context.Background(), in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var resp rpcResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (raw=%q)", err, out.String())
	}
	if resp.Error != nil {
		t.Errorf("ping after empty lines failed: %+v", resp.Error)
	}
}

// TestInvalidJSONRPCVersion — frames missing or wrong-version must
// return -32600 invalid request. We're strict because letting a
// non-2.0 frame through risks an interpretation drift in some MCP
// client implementation.
func TestInvalidJSONRPCVersion(t *testing.T) {
	srv := newTestServer(false, nil)
	resp := runOnce(t, srv, map[string]any{"id": 1, "method": "ping"}) // missing jsonrpc
	if resp.Error == nil || resp.Error.Code != errInvalidRequest {
		t.Errorf("expected -32600 invalid request, got %+v", resp.Error)
	}
}

// TestResourcesAndPromptsListReturnEmpty — we declare neither
// capability, but some clients probe anyway. Return an empty list
// rather than -32601 so the client sees "no resources" and moves on.
func TestResourcesAndPromptsListReturnEmpty(t *testing.T) {
	srv := newTestServer(false, nil)
	for _, m := range []string{"resources/list", "prompts/list"} {
		resp := runOnce(t, srv, map[string]any{"jsonrpc": "2.0", "id": 1, "method": m})
		if resp.Error != nil {
			t.Errorf("%s errored: %+v", m, resp.Error)
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, x := range haystack {
		if x == needle {
			return true
		}
	}
	return false
}

func names(ts []toolDescriptor) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

type errFakeMsg string

func errFake(s string) error       { return errFakeMsg(s) }
func (e errFakeMsg) Error() string { return string(e) }
