package daemon

import (
	"sync"
	"testing"
	"time"
)

// TestEventBus_PublishReachesSubscriber — a published event lands on a
// registered subscriber's channel.
func TestEventBus_PublishReachesSubscriber(t *testing.T) {
	b := NewEventBus()
	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	b.Publish(SessionEvent{Kind: "created"})

	select {
	case got := <-ch:
		if got.Kind != "created" {
			t.Fatalf("received Kind %q, want %q", got.Kind, "created")
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive the published event")
	}
}

// TestEventBus_AllSubscribersReceive — Publish fans out to every
// registered subscriber.
func TestEventBus_AllSubscribersReceive(t *testing.T) {
	b := NewEventBus()
	chs := []chan SessionEvent{b.Subscribe(), b.Subscribe(), b.Subscribe()}

	b.Publish(SessionEvent{Kind: "needs_input"})

	for i, ch := range chs {
		select {
		case got := <-ch:
			if got.Kind != "needs_input" {
				t.Errorf("subscriber %d received Kind %q", i, got.Kind)
			}
		case <-time.After(time.Second):
			t.Errorf("subscriber %d received nothing", i)
		}
	}
}

// TestEventBus_UnsubscribeClosesChannel — Unsubscribe closes the channel
// and a later Publish (with no subscribers left) must not panic.
func TestEventBus_UnsubscribeClosesChannel(t *testing.T) {
	b := NewEventBus()
	ch := b.Subscribe()
	b.Unsubscribe(ch)

	if _, ok := <-ch; ok {
		t.Fatal("channel still open after Unsubscribe")
	}
	b.Publish(SessionEvent{Kind: "killed"}) // must not panic
}

// TestEventBus_PublishWithNoSubscribers — publishing into an empty bus is
// a no-op, not a panic or a block.
func TestEventBus_PublishWithNoSubscribers(t *testing.T) {
	b := NewEventBus()
	b.Publish(SessionEvent{Kind: "created"})
}

// TestEventBus_SlowSubscriberDropsEvents — Publish is non-blocking: once a
// subscriber's buffer (32) is full, further events are dropped rather
// than stalling the poll loop.
func TestEventBus_SlowSubscriberDropsEvents(t *testing.T) {
	b := NewEventBus()
	ch := b.Subscribe() // never drained
	defer b.Unsubscribe(ch)

	for i := 0; i < 100; i++ {
		b.Publish(SessionEvent{Kind: "state_change"}) // must never block
	}
	if got := len(ch); got != 32 {
		t.Fatalf("buffered %d events, want 32 (excess must be dropped)", got)
	}
}

// TestEventBus_Concurrent — Publish racing against Subscribe/Unsubscribe
// must not panic with a send-on-closed-channel (meaningful under -race).
func TestEventBus_Concurrent(t *testing.T) {
	b := NewEventBus()
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				b.Publish(SessionEvent{Kind: "state_change"})
			}
		}()
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch := b.Subscribe()
			b.Unsubscribe(ch)
		}()
	}
	wg.Wait()
}
