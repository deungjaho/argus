package main

import (
	"fmt"
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

func TestEventHubCriticalPublishDoesNotDeadlockWhenBufferStalls(t *testing.T) {
	hub := newEventHub(0)
	defer hub.close()

	sub := hub.subscribe("critical", 1, true)
	defer hub.unsubscribe(sub)

	// Publish event 1 (fills the buffer of 1)
	hub.publish(&browserCDPEnvelope{Method: "ev1"})

	// Publish event 2 (stalls buffer; should offload to overflow queue and NOT hang forever)
	done := make(chan struct{})
	go func() {
		hub.publish(&browserCDPEnvelope{Method: "ev2"})
		hub.publish(&browserCDPEnvelope{Method: "ev3"})
		close(done)
	}()

	select {
	case <-done:
		// Succeeded in returning without blocking indefinitely
	case <-time.After(500 * time.Millisecond):
		t.Fatal("publish deadlocked when subscriber buffer stalled!")
	}

	// Drain subscriber and verify all 3 events arrive in FIFO order with 0 drops
	var received []string
	timeout := time.After(500 * time.Millisecond)
	for len(received) < 3 {
		select {
		case ev := <-sub.ch:
			received = append(received, ev.Method)
		case <-timeout:
			t.Fatalf("timed out waiting for events, received %d: %v", len(received), received)
		}
	}

	if sub.droppedCount() != 0 {
		t.Errorf("critical subscriber dropped %d events, want 0", sub.droppedCount())
	}
	expected := []string{"ev1", "ev2", "ev3"}
	for i, m := range expected {
		if received[i] != m {
			t.Errorf("received[%d] = %s, want %s", i, received[i], m)
		}
	}
}

func TestEventHubCriticalPublishStrictFIFOOrdering(t *testing.T) {
	hub := newEventHub(0)
	defer hub.close()

	// Small buffer of 2 to force rapid overflow queue transitions
	sub := hub.subscribe("critical_fifo", 2, true)
	defer hub.unsubscribe(sub)

	const total = 50
	donePublish := make(chan struct{})
	go func() {
		for i := 0; i < total; i++ {
			hub.publish(&browserCDPEnvelope{Method: fmt.Sprintf("ev-%02d", i)})
		}
		close(donePublish)
	}()

	var received []string
	timeout := time.After(2 * time.Second)
	for len(received) < total {
		select {
		case ev := <-sub.ch:
			received = append(received, ev.Method)
			// Intermittent micro-pause to force draining state and overflow interleaving
			if len(received)%5 == 0 {
				time.Sleep(5 * time.Millisecond)
			}
		case <-timeout:
			t.Fatalf("timed out waiting for events, got %d of %d", len(received), total)
		}
	}

	<-donePublish

	for i := 0; i < total; i++ {
		want := fmt.Sprintf("ev-%02d", i)
		if received[i] != want {
			t.Fatalf("FIFO ordering violation at index %d: got %s, want %s", i, received[i], want)
		}
	}
}
