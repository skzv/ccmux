package daemon

import (
	"sync"
)

// EventBus broadcasts SessionEvent values to all registered subscribers.
// The daemon's poll loop publishes events; SSE handlers subscribe.
type EventBus struct {
	mu   sync.Mutex
	subs map[chan SessionEvent]struct{}
}

func NewEventBus() *EventBus {
	return &EventBus{subs: make(map[chan SessionEvent]struct{})}
}

// Subscribe returns a channel that receives events until Unsubscribe is called.
func (b *EventBus) Subscribe() chan SessionEvent {
	ch := make(chan SessionEvent, 32)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
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

// Publish sends an event to all subscribers (non-blocking; slow subscribers drop events).
func (b *EventBus) Publish(e SessionEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}
