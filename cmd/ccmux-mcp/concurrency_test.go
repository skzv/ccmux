package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
)

// hungClient is a DaemonClient whose Sessions / Health / SendKeys /
// Kill block until released (or their context ends), recording what
// ran and how many calls were inside the daemon at once.
type hungClient struct {
	fakeClient
	release chan struct{} // close to let every blocked call return

	mu       sync.Mutex
	active   int
	peak     int
	order    []string
	ctxErrs  []error
	entered  chan string // one value per call entering the daemon
	blockOps map[string]bool
}

func newHungClient(blockOps ...string) *hungClient {
	h := &hungClient{release: make(chan struct{}), entered: make(chan string, 64), blockOps: map[string]bool{}}
	for _, op := range blockOps {
		h.blockOps[op] = true
	}
	return h
}

func (h *hungClient) block(ctx context.Context, op string) error {
	h.mu.Lock()
	h.active++
	if h.active > h.peak {
		h.peak = h.active
	}
	h.order = append(h.order, op+":start")
	h.mu.Unlock()
	h.entered <- op
	var err error
	if h.blockOps[op] {
		select {
		case <-h.release:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	h.mu.Lock()
	h.active--
	h.order = append(h.order, op+":end")
	h.ctxErrs = append(h.ctxErrs, err)
	h.mu.Unlock()
	return err
}

func (h *hungClient) Sessions(ctx context.Context) ([]daemon.SessionState, error) {
	if err := h.block(ctx, "sessions"); err != nil {
		return nil, err
	}
	return []daemon.SessionState{}, nil
}

func (h *hungClient) Health(ctx context.Context) (daemon.HealthInfo, error) {
	if err := h.block(ctx, "health"); err != nil {
		return daemon.HealthInfo{}, err
	}
	return daemon.HealthInfo{OK: true}, nil
}

func (h *hungClient) SendKeys(ctx context.Context, _, _ string) error {
	return h.block(ctx, "send_keys")
}

func (h *hungClient) Kill(ctx context.Context, _ string) error { return h.block(ctx, "kill") }

// liveServer drives Server.Run over a pipe so a test can interleave
// writes and reads (the one-shot helpers only see output after EOF).
type liveServer struct {
	t    *testing.T
	in   *io.PipeWriter
	out  chan string
	done chan error
}

// frameWriter hands every Write (json.Encoder writes one frame per
// Encode call) to a channel.
type frameWriter struct{ ch chan string }

func (w frameWriter) Write(p []byte) (int, error) {
	w.ch <- string(p)
	return len(p), nil
}

func startLive(t *testing.T, srv *Server) *liveServer {
	t.Helper()
	inR, inW := io.Pipe()
	l := &liveServer{t: t, in: inW, out: make(chan string, 64), done: make(chan error, 1)}
	go func() { l.done <- srv.Run(context.Background(), inR, frameWriter{l.out}) }()
	t.Cleanup(func() { _ = inW.Close() })
	return l
}

func (l *liveServer) send(frame string) {
	l.t.Helper()
	if _, err := l.in.Write([]byte(frame + "\n")); err != nil {
		l.t.Fatalf("write frame: %v", err)
	}
}

// next returns the next response frame, failing if none arrives in d.
func (l *liveServer) next(d time.Duration, what string) string {
	l.t.Helper()
	select {
	case f := <-l.out:
		return f
	case <-time.After(d):
		l.t.Fatalf("no response within %v: %s", d, what)
		return ""
	}
}

// finish closes stdin and waits for Run, returning any frames still
// written after that.
func (l *liveServer) finish() []string {
	l.t.Helper()
	_ = l.in.Close()
	select {
	case err := <-l.done:
		if err != nil {
			l.t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		l.t.Fatal("Run did not return after EOF")
	}
	var rest []string
	for {
		select {
		case f := <-l.out:
			rest = append(rest, f)
		default:
			return rest
		}
	}
}

func frameID(t *testing.T, frame string) string {
	t.Helper()
	var r rpcResponse
	if err := json.Unmarshal([]byte(frame), &r); err != nil {
		t.Fatalf("decode %q: %v", frame, err)
	}
	return string(r.ID)
}

func waitEntered(t *testing.T, h *hungClient, want string) {
	t.Helper()
	select {
	case got := <-h.entered:
		if got != want {
			t.Fatalf("daemon call %q entered, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("daemon call %q never started", want)
	}
}

// TestPingNotBlockedByHungToolCall — ccmux-mcp handled one frame at a
// time, so a tool call stuck on a hung daemon held every later frame
// for its 30s backstop; a client pinging meanwhile saw no answer and
// dropped the connection. The ping must be answered while the call is
// still hung.
func TestPingNotBlockedByHungToolCall(t *testing.T) {
	h := newHungClient("sessions")
	l := startLive(t, NewServer(h, false, "test"))

	l.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_sessions"}}`)
	waitEntered(t, h, "sessions")
	l.send(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if id := frameID(t, l.next(3*time.Second, "ping behind a hung tool call")); id != "2" {
		t.Fatalf("first response id = %s, want the ping (2)", id)
	}

	close(h.release)
	if id := frameID(t, l.next(5*time.Second, "the released tool call")); id != "1" {
		t.Fatalf("second response id = %s, want the tool call (1)", id)
	}
	if rest := l.finish(); len(rest) != 0 {
		t.Errorf("unexpected extra frames: %q", rest)
	}
}

// TestCancelledNotificationAbortsInFlightCall — notifications/cancelled
// sat in the queue behind the very call it meant to cancel. It must
// cancel the call's context at once, and (per MCP) the cancelled
// request gets no response.
func TestCancelledNotificationAbortsInFlightCall(t *testing.T) {
	h := newHungClient("sessions")
	defer close(h.release)
	l := startLive(t, NewServer(h, false, "test"))

	l.send(`{"jsonrpc":"2.0","id":"call-7","method":"tools/call","params":{"name":"list_sessions"}}`)
	waitEntered(t, h, "sessions")
	l.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"call-7","reason":"user"}}`)
	l.send(`{"jsonrpc":"2.0","id":8,"method":"ping"}`)
	if id := frameID(t, l.next(3*time.Second, "ping after the cancellation")); id != "8" {
		t.Fatalf("response id = %s, want the ping (8)", id)
	}
	rest := l.finish()
	for _, f := range rest {
		if strings.Contains(f, `"call-7"`) {
			t.Errorf("a cancelled request must get no response, got %s", f)
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.ctxErrs) != 1 || h.ctxErrs[0] == nil {
		t.Errorf("the hung daemon call should have seen its context cancelled; got %v", h.ctxErrs)
	}
}

// TestReadOnlyCallsRunConcurrently — two list_sessions calls must be
// inside the daemon at the same time; the sequential loop ran the
// second only after the first returned.
func TestReadOnlyCallsRunConcurrently(t *testing.T) {
	h := newHungClient("sessions")
	l := startLive(t, NewServer(h, false, "test"))
	l.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_sessions"}}`)
	l.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_sessions"}}`)
	waitEntered(t, h, "sessions")
	waitEntered(t, h, "sessions")
	close(h.release)
	l.next(5*time.Second, "first call")
	l.next(5*time.Second, "second call")
	l.finish()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.peak != 2 {
		t.Errorf("peak concurrent daemon calls = %d, want 2", h.peak)
	}
}

// TestMutationsKeepWireOrder — concurrency must not reorder mutations:
// a pipelined send_keys then kill_session has to type before it kills,
// and a mutation waits for the reads before it.
func TestMutationsKeepWireOrder(t *testing.T) {
	h := newHungClient("sessions", "send_keys")
	l := startLive(t, NewServer(h, true, "test"))
	l.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_sessions"}}`)
	l.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"send_keys","arguments":{"name":"s","keys":"Enter"}}}`)
	l.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"kill_session","arguments":{"name":"s"}}}`)
	l.send(`{"jsonrpc":"2.0","id":4,"method":"ping"}`)
	waitEntered(t, h, "sessions")
	// The ping is still answered while all three wait.
	if id := frameID(t, l.next(3*time.Second, "ping")); id != "4" {
		t.Fatalf("response id = %s, want the ping (4)", id)
	}
	select {
	case op := <-h.entered:
		t.Fatalf("%s started while the read before it was still running", op)
	case <-time.After(200 * time.Millisecond):
	}
	close(h.release)
	for i := 0; i < 3; i++ {
		l.next(5*time.Second, "tool call")
	}
	l.finish()
	h.mu.Lock()
	defer h.mu.Unlock()
	want := "sessions:start sessions:end send_keys:start send_keys:end kill:start kill:end"
	if got := strings.Join(h.order, " "); got != want {
		t.Errorf("daemon call order = %s\nwant %s", got, want)
	}
}

// TestBatchRunsCallsConcurrentlyIntoOneArray — a batch's tool calls run
// concurrently, but the reply is still one array, in element order.
func TestBatchRunsCallsConcurrentlyIntoOneArray(t *testing.T) {
	h := newHungClient("sessions")
	l := startLive(t, NewServer(h, false, "test"))
	l.send(`[{"jsonrpc":"2.0","id":"a","method":"tools/call","params":{"name":"list_sessions"}},` +
		`{"jsonrpc":"2.0","id":"b","method":"tools/call","params":{"name":"list_sessions"}},` +
		`{"jsonrpc":"2.0","id":"c","method":"ping"}]`)
	waitEntered(t, h, "sessions")
	waitEntered(t, h, "sessions")
	close(h.release)
	frame := l.next(5*time.Second, "batch reply")
	l.finish()
	var resps []rpcResponse
	if err := json.Unmarshal([]byte(frame), &resps); err != nil {
		t.Fatalf("batch reply is not one array: %v (%q)", err, frame)
	}
	var ids []string
	for _, r := range resps {
		ids = append(ids, string(r.ID))
	}
	if got := strings.Join(ids, ","); got != `"a","b","c"` {
		t.Errorf("batch reply ids = %s, want \"a\",\"b\",\"c\"", got)
	}
}

// TestInFlightCallsAreBounded — at most maxInFlight tool calls run at
// once; the rest wait their turn and still complete.
func TestInFlightCallsAreBounded(t *testing.T) {
	h := newHungClient("sessions")
	l := startLive(t, NewServer(h, false, "test"))
	const n = maxInFlight + 4
	go func() {
		for i := 0; i < n; i++ {
			_, _ = l.in.Write([]byte(`{"jsonrpc":"2.0","id":` + itoa(i) + `,"method":"tools/call","params":{"name":"list_sessions"}}` + "\n"))
		}
	}()
	for i := 0; i < maxInFlight; i++ {
		waitEntered(t, h, "sessions")
	}
	select {
	case <-h.entered:
		t.Fatalf("more than maxInFlight (%d) calls entered the daemon at once", maxInFlight)
	case <-time.After(200 * time.Millisecond):
	}
	close(h.release)
	for i := 0; i < n; i++ {
		l.next(5*time.Second, "bounded call")
	}
	l.finish()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.peak > maxInFlight {
		t.Errorf("peak concurrent calls = %d, want <= %d", h.peak, maxInFlight)
	}
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

// TestHealthProbeFailsFast — get_daemon_health is the "is the daemon
// alive?" first probe, but against a hung daemon it took the full 30s
// tool backstop to say so. It has its own short budget.
func TestHealthProbeFailsFast(t *testing.T) {
	if healthTimeout > 5*time.Second {
		t.Errorf("healthTimeout = %v, want a short first-probe budget (<= 5s)", healthTimeout)
	}
	prev := healthTimeout
	healthTimeout = 150 * time.Millisecond
	t.Cleanup(func() { healthTimeout = prev })

	h := newHungClient("health")
	defer close(h.release)
	l := startLive(t, NewServer(h, false, "test"))
	start := time.Now()
	l.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_daemon_health"}}`)
	frame := l.next(5*time.Second, "health probe against a hung daemon")
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("health probe took %v against a hung daemon", elapsed)
	}
	var resp struct {
		Result toolResult `json:"result"`
	}
	if err := json.Unmarshal([]byte(frame), &resp); err != nil || !resp.Result.IsError {
		t.Errorf("health probe against a hung daemon should be an isError result: %v %s", err, frame)
	}
	l.finish()
}

// sendAsync writes frames from a goroutine: against a reader that
// blocks, a plain send would hang the test instead of failing it.
func (l *liveServer) sendAsync(frames ...string) {
	go func() {
		for _, f := range frames {
			if _, err := l.in.Write([]byte(f + "\n")); err != nil {
				return
			}
		}
	}()
}

func listSessionsCall(id int) string {
	return `{"jsonrpc":"2.0","id":` + itoa(id) + `,"method":"tools/call","params":{"name":"list_sessions"}}`
}

// fillSlots starts maxInFlight list_sessions calls (ids 0..maxInFlight-1)
// that hang in the daemon, and waits until every one is inside it.
func fillSlots(t *testing.T, l *liveServer, h *hungClient) {
	t.Helper()
	frames := make([]string, maxInFlight)
	for i := range frames {
		frames[i] = listSessionsCall(i)
	}
	l.sendAsync(frames...)
	for i := 0; i < maxInFlight; i++ {
		waitEntered(t, h, "sessions")
	}
}

// TestPingAnsweredPastTheInFlightCap — past maxInFlight hung calls the
// reader used to block waiting for a slot, so a ping behind them sat
// unread until a call finished (up to the 30s tool backstop against a
// hung daemon; clients drop the connection over far less). Excess calls
// now queue without blocking the reader.
func TestPingAnsweredPastTheInFlightCap(t *testing.T) {
	h := newHungClient("sessions")
	l := startLive(t, NewServer(h, false, "test"))
	fillSlots(t, l, h)

	start := time.Now()
	l.sendAsync(listSessionsCall(100), listSessionsCall(101), listSessionsCall(102), listSessionsCall(103),
		`{"jsonrpc":"2.0","id":"p","method":"ping"}`)
	if id := frameID(t, l.next(500*time.Millisecond, "ping behind maxInFlight+4 hung calls")); id != `"p"` {
		t.Fatalf("first response id = %s, want the ping", id)
	}
	t.Logf("ping answered in %v with %d hung calls ahead of it", time.Since(start), maxInFlight+4)
	select {
	case op := <-h.entered:
		t.Fatalf("%s entered the daemon while every slot was taken", op)
	case <-time.After(100 * time.Millisecond):
	}

	close(h.release)
	for i := 0; i < maxInFlight+4; i++ {
		l.next(5*time.Second, "hung or queued call after release")
	}
	if rest := l.finish(); len(rest) != 0 {
		t.Errorf("unexpected extra frames: %q", rest)
	}
}

// TestCancelQueuedCallNeverRuns — a notifications/cancelled for a call
// still waiting for a slot used to sit unread behind it, and by the
// time it was read the call had run. Now it's read at once: the call
// never runs and, like any cancelled call, gets no response. A
// cancelled mutation must not hold up the calls queued after it.
func TestCancelQueuedCallNeverRuns(t *testing.T) {
	h := newHungClient("sessions")
	l := startLive(t, NewServer(h, true, "test"))
	fillSlots(t, l, h)

	l.sendAsync(
		`{"jsonrpc":"2.0","id":"q-read","method":"tools/call","params":{"name":"get_daemon_health"}}`,
		`{"jsonrpc":"2.0","id":"q-kill","method":"tools/call","params":{"name":"kill_session","arguments":{"name":"s"}}}`,
		`{"jsonrpc":"2.0","id":"after","method":"tools/call","params":{"name":"get_daemon_health"}}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"q-read"}}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"q-kill"}}`,
		`{"jsonrpc":"2.0","id":"p","method":"ping"}`,
	)
	if id := frameID(t, l.next(500*time.Millisecond, "ping after cancelling queued calls")); id != `"p"` {
		t.Fatalf("first response id = %s, want the ping", id)
	}

	close(h.release)
	seen := map[string]bool{}
	for i := 0; i < maxInFlight+1; i++ {
		seen[frameID(t, l.next(5*time.Second, "remaining calls"))] = true
	}
	for _, f := range l.finish() {
		seen[frameID(t, f)] = true
	}
	if seen[`"q-read"`] || seen[`"q-kill"`] {
		t.Errorf("a cancelled queued call got a response: %v", seen)
	}
	if !seen[`"after"`] {
		t.Errorf("the call queued after a cancelled mutation never completed: %v", seen)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	health, kills := 0, 0
	for _, ev := range h.order {
		switch ev {
		case "health:start":
			health++
		case "kill:start":
			kills++
		}
	}
	if kills != 0 || health != 1 {
		t.Errorf("daemon saw %d kill and %d health calls, want 0 and 1 (only the uncancelled one): %v", kills, health, h.order)
	}
}

// TestQueueOverflowIsBusyAtOnce — the queue has a hard cap; a call past
// it is refused immediately with errServerBusy rather than blocking the
// reader (or growing without bound).
func TestQueueOverflowIsBusyAtOnce(t *testing.T) {
	h := newHungClient("sessions")
	h.entered = make(chan string, maxInFlight+maxQueued+1)
	l := startLive(t, NewServer(h, false, "test"))
	fillSlots(t, l, h)

	frames := make([]string, 0, maxQueued+1)
	for i := 0; i < maxQueued; i++ {
		frames = append(frames, listSessionsCall(1000+i))
	}
	frames = append(frames, `{"jsonrpc":"2.0","id":"over","method":"tools/call","params":{"name":"list_sessions"}}`)
	l.sendAsync(frames...)
	frame := l.next(2*time.Second, "the call past the queue cap")
	var resp rpcResponse
	if err := json.Unmarshal([]byte(frame), &resp); err != nil {
		t.Fatalf("decode %q: %v", frame, err)
	}
	if string(resp.ID) != `"over"` || resp.Error == nil || resp.Error.Code != errServerBusy {
		t.Fatalf("call past the queue cap = %s, want a %d busy error for id \"over\"", frame, errServerBusy)
	}
	if !strings.Contains(resp.Error.Message, "busy") {
		t.Errorf("busy error message = %q, want it to say the server is busy", resp.Error.Message)
	}

	close(h.release)
	for i := 0; i < maxInFlight+maxQueued; i++ {
		if frame := l.next(5*time.Second, "accepted call after release"); strings.Contains(frame, `"error"`) {
			t.Fatalf("an accepted call failed: %s", frame)
		}
	}
	if rest := l.finish(); len(rest) != 0 {
		t.Errorf("unexpected extra frames: %d", len(rest))
	}
}

// TestQueueByteBudgetIsBusy — queued calls hold their params until they
// run, so the queue is bounded in bytes too: past the budget a call is
// refused at once instead of buffering up to maxQueued 4 MiB frames.
func TestQueueByteBudgetIsBusy(t *testing.T) {
	prev := maxQueuedBytes
	maxQueuedBytes = 1024
	t.Cleanup(func() { maxQueuedBytes = prev })

	h := newHungClient("sessions")
	l := startLive(t, NewServer(h, false, "test"))
	fillSlots(t, l, h)
	pad := strings.Repeat("x", 600)
	padded := func(id string) string {
		return `{"jsonrpc":"2.0","id":"` + id + `","method":"tools/call","params":{"name":"list_sessions","arguments":{"pad":"` + pad + `"}}}`
	}
	l.sendAsync(padded("fits"), padded("over"))
	frame := l.next(2*time.Second, "the call past the byte budget")
	var resp rpcResponse
	if err := json.Unmarshal([]byte(frame), &resp); err != nil {
		t.Fatalf("decode %q: %v", frame, err)
	}
	if string(resp.ID) != `"over"` || resp.Error == nil || resp.Error.Code != errServerBusy {
		t.Fatalf("call past the byte budget = %s, want a %d busy error for id \"over\"", frame, errServerBusy)
	}
	close(h.release)
	for i := 0; i < maxInFlight+1; i++ {
		l.next(5*time.Second, "accepted call after release")
	}
	l.finish()
}

// TestEOFRunsQueuedCallsThenReturns — EOF with calls still queued: Run
// keeps its promise to answer everything it accepted, then returns.
func TestEOFRunsQueuedCallsThenReturns(t *testing.T) {
	h := newHungClient("sessions")
	l := startLive(t, NewServer(h, false, "test"))
	fillSlots(t, l, h)
	l.sendAsync(listSessionsCall(100), listSessionsCall(101), listSessionsCall(102), listSessionsCall(103),
		`{"jsonrpc":"2.0","id":"p","method":"ping"}`)
	// The ping's answer proves every call before it was read.
	if id := frameID(t, l.next(500*time.Millisecond, "ping behind queued calls")); id != `"p"` {
		t.Fatalf("first response id = %s, want the ping", id)
	}
	_ = l.in.Close()
	select {
	case err := <-l.done:
		t.Fatalf("Run returned (%v) with calls still hung and queued", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(h.release)
	ids := map[string]bool{}
	for i := 0; i < maxInFlight+4; i++ {
		ids[frameID(t, l.next(5*time.Second, "queued call after EOF"))] = true
	}
	for _, want := range []string{"100", "101", "102", "103"} {
		if !ids[want] {
			t.Errorf("queued call %s was not answered after EOF", want)
		}
	}
	select {
	case err := <-l.done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after EOF once its calls finished")
	}
}

// brokenWriter fails every write: stdout has gone away.
type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// TestWriteFailureDropsQueuedCalls — once stdout is gone Run stops, and
// calls still queued must never run (nobody can read their answers) —
// nor may Run wait on them.
func TestWriteFailureDropsQueuedCalls(t *testing.T) {
	h := newHungClient("sessions")
	defer close(h.release)
	inR, inW := io.Pipe()
	defer inW.Close()
	done := make(chan error, 1)
	go func() { done <- NewServer(h, false, "test").Run(context.Background(), inR, brokenWriter{}) }()

	go func() {
		for i := 0; i < maxInFlight+4; i++ {
			if _, err := inW.Write([]byte(listSessionsCall(i) + "\n")); err != nil {
				return
			}
		}
	}()
	for i := 0; i < maxInFlight; i++ {
		waitEntered(t, h, "sessions")
	}
	// Only now the ping, whose answer is the write that fails: sent any
	// earlier, it could stop Run before every slot's call had reached
	// the daemon.
	go func() { _, _ = inW.Write([]byte(`{"jsonrpc":"2.0","id":"p","method":"ping"}` + "\n")) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "write stdout") {
			t.Fatalf("Run = %v, want the write failure", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop after stdout failed with calls queued")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if starts := strings.Count(strings.Join(h.order, " "), "sessions:start"); starts != maxInFlight {
		t.Errorf("%d calls entered the daemon, want only the %d that held slots before stdout failed", starts, maxInFlight)
	}
}
