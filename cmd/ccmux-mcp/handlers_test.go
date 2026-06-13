package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
)

// fakeClient is a minimal DaemonClient implementation that records
// every call and returns whatever the test wired up. Lets handler
// tests assert "the right daemon call was made with the right args"
// independent of a live ccmuxd.
type fakeClient struct {
	sessions      []daemonSessionShorthand // re-marshaled to daemon.SessionState
	sessionsErr   error
	preview       daemon.PreviewResponse
	previewErr    error
	previewCalls  []previewCall
	projects      []daemon.ProjectInfo
	projectsErr   error
	conversations []daemon.Conversation
	conversErr    error
	usage         daemon.AgentUsage
	usageErr      error
	peers         []daemon.PeerInfo
	peersErr      error
	notes         []daemon.NoteEntry
	notesErr      error
	notesCalls    []string
	noteContent   daemon.NoteContent
	noteContErr   error
	noteContCalls []noteContentCall
	searchHits    []daemon.SearchHit
	searchErr     error
	searchCalls   []searchCall
	health        daemon.HealthInfo
	healthErr     error

	newSessionResp daemon.SessionState
	newSessionErr  error
	newSessionReq  *daemon.NewSessionRequest
	newBareResp    daemon.NewBareSessionResponse
	newBareErr     error
	newBareReq     *daemon.NewBareSessionRequest
	sendKeysErr    error
	sendKeysCalls  []sendKeysCall
	killErr        error
	killCalls      []string
}

// daemonSessionShorthand keeps test data compact — only the fields
// the handler tests assert against, expanded to daemon.SessionState
// on demand.
type daemonSessionShorthand struct {
	Name  string
	State string
}

func (f *fakeClient) Sessions(_ context.Context) ([]daemon.SessionState, error) {
	if f.sessionsErr != nil {
		return nil, f.sessionsErr
	}
	out := make([]daemon.SessionState, len(f.sessions))
	for i, s := range f.sessions {
		out[i] = daemon.SessionState{Name: s.Name, State: s.State, Host: "local"}
	}
	return out, nil
}

type previewCall struct {
	Name  string
	Lines int
}

func (f *fakeClient) Preview(_ context.Context, name string, lines int) (daemon.PreviewResponse, error) {
	f.previewCalls = append(f.previewCalls, previewCall{Name: name, Lines: lines})
	return f.preview, f.previewErr
}

func (f *fakeClient) Projects(_ context.Context) ([]daemon.ProjectInfo, error) {
	return f.projects, f.projectsErr
}

func (f *fakeClient) Conversations(_ context.Context) ([]daemon.Conversation, error) {
	return f.conversations, f.conversErr
}

func (f *fakeClient) Usage(_ context.Context) (daemon.AgentUsage, error) {
	return f.usage, f.usageErr
}

func (f *fakeClient) Peers(_ context.Context) ([]daemon.PeerInfo, error) {
	return f.peers, f.peersErr
}

func (f *fakeClient) Notes(_ context.Context, project string) ([]daemon.NoteEntry, error) {
	f.notesCalls = append(f.notesCalls, project)
	return f.notes, f.notesErr
}

type noteContentCall struct {
	Project string
	Path    string
}

func (f *fakeClient) NoteContent(_ context.Context, project, rel string) (daemon.NoteContent, error) {
	f.noteContCalls = append(f.noteContCalls, noteContentCall{Project: project, Path: rel})
	return f.noteContent, f.noteContErr
}

type searchCall struct {
	Project string
	Query   string
}

func (f *fakeClient) SearchNotes(_ context.Context, project, q string) ([]daemon.SearchHit, error) {
	f.searchCalls = append(f.searchCalls, searchCall{Project: project, Query: q})
	return f.searchHits, f.searchErr
}

func (f *fakeClient) Health(_ context.Context) (daemon.HealthInfo, error) {
	return f.health, f.healthErr
}

func (f *fakeClient) NewSession(_ context.Context, req daemon.NewSessionRequest) (daemon.SessionState, error) {
	f.newSessionReq = &req
	return f.newSessionResp, f.newSessionErr
}

func (f *fakeClient) NewBareSession(_ context.Context, req daemon.NewBareSessionRequest) (daemon.NewBareSessionResponse, error) {
	f.newBareReq = &req
	return f.newBareResp, f.newBareErr
}

type sendKeysCall struct {
	Name string
	Keys string
}

func (f *fakeClient) SendKeys(_ context.Context, name, keys string) error {
	f.sendKeysCalls = append(f.sendKeysCalls, sendKeysCall{Name: name, Keys: keys})
	return f.sendKeysErr
}

func (f *fakeClient) Kill(_ context.Context, name string) error {
	f.killCalls = append(f.killCalls, name)
	return f.killErr
}

// callTool invokes a handler the same way the dispatcher would,
// without going through the JSON-RPC framing. Faster iteration for
// per-handler tests.
func callTool(t *testing.T, srv *Server, name string, args any) (any, error) {
	t.Helper()
	tool, ok := srv.tools[name]
	if !ok {
		t.Fatalf("tool %q not registered", name)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return tool.Handler(context.Background(), raw)
}

// TestHandleReadPane_RequiresName — the schema marks name required,
// but the handler also validates explicitly so a malformed payload
// produces a clean error rather than a confusing daemon call.
func TestHandleReadPane_RequiresName(t *testing.T) {
	srv := newTestServer(false, &fakeClient{})
	_, err := callTool(t, srv, "read_pane", map[string]any{})
	if err == nil {
		t.Fatal("read_pane without name must error")
	}
	if _, ok := err.(*invalidArgs); !ok {
		t.Errorf("error type = %T, want *invalidArgs (so dispatch returns -32602)", err)
	}
}

// TestHandleReadPane_CapsLines — a malicious or buggy agent asking
// for 10_000 lines should be silently capped at 500 to keep the
// daemon (and ccmux-mcp's stdout pipe) from being flooded.
func TestHandleReadPane_CapsLines(t *testing.T) {
	fake := &fakeClient{preview: daemon.PreviewResponse{Lines: 500, Content: "ok"}}
	srv := newTestServer(false, fake)
	if _, err := callTool(t, srv, "read_pane", map[string]any{"name": "s", "lines": 10000}); err != nil {
		t.Fatalf("read_pane: %v", err)
	}
	if len(fake.previewCalls) != 1 {
		t.Fatalf("expected 1 daemon call, got %d", len(fake.previewCalls))
	}
	if fake.previewCalls[0].Lines != 500 {
		t.Errorf("lines = %d, want 500 (capped)", fake.previewCalls[0].Lines)
	}
}

// TestHandleReadPane_NegativeLinesRejected — negative line counts
// must hit invalidArgs (not pass to the daemon where they'd 500).
func TestHandleReadPane_NegativeLinesRejected(t *testing.T) {
	srv := newTestServer(false, &fakeClient{})
	_, err := callTool(t, srv, "read_pane", map[string]any{"name": "s", "lines": -1})
	if err == nil {
		t.Fatal("negative lines must error")
	}
	if _, ok := err.(*invalidArgs); !ok {
		t.Errorf("error type = %T, want *invalidArgs", err)
	}
}

// TestHandleReadPane_ZeroLinesIsOK — 0 means "use the daemon's
// default" and must NOT be passed as a query param.
func TestHandleReadPane_ZeroLinesIsOK(t *testing.T) {
	fake := &fakeClient{preview: daemon.PreviewResponse{Lines: 40, Content: "ok"}}
	srv := newTestServer(false, fake)
	if _, err := callTool(t, srv, "read_pane", map[string]any{"name": "s"}); err != nil {
		t.Fatalf("read_pane: %v", err)
	}
	if fake.previewCalls[0].Lines != 0 {
		t.Errorf("lines = %d, want 0 (handler shouldn't fabricate a default)", fake.previewCalls[0].Lines)
	}
}

// TestHandleListNotes_PassesProjectThrough — handler must forward
// `project` to the daemon verbatim. A regex translation here would
// break for projects with funny names.
func TestHandleListNotes_PassesProjectThrough(t *testing.T) {
	fake := &fakeClient{notes: []daemon.NoteEntry{{Rel: "README.md"}}}
	srv := newTestServer(false, fake)
	if _, err := callTool(t, srv, "list_notes", map[string]any{"project": "ccmux"}); err != nil {
		t.Fatalf("list_notes: %v", err)
	}
	if len(fake.notesCalls) != 1 || fake.notesCalls[0] != "ccmux" {
		t.Errorf("notes called with %v, want [ccmux]", fake.notesCalls)
	}
}

// TestHandleListNotes_RequiresProject — missing project → invalidArgs.
func TestHandleListNotes_RequiresProject(t *testing.T) {
	srv := newTestServer(false, &fakeClient{})
	_, err := callTool(t, srv, "list_notes", map[string]any{})
	if _, ok := err.(*invalidArgs); !ok {
		t.Errorf("error type = %T, want *invalidArgs", err)
	}
}

// TestHandleReadNote_RequiresBothFields — both project and path
// must be provided. We test each missing-field case individually so
// a regression doesn't silently get covered by the other.
func TestHandleReadNote_RequiresBothFields(t *testing.T) {
	srv := newTestServer(false, &fakeClient{})
	for _, args := range []map[string]any{
		{},
		{"project": "ccmux"},
		{"path": "README.md"},
	} {
		_, err := callTool(t, srv, "read_note", args)
		if _, ok := err.(*invalidArgs); !ok {
			t.Errorf("args=%v: error type = %T, want *invalidArgs", args, err)
		}
	}
}

// TestHandleSearchNotes_PassesArgs — project and query must reach
// the daemon as-is. Empty query or project rejected.
func TestHandleSearchNotes_PassesArgs(t *testing.T) {
	fake := &fakeClient{searchHits: []daemon.SearchHit{{Rel: "x.md", LineNum: 1, Snippet: "hit"}}}
	srv := newTestServer(false, fake)
	if _, err := callTool(t, srv, "search_notes", map[string]any{"project": "ccmux", "query": "TODO"}); err != nil {
		t.Fatalf("search_notes: %v", err)
	}
	if len(fake.searchCalls) != 1 || fake.searchCalls[0].Project != "ccmux" || fake.searchCalls[0].Query != "TODO" {
		t.Errorf("daemon called with %+v, want {ccmux TODO}", fake.searchCalls)
	}
}

// TestHandleSpawnSession_OnlyWithMutateFlag — without --allow-mutate
// the tool isn't registered at all. Hidden, not just guarded.
func TestHandleSpawnSession_OnlyWithMutateFlag(t *testing.T) {
	srv := newTestServer(false, &fakeClient{})
	if _, ok := srv.tools["spawn_session"]; ok {
		t.Fatal("spawn_session must NOT be registered without --allow-mutate")
	}
}

// TestHandleSpawnSession_ForwardsAllFields — when mutate IS allowed,
// every field on the args struct must reach the daemon request. Easy
// to forget one and silently break a feature.
func TestHandleSpawnSession_ForwardsAllFields(t *testing.T) {
	fake := &fakeClient{newSessionResp: daemon.SessionState{Name: "c-foo", State: "active"}}
	srv := newTestServer(true, fake)
	args := map[string]any{
		"project":  "foo",
		"path":     "/tmp/foo",
		"agent":    "codex",
		"continue": true,
		"name":     "explicit",
	}
	if _, err := callTool(t, srv, "spawn_session", args); err != nil {
		t.Fatalf("spawn_session: %v", err)
	}
	got := fake.newSessionReq
	if got == nil {
		t.Fatal("daemon NewSession wasn't called")
	}
	want := daemon.NewSessionRequest{Project: "foo", Path: "/tmp/foo", Agent: "codex", Continue: true, Name: "explicit"}
	if *got != want {
		t.Errorf("daemon req = %+v, want %+v", *got, want)
	}
}

// TestHandleSendKeys_ForwardsBoth — the most security-sensitive
// mutating tool. We want surgical proof: the exact name and the
// exact keys land on the daemon, nothing else.
func TestHandleSendKeys_ForwardsBoth(t *testing.T) {
	fake := &fakeClient{}
	srv := newTestServer(true, fake)
	if _, err := callTool(t, srv, "send_keys", map[string]any{"name": "c-foo", "keys": "C-c"}); err != nil {
		t.Fatalf("send_keys: %v", err)
	}
	if len(fake.sendKeysCalls) != 1 || fake.sendKeysCalls[0].Name != "c-foo" || fake.sendKeysCalls[0].Keys != "C-c" {
		t.Errorf("daemon SendKeys called with %+v, want [{c-foo C-c}]", fake.sendKeysCalls)
	}
}

// TestHandleKillSession_ForwardsName — kill is destructive; pin the
// exact daemon call so a refactor can't substitute a no-op.
func TestHandleKillSession_ForwardsName(t *testing.T) {
	fake := &fakeClient{}
	srv := newTestServer(true, fake)
	if _, err := callTool(t, srv, "kill_session", map[string]any{"name": "c-bar"}); err != nil {
		t.Fatalf("kill_session: %v", err)
	}
	if len(fake.killCalls) != 1 || fake.killCalls[0] != "c-bar" {
		t.Errorf("daemon Kill called with %v, want [c-bar]", fake.killCalls)
	}
}

// TestHandleListSessions_NilSafe — if the daemon returns a nil
// slice (no sessions), the handler must produce an empty JSON array,
// not `null`. Agents shouldn't have to special-case the difference.
func TestHandleListSessions_NilSafe(t *testing.T) {
	srv := newTestServer(false, &fakeClient{})
	out, err := callTool(t, srv, "list_sessions", map[string]any{})
	if err != nil {
		t.Fatalf("list_sessions: %v", err)
	}
	body, _ := json.Marshal(out)
	if string(body) == "null" {
		t.Error("nil slice serialized as null — must be []")
	}
}

// TestHandleGetUsage_PassThrough — verify the AgentUsage is returned
// unmodified. Otherwise the cost-tracking surface drifts from the
// dashboard.
func TestHandleGetUsage_PassThrough(t *testing.T) {
	fake := &fakeClient{usage: daemon.AgentUsage{
		Claude: daemon.UsageSummary{HasData: true, Prompts: 42, EstimatedCost: 1.23},
	}}
	srv := newTestServer(false, fake)
	out, err := callTool(t, srv, "get_usage", map[string]any{})
	if err != nil {
		t.Fatalf("get_usage: %v", err)
	}
	body, _ := json.Marshal(out)
	if !strings.Contains(string(body), `"prompts":42`) {
		t.Errorf("usage missing daemon-supplied value; got %s", body)
	}
}

// TestHandleListProjects_RoundTrips — sanity check the project list
// flows through. Just enough to catch wiring breaks.
func TestHandleListProjects_RoundTrips(t *testing.T) {
	fake := &fakeClient{projects: []daemon.ProjectInfo{{Name: "ccmux", Host: "local", Modified: time.Now()}}}
	srv := newTestServer(false, fake)
	out, err := callTool(t, srv, "list_projects", map[string]any{})
	if err != nil {
		t.Fatalf("list_projects: %v", err)
	}
	body, _ := json.Marshal(out)
	if !strings.Contains(string(body), `"name":"ccmux"`) {
		t.Errorf("project list missing daemon name; got %s", body)
	}
}

// TestHandleListConversations_RoundTrips — same shape pin for the
// Conversations endpoint. The MCP tool is the primary "what work did
// I do recently" surface for agents.
func TestHandleListConversations_RoundTrips(t *testing.T) {
	fake := &fakeClient{conversations: []daemon.Conversation{{ID: "abc", Agent: "claude", Preview: "fix bug"}}}
	srv := newTestServer(false, fake)
	out, err := callTool(t, srv, "list_conversations", map[string]any{})
	if err != nil {
		t.Fatalf("list_conversations: %v", err)
	}
	body, _ := json.Marshal(out)
	if !strings.Contains(string(body), `"agent":"claude"`) {
		t.Errorf("conversation list missing agent label; got %s", body)
	}
}

// TestHandleListMachines_RoundTrips — peer list, same wiring check.
func TestHandleListMachines_RoundTrips(t *testing.T) {
	fake := &fakeClient{peers: []daemon.PeerInfo{{Hostname: "mini", Addr: "100.x.x.x", Online: true, RunsCCMuxd: true}}}
	srv := newTestServer(false, fake)
	out, err := callTool(t, srv, "list_machines", map[string]any{})
	if err != nil {
		t.Fatalf("list_machines: %v", err)
	}
	body, _ := json.Marshal(out)
	if !strings.Contains(string(body), `"hostname":"mini"`) {
		t.Errorf("peer list missing hostname; got %s", body)
	}
}

// TestHandleGetHealth_RoundTrips — first-probe surface; an agent
// uses this to confirm the daemon is alive before issuing real tool
// calls. Wiring matters.
func TestHandleGetHealth_RoundTrips(t *testing.T) {
	fake := &fakeClient{health: daemon.HealthInfo{OK: true, Hostname: "mini", Sessions: 3, Version: "v0.1.27"}}
	srv := newTestServer(false, fake)
	out, err := callTool(t, srv, "get_daemon_health", map[string]any{})
	if err != nil {
		t.Fatalf("get_daemon_health: %v", err)
	}
	body, _ := json.Marshal(out)
	if !strings.Contains(string(body), `"hostname":"mini"`) {
		t.Errorf("health missing hostname; got %s", body)
	}
}
