//go:build integration

package e2e

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// mcpProc is a running ccmux-mcp child wired to a real ccmuxd. The
// stdin pipe sends JSON-RPC frames; the stdout pipe receives them.
type mcpProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	t      *testing.T
}

func (e *Env) startMCP(args ...string) *mcpProc {
	e.t.Helper()
	cmd := exec.Command(builtCcmuxMCP, args...)
	cmd.Dir = e.Home
	cmd.Env = os.Environ() // newEnv already set sandbox HOME / PATH on the process env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		e.t.Fatalf("mcp stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		e.t.Fatalf("mcp stdout: %v", err)
	}
	cmd.Stderr = &safeBuffer{} // discard but keep around for debug
	if err := cmd.Start(); err != nil {
		e.t.Fatalf("start ccmux-mcp: %v", err)
	}
	mp := &mcpProc{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), t: e.t}
	e.t.Cleanup(mp.stop)
	return mp
}

func (m *mcpProc) stop() {
	if m == nil || m.cmd == nil || m.cmd.Process == nil {
		return
	}
	_ = m.stdin.Close()
	_ = m.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _, _ = m.cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = m.cmd.Process.Kill()
		<-done
	}
}

// call writes one JSON-RPC request and reads exactly one response. Any
// other framing case (notification, parse-error retry) belongs in
// dedicated tests, not this helper.
func (m *mcpProc) call(method string, id int, params any) map[string]any {
	m.t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	b, err := json.Marshal(req)
	if err != nil {
		m.t.Fatalf("marshal %s: %v", method, err)
	}
	if _, err := m.stdin.Write(append(b, '\n')); err != nil {
		m.t.Fatalf("write %s: %v", method, err)
	}
	line, err := m.stdout.ReadBytes('\n')
	if err != nil {
		m.t.Fatalf("read %s: %v", method, err)
	}
	var resp map[string]any
	if err := json.Unmarshal(line, &resp); err != nil {
		m.t.Fatalf("decode %s: %v (raw=%q)", method, err, line)
	}
	return resp
}

// TestMCPServer_RoundTripsThroughLiveDaemon — the only true end-to-end
// path: a real ccmuxd, a real tmux session, a real ccmux-mcp binary
// reading and writing real stdio. Confirms the JSON-RPC wiring,
// initialize handshake, tools/list, and one real tool call all work.
func TestMCPServer_RoundTripsThroughLiveDaemon(t *testing.T) {
	e := newEnv(t)
	writeFile(t, filepath.Join(e.Root, "mcpproj", "CLAUDE.md"), "# mcpproj\n")
	e.newTmuxSession("c-mcp", e.Root)
	e.startDaemon()

	mp := e.startMCP()

	// initialize must come back with the documented protocol version.
	init := mp.call("initialize", 1, map[string]any{"protocolVersion": "2025-06-18"})
	if init["error"] != nil {
		t.Fatalf("initialize errored: %v", init["error"])
	}
	res, _ := init["result"].(map[string]any)
	if got, _ := res["protocolVersion"].(string); got != "2025-06-18" {
		t.Errorf("protocolVersion = %q, want 2025-06-18", got)
	}

	// tools/list must include the read-only set we ship by default.
	tl := mp.call("tools/list", 2, nil)
	tlRes, _ := tl["result"].(map[string]any)
	tools, _ := tlRes["tools"].([]any)
	names := map[string]bool{}
	for _, raw := range tools {
		obj, _ := raw.(map[string]any)
		if n, ok := obj["name"].(string); ok {
			names[n] = true
		}
	}
	for _, must := range []string{"list_sessions", "list_projects", "read_pane", "get_daemon_health"} {
		if !names[must] {
			t.Errorf("tools/list missing %q (got %v)", must, keys(names))
		}
	}
	// And the mutating tools must NOT be present (we didn't pass --allow-mutate).
	for _, mustNot := range []string{"spawn_session", "send_keys", "kill_session"} {
		if names[mustNot] {
			t.Errorf("mutating tool %q leaked into read-only listing", mustNot)
		}
	}

	// tools/call list_sessions must return the harness's c-mcp session.
	call := mp.call("tools/call", 3, map[string]any{"name": "list_sessions", "arguments": map[string]any{}})
	if call["error"] != nil {
		t.Fatalf("tools/call list_sessions errored: %v", call["error"])
	}
	callRes, _ := call["result"].(map[string]any)
	content, _ := callRes["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(content))
	}
	block, _ := content[0].(map[string]any)
	text, _ := block["text"].(string)
	if !strings.Contains(text, `"name": "c-mcp"`) {
		t.Errorf("list_sessions didn't surface the live session; got %s", text)
	}

	// Same call to list_projects must surface the project we wrote.
	pcall := mp.call("tools/call", 4, map[string]any{"name": "list_projects", "arguments": map[string]any{}})
	if pcall["error"] != nil {
		t.Fatalf("list_projects errored: %v", pcall["error"])
	}
	pres, _ := pcall["result"].(map[string]any)
	pcontent, _ := pres["content"].([]any)
	pblock, _ := pcontent[0].(map[string]any)
	ptext, _ := pblock["text"].(string)
	if !strings.Contains(ptext, `"name": "mcpproj"`) {
		t.Errorf("list_projects didn't surface mcpproj; got %s", ptext)
	}
}

// TestMCPServer_MutateGateOff — calling spawn_session via the
// read-only server must produce JSON-RPC method-not-found. Pins the
// security contract: opt-in or it's not on the wire.
func TestMCPServer_MutateGateOff(t *testing.T) {
	e := newEnv(t)
	e.startDaemon()

	mp := e.startMCP() // default: no --allow-mutate
	resp := mp.call("tools/call", 1, map[string]any{
		"name":      "spawn_session",
		"arguments": map[string]any{"project": "x"},
	})
	rerr, _ := resp["error"].(map[string]any)
	if rerr == nil {
		t.Fatal("spawn_session must return a JSON-RPC error when mutate is gated")
	}
	if code, _ := rerr["code"].(float64); int(code) != -32601 {
		t.Errorf("error code = %v, want -32601 method not found", rerr["code"])
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
