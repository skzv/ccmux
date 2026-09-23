package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// runRaw feeds raw newline-delimited input to the server and returns
// the raw output, so tests can see array (batch) frames and the
// absence of a frame.
func runRaw(t *testing.T, srv *Server, input string) string {
	t.Helper()
	var out bytes.Buffer
	if err := srv.Run(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out.String()
}

// outputLines splits server output into its (non-empty) frames.
func outputLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// TestToolsCallWithoutArguments_AllOptional — `arguments` is optional
// in MCP. For a tool whose arguments are all optional, omitting it used
// to fail with -32602 "unexpected end of JSON input" because the
// handler unmarshalled an empty RawMessage.
func TestToolsCallWithoutArguments_AllOptional(t *testing.T) {
	for _, params := range []string{
		`{"name":"spawn_bare_session"}`,
		`{"name":"spawn_bare_session","arguments":null}`,
	} {
		fake := &fakeClient{}
		srv := newTestServer(true, fake)
		out := runRaw(t, srv, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":`+params+"}\n")
		var resp rpcResponse
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("decode %q: %v", out, err)
		}
		if resp.Error != nil {
			t.Errorf("params %s: tools/call errored: %+v", params, resp.Error)
			continue
		}
		if fake.newBareReq == nil {
			t.Errorf("params %s: spawn_bare_session never reached the daemon", params)
		}
	}
}

// TestToolsCallWithoutArguments_RequiredFieldNamed — with required
// fields, a missing `arguments` must report WHICH field is missing,
// not a JSON decoding error.
func TestToolsCallWithoutArguments_RequiredFieldNamed(t *testing.T) {
	srv := newTestServer(false, nil)
	resp := runOnce(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "read_pane"},
	})
	if resp.Error == nil || resp.Error.Code != errInvalidParams {
		t.Fatalf("expected -32602, got %+v", resp.Error)
	}
	if !strings.Contains(resp.Error.Message, "'name' is required") {
		t.Errorf("message = %q, want it to name the missing field", resp.Error.Message)
	}
}

// TestBatch_RespondsWithArray — initialize negotiates 2025-03-26,
// which requires servers to accept JSON-RPC batches. A batch used to
// get a single -32700 parse error. It must get one array frame with a
// response per request, in order, and none for notifications.
func TestBatch_RespondsWithArray(t *testing.T) {
	srv := newTestServer(false, &fakeClient{})
	out := runRaw(t, srv, `[`+
		`{"jsonrpc":"2.0","id":1,"method":"ping"},`+
		`{"jsonrpc":"2.0","method":"notifications/initialized"},`+
		`{"jsonrpc":"2.0","id":"b","method":"tools/call","params":{"name":"list_sessions"}}`+
		"]\n")
	lines := outputLines(out)
	if len(lines) != 1 {
		t.Fatalf("want exactly one frame for a batch, got %d: %q", len(lines), out)
	}
	var resps []rpcResponse
	if err := json.Unmarshal([]byte(lines[0]), &resps); err != nil {
		t.Fatalf("batch reply is not a JSON array of responses: %v (%q)", err, lines[0])
	}
	if len(resps) != 2 {
		t.Fatalf("got %d responses, want 2 (the notification gets none): %q", len(resps), lines[0])
	}
	if string(resps[0].ID) != "1" || resps[0].Error != nil {
		t.Errorf("first response = %+v, want ping result for id 1", resps[0])
	}
	if string(resps[1].ID) != `"b"` || resps[1].Error != nil {
		t.Errorf("second response = %+v, want tools/call result for id \"b\"", resps[1])
	}
}

// TestBatch_Empty — `[]` is an Invalid Request, answered with a single
// error object (not an array) with a null id.
func TestBatch_Empty(t *testing.T) {
	srv := newTestServer(false, nil)
	out := runRaw(t, srv, "[]\n")
	var resp rpcResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
		t.Fatalf("empty batch reply should be a single object: %v (%q)", err, out)
	}
	if resp.Error == nil || resp.Error.Code != errInvalidRequest {
		t.Errorf("empty batch: want -32600, got %+v", resp.Error)
	}
	if !strings.Contains(out, `"id":null`) {
		t.Errorf("empty batch error must carry id null: %q", out)
	}
}

// TestBatch_AllNotificationsGetsNoReply — and the loop keeps serving.
func TestBatch_AllNotificationsGetsNoReply(t *testing.T) {
	srv := newTestServer(false, nil)
	out := runRaw(t, srv,
		`[{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","method":"notifications/cancelled"}]`+"\n"+
			`{"jsonrpc":"2.0","id":9,"method":"ping"}`+"\n")
	lines := outputLines(out)
	if len(lines) != 1 {
		t.Fatalf("want only the ping reply, got %d frames: %q", len(lines), out)
	}
	if !strings.Contains(lines[0], `"id":9`) {
		t.Errorf("surviving frame should answer the ping: %q", lines[0])
	}
}

// TestBatch_InvalidElement — a non-object element gets its own -32600
// (id null) inside the array; valid siblings still run.
func TestBatch_InvalidElement(t *testing.T) {
	srv := newTestServer(false, nil)
	out := runRaw(t, srv, `[1,{"jsonrpc":"2.0","id":2,"method":"ping"}]`+"\n")
	var resps []rpcResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resps); err != nil {
		t.Fatalf("decode batch reply: %v (%q)", err, out)
	}
	if len(resps) != 2 {
		t.Fatalf("got %d responses, want 2: %q", len(resps), out)
	}
	if resps[0].Error == nil || resps[0].Error.Code != errInvalidRequest || string(resps[0].ID) != "null" {
		t.Errorf("element `1` = %+v, want -32600 with id null", resps[0])
	}
	if resps[1].Error != nil || string(resps[1].ID) != "2" {
		t.Errorf("valid sibling = %+v, want ping result for id 2", resps[1])
	}
}

// TestBatch_MalformedIsParseError — broken JSON starting with `[` is
// still a single -32700.
func TestBatch_MalformedIsParseError(t *testing.T) {
	srv := newTestServer(false, nil)
	out := runRaw(t, srv, `[{"jsonrpc":"2.0",`+"\n")
	var resp rpcResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
		t.Fatalf("decode: %v (%q)", err, out)
	}
	if resp.Error == nil || resp.Error.Code != errParseError {
		t.Errorf("want -32700, got %+v", resp.Error)
	}
}
