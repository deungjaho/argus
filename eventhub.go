package main

import (
	"sync"
	"sync/atomic"
)

// --- eventHub: bounded pub/sub for CDP events ---
//
// Each cdpConn has one eventHub. readLoop publishes events to the hub.
// Each subscriber (intercept listener, recorder, console collector, etc.)
// gets its own bounded buffer.
//
// Event delivery guarantees:
//   - Normal events (console, network, DOM): best-effort. If a subscriber's
//     buffer is full, the event is dropped and dropped counter incremented.
//   - Critical events (Fetch.requestPaused): never dropped. If the subscriber's
//     buffer is full, publish blocks until space is available or the hub closes.
//     This prevents browser requests from hanging forever in paused state.
//
// Close protocol:
//   - hub.close() closes all subscriber channels and marks the hub as closed.
//   - After close, publish returns immediately (non-blocking).
//   - close and publish are safe to call concurrently (no send-on-closed panic).
//   - unsubscribe closes a single subscriber's channel safely.
//
// Concurrency safety:
//   - close/unsubscribe close subscriber channels.
//   - publish may be sending to a channel concurrently with close.
//   - To prevent send-on-closed-channel panic, each subscriber has its own
//     mutex. publish holds the sub's RLock while sending; close/unsubscribe
//     hold the sub's Lock while closing. This ensures no send happens after close.

type eventSubscription struct {
	name     string
	ch       chan *browserCDPEnvelope
	dropped  int64
	hub      *eventHub
	critical bool         // if true, publish blocks instead of dropping
	closeMu  sync.RWMutex // protects ch from send-after-close
	closed   bool
}

func (s *eventSubscription) droppedCount() int64 {
	return atomic.LoadInt64(&s.dropped)
}

type eventHub struct {
	mu      sync.RWMutex
	subs    map[*eventSubscription]struct{}
	closed  bool
	closeCh chan struct{} // closed when hub is closed (used for blocking publish)
}

func newEventHub(_ int) *eventHub {
	return &eventHub{
		subs:    make(map[*eventSubscription]struct{}),
		closeCh: make(chan struct{}),
	}
}

// subscribe creates a new subscription with the given buffer size.
// If critical is true, events published to this subscriber will block
// instead of dropping when the buffer is full. Use for Fetch.requestPaused
// subscribers where dropping would leave browser requests paused forever.
func (h *eventHub) subscribe(name string, bufSize int, critical bool) *eventSubscription {
	if bufSize <= 0 {
		bufSize = 16
	}
	sub := &eventSubscription{
		name:     name,
		ch:       make(chan *browserCDPEnvelope, bufSize),
		hub:      h,
		critical: critical,
	}
	h.mu.Lock()
	h.subs[sub] = struct{}{}
	h.mu.Unlock()
	return sub
}

// unsubscribe removes a subscriber and closes its channel.
// Safe to call concurrently with publish.
func (h *eventHub) unsubscribe(sub *eventSubscription) {
	h.mu.Lock()
	_, exists := h.subs[sub]
	if exists {
		delete(h.subs, sub)
	}
	h.mu.Unlock()
	if exists {
		sub.closeMu.Lock()
		sub.closed = true
		close(sub.ch)
		sub.closeMu.Unlock()
	}
}

// publish delivers ev to all subscribers.
// For non-critical subscribers: non-blocking, drops on full buffer.
// For critical subscribers: blocks until space available or hub closes.
// Safe to call concurrently with close/unsubscribe.
func (h *eventHub) publish(ev *browserCDPEnvelope) {
	h.mu.RLock()
	if h.closed {
		h.mu.RUnlock()
		return
	}
	subs := make([]*eventSubscription, 0, len(h.subs))
	for sub := range h.subs {
		subs = append(subs, sub)
	}
	h.mu.RUnlock()

	// Deliver to non-critical subscribers first (fast, non-blocking),
	// then to critical subscribers (may block). This ensures a slow
	// critical subscriber doesn't delay drops for normal subscribers.
	for _, sub := range subs {
		if sub.critical {
			continue
		}
		sub.closeMu.RLock()
		if sub.closed {
			sub.closeMu.RUnlock()
			continue
		}
		select {
		case sub.ch <- ev:
		default:
			atomic.AddInt64(&sub.dropped, 1)
		}
		sub.closeMu.RUnlock()
	}
	for _, sub := range subs {
		if !sub.critical {
			continue
		}
		// Critical subscriber: block until delivered or hub closes.
		// This ensures Fetch.requestPaused is never dropped.
		// Hold closeMu.RLock for the entire select so close() can't
		// close the channel between our check and our send.
		// close() closes h.closeCh first (under hub.mu, not closeMu),
		// which unblocks this select, then we release RLock, then
		// close() acquires closeMu.Lock to close the channel.
		sub.closeMu.RLock()
		if sub.closed {
			sub.closeMu.RUnlock()
			continue
		}
		select {
		case sub.ch <- ev:
			sub.closeMu.RUnlock()
		case <-h.closeCh:
			sub.closeMu.RUnlock()
			return
		}
	}
}

// close shuts down the hub: marks it closed, closes closeCh (unblocks
// critical publishers), and closes all subscriber channels.
// Safe to call multiple times.
func (h *eventHub) close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	close(h.closeCh)
	subs := make([]*eventSubscription, 0, len(h.subs))
	for sub := range h.subs {
		subs = append(subs, sub)
	}
	h.subs = make(map[*eventSubscription]struct{})
	h.mu.Unlock()

	// Close channels outside the hub lock to avoid blocking publish
	// that might be waiting on a critical subscriber.
	// Each sub's closeMu ensures no send happens after close.
	for _, sub := range subs {
		sub.closeMu.Lock()
		sub.closed = true
		close(sub.ch)
		sub.closeMu.Unlock()
	}
}
