//go:build integration

package e2e

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestMCPServer_SurvivesOversizedFrame pins the fix from #172: one
// stdin line over the server's 4 MiB cap used to poison the
// bufio.Scanner (ErrTooLong is unrecoverable) and kill the whole
// transport. Now the oversized frame must get a JSON-RPC parse error
// with a literal-null id, and the NEXT valid request must still be
// served — the server did not die.
func TestMCPServer_SurvivesOversizedFrame(t *testing.T) {
	e := newEnv(t)
	e.startDaemon()
	mp := e.startMCP()

	// Sanity: a normal handshake works before the abuse.
	init := mp.call("initialize", 1, map[string]any{"protocolVersion": "2025-06-18"})
	if init["error"] != nil {
		t.Fatalf("initialize errored: %v", init["error"])
	}

	// One line over the 4 MiB cap. Content doesn't matter — length
	// alone must trigger the parse-error response.
	big := bytes.Repeat([]byte("a"), (4<<20)+1024)
	big = append(big, '\n')
	if _, err := mp.stdin.Write(big); err != nil {
		t.Fatalf("write oversized frame: %v", err)
	}

	line, err := mp.stdout.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read response to oversized frame (server died?): %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode oversized-frame response: %v (raw=%q)", err, line)
	}
	// Per JSON-RPC 2.0, a response whose request id could not be
	// determined must carry id: null — the member present and literal
	// null, not absent (strict clients discard the response otherwise).
	id, present := resp["id"]
	if !present {
		t.Errorf("oversized-frame response is missing the id member (must be literal null): %v", resp)
	} else if id != nil {
		t.Errorf("oversized-frame response id = %v, want null", id)
	}
	rerr, _ := resp["error"].(map[string]any)
	if rerr == nil {
		t.Fatalf("oversized frame did not produce a JSON-RPC error: %v", resp)
	}
	if code, _ := rerr["code"].(float64); int(code) != -32700 {
		t.Errorf("oversized-frame error code = %v, want -32700 parse error", rerr["code"])
	}

	// The server must still be alive: the next valid request gets a
	// correct response.
	tl := mp.call("tools/list", 2, nil)
	if tl["error"] != nil {
		t.Fatalf("tools/list after oversized frame errored: %v", tl["error"])
	}
	res, _ := tl["result"].(map[string]any)
	tools, _ := res["tools"].([]any)
	if len(tools) == 0 {
		t.Errorf("tools/list after oversized frame returned no tools: %v", tl)
	}
}

// TestMCPServer_ProtocolVersionNegotiation pins the initialize
// handshake from #172: a protocolVersion the server supports is echoed
// back verbatim; an unsupported or garbage one gets the server's own
// latest revision (per spec, the client then decides whether to
// proceed or disconnect).
func TestMCPServer_ProtocolVersionNegotiation(t *testing.T) {
	e := newEnv(t)
	e.startDaemon()
	mp := e.startMCP()

	// Supported (older) revision → echoed back.
	resp := mp.call("initialize", 1, map[string]any{"protocolVersion": "2025-03-26"})
	if resp["error"] != nil {
		t.Fatalf("initialize(2025-03-26) errored: %v", resp["error"])
	}
	res, _ := resp["result"].(map[string]any)
	if got, _ := res["protocolVersion"].(string); got != "2025-03-26" {
		t.Errorf("initialize(2025-03-26) negotiated %q, want it echoed back", got)
	}

	// Unsupported / garbage → the server answers with its own latest.
	for i, bad := range []string{"1999-01-01", "banana"} {
		resp := mp.call("initialize", 2+i, map[string]any{"protocolVersion": bad})
		if resp["error"] != nil {
			t.Fatalf("initialize(%q) errored: %v", bad, resp["error"])
		}
		res, _ := resp["result"].(map[string]any)
		if got, _ := res["protocolVersion"].(string); got != "2025-06-18" {
			t.Errorf("initialize(%q) negotiated %q, want the server's latest 2025-06-18", bad, got)
		}
	}
}
