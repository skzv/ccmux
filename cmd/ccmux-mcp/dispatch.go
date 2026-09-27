package main

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
)

// Concurrent dispatch.
//
// ccmux-mcp used to handle one frame at a time, so a single tool call
// stuck on a hung daemon froze the whole transport for its 30s
// backstop: a ping behind it went unanswered (clients tear the
// connection down over that) and so did the notifications/cancelled
// meant to abort it. Now:
//
//   - The reader parses frames in order. Everything that doesn't touch
//     the daemon — initialize, ping, tools/list, protocol errors,
//     notifications — is answered inline, immediately.
//   - Each tools/call runs in its own goroutine, at most maxInFlight at
//     once, and writes its own response when done. Writes go through
//     one mutex, so frames never interleave.
//   - Read-only calls run concurrently with each other, but a mutating
//     call (spawn / send_keys / kill) waits for every call read before
//     it, and every call read after it waits for it (orderGate). So a
//     pipelined `send_keys` then `kill_session` can't swap, and a
//     mutation never starts once an earlier response failed to write.
//   - notifications/cancelled cancels the named in-flight call's
//     context; per MCP, a cancelled request gets no response.
//   - A batch still gets exactly one array response, written once all
//     its calls have finished.

// runState is the per-Run dispatch state.
type runState struct {
	s     *Server
	enc   *json.Encoder
	ctx   context.Context // cancelled when Run stops or a write fails
	stop  context.CancelFunc
	slots chan struct{} // maxInFlight semaphore
	wg    sync.WaitGroup
	gate  *orderGate

	mu       sync.Mutex
	inflight map[string]*job // by request id, for notifications/cancelled
	werr     error           // first write failure; guarded by s.writeMu
}

func newRunState(ctx context.Context, s *Server, enc *json.Encoder) *runState {
	rctx, stop := context.WithCancel(ctx)
	return &runState{
		s:        s,
		enc:      enc,
		ctx:      rctx,
		stop:     stop,
		slots:    make(chan struct{}, maxInFlight),
		gate:     newOrderGate(),
		inflight: map[string]*job{},
	}
}

// write serializes one frame (a response or a batch's []rpcResponse)
// onto stdout. The first failure is sticky: later writes return it
// without writing, in-flight calls are cancelled, and Run stops before
// running another frame.
func (rs *runState) write(v any) error {
	rs.s.writeMu.Lock()
	defer rs.s.writeMu.Unlock()
	if rs.werr != nil {
		return rs.werr
	}
	if err := rs.enc.Encode(v); err != nil {
		rs.werr = err
		rs.stop()
		return err
	}
	return nil
}

// writeFailure reports the first write error, if any.
func (rs *runState) writeFailure() error {
	rs.s.writeMu.Lock()
	defer rs.s.writeMu.Unlock()
	return rs.werr
}

// call is one parsed request: either answered already (resp/respond)
// or running as a job.
type call struct {
	resp    rpcResponse
	respond bool
	job     *job
}

// job is one tools/call running in its own goroutine.
type job struct {
	key       string // request id for cancellation; "" for a notification
	ctx       context.Context
	cancel    context.CancelFunc
	cancelled atomic.Bool
	done      chan struct{} // closed once resp is final
	resp      rpcResponse
	respond   bool // false for a notification, or once cancelled/abandoned
}

// dispatchLine handles one raw frame — a single request or a batch.
func (rs *runState) dispatchLine(line []byte) error {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return rs.dispatchBatch(trimmed)
	}
	c := rs.prepare(line, true)
	if c.job != nil || !c.respond {
		return nil // a job writes its own response
	}
	return rs.write(c.resp)
}

// dispatchBatch handles a JSON-RPC 2.0 batch (an array of requests).
// Protocol 2025-03-26, which initialize negotiates, requires servers
// to accept batches. Per JSON-RPC: the reply is ONE array holding a
// response per request (notifications get none); an all-notification
// batch gets no reply at all; an empty array is itself an Invalid
// Request, answered with a single (non-array) error. Responses keep
// the elements' order; the tool calls themselves run concurrently,
// under the same ordering rules as separate frames.
func (rs *runState) dispatchBatch(line []byte) error {
	var elems []json.RawMessage
	if err := json.Unmarshal(line, &elems); err != nil {
		return rs.write(rpcResponse{JSONRPC: "2.0", ID: nullID, Error: &rpcError{Code: errParseError, Message: "parse error: " + err.Error()}})
	}
	if len(elems) == 0 {
		return rs.write(rpcResponse{JSONRPC: "2.0", ID: nullID, Error: &rpcError{Code: errInvalidRequest, Message: "invalid request: empty batch"}})
	}
	calls := make([]call, 0, len(elems))
	var jobs []*job
	for _, el := range elems {
		c := rs.prepare(el, false)
		calls = append(calls, c)
		if c.job != nil {
			jobs = append(jobs, c.job)
		}
	}
	if len(jobs) == 0 {
		return rs.writeBatch(calls)
	}
	rs.wg.Add(1)
	go func() {
		defer rs.wg.Done()
		for _, j := range jobs {
			<-j.done
		}
		_ = rs.writeBatch(calls) // a failure is sticky in rs.werr
	}()
	return nil
}

// writeBatch writes a batch's responses as one array, skipping
// notifications and cancelled calls; nothing at all when none remain.
func (rs *runState) writeBatch(calls []call) error {
	out := make([]rpcResponse, 0, len(calls))
	for _, c := range calls {
		switch {
		case c.job != nil:
			if c.job.respond {
				out = append(out, c.job.resp)
			}
		case c.respond:
			out = append(out, c.resp)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return rs.write(out)
}

// prepare parses and validates one request object and either answers
// it inline or starts it as a job. writeSelf says whether a job writes
// its own response (a lone request) or leaves it to its batch.
func (rs *runState) prepare(raw []byte, writeSelf bool) call {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		// The id couldn't be determined, so per spec it must be
		// literal null — not absent. Well-formed JSON that isn't a
		// request object (e.g. a batch element `1`) is an Invalid
		// Request rather than a parse error.
		if json.Valid(raw) {
			return errCall(nullID, errInvalidRequest, "invalid request: "+err.Error())
		}
		return errCall(nullID, errParseError, "parse error: "+err.Error())
	}
	if req.JSONRPC != "2.0" {
		id := req.ID
		if len(id) == 0 {
			id = nullID
		}
		return errCall(id, errInvalidRequest, `jsonrpc must be "2.0"`)
	}
	switch req.Method {
	case "notifications/cancelled":
		rs.cancelRequest(req.Params)
		return call{}
	case "tools/call":
		return rs.startToolCall(&req, writeSelf)
	}
	resp, isNotification := rs.s.handle(&req)
	return call{resp: resp, respond: !isNotification}
}

func errCall(id json.RawMessage, code int, msg string) call {
	return call{resp: rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}, respond: true}
}

// startToolCall runs a tools/call in its own goroutine. It blocks only
// while maxInFlight calls are already running.
func (rs *runState) startToolCall(req *rpcRequest, writeSelf bool) call {
	j := &job{
		done:    make(chan struct{}),
		resp:    rpcResponse{JSONRPC: "2.0", ID: req.ID},
		respond: len(req.ID) != 0,
	}
	select {
	case rs.slots <- struct{}{}:
	case <-rs.ctx.Done():
		// Output is gone; nothing will read an answer.
		j.respond = false
		close(j.done)
		return call{job: j}
	}
	j.ctx, j.cancel = context.WithCancel(rs.ctx)
	if j.respond {
		j.key = idKey(req.ID)
		rs.mu.Lock()
		rs.inflight[j.key] = j
		rs.mu.Unlock()
	}
	tool, known := rs.s.toolFor(req.Params)
	mutating := known && tool.Mutating
	var ready <-chan struct{}
	var release func()
	if mutating {
		ready, release = rs.gate.exclusive()
	} else {
		ready, release = rs.gate.shared()
	}
	params := req.Params

	rs.wg.Add(1)
	go func() {
		defer rs.wg.Done()
		defer func() { <-rs.slots }()
		rs.runJob(j, params, mutating, ready)
		if j.key != "" {
			rs.mu.Lock()
			if rs.inflight[j.key] == j {
				delete(rs.inflight, j.key)
			}
			rs.mu.Unlock()
		}
		if j.cancelled.Load() {
			j.respond = false
		}
		j.cancel()
		close(j.done)
		if writeSelf && j.respond {
			_ = rs.write(j.resp) // a failure is sticky in rs.werr
		}
		// Only now may a later mutation start: a lone call's response
		// is on the wire (or failed to get there).
		release()
	}()
	return call{job: j}
}

// runJob waits for the call's turn, then runs the tool into j.resp.
func (rs *runState) runJob(j *job, params json.RawMessage, mutating bool, ready <-chan struct{}) {
	select {
	case <-ready:
	case <-j.ctx.Done():
		j.respond = false // cancelled while queued behind a mutation
		return
	}
	if mutating && rs.writeFailure() != nil {
		// Never mutate once an earlier response failed to reach the
		// client: it can no longer see what it asked for.
		j.respond = false
		return
	}
	out, rerr := rs.s.handleToolsCall(j.ctx, params)
	if rerr != nil {
		j.resp.Error = rerr
	} else {
		j.resp.Result = out
	}
}

// cancelRequest handles notifications/cancelled: cancel the named
// in-flight call's context and suppress its response. Unknown or
// already-finished ids are ignored, as the spec requires.
func (rs *runState) cancelRequest(params json.RawMessage) {
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if err := json.Unmarshal(params, &p); err != nil || len(p.RequestID) == 0 {
		return
	}
	rs.mu.Lock()
	j := rs.inflight[idKey(p.RequestID)]
	rs.mu.Unlock()
	if j != nil {
		j.cancelled.Store(true)
		j.cancel()
	}
}

// idKey normalizes a request id for the in-flight map, so `"a"` in a
// request and `"a" ` in a cancellation match.
func idKey(raw json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return string(raw)
	}
	return b.String()
}

// orderGate orders tool calls around mutations, in the order the
// reader dispatched them: read-only calls share, a mutating call is
// exclusive — it runs only after every earlier call has released, and
// every later call waits for it. Registration happens on the reader
// goroutine, so the order is the wire order.
type orderGate struct {
	mu      sync.Mutex
	barrier chan struct{}   // closed once the latest mutating call released
	readers *sync.WaitGroup // shared calls registered since that mutation
}

func newOrderGate() *orderGate {
	b := make(chan struct{})
	close(b)
	return &orderGate{barrier: b, readers: &sync.WaitGroup{}}
}

// shared registers a read-only call: it may run once ready is closed,
// and must call release exactly once when finished (or abandoned).
func (g *orderGate) shared() (ready <-chan struct{}, release func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.readers.Add(1)
	return g.barrier, g.readers.Done
}

// exclusive registers a mutating call; same contract as shared.
func (g *orderGate) exclusive() (ready <-chan struct{}, release func()) {
	g.mu.Lock()
	prev, readers := g.barrier, g.readers
	mine := make(chan struct{})
	g.barrier, g.readers = mine, &sync.WaitGroup{}
	g.mu.Unlock()

	r := make(chan struct{})
	go func() {
		<-prev
		readers.Wait()
		close(r)
	}()
	// Releasing before our own turn came (a cancelled call) must still
	// hold later calls until everything before us is done.
	return r, func() {
		go func() {
			<-r
			close(mine)
		}()
	}
}
