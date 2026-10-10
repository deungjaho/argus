package main

import (
	"sync"
	"sync/atomic"
	"time"
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

const criticalPublishTimeout = 100 * time.Millisecond

type eventSubscription struct {
	name     string
	ch       chan *browserCDPEnvelope
	dropped  int64
	hub      *eventHub
	critical bool         // if true, never dropped; uses bounded wait + overflow queue to prevent deadlock
	closeMu  sync.RWMutex // protects ch from send-after-close
	closed   bool
	stopCh   chan struct{} // closed when unsubscribed or hub closed

	overflowMu sync.Mutex
	overflow   []*browserCDPEnvelope
	draining   bool
}

func (s *eventSubscription) droppedCount() int64 {
	return atomic.LoadInt64(&s.dropped)
}

func (s *eventSubscription) enqueueCritical(ev *browserCDPEnvelope) {
	s.overflowMu.Lock()
	defer s.overflowMu.Unlock()
	s.closeMu.RLock()
	closed := s.closed
	s.closeMu.RUnlock()
	if closed {
		return
	}
	s.overflow = append(s.overflow, ev)
	if !s.draining {
		s.draining = true
		go s.drainOverflow()
	}
}

func (s *eventSubscription) drainOverflow() {
	for {
		s.overflowMu.Lock()
		if len(s.overflow) == 0 {
			s.draining = false
			s.overflowMu.Unlock()
			return
		}
		ev := s.overflow[0]
		s.overflow[0] = nil
		s.overflow = s.overflow[1:]
		s.overflowMu.Unlock()

		s.closeMu.RLock()
		if s.closed {
			s.closeMu.RUnlock()
			s.overflowMu.Lock()
			s.overflow = nil
			s.draining = false
			s.overflowMu.Unlock()
			return
		}
		select {
		case s.ch <- ev:
			s.closeMu.RUnlock()
		case <-s.hub.closeCh:
			s.closeMu.RUnlock()
			s.overflowMu.Lock()
			s.overflow = nil
			s.draining = false
			s.overflowMu.Unlock()
			return
		case <-s.stopCh:
			s.closeMu.RUnlock()
			s.overflowMu.Lock()
			s.overflow = nil
			s.draining = false
			s.overflowMu.Unlock()
			return
		}
	}
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
// If critical is true, events published to this subscriber will not be dropped.
// When full, publish uses bounded wait before offloading to an overflow queue,
// preventing WebSocket readLoop deadlocks.
func (h *eventHub) subscribe(name string, bufSize int, critical bool) *eventSubscription {
	if bufSize <= 0 {
		bufSize = 16
	}
	sub := &eventSubscription{
		name:     name,
		ch:       make(chan *browserCDPEnvelope, bufSize),
		hub:      h,
		critical: critical,
		stopCh:   make(chan struct{}),
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
		if !sub.closed {
			sub.closed = true
			close(sub.stopCh)
			close(sub.ch)
		}
		sub.closeMu.Unlock()
	}
}

// publish delivers ev to all subscribers.
// For non-critical subscribers: non-blocking, drops on full buffer.
// For critical subscribers: bounded wait, offloads to overflow queue if full,
// ensuring the WebSocket readLoop is never blocked indefinitely.
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
	// then to critical subscribers (bounded wait + overflow queue).
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
		sub.closeMu.RLock()
		if sub.closed {
			sub.closeMu.RUnlock()
			continue
		}

		sub.overflowMu.Lock()
		if sub.draining || len(sub.overflow) > 0 {
			sub.overflow = append(sub.overflow, ev)
			if !sub.draining {
				sub.draining = true
				go sub.drainOverflow()
			}
			sub.overflowMu.Unlock()
			sub.closeMu.RUnlock()
			continue
		}
		sub.overflowMu.Unlock()

		timer := time.NewTimer(criticalPublishTimeout)
		select {
		case sub.ch <- ev:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			sub.closeMu.RUnlock()
		case <-h.closeCh:
			timer.Stop()
			sub.closeMu.RUnlock()
			return
		case <-sub.stopCh:
			timer.Stop()
			sub.closeMu.RUnlock()
			continue
		case <-timer.C:
			// Channel full and timed out.
			// Do NOT block readLoop indefinitely!
			// Offload to overflow queue so background worker delivers when ready.
			sub.closeMu.RUnlock()
			sub.enqueueCritical(ev)
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

	// Close channels outside the hub lock to avoid blocking publish.
	// Each sub's closeMu ensures no send happens after close.
	for _, sub := range subs {
		sub.closeMu.Lock()
		if !sub.closed {
			sub.closed = true
			close(sub.stopCh)
			close(sub.ch)
		}
		sub.closeMu.Unlock()
	}
}
