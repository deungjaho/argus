package main

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// --- CDP connection pool ---
//
// Each page-level WebSocket URL gets one persistent connection.
// Requests are multiplexed by ID. CDP events are dispatched to handlers.
//
// Reliability features:
//   - readLoop has panic recovery (one panic doesn't crash the server)
//   - on connection death: pool entry removed, event hub closed, pending requests notified
//   - event hub provides per-subscriber bounded pub/sub (no event stealing)
//   - intercept listener exits when subscription channel closes (no goroutine leak)
//   - Shutdown coordinator for graceful stop

type cdpConn struct {
	ws        *websocket.Conn
	writeMu   sync.Mutex // serializes writes to the WebSocket
	nextID    int64
	pending   map[int]chan *browserCDPEnvelope
	pendingMu sync.Mutex
	hub       *eventHub
	done      chan struct{} // closed when readLoop exits
	closed    bool
	closeMu   sync.Mutex
	log       *Logger
	// epoch is the connection epoch this connection operates under.
	// Minted by the recorder when the connection is established
	// (BeginEpoch for first connection, RecordReconnectGap for a
	// reconnect). The recorder subscriber stamps every event with
	// this epoch so pre-reconnect IDs cannot be silently joined to
	// post-reconnect IDs.
	epoch ConnectionEpoch
}

var (
	poolMu sync.Mutex
	pool   = map[string]*cdpConn{}
)

// getConn returns the pooled connection for wsURL, creating one if needed.
// Dead connections are removed from the pool and replaced.
func getConn(wsURL string) (*cdpConn, error) {
	poolMu.Lock()

	// Check for existing alive connection
	if conn, ok := pool[wsURL]; ok {
		if conn.alive() {
			poolMu.Unlock()
			return conn, nil
		}
		// Dead connection — remove it from pool
		delete(pool, wsURL)
	}

	// Create new connection
	conn, err := dialConn(wsURL)
	if err != nil {
		poolMu.Unlock()
		return nil, err
	}
	pool[wsURL] = conn
	poolMu.Unlock()
	if isPageEndpoint(wsURL) {
		rearmInterceptState(conn, wsURL)
	}
	return conn, nil
}

func dialConn(wsURL string) (*cdpConn, error) {
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, err
	}
	conn := &cdpConn{
		ws:      ws,
		pending: map[int]chan *browserCDPEnvelope{},
		hub:     newEventHub(256),
		done:    make(chan struct{}),
		log:     NewLogger("cdp:" + shortID(wsURL)),
	}
	go conn.readLoop()
	// Start persistent recorder subscriber only on page-level endpoints.
	// The Browser endpoint (/devtools/browser/...) does not support
	// Runtime/Log/Network domains and would produce spurious
	// domain_enable_error counters.
	if isPageEndpoint(wsURL) {
		// Mint the connection epoch for this target. If the target
		// already has an epoch (prior connection died and was
		// replaced), this is a reconnect: RecordReconnectGap mints a
		// new epoch and records a visible reconnect event. If the
		// target has no prior epoch, BeginEpoch mints epoch_0000.
		//
		// We check the recorder's epoch state rather than the pool
		// because the readLoop may have already removed the dead
		// connection from the pool before getConn runs. The epoch
		// map is the durable record of whether a target had a prior
		// connection.
		targetID := shortID(wsURL)
		if rec.CurrentEpoch(targetID) != "" {
			conn.epoch = rec.RecordReconnectGap(targetID)
		} else {
			conn.epoch = rec.BeginEpoch(targetID)
		}
		startRecorderSubscriber(conn, wsURL)
	}
	return conn, nil
}

// isPageEndpoint returns true if wsURL points to a page-level CDP
// endpoint (/devtools/page/...), false for the browser-level endpoint
// (/devtools/browser/...).
func isPageEndpoint(wsURL string) bool {
	return strings.Contains(wsURL, "/devtools/page/")
}

// shortID extracts a short identifier from a WebSocket URL for logging.
func shortID(wsURL string) string {
	// e.g. ws://127.0.0.1:9224/devtools/page/ABC123 -> ABC123
	idx := strings.LastIndex(wsURL, "/")
	if idx >= 0 && idx < len(wsURL)-1 {
		return wsURL[idx+1:]
	}
	return "?"
}

func (c *cdpConn) alive() bool {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	return !c.closed
}

// close marks the connection as closed and cleans up resources.
// Safe to call multiple times.
func (c *cdpConn) close() {
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return
	}
	c.closed = true
	c.closeMu.Unlock()

	c.ws.Close()
	close(c.done)

	// Close event hub so all subscribers exit
	c.hub.close()

	// Notify all pending requests
	c.pendingMu.Lock()
	for id, ch := range c.pending {
		select {
		case ch <- nil:
		default:
		}
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
}

// readLoop reads from the WebSocket continuously, dispatching responses and events.
// Has panic recovery so one bad message doesn't crash the server.
func (c *cdpConn) readLoop() {
	defer func() {
		if r := recover(); r != nil {
			c.log.Error("readLoop panic: %v", r)
		}
		c.close()
		// Remove from pool if still present
		poolMu.Lock()
		for url, conn := range pool {
			if conn == c {
				delete(pool, url)
				break
			}
		}
		poolMu.Unlock()
		c.log.Debug("readLoop exited")
	}()

	for {
		var envelope browserCDPEnvelope
		if err := c.ws.ReadJSON(&envelope); err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Read timeout — continue (we don't set read deadlines, but be safe)
				continue
			}
			// Connection died (EOF, reset, etc.)
			c.log.Debug("readLoop: connection closed: %v", err)
			return
		}

		if envelope.ID != 0 {
			// Response to a request
			c.pendingMu.Lock()
			ch, ok := c.pending[envelope.ID]
			if ok {
				delete(c.pending, envelope.ID)
			}
			c.pendingMu.Unlock()
			if ok {
				select {
				case ch <- &envelope:
				default:
				}
			}
			continue
		}

		// Event (no ID) — publish to hub (per-subscriber backpressure)
		c.hub.publish(&envelope)
	}
}

// call sends a CDP request and waits for the response.
func (c *cdpConn) call(method string, params any, result any, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	id := int(atomic.AddInt64(&c.nextID, 1))
	respCh := make(chan *browserCDPEnvelope, 1)

	c.pendingMu.Lock()
	c.pending[id] = respCh
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	c.writeMu.Lock()
	err := c.ws.WriteJSON(map[string]any{"id": id, "method": method, "params": params})
	c.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("write to CDP: %w", err)
	}

	select {
	case resp := <-respCh:
		if resp == nil {
			return fmt.Errorf("CDP connection closed during '%s'", method)
		}
		if resp.Error != nil {
			return fmt.Errorf("%s", firstNonEmpty(strings.TrimSpace(resp.Error.Message), "CDP request failed"))
		}
		if result != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("decode CDP result: %w", err)
			}
		}
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("CDP request '%s' timed out after %s", method, timeout)
	case <-c.done:
		return fmt.Errorf("CDP connection closed during '%s'", method)
	}
}

// subscribeEvents creates a new event subscription on this connection.
// Each subscriber gets its own bounded buffer. Returns the subscription
// and its event channel. Call unsubscribe to stop receiving.
// Set critical=true for subscribers that must never miss events (e.g.
// Fetch.requestPaused handler). Critical subscribers block the publisher
// (readLoop) when their buffer is full, instead of dropping events.
func (c *cdpConn) subscribeEvents(name string, bufSize int, critical bool) *eventSubscription {
	return c.hub.subscribe(name, bufSize, critical)
}

// closeConn removes a connection from the pool and closes it.
func closeConn(wsURL string) {
	poolMu.Lock()
	conn, ok := pool[wsURL]
	if ok {
		delete(pool, wsURL)
	}
	poolMu.Unlock()
	if ok {
		conn.close()
	}
	if isPageEndpoint(wsURL) {
		removeInterceptState(wsURL)
	}
}

// closeAllConns closes all pooled connections (used on shutdown and the
// Chrome crash path) and drops their intercept state — dead targets' rules
// must not linger in interceptTargets.
func closeAllConns() {
	poolMu.Lock()
	conns := make([]*cdpConn, 0, len(pool))
	urls := make([]string, 0, len(pool))
	for url, conn := range pool {
		conns = append(conns, conn)
		urls = append(urls, url)
		delete(pool, url)
	}
	poolMu.Unlock()
	for _, conn := range conns {
		conn.close()
	}
	for _, url := range urls {
		if isPageEndpoint(url) {
			removeInterceptState(url)
		}
	}
}
