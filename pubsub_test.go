package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- eventHub pub/sub tests ---

func TestEventHubSubscribeReceive(t *testing.T) {
	hub := newEventHub(64)
	defer hub.close()

	sub := hub.subscribe("test", 16, false)
	hub.publish(&browserCDPEnvelope{Method: "Fetch.requestPaused", Params: []byte(`{}`)})

	select {
	case ev := <-sub.ch:
		if ev.Method != "Fetch.requestPaused" {
			t.Errorf("got method %q, want Fetch.requestPaused", ev.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive event")
	}
}

func TestEventHubMultipleSubscribers(t *testing.T) {
	hub := newEventHub(64)
	defer hub.close()

	sub1 := hub.subscribe("s1", 8, false)
	sub2 := hub.subscribe("s2", 8, false)

	hub.publish(&browserCDPEnvelope{Method: "Network.responseReceived"})

	for i, sub := range []*eventSubscription{sub1, sub2} {
		select {
		case ev := <-sub.ch:
			if ev.Method != "Network.responseReceived" {
				t.Errorf("sub%d: got %q", i, ev.Method)
			}
		case <-time.After(time.Second):
			t.Errorf("sub%d did not receive", i)
		}
	}
}

func TestEventHubUnsubscribe(t *testing.T) {
	hub := newEventHub(64)
	defer hub.close()

	sub := hub.subscribe("test", 8, false)
	hub.unsubscribe(sub)

	// After unsubscribe, publish should not deliver to this sub
	hub.publish(&browserCDPEnvelope{Method: "test"})

	select {
	case <-sub.ch:
		// Could be a buffered old event, but new ones should not arrive
	case <-time.After(50 * time.Millisecond):
		// Good — no event
	}

	// Verify subscriber is removed from hub
	hub.mu.RLock()
	count := len(hub.subs)
	hub.mu.RUnlock()
	if count != 0 {
		t.Errorf("after unsubscribe, hub has %d subs, want 0", count)
	}
}

func TestEventHubBackpressureDropCount(t *testing.T) {
	hub := newEventHub(64)
	defer hub.close()

	// Small buffer, don't consume
	sub := hub.subscribe("test", 2, false)

	// Publish more than buffer capacity
	for i := 0; i < 10; i++ {
		hub.publish(&browserCDPEnvelope{Method: "test"})
	}

	// Give publish time to process
	time.Sleep(50 * time.Millisecond)

	dropped := sub.droppedCount()
	if dropped == 0 {
		t.Error("expected dropped count > 0 when subscriber buffer overflows")
	}
	t.Logf("dropped %d events", dropped)
}

func TestEventHubPublishDoesNotBlockOnSlowSubscriber(t *testing.T) {
	hub := newEventHub(64)
	defer hub.close()

	sub := hub.subscribe("slow", 1, false) // tiny buffer

	// Publish many events rapidly — should not block
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			hub.publish(&browserCDPEnvelope{Method: "test"})
		}
	}()

	select {
	case <-done:
		// Good — publish didn't block
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on slow subscriber")
	}

	// Slow subscriber should have dropped most events
	if sub.droppedCount() == 0 {
		t.Error("expected drops from slow subscriber")
	}
}

func TestEventHubConcurrentPublishSubscribe(t *testing.T) {
	hub := newEventHub(128)
	defer hub.close()

	var wg sync.WaitGroup
	var received int64

	// 5 subscribers
	for i := 0; i < 5; i++ {
		sub := hub.subscribe("concurrent", 32, false)
		wg.Add(1)
		go func(s *eventSubscription) {
			defer wg.Done()
			for {
				select {
				case <-s.ch:
					atomic.AddInt64(&received, 1)
				case <-time.After(100 * time.Millisecond):
					return
				}
			}
		}(sub)
	}

	// 10 publishers x 100 events
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				hub.publish(&browserCDPEnvelope{Method: "test"})
			}
		}()
	}

	wg.Wait()
	t.Logf("received %d events across 5 subscribers", atomic.LoadInt64(&received))
}

func TestEventHubCloseUnblocksSubscribers(t *testing.T) {
	hub := newEventHub(64)
	sub := hub.subscribe("test", 8, false)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range sub.ch {
		}
	}()

	hub.close()

	select {
	case <-done:
		// Good — subscriber unblocked when hub closed
	case <-time.After(time.Second):
		t.Fatal("subscriber not unblocked after hub close")
	}
}

func TestEventHubPerSubscriberBuffer(t *testing.T) {
	hub := newEventHub(64)
	defer hub.close()

	// Two subscribers with different buffer sizes
	sub1 := hub.subscribe("big", 32, false)
	sub2 := hub.subscribe("small", 2, false)

	// Publish 5 events
	for i := 0; i < 5; i++ {
		hub.publish(&browserCDPEnvelope{Method: "test"})
	}
	time.Sleep(50 * time.Millisecond)

	// sub1 should have all 5, sub2 should have dropped some
	sub1Count := 0
	sub2Count := 0
	for {
		select {
		case <-sub1.ch:
			sub1Count++
		default:
			goto check2
		}
	}
check2:
	for {
		select {
		case <-sub2.ch:
			sub2Count++
		default:
			goto done
		}
	}
done:
	if sub1Count < 3 {
		t.Errorf("sub1 received %d, expected at least 3", sub1Count)
	}
	if sub2.droppedCount() == 0 {
		t.Error("sub2 should have dropped events")
	}
}

// --- Critical subscriber tests ---

func TestEventHubCriticalSubscriberNeverDrops(t *testing.T) {
	hub := newEventHub(0)
	defer hub.close()

	// Critical subscriber with buffer of 1
	critSub := hub.subscribe("critical", 1, true)
	normalSub := hub.subscribe("normal", 1, false)

	// Fill both buffers
	hub.publish(&browserCDPEnvelope{Method: "ev1"})
	time.Sleep(10 * time.Millisecond)

	// Both buffers are now full (1 event each)

	// Publish another event — normal drops, critical blocks
	// Use a goroutine to verify critical blocks
	delivered := make(chan struct{})
	go func() {
		hub.publish(&browserCDPEnvelope{Method: "ev2"})
		close(delivered)
	}()

	// Critical publish should be blocked (not delivered yet)
	select {
	case <-delivered:
		t.Fatal("critical publish should block when buffer is full")
	case <-time.After(50 * time.Millisecond):
		// expected: blocked
	}

	// Normal subscriber should have dropped
	if normalSub.droppedCount() == 0 {
		t.Error("normal subscriber should have dropped event")
	}
	if critSub.droppedCount() != 0 {
		t.Error("critical subscriber should never drop")
	}

	// Drain critical buffer — publish should now complete
	<-critSub.ch
	select {
	case <-delivered:
		// expected: publish completed after drain
	case <-time.After(100 * time.Millisecond):
		t.Fatal("critical publish should complete after draining buffer")
	}

	// Verify the event was delivered
	ev := <-critSub.ch
	if ev.Method != "ev2" {
		t.Errorf("got ev.Method=%s, want ev2", ev.Method)
	}
}

func TestEventHubCriticalSubscriberUnblockedByClose(t *testing.T) {
	hub := newEventHub(0)

	critSub := hub.subscribe("critical", 1, true)
	defer hub.unsubscribe(critSub)
	hub.publish(&browserCDPEnvelope{Method: "ev1"})
	time.Sleep(10 * time.Millisecond)

	// Buffer full, publish should block
	delivered := make(chan struct{})
	go func() {
		hub.publish(&browserCDPEnvelope{Method: "ev2"})
		close(delivered)
	}()

	select {
	case <-delivered:
		t.Fatal("publish should block on full critical buffer")
	case <-time.After(50 * time.Millisecond):
	}

	// Close hub — should unblock the publish
	hub.close()

	select {
	case <-delivered:
		// expected: publish returned after close
	case <-time.After(100 * time.Millisecond):
		t.Fatal("publish should unblock after hub.close()")
	}
}

func TestEventHubCloseIsIdempotent(t *testing.T) {
	hub := newEventHub(0)
	hub.close()
	hub.close() // should not panic
	hub.close() // should not panic
}

func TestEventHubPublishAfterCloseReturnsImmediately(t *testing.T) {
	hub := newEventHub(0)
	hub.close()

	// publish after close should return immediately, not panic
	done := make(chan struct{})
	go func() {
		hub.publish(&browserCDPEnvelope{Method: "test"})
		close(done)
	}()

	select {
	case <-done:
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("publish after close should return immediately")
	}
}

func TestEventHubCloseUnblocksCriticalPublishConcurrently(t *testing.T) {
	// Stress test: concurrent publish + close should never panic
	for i := 0; i < 100; i++ {
		hub := newEventHub(0)
		sub := hub.subscribe("crit", 1, true)
		hub.publish(&browserCDPEnvelope{Method: "fill"})

		go hub.publish(&browserCDPEnvelope{Method: "blocked"})
		go hub.close()
		go hub.unsubscribe(sub)

		time.Sleep(time.Millisecond)
	}
}
