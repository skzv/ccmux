package clipboard

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDeps returns Deps wired with the supplied behaviors. Test helper
// keeps each test focused on the scenario rather than struct
// boilerplate.
func fakeDeps(t *testing.T, clients []TmuxClient, queryErr error, sshPIDs map[int]bool, tool []string) Deps {
	t.Helper()
	return Deps{
		ListTmuxClients: func(ctx context.Context) ([]TmuxClient, error) {
			return clients, queryErr
		},
		IsClientSSH: func(pid int) bool {
			return sshPIDs[pid]
		},
		NativeClipboardTool: func() []string {
			return tool
		},
	}
}

// pipeToTempFile returns a tool argv that writes stdin to a tempfile
// (instead of the real pbcopy). The returned function is the assertion
// hook — call it to read back whatever was written.
//
// Why /bin/sh + redirect rather than `tee`: tee echoes to stdout which
// would clutter test output, and we want a tool that's available on
// any CI runner without depending on `tee` quirks.
func pipeToTempFile(t *testing.T) (tool []string, readBack func() string) {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "captured")
	// `sh -c 'cat > $OUT'` works on macOS and Linux runners.
	return []string{"sh", "-c", "cat > " + out},
		func() string {
			b, err := os.ReadFile(out)
			if err != nil {
				return "" // captured nothing
			}
			return string(b)
		}
}

// TestPipeSelection_LocalClient_PipesToNative — the happy path. A
// single local client is attached and the native tool is present.
// Selection should land in the tool's stdin.
func TestPipeSelection_LocalClient_PipesToNative(t *testing.T) {
	tool, readBack := pipeToTempFile(t)
	deps := fakeDeps(t,
		[]TmuxClient{{PID: 100}},
		nil,
		map[int]bool{}, // PID 100 not SSH → local
		tool,
	)
	in := strings.NewReader("hello clipboard")
	if err := PipeSelection(context.Background(), in, "", deps); err != nil {
		t.Fatalf("PipeSelection: %v", err)
	}
	if got := readBack(); got != "hello clipboard" {
		t.Errorf("native tool received %q, want %q", got, "hello clipboard")
	}
}

// TestPipeSelection_RemoteClientOnly_DoesNotPipe — the case the user
// flagged as wrong-clipboard poisoning. A single client attached via
// SSH means the OSC 52 path has already forwarded the selection to
// the right place; running pbcopy here would put a STALE copy on the
// daemon machine's clipboard, surprising whoever sits there later.
//
// Verification: the fake native tool must NOT be invoked. We assert
// that by leaving the tempfile empty and checking emptiness after.
func TestPipeSelection_RemoteClientOnly_DoesNotPipe(t *testing.T) {
	tool, readBack := pipeToTempFile(t)
	deps := fakeDeps(t,
		[]TmuxClient{{PID: 200}},
		nil,
		map[int]bool{200: true}, // PID 200 IS SSH → remote
		tool,
	)
	in := strings.NewReader("should not reach pbcopy")
	if err := PipeSelection(context.Background(), in, "", deps); err != nil {
		t.Fatalf("PipeSelection: %v", err)
	}
	if got := readBack(); got != "" {
		t.Errorf("native tool was invoked with %q — should have been skipped for remote-only client", got)
	}
}

// TestPipeSelection_MixedClients_LocalWins — if even one local client
// is attached alongside remote clients, run the native tool. Reason:
// the local user explicitly wants their copy to land somewhere they
// can paste it locally. The remote user already got their copy via
// OSC 52. Both happy.
func TestPipeSelection_MixedClients_LocalWins(t *testing.T) {
	tool, readBack := pipeToTempFile(t)
	deps := fakeDeps(t,
		[]TmuxClient{{PID: 100}, {PID: 200}},
		nil,
		map[int]bool{200: true}, // 100=local, 200=ssh
		tool,
	)
	in := strings.NewReader("mixed")
	if err := PipeSelection(context.Background(), in, "", deps); err != nil {
		t.Fatalf("PipeSelection: %v", err)
	}
	if got := readBack(); got != "mixed" {
		t.Errorf("native tool received %q, want %q", got, "mixed")
	}
}

// TestPipeSelection_NoClients_DoesNotPipe — `tmux list-clients`
// returning empty is its own signal. No one attached → nowhere
// reasonable to send the selection.
func TestPipeSelection_NoClients_DoesNotPipe(t *testing.T) {
	tool, readBack := pipeToTempFile(t)
	deps := fakeDeps(t,
		[]TmuxClient{}, // none
		nil,
		map[int]bool{},
		tool,
	)
	in := strings.NewReader("nope")
	if err := PipeSelection(context.Background(), in, "", deps); err != nil {
		t.Fatalf("PipeSelection: %v", err)
	}
	if got := readBack(); got != "" {
		t.Errorf("native tool invoked %q with no clients attached — should have skipped", got)
	}
}

// TestPipeSelection_TmuxQueryFails_FallsBackToSafe — `tmux
// list-clients` can fail (no server, permission denied, ancient tmux
// without -F). The safe default is "don't pipe" so we never accidentally
// write to the daemon's clipboard when we can't verify it's the right
// target.
func TestPipeSelection_TmuxQueryFails_FallsBackToSafe(t *testing.T) {
	tool, readBack := pipeToTempFile(t)
	deps := fakeDeps(t,
		nil,
		errors.New("tmux: no server"),
		map[int]bool{},
		tool,
	)
	in := strings.NewReader("nope")
	if err := PipeSelection(context.Background(), in, "", deps); err != nil {
		t.Fatalf("PipeSelection: %v", err)
	}
	if got := readBack(); got != "" {
		t.Errorf("native tool invoked %q despite tmux query failure — should have skipped", got)
	}
}

// TestPipeSelection_NoNativeTool_GracefulNoop — degraded host: local
// client attached but no pbcopy/wl-copy/xclip available. Function must
// succeed cleanly — the OSC 52 path is still ccmux's main copy
// mechanism and shouldn't be derailed by a missing pipe target.
func TestPipeSelection_NoNativeTool_GracefulNoop(t *testing.T) {
	deps := fakeDeps(t,
		[]TmuxClient{{PID: 100}},
		nil,
		map[int]bool{},
		nil, // no tool available
	)
	in := strings.NewReader("anything")
	if err := PipeSelection(context.Background(), in, "", deps); err != nil {
		t.Errorf("PipeSelection should not error when no native tool is available: %v", err)
	}
}

// TestPipeSelection_EmptySelection_NoOp — a zero-byte selection skips
// the entire dispatch. Avoids leaving an empty clipboard entry behind
// when tmux fires the binding spuriously (e.g. mouse click with no
// drag).
func TestPipeSelection_EmptySelection_NoOp(t *testing.T) {
	called := 0
	tool, readBack := pipeToTempFile(t)
	deps := Deps{
		ListTmuxClients: func(ctx context.Context) ([]TmuxClient, error) {
			called++
			return []TmuxClient{{PID: 100}}, nil
		},
		IsClientSSH:         func(int) bool { return false },
		NativeClipboardTool: func() []string { return tool },
	}
	if err := PipeSelection(context.Background(), strings.NewReader(""), "", deps); err != nil {
		t.Fatalf("PipeSelection on empty input: %v", err)
	}
	if got := readBack(); got != "" {
		t.Errorf("empty selection should not invoke native tool, got %q", got)
	}
	// stdin is drained before tmux is asked anything, so an empty
	// selection never reaches the tmux server.
	if called != 0 {
		t.Errorf("ListTmuxClients called %d times for empty input — should short-circuit", called)
	}
}

// TestPipeSelection_RoutesByCopyingClient — the binding names the
// client that made the selection (--client '#{client_tty}'), and with
// a Mac and a phone attached at once only that client decides. Before
// the flag, a phone's copy went into the Mac's pbcopy because "a local
// client is attached" was all the pipe could see.
//
// When the flag is unusable — absent, empty (tmux < 3.0 expands client
// formats in copy-pipe to ""), the literal format (a tmux that didn't
// expand it), or a tty that isn't one of this server's clients — the
// copier is unknown and the old any-local-client rule applies.
func TestPipeSelection_RoutesByCopyingClient(t *testing.T) {
	mac := TmuxClient{PID: 100, TTY: "/dev/ttys001"}
	phone := TmuxClient{PID: 200, TTY: "/dev/pts/7"}
	remote := map[int]bool{200: true}
	cases := []struct {
		name    string
		clients []TmuxClient
		copier  string
		want    bool // native clipboard written
	}{
		{"phone copies while mac attached", []TmuxClient{mac, phone}, phone.TTY, false},
		{"mac copies while phone attached", []TmuxClient{mac, phone}, mac.TTY, true},
		{"mac copies alone", []TmuxClient{mac}, mac.TTY, true},
		{"phone copies alone", []TmuxClient{phone}, phone.TTY, false},
		{"copier tty padded with whitespace", []TmuxClient{mac, phone}, " " + phone.TTY + "\n", false},
		{"no flag falls back to any local", []TmuxClient{mac, phone}, "", true},
		{"no flag, only remote attached", []TmuxClient{phone}, "", false},
		{"unexpanded format falls back", []TmuxClient{mac, phone}, "#{client_tty}", true},
		{"non-client tty is refused, falls back to any local", []TmuxClient{mac, phone}, "/dev/ttys999", true},
		{"non-client tty is refused, only remote attached", []TmuxClient{phone}, "/dev/ttys001", false},
		{"no clients at all", nil, mac.TTY, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, readBack := pipeToTempFile(t)
			deps := fakeDeps(t, tc.clients, nil, remote, tool)
			if err := PipeSelection(context.Background(), strings.NewReader("sel"), tc.copier, deps); err != nil {
				t.Fatalf("PipeSelection: %v", err)
			}
			if got := readBack() == "sel"; got != tc.want {
				t.Errorf("native clipboard written = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPipeSelection_CopierWithFailedQuery — a named copier is only
// trusted once tmux confirms it is attached. With the query failing
// there is nothing to confirm it against, so nothing is piped.
func TestPipeSelection_CopierWithFailedQuery(t *testing.T) {
	tool, readBack := pipeToTempFile(t)
	deps := fakeDeps(t, nil, errors.New("tmux: no server"), map[int]bool{}, tool)
	if err := PipeSelection(context.Background(), strings.NewReader("sel"), "/dev/ttys001", deps); err != nil {
		t.Fatalf("PipeSelection: %v", err)
	}
	if got := readBack(); got != "" {
		t.Errorf("native tool invoked %q for an unverifiable copier", got)
	}
}

// TestCopyingClient — the --client value is matched against tmux's
// own list, exactly; nothing that isn't an attached client's tty
// resolves.
func TestCopyingClient(t *testing.T) {
	clients := []TmuxClient{{PID: 1, TTY: "/dev/ttys001"}, {PID: 2}, {PID: 3, TTY: "/dev/pts/3"}}
	cases := []struct {
		copier  string
		wantPID int // 0 = unresolved
	}{
		{"/dev/ttys001", 1},
		{"/dev/pts/3", 3},
		{"  /dev/pts/3  ", 3},
		{"", 0},
		{"   ", 0},
		{"#{client_tty}", 0},
		{"/dev/ttys00", 0},
		{"/dev/ttys0011", 0},
		{"../../dev/ttys001", 0},
		{"/etc/passwd", 0},
	}
	for _, tc := range cases {
		c, ok := copyingClient(tc.copier, clients)
		if got := map[bool]int{true: c.PID}[ok]; got != tc.wantPID {
			t.Errorf("copyingClient(%q) = pid %d (ok=%v), want %d", tc.copier, c.PID, ok, tc.wantPID)
		}
	}
}

// TestPipeSelection_BinarySafe — selection content can be anything;
// the dispatcher just hands bytes to the tool's stdin. NUL bytes,
// invalid UTF-8, and embedded escape sequences must round-trip
// unchanged.
func TestPipeSelection_BinarySafe(t *testing.T) {
	tool, readBack := pipeToTempFile(t)
	deps := fakeDeps(t,
		[]TmuxClient{{PID: 100}},
		nil,
		map[int]bool{},
		tool,
	)
	tricky := "a\x00b\x1bc\x07d\ne\xff\xfe"
	if err := PipeSelection(context.Background(), strings.NewReader(tricky), "", deps); err != nil {
		t.Fatalf("PipeSelection: %v", err)
	}
	if got := readBack(); got != tricky {
		t.Errorf("binary round-trip mismatch:\n got  %q\n want %q", got, tricky)
	}
}

// TestPipeSelection_StdinReadError_Surfaces — when the input reader
// errors mid-stream (rare but possible if stdin is a closed pipe),
// PipeSelection should surface it rather than silently swallow.
func TestPipeSelection_StdinReadError_Surfaces(t *testing.T) {
	deps := fakeDeps(t,
		[]TmuxClient{{PID: 100}},
		nil,
		map[int]bool{},
		[]string{"true"}, // tool argv that doesn't matter; we error before invoking
	)
	r := io.MultiReader(strings.NewReader("partial"), &errReader{})
	err := PipeSelection(context.Background(), r, "", deps)
	if err == nil {
		t.Error("expected error when stdin read fails, got nil")
	}
}

// errReader returns an error on every Read. Used to simulate a broken
// stdin pipe.
type errReader struct{}

func (errReader) Read(p []byte) (int, error) { return 0, errors.New("simulated pipe break") }

// TestDeps_ZeroValueFillsAllHooks — the production code path uses
// Deps{} and relies on .fill() resolving every hook to its default.
// A nil slot after .fill() would cause a nil-pointer panic on first
// invocation, which would be terrible for a tmux binding that's
// invoked on every copy.
func TestDeps_ZeroValueFillsAllHooks(t *testing.T) {
	filled := Deps{}.fill()
	if filled.ListTmuxClients == nil {
		t.Error("ListTmuxClients hook not filled")
	}
	if filled.IsClientSSH == nil {
		t.Error("IsClientSSH hook not filled")
	}
	if filled.NativeClipboardTool == nil {
		t.Error("NativeClipboardTool hook not filled")
	}
}

// TestParseTmuxClientList covers the per-line parser. tmux's
// `-F '#{client_pid}'` is a stable format but the parser still has to
// be tolerant of trailing newlines, blank lines, and the (rare but
// possible) garbage line. A regression in the trim would route every
// selection to the "no clients attached" branch and break the dispatch
// silently — exactly the failure mode runtime dispatch was meant to
// avoid.
func TestParseTmuxClientList(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []TmuxClient
	}{
		{"empty", "", nil},
		{"trailing-newline", "12345\n", []TmuxClient{{PID: 12345}}},
		{"multiple", "12345\n67890\n", []TmuxClient{{PID: 12345}, {PID: 67890}}},
		{"blank-line-in-middle", "111\n\n222\n", []TmuxClient{{PID: 111}, {PID: 222}}},
		{"garbage-skipped", "111\nnot-a-pid\n222\n", []TmuxClient{{PID: 111}, {PID: 222}}},
		{"whitespace-tolerated", "  333\n  444  \n", []TmuxClient{{PID: 333}, {PID: 444}}},
		// listClientsFormat: "#{client_pid} #{client_tty}".
		{"pid-and-tty", "12345 /dev/ttys003\n67890 /dev/pts/4\n",
			[]TmuxClient{{PID: 12345, TTY: "/dev/ttys003"}, {PID: 67890, TTY: "/dev/pts/4"}}},
		{"client-without-tty", "12345 \n", []TmuxClient{{PID: 12345}}},
		{"garbage-pid-with-tty-skipped", "x /dev/ttys003\n5 /dev/ttys004\n",
			[]TmuxClient{{PID: 5, TTY: "/dev/ttys004"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseTmuxClientList([]byte(tc.in))
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d (got: %+v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d]: %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestEnvMarksRemote — a client counts as remote when it came in over
// SSH, or when ccmuxd spawned it for a phone's attach WebSocket (no
// SSH_CONNECTION in that process tree, but the user isn't here).
func TestEnvMarksRemote(t *testing.T) {
	for env, want := range map[string]bool{
		"HOME=/Users/x\x00TERM=xterm\x00":                         false,
		"SSH_CONNECTION=100.1.2.3 5 100.1.2.4 22\x00":             true,
		"TERM=xterm-256color\x00" + RemoteClientEnv + "=1\x00":    true,
		"USER=x COLORTERM=truecolor " + RemoteClientEnv + "=1 PS": true, // macOS `ps -E` shape
	} {
		if got := envMarksRemote([]byte(env)); got != want {
			t.Errorf("envMarksRemote(%q) = %v, want %v", env, got, want)
		}
	}
}
