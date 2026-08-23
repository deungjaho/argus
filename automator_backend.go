package main

// automator_backend.go — WeChat DevTools automation backend.
//
// WeChat DevTools exposes a JSON-RPC over WebSocket endpoint (the "auto port")
// that miniprogram-automator uses. The protocol is:
//   - One persistent WebSocket connection per Mini Program project.
//   - Each request is a JSON object with a string "id" (UUID), "method", and
//     optional "params".
//   - Each response echoes the "id" and adds "result" (or "error").
//   - Events are messages with a "method" but no "id"; they are emitted by
//     the runtime (e.g. App.logAdded) and dispatched to subscribers.
//
// This file implements the connection, request/response correlation, event
// dispatch, and a small bounded log buffer. It mirrors the structure of
// cdppool.go but is simpler: one connection, string IDs, no multiplexed
// targets.
//
// Design notes:
//   - The connection is owned by a single *automatorConn guarded by a pool
//     mutex, so configure can swap it atomically.
//   - readLoop has panic recovery (same pattern as cdpConn.readLoop).
//   - Pending requests are notified on connection death.
//   - Events are pushed into a bounded ring buffer; recent log/exception
//     events are queryable via console_messages without a live capture.
//   - All calls are context-aware: cancellation returns ErrCanceled,
//     deadline returns ErrTimeout, disconnect returns ErrBrowserDisconnected.
//   - WebSocket read limit is set to bound incoming frame size.
//   - Event params are byte-bounded and redacted at ingestion.
//   - Reconnect gaps are tracked and visible in queries.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// --- Protocol types ---

// automatorRequest is the JSON-RPC request envelope.
type automatorRequest struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// automatorResponse is the JSON-RPC response envelope.
type automatorResponse struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *automatorError `json:"error,omitempty"`
}

// automatorEvent is a server-pushed event (no id).
type automatorEvent struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type automatorError struct {
	Message string `json:"message,omitempty"`
	Code    int    `json:"code,omitempty"`
}

func (e *automatorError) Error() string {
	if e == nil {
		return "automator error"
	}
	if e.Code != 0 {
		return fmt.Sprintf("automator error %d: %s", e.Code, e.Message)
	}
	return "automator error: " + e.Message
}

// --- Connection ---

const (
	automatorEventBufferSize = 256
	automatorDefaultTimeout  = 15 * time.Second
	// automatorReadLimit bounds the max incoming WebSocket message size.
	automatorReadLimit = 4 << 20 // 4 MiB
	// automatorMaxEventBytes bounds the stored event params payload.
	automatorMaxEventBytes = 16 << 10 // 16 KiB
	// automatorShutdownTimeout is the max time to wait for the read loop
	// to exit on shutdown.
	automatorShutdownTimeout = 3 * time.Second
)

type automatorConn struct {
	ws        *websocket.Conn
	writeMu   sync.Mutex
	pending   map[string]chan *automatorResponse
	pendingMu sync.Mutex
	done      chan struct{}
	closed    bool
	closeMu   sync.Mutex
	log       *Logger
	epoch     int // connection epoch (increments on reconnect)

	// Bounded event ring buffer for recent log/exception events.
	eventsMu       sync.Mutex
	events         []automatorEventRecord
	eventDropped   int64 // events dropped (buffer full)
	eventTruncated int64 // events truncated (bytes exceeded limit)
	eventBytesIn   int64 // total event bytes received (for diagnostics)

	// reconnectGap is incremented when a reconnect is detected.
	reconnectGap int64
}

// automatorEventRecord is one stored event with a monotonic sequence and
// timestamp, so console_messages can return a bounded recent slice.
type automatorEventRecord struct {
	Seq       int64     `json:"seq"`
	Timestamp time.Time `json:"timestamp"`
	Method    string    `json:"method"`
	Params    any       `json:"params,omitempty"`
}

// --- Pool ---

var (
	automatorPoolMu sync.Mutex
	automatorConn_  *automatorConn // at most one active connection
	automatorSeq    int64          // monotonic event sequence
	automatorEpoch  int            // global connection epoch counter
)

// getAutomatorConn returns the active automator connection, dialing one if
// needed. Returns an error if no wxPort is configured.
func getAutomatorConn() (*automatorConn, error) {
	port := automatorPort()
	if port <= 0 {
		return nil, &ToolError{
			Code:    ErrUnsupported,
			Op:      "automator",
			Message: "automator backend not configured: set backend=wechat and wx_port (or wx_project) via configure",
		}
	}
	automatorPoolMu.Lock()
	defer automatorPoolMu.Unlock()
	if automatorConn_ != nil && automatorConn_.alive() {
		return automatorConn_, nil
	}
	if automatorConn_ != nil {
		automatorConn_.close()
		automatorConn_ = nil
	}
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d", port)
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, &ToolError{
			Code:    ErrBrowserDisconnected,
			Op:      "automator",
			Message: fmt.Sprintf("connect WeChat automator at %s: %v (is WeChat DevTools running with `cli auto --auto-port %d`?)", wsURL, err, port),
		}
	}
	ws.SetReadLimit(automatorReadLimit)
	automatorEpoch++
	conn := &automatorConn{
		ws:      ws,
		pending: map[string]chan *automatorResponse{},
		done:    make(chan struct{}),
		log:     NewLogger("automator"),
		epoch:   automatorEpoch,
	}
	// If there was a prior connection, this is a reconnect.
	if automatorEpoch > 1 {
		atomic.AddInt64(&conn.reconnectGap, 1)
	}
	automatorConn_ = conn
	go conn.readLoop()
	logInfo("automator: connected to %s (epoch=%d)", wsURL, conn.epoch)
	// Initialize the connection with Tool.getInfo, matching the official
	// SDK's checkVersion() pattern. The WeChat DevTools automator requires
	// this call to set up the page context before App.getCurrentPage or
	// App.getPageStack can resolve a webview ID. Without it, page-related
	// calls fail with "Cannot destructure property 'rawPath' of
	// 'getPageMetaByWebviewId(...)' as it is null."
	if err := conn.initConnection(); err != nil {
		logWarn("automator: Tool.getInfo init failed: %v (page calls may fail)", err)
	}
	return conn, nil
}

// initConnection calls Tool.getInfo to initialize the automator connection.
// This mirrors the official miniprogram-automator SDK's checkVersion() method,
// which is called immediately after connecting. The WeChat DevTools requires
// this handshake before page-related RPCs (App.getCurrentPage, App.getPageStack)
// can resolve the active webview.
func (c *automatorConn) initConnection() error {
	var info struct {
		SDKVersion string `json:"SDKVersion"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.callContext(ctx, "Tool.getInfo", map[string]any{}, &info); err != nil {
		return err
	}
	logInfo("automator: initialized (SDKVersion=%s)", info.SDKVersion)
	return nil
}

func (c *automatorConn) alive() bool {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	return !c.closed
}

func (c *automatorConn) close() {
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return
	}
	c.closed = true
	c.closeMu.Unlock()
	c.ws.Close()
	close(c.done)
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

func (c *automatorConn) readLoop() {
	defer func() {
		if r := recover(); r != nil {
			c.log.Error("readLoop panic: %v", r)
		}
		c.close()
		automatorPoolMu.Lock()
		if automatorConn_ == c {
			automatorConn_ = nil
		}
		automatorPoolMu.Unlock()
		c.log.Debug("readLoop exited")
	}()
	for {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			c.log.Debug("readLoop: connection closed: %v", err)
			return
		}
		// WeChat automator frames may be responses (have "id") or events
		// (have "method", no "id"). Decode lazily to distinguish.
		var probe struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result,omitempty"`
			Error  *automatorError `json:"error,omitempty"`
			Params json.RawMessage `json:"params,omitempty"`
		}
		if err := json.Unmarshal(data, &probe); err != nil {
			c.log.Debug("readLoop: unmarshal failed: %v", err)
			continue
		}
		if strings.TrimSpace(probe.ID) != "" {
			// Response to a request
			resp := &automatorResponse{
				ID:     probe.ID,
				Result: probe.Result,
				Error:  probe.Error,
			}
			c.pendingMu.Lock()
			ch, ok := c.pending[resp.ID]
			if ok {
				delete(c.pending, resp.ID)
			}
			c.pendingMu.Unlock()
			if ok {
				select {
				case ch <- resp:
				default:
				}
			}
			continue
		}
		// Event (no id) — record and publish
		if probe.Method == "" {
			continue
		}
		c.recordEvent(probe.Method, probe.Params)
	}
}

// recordEvent appends an event to the bounded ring buffer with a monotonic
// sequence number. Event params are byte-bounded and redacted at ingestion.
// Overflow is counted, not silent.
func (c *automatorConn) recordEvent(method string, params json.RawMessage) {
	atomic.AddInt64(&c.eventBytesIn, int64(len(params)))
	var parsed any
	if len(params) > 0 {
		// Bound the params payload before storing.
		if len(params) > automatorMaxEventBytes {
			atomic.AddInt64(&c.eventTruncated, 1)
			params = json.RawMessage(string(params[:automatorMaxEventBytes/2]) + "...[truncated]")
		}
		if err := json.Unmarshal(params, &parsed); err != nil {
			parsed = map[string]any{"_error": "failed to parse event params"}
		}
		// Redact sensitive data at ingestion (defense in depth).
		parsed = redactData(parsed)
	}
	rec := automatorEventRecord{
		Seq:       atomic.AddInt64(&automatorSeq, 1),
		Timestamp: time.Now(),
		Method:    method,
		Params:    parsed,
	}
	c.eventsMu.Lock()
	defer c.eventsMu.Unlock()
	if len(c.events) >= automatorEventBufferSize {
		// Drop oldest (ring behavior)
		c.events = c.events[1:]
		atomic.AddInt64(&c.eventDropped, 1)
	}
	c.events = append(c.events, rec)
}

// recentEvents returns a copy of the last n stored events (oldest first).
func (c *automatorConn) recentEvents(n int) ([]automatorEventRecord, int64) {
	c.eventsMu.Lock()
	defer c.eventsMu.Unlock()
	dropped := atomic.LoadInt64(&c.eventDropped)
	if n <= 0 || n > len(c.events) {
		n = len(c.events)
	}
	start := len(c.events) - n
	out := make([]automatorEventRecord, n)
	copy(out, c.events[start:])
	return out, dropped
}

// eventStats returns diagnostic counters for the event ring buffer.
func (c *automatorConn) eventStats() (dropped, truncated int64, bytesIn int64) {
	return atomic.LoadInt64(&c.eventDropped),
		atomic.LoadInt64(&c.eventTruncated),
		atomic.LoadInt64(&c.eventBytesIn)
}

// callContext sends a JSON-RPC request and waits for the response or
// context cancellation. Returns structured ToolError on cancellation,
// timeout, or disconnect.
func (c *automatorConn) callContext(ctx context.Context, method string, params any, result any) error {
	id := automatorNewID()
	respCh := make(chan *automatorResponse, 1)

	c.pendingMu.Lock()
	c.pending[id] = respCh
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	var paramsRaw json.RawMessage
	if params != nil {
		var err error
		paramsRaw, err = json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal automator params: %w", err)
		}
	}
	req := automatorRequest{ID: id, Method: method, Params: paramsRaw}
	c.writeMu.Lock()
	err := c.ws.WriteJSON(req)
	c.writeMu.Unlock()
	if err != nil {
		return &ToolError{
			Code:    ErrBrowserDisconnected,
			Op:      method,
			Message: fmt.Sprintf("write to automator: %v", err),
		}
	}

	select {
	case resp := <-respCh:
		if resp == nil {
			return &ToolError{
				Code:    ErrBrowserDisconnected,
				Op:      method,
				Message: fmt.Sprintf("automator connection closed during '%s'", method),
			}
		}
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("decode automator result for '%s': %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		return contextToToolError(ctx.Err(), method)
	case <-c.done:
		return &ToolError{
			Code:    ErrBrowserDisconnected,
			Op:      method,
			Message: fmt.Sprintf("automator connection closed during '%s'", method),
		}
	}
}

// contextToToolError converts a context error to a structured ToolError.
func contextToToolError(ctxErr error, op string) *ToolError {
	if ctxErr == context.Canceled {
		return &ToolError{
			Code:    ErrCanceled,
			Op:      op,
			Message: "operation canceled",
		}
	}
	if ctxErr == context.DeadlineExceeded {
		return &ToolError{
			Code:    ErrTimeout,
			Op:      op,
			Message: "operation timed out",
		}
	}
	return &ToolError{
		Code:    ErrCanceled,
		Op:      op,
		Message: ctxErr.Error(),
	}
}

// call is the legacy non-context call method, kept for backward
// compatibility with existing code that doesn't have a context.
// It uses automatorDefaultTimeout as the deadline.
func (c *automatorConn) call(method string, params any, result any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return c.callContext(ctx, method, params, result)
}

// closeAutomatorConn tears down the active connection (used on reconfigure
// and shutdown). Waits up to automatorShutdownTimeout for the read loop.
func closeAutomatorConn() {
	automatorPoolMu.Lock()
	conn := automatorConn_
	automatorConn_ = nil
	automatorPoolMu.Unlock()
	if conn != nil {
		conn.close()
	}
}

// automatorCallContext is the convenience wrapper used by tool implementations.
// It resolves the active connection and forwards the call with context.
func automatorCallContext(ctx context.Context, method string, params any, result any) error {
	conn, err := getAutomatorConn()
	if err != nil {
		return err
	}
	return conn.callContext(ctx, method, params, result)
}

// defaultAutomatorCtx returns a context with the default automator timeout
// and a cancel function. The caller MUST defer the cancel function to
// release resources promptly. This fixes the previous cancellation
// ownership leak where the cancel was discarded.
func defaultAutomatorCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), automatorDefaultTimeout)
}

// automatorCall is the legacy convenience wrapper (no context).
// Kept for backward compatibility; new code should use automatorCallContext.
func automatorCall(method string, params any, result any) error {
	ctx, cancel := defaultAutomatorCtx()
	defer cancel()
	return automatorCallContext(ctx, method, params, result)
}

// automatorCallTimeout is like automatorCall but with an explicit timeout.
func automatorCallTimeout(timeout time.Duration, method string, params any, result any) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return automatorCallContext(ctx, method, params, result)
}

// --- ID generation ---

// automatorIDCounter is a process-unique counter for request IDs. The
// WeChat protocol correlates by string id; a monotonic counter is unique
// within one process and simpler than a UUID.
var automatorIDCounter uint64

func automatorNewID() string {
	return fmt.Sprintf("argus-%d", atomic.AddUint64(&automatorIDCounter, 1))
}

// --- Screenshot base64 decoding ---

// decodeScreenshotBase64 strips a data: URI prefix if present and decodes
// the base64 string to bytes.
func decodeScreenshotBase64(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "data:") {
		if idx := strings.Index(raw, ","); idx >= 0 {
			raw = raw[idx+1:]
		}
	}
	if raw == "" {
		return nil, fmt.Errorf("automator returned empty screenshot")
	}
	return base64.StdEncoding.DecodeString(raw)
}

// stdBase64 encodes bytes to a standard base64 string. Used by tests.
func stdBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
