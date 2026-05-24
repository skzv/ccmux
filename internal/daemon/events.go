package daemon

import (
	"sync"
	"sync/atomic"
)

// eventBufferSize is the per-subscriber buffer. Bigger than the previous
// 32 so a brief subscriber stall (an SSE consumer over a flaky link)
// doesn't immediately start dropping events.
const eventBufferSize = 256

// EventBus broadcasts SessionEvent values to all registered subscribers.
// The daemon's poll loop publishes events; SSE handlers subscribe.
type EventBus struct {
	mu   sync.Mutex
	subs map[chan SessionEvent]*subStats
}

// subStats tracks drop counts per subscriber. Exposed via Stats() so
// SSE handlers can include "you missed N events" markers on the wire
// — silent drops are how a client desyncs from the daemon without
// knowing it.
type subStats struct {
	dropped atomic.Uint64
}

func NewEventBus() *EventBus {
	return &EventBus{subs: make(map[chan SessionEvent]*subStats)}
}

// Subscribe returns a channel that receives events until Unsubscribe is called.
func (b *EventBus) Subscribe() chan SessionEvent {
	ch := make(chan SessionEvent, eventBufferSize)
	b.mu.Lock()
	b.subs[ch] = &subStats{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes and closes the channel.
func (b *EventBus) Unsubscribe(ch chan SessionEvent) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
	close(ch)
}

// Publish sends an event to all subscribers. Non-blocking: when a
// subscriber's buffer is full the event is dropped for that subscriber
// only and its drop counter is incremented (visible via Dropped()).
func (b *EventBus) Publish(e SessionEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch, stats := range b.subs {
		select {
		case ch <- e:
		default:
			stats.dropped.Add(1)
		}
	}
}

// Dropped returns the number of events dropped for `ch` since its
// Subscribe call. Used by SSE handlers to detect their own desync and
// surface it to clients instead of silently lying about being current.
func (b *EventBus) Dropped(ch chan SessionEvent) uint64 {
	b.mu.Lock()
	stats, ok := b.subs[ch]
	b.mu.Unlock()
	if !ok {
		return 0
	}
	return stats.dropped.Load()
}
