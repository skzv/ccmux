package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
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
//   - The reader parses frames in order and never blocks on a tool
//     call. Everything that doesn't touch the daemon — initialize,
//     ping, tools/list, protocol errors, notifications — is answered
//     inline, immediately.
//   - Each tools/call runs in its own goroutine, at most maxInFlight at
//     once, and writes its own response when done. Writes go through
//     one mutex, so frames never interleave.
//   - Past maxInFlight, calls wait in a FIFO queue and start in wire
//     order as slots free up. The reader used to block on a free slot
//     instead, so with every slot held by a hung call a ping sat unread
//     until the 30s backstop, and a cancellation arrived only after the
//     call it meant to stop had run. The queue is capped (maxQueued
//     calls, maxQueuedBytes of params): past that a call is answered at
//     once with errServerBusy.
//   - Read-only calls run concurrently with each other, but a mutating
//     call (spawn / send_keys / kill) waits for every call read before
//     it, and every call read after it waits for it (orderGate). So a
//     pipelined `send_keys` then `kill_session` can't swap, and a
//     mutation never starts once an earlier response failed to write.
//     Calls start in wire order, so a running call only ever waits on
//     calls that have already started: the gate can't deadlock on a
//     queued one.
//   - notifications/cancelled cancels the named call's context; per MCP,
//     a cancelled request gets no response. A call cancelled while still
//     queued is dropped on the spot and never runs.
//   - A batch still gets exactly one array response, written once all
//     its calls have finished.

// runState is the per-Run dispatch state.
type runState struct {
	s    *Server
	enc  *json.Encoder
	ctx  context.Context // cancelled when Run stops or a write fails
	stop context.CancelFunc
	wg   sync.WaitGroup // accepted calls until settled, and batch writers
	gate *orderGate

	mu          sync.Mutex
	inflight    map[string]*job // running or queued, by request id, for notifications/cancelled
	running     int             // calls holding one of the maxInFlight slots
	queue       []*job          // accepted calls waiting for a slot, in wire order
	queuedBytes int             // params held by queue
	werr        error           // first write failure; guarded by s.writeMu
}

func newRunState(ctx context.Context, s *Server, enc *json.Encoder) *runState {
	rctx, stop := context.WithCancel(ctx)
	return &runState{
		s:        s,
		enc:      enc,
		ctx:      rctx,
		stop:     stop,
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

// job is one accepted tools/call: queued, then running in its own
// goroutine.
type job struct {
	key       string // request id for cancellation; "" for a notification
	ctx       context.Context
	cancel    context.CancelFunc
	cancelled atomic.Bool
	done      chan struct{} // closed once resp is final
	resp      rpcResponse
	respond   bool // false for a notification, or once cancelled/abandoned

	params    json.RawMessage
	mutating  bool
	writeSelf bool            // a lone call writes its own response; a batch's are collected
	ready     <-chan struct{} // orderGate: closed when the call may run
	release   func()          // orderGate: called exactly once, when settled
	queued    bool            // waiting in runState.queue; guarded by runState.mu
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
	// JSON-RPC ids are strings, numbers or null. An object or array id
	// can't be echoed back meaningfully, so it's an Invalid Request
	// answered with a null id (it used to be accepted and echoed).
	if !validID(req.ID) {
		return errCall(nullID, errInvalidRequest, "invalid request: id must be a string, number or null")
	}
	id := req.ID
	if len(id) == 0 {
		id = nullID
	}
	if req.JSONRPC != "2.0" {
		return errCall(id, errInvalidRequest, `jsonrpc must be "2.0"`)
	}
	if req.Method == "" {
		if len(req.Result) > 0 || len(req.Error) > 0 {
			// A response to a request we never sent: answering it
			// could only confuse the client. Drop it.
			return call{}
		}
		// A request without a method is malformed (-32600), not a call
		// to an unknown method (-32601).
		return errCall(id, errInvalidRequest, "invalid request: method is required")
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

// validID reports whether a request id is absent (a notification) or a
// JSON string, number or null.
func validID(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 {
		return true
	}
	switch c := t[0]; {
	case c == '"', c == '-', c >= '0' && c <= '9':
		return true
	case c == 'n':
		return bytes.Equal(t, []byte("null"))
	}
	return false // object, array, true/false
}

func errCall(id json.RawMessage, code int, msg string) call {
	return call{resp: rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}, respond: true}
}

// startToolCall accepts a tools/call and returns at once, without ever
// blocking the reader: the call starts now if one of the maxInFlight
// slots is free, and otherwise waits in the FIFO queue. Only a full
// queue refuses it, with an immediate errServerBusy.
func (rs *runState) startToolCall(req *rpcRequest, writeSelf bool) call {
	j := &job{
		done:      make(chan struct{}),
		resp:      rpcResponse{JSONRPC: "2.0", ID: req.ID},
		respond:   len(req.ID) != 0,
		params:    req.Params,
		writeSelf: writeSelf,
	}
	if rs.ctx.Err() != nil {
		// Output is gone; nothing will read an answer.
		j.respond = false
		close(j.done)
		return call{job: j}
	}
	tool, known := rs.s.toolFor(req.Params)
	j.mutating = known && tool.Mutating

	rs.mu.Lock()
	start := rs.running < maxInFlight
	if !start && (len(rs.queue) >= maxQueued || rs.queuedBytes+len(j.params) > maxQueuedBytes) {
		waiting := len(rs.queue)
		rs.mu.Unlock()
		if !j.respond {
			return call{} // a notification gets no answer, not even this one
		}
		return errCall(req.ID, errServerBusy, fmt.Sprintf("server busy: %d tool calls running and %d waiting; retry later", maxInFlight, waiting))
	}
	j.ctx, j.cancel = context.WithCancel(rs.ctx)
	if j.respond {
		j.key = idKey(req.ID)
		rs.inflight[j.key] = j
	}
	// Registering here, on the reader goroutine, puts calls in the gate
	// in wire order.
	if j.mutating {
		j.ready, j.release = rs.gate.exclusive()
	} else {
		j.ready, j.release = rs.gate.shared()
	}
	rs.wg.Add(1)
	if start {
		rs.running++
	} else {
		j.queued = true
		rs.queue = append(rs.queue, j)
		rs.queuedBytes += len(j.params)
	}
	rs.mu.Unlock()
	if start {
		go rs.work(j)
	}
	return call{job: j}
}

// work runs j on the slot it was given, then keeps the slot busy with
// the oldest queued call until the queue is empty.
func (rs *runState) work(j *job) {
	for j != nil {
		rs.execute(j)
		j = rs.nextQueued()
	}
}

// nextQueued hands the caller's slot to the oldest queued call, or
// frees the slot when nothing is waiting.
func (rs *runState) nextQueued() *job {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if len(rs.queue) == 0 {
		rs.running--
		return nil
	}
	j := rs.queue[0]
	rs.queue[0] = nil
	rs.queue = rs.queue[1:]
	rs.queuedBytes -= len(j.params)
	j.queued = false
	return j
}

// execute runs one call that holds a slot — unless it was cancelled, or
// the run stopped, while it waited — and settles it.
func (rs *runState) execute(j *job) {
	defer rs.wg.Done()
	if j.ctx.Err() != nil {
		j.respond = false
	} else {
		rs.runJob(j)
	}
	rs.settle(j)
}

// runJob waits for the call's turn, then runs the tool into j.resp.
func (rs *runState) runJob(j *job) {
	select {
	case <-j.ready:
	case <-j.ctx.Done():
		j.respond = false // cancelled while waiting behind a mutation
		return
	}
	if j.mutating && rs.writeFailure() != nil {
		// Never mutate once an earlier response failed to reach the
		// client: it can no longer see what it asked for.
		j.respond = false
		return
	}
	out, rerr := rs.s.handleToolsCall(j.ctx, j.params)
	if rerr != nil {
		j.resp.Error = rerr
	} else {
		j.resp.Result = out
	}
}

// settle finishes a call that ran or was dropped: no response once
// cancelled, done closed for its batch, a lone call's response written,
// and only then its place in the order gate released.
func (rs *runState) settle(j *job) {
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
	if j.writeSelf && j.respond {
		_ = rs.write(j.resp) // a failure is sticky in rs.werr
	}
	// Only now may a later mutation start: a lone call's response is on
	// the wire (or failed to get there).
	j.release()
}

// cancelRequest handles notifications/cancelled: cancel the named
// call's context and suppress its response. A call still queued is
// dropped here and never runs. Unknown or already-finished ids are
// ignored, as the spec requires.
func (rs *runState) cancelRequest(params json.RawMessage) {
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if err := json.Unmarshal(params, &p); err != nil || len(p.RequestID) == 0 {
		return
	}
	rs.mu.Lock()
	j := rs.inflight[idKey(p.RequestID)]
	queued := j != nil && j.queued
	if queued {
		i := slices.Index(rs.queue, j)
		rs.queue = slices.Delete(rs.queue, i, i+1)
		rs.queuedBytes -= len(j.params)
		j.queued = false
	}
	rs.mu.Unlock()
	if j == nil {
		return
	}
	j.cancelled.Store(true)
	j.cancel()
	if queued {
		// It holds no slot and no goroutine: settle it here.
		j.respond = false
		rs.settle(j)
		rs.wg.Done()
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
