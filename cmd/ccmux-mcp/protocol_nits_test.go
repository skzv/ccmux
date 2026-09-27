package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// decodeOne decodes the single response frame out.
func decodeOne(t *testing.T, out string) rpcResponse {
	t.Helper()
	lines := outputLines(out)
	if len(lines) != 1 {
		t.Fatalf("want exactly one response frame, got %d: %q", len(lines), out)
	}
	var r rpcResponse
	if err := json.Unmarshal([]byte(lines[0]), &r); err != nil {
		t.Fatalf("decode %q: %v", lines[0], err)
	}
	return r
}

// TestRequestWithoutMethodIsInvalidRequest — a request with no method
// is malformed (-32600), not a call to an unknown method (-32601).
func TestRequestWithoutMethodIsInvalidRequest(t *testing.T) {
	srv := newTestServer(false, nil)
	r := decodeOne(t, runRaw(t, srv, `{"jsonrpc":"2.0","id":4}`+"\n"))
	if r.Error == nil || r.Error.Code != errInvalidRequest {
		t.Errorf("method-less request: got %+v, want -32600", r.Error)
	}
	if string(r.ID) != "4" {
		t.Errorf("id = %s, want the request's 4 echoed", r.ID)
	}
}

// TestClientResponseFramesAreDropped — a response object (result or
// error, no method) isn't a request; answering it would only confuse
// the client.
func TestClientResponseFramesAreDropped(t *testing.T) {
	srv := newTestServer(false, nil)
	out := runRaw(t, srv,
		`{"jsonrpc":"2.0","id":5,"result":{}}`+"\n"+
			`{"jsonrpc":"2.0","id":6,"error":{"code":-1,"message":"x"}}`+"\n"+
			`{"jsonrpc":"2.0","id":7,"method":"ping"}`+"\n")
	if r := decodeOne(t, out); string(r.ID) != "7" || r.Error != nil {
		t.Errorf("only the ping should be answered, got %+v", r)
	}
}

// TestWhitespaceOnlyLineIsIgnored — a line of spaces/tabs is a
// keep-alive like an empty line, not a -32700 parse error.
func TestWhitespaceOnlyLineIsIgnored(t *testing.T) {
	srv := newTestServer(false, nil)
	out := runRaw(t, srv, "   \t \r\n"+`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n")
	if r := decodeOne(t, out); string(r.ID) != "1" || r.Error != nil {
		t.Errorf("want only the ping's result, got %+v", r)
	}
}

// TestNonScalarIDIsInvalidRequest — JSON-RPC ids are strings, numbers
// or null; an object/array/bool id was accepted and echoed back.
func TestNonScalarIDIsInvalidRequest(t *testing.T) {
	for _, id := range []string{`{"a":1}`, `[1]`, `true`} {
		srv := newTestServer(false, nil)
		r := decodeOne(t, runRaw(t, srv, `{"jsonrpc":"2.0","id":`+id+`,"method":"ping"}`+"\n"))
		if r.Error == nil || r.Error.Code != errInvalidRequest {
			t.Errorf("id %s: got %+v, want -32600", id, r.Error)
		}
		if string(r.ID) != "null" {
			t.Errorf("id %s: response id = %s, want null", id, r.ID)
		}
	}
	// Strings, numbers and null stay valid.
	for _, id := range []string{`"x"`, `-3`, `1.5`, `null`} {
		srv := newTestServer(false, nil)
		if r := decodeOne(t, runRaw(t, srv, `{"jsonrpc":"2.0","id":`+id+`,"method":"ping"}`+"\n")); r.Error != nil {
			t.Errorf("id %s: ping errored %+v", id, r.Error)
		}
	}
}

// TestResourcesAndPromptsListOwnKeyOnly — each empty listing carries
// its own key; both used to return {"resources":[],"prompts":[]}.
func TestResourcesAndPromptsListOwnKeyOnly(t *testing.T) {
	for method, key := range map[string]string{"resources/list": "resources", "prompts/list": "prompts"} {
		srv := newTestServer(false, nil)
		out := runRaw(t, srv, `{"jsonrpc":"2.0","id":1,"method":"`+method+`"}`+"\n")
		var r struct {
			Result map[string]json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &r); err != nil {
			t.Fatalf("%s: decode %q: %v", method, out, err)
		}
		if len(r.Result) != 1 || string(r.Result[key]) != "[]" {
			t.Errorf("%s result = %s, want only %q: []", method, out, key)
		}
	}
}

// TestToolArgumentsMustBeAnObject — every inputSchema is an object; a
// string or array `arguments` ran parameterless tools anyway.
func TestToolArgumentsMustBeAnObject(t *testing.T) {
	for _, args := range []string{`"str"`, `[]`, `42`, `true`} {
		fake := &fakeClient{}
		srv := newTestServer(false, fake)
		r := decodeOne(t, runRaw(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_sessions","arguments":`+args+`}}`+"\n"))
		if r.Error == nil || r.Error.Code != errInvalidParams {
			t.Errorf("arguments %s: got %+v, want -32602", args, r.Error)
		}
	}
	// Missing / null / an object — including one with an unknown field,
	// which some clients send to parameterless tools — still work.
	for _, params := range []string{`{"name":"list_sessions"}`, `{"name":"list_sessions","arguments":null}`, `{"name":"list_sessions","arguments":{"random_string":"x"}}`} {
		srv := newTestServer(false, &fakeClient{})
		if r := decodeOne(t, runRaw(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+params+`}`+"\n")); r.Error != nil {
			t.Errorf("params %s: got %+v, want success", params, r.Error)
		}
	}
}

// TestRemoteAddr — `--host http://mini:7474` was used verbatim and
// dialed http://http//mini:7474.
func TestRemoteAddr(t *testing.T) {
	ok := map[string]string{
		"mini.tail-x.ts.net:7474":         "mini.tail-x.ts.net:7474",
		"http://mini.tail-x.ts.net:7474":  "mini.tail-x.ts.net:7474",
		"HTTP://mini:7474/":               "mini:7474",
		"mini":                            "mini:7474",
		"http://mini":                     "mini:7474",
		" 100.64.0.1:9000 ":               "100.64.0.1:9000",
		"[fd7a:115c:a1e0::1]:7474":        "[fd7a:115c:a1e0::1]:7474",
		"http://[fd7a:115c:a1e0::1]:7474": "[fd7a:115c:a1e0::1]:7474",
		"fd7a:115c:a1e0::1":               "[fd7a:115c:a1e0::1]:7474",
	}
	for in, want := range ok {
		if got, err := remoteAddr(in); err != nil || got != want {
			t.Errorf("remoteAddr(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "http://", "https://mini:7474", "mini:0", "mini:99999", "mini:abc", "http://mini:7474/v1", "user@mini", "mini:7474?x"} {
		if got, err := remoteAddr(in); err == nil {
			t.Errorf("remoteAddr(%q) = %q, want an error", in, got)
		}
	}
}

// TestSendKeysDescriptionIsAccurate — the schema said "Use 'Enter' for
// newline", which models read as "append Enter to the text"; tmux only
// presses a key when the whole argument is a key name.
func TestSendKeysDescriptionIsAccurate(t *testing.T) {
	srv := newTestServer(true, nil)
	tool := srv.tools["send_keys"]
	keys, _ := json.Marshal(tool.InputSchema)
	all := tool.Description + string(keys)
	if strings.Contains(all, "Use 'Enter' for newline") {
		t.Errorf("send_keys still documents the misleading Enter-for-newline shortcut:\n%s", all)
	}
	if !strings.Contains(tool.Description, "call send_keys twice") {
		t.Errorf("send_keys should explain how to type text and then submit it:\n%s", tool.Description)
	}
}
