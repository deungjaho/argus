package main

// automator_fake_test.go — deterministic fake WeChat automator WebSocket
// protocol server for contract tests.
//
// This harness replaces a real WeChat DevTools instance so tests can assert
// exact JSON-RPC method names, parameter fields, and response unwrapping
// without requiring a running Mini Program. It is the single source of
// protocol truth in the test suite.
//
// The fake server:
//   - Accepts WebSocket connections on a random localhost port.
//   - Reads JSON-RPC requests ({id, method, params}).
//   - Dispatches to a configurable handler map keyed by method name.
//   - Records every request for assertion.
//   - Supports delayed responses, error responses, and event pushes.
//   - Tracks connection lifecycle (open, close, reconnect).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

// fakeWxHandler is a handler for one automator method.
// It receives the decoded params and returns either a result value
// (to be wrapped in {id, result}) or an error (to be wrapped in
// {id, error: {message}}).
type fakeWxHandler func(params json.RawMessage) (result any, err error)

// fakeWxRequest is one recorded JSON-RPC request received by the fake server.
type fakeWxRequest struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// fakeWxServer is a deterministic fake WeChat automator WebSocket server.
type fakeWxServer struct {
	t        *testing.T
	server   *httptest.Server
	mu       sync.Mutex
	handlers map[string]fakeWxHandler
	requests []fakeWxRequest
	// conns tracks all WebSocket connections accepted.
	conns []*websocket.Conn
	// connMu guards conns.
	connMu sync.Mutex
	// eventCh, if non-nil, is used to push events to the client.
	// Events are sent as {method, params} frames.
	eventMu sync.Mutex
	eventCh chan struct{} // closed when a test wants to stop
	// wsURL is the ws:// URL of the server.
	wsURL string
	// readLimit is the max read message size (bytes).
	readLimit int64
	// allowReconnect controls whether the server accepts a second
	// connection after the first closes (for reconnect tests).
	allowReconnect bool
	// closeOnConnect, if true, closes the connection immediately
	// after upgrade (for disconnect tests).
	closeOnConnect bool
}

// newFakeWxServer starts a fake automator server on a random port.
func newFakeWxServer(t *testing.T, handlers map[string]fakeWxHandler) *fakeWxServer {
	t.Helper()
	f := &fakeWxServer{
		t:         t,
		handlers:  handlers,
		readLimit: 1 << 20, // 1 MiB
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handleWS))
	f.wsURL = "ws" + strings.TrimPrefix(f.server.URL, "http")
	return f
}

func (f *fakeWxServer) handleWS(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		f.t.Errorf("fake server upgrade failed: %v", err)
		return
	}
	ws.SetReadLimit(f.readLimit)
	f.connMu.Lock()
	f.conns = append(f.conns, ws)
	f.connMu.Unlock()

	if f.closeOnConnect {
		ws.Close()
		return
	}
	defer ws.Close()
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		var req fakeWxRequest
		if err := json.Unmarshal(data, &req); err != nil {
			f.t.Errorf("fake server: failed to parse request: %v\nraw: %s", err, string(data))
			return
		}
		f.mu.Lock()
		f.requests = append(f.requests, req)
		handler, ok := f.handlers[req.Method]
		f.mu.Unlock()
		if !ok {
			// No handler registered — return an error.
			f.sendResponse(ws, req.ID, nil, fmt.Errorf("unknown method: %s", req.Method))
			continue
		}
		result, err := handler(req.Params)
		f.sendResponse(ws, req.ID, result, err)
	}
}

func (f *fakeWxServer) sendResponse(ws *websocket.Conn, id string, result any, err error) {
	resp := map[string]any{"id": id}
	if err != nil {
		resp["error"] = map[string]any{"message": err.Error()}
	} else {
		resp["result"] = result
	}
	data, _ := json.Marshal(resp)
	f.connMu.Lock()
	ws.WriteMessage(websocket.TextMessage, data)
	f.connMu.Unlock()
}

// sendEvent pushes a server-side event ({method, params}) to the first
// active connection. Used to test event ingestion and bounding.
func (f *fakeWxServer) sendEvent(method string, params any) {
	f.connMu.Lock()
	defer f.connMu.Unlock()
	if len(f.conns) == 0 {
		return
	}
	data, _ := json.Marshal(map[string]any{"method": method, "params": params})
	f.conns[0].WriteMessage(websocket.TextMessage, data)
}

// port returns the TCP port the fake server is listening on.
func (f *fakeWxServer) port() int {
	// Extract port from wsURL.
	// wsURL is like "ws://127.0.0.1:PORT"
	parts := strings.Split(f.wsURL, ":")
	if len(parts) < 3 {
		return 0
	}
	var port int
	fmt.Sscanf(parts[len(parts)-1], "%d", &port)
	return port
}

// close stops the fake server.
func (f *fakeWxServer) close() {
	f.server.Close()
}

// recordedRequests returns a copy of all requests received by the fake server.
func (f *fakeWxServer) recordedRequests() []fakeWxRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeWxRequest, len(f.requests))
	copy(out, f.requests)
	return out
}

// findRequest returns the first recorded request with the given method,
// or nil if none was received.
func (f *fakeWxServer) findRequest(method string) *fakeWxRequest {
	for _, req := range f.recordedRequests() {
		if req.Method == method {
			r := req
			return &r
		}
	}
	return nil
}

// requireRequest asserts that a request with the given method was received
// and returns it, failing the test if not found.
func (f *fakeWxServer) requireRequest(method string) fakeWxRequest {
	f.t.Helper()
	req := f.findRequest(method)
	if req == nil {
		f.t.Fatalf("expected request for method %s, not received. All methods: %v", method, f.allMethods())
	}
	return *req
}

// allMethods returns the list of all method names received, in order.
func (f *fakeWxServer) allMethods() []string {
	reqs := f.recordedRequests()
	methods := make([]string, len(reqs))
	for i, r := range reqs {
		methods[i] = r.Method
	}
	return methods
}

// assertParam asserts that a request's params JSON contains the given
// key with the expected value (compared via JSON normalization).
func (f *fakeWxServer) assertParam(req fakeWxRequest, key string, expected any) {
	f.t.Helper()
	var params map[string]any
	if err := json.Unmarshal(req.Params, &params); err != nil {
		f.t.Fatalf("failed to parse params for %s: %v\nraw: %s", req.Method, err, string(req.Params))
	}
	got, ok := params[key]
	if !ok {
		f.t.Errorf("request %s: expected param %q not found. Params: %v", req.Method, key, params)
		return
	}
	// Compare via JSON round-trip to handle number types.
	expJSON, _ := json.Marshal(expected)
	gotJSON, _ := json.Marshal(got)
	if string(expJSON) != string(gotJSON) {
		f.t.Errorf("request %s: param %q = %s, want %s", req.Method, key, string(gotJSON), string(expJSON))
	}
}

// assertNoParam asserts that a request's params do NOT contain the given key.
func (f *fakeWxServer) assertNoParam(req fakeWxRequest, key string) {
	f.t.Helper()
	var params map[string]any
	if err := json.Unmarshal(req.Params, &params); err != nil {
		f.t.Fatalf("failed to parse params for %s: %v", req.Method, err)
	}
	if _, ok := params[key]; ok {
		f.t.Errorf("request %s: param %q should not be present. Params: %v", req.Method, key, params)
	}
}

// parseParams decodes a request's params into the target.
func parseParams(t *testing.T, req fakeWxRequest, target any) {
	t.Helper()
	if err := json.Unmarshal(req.Params, target); err != nil {
		t.Fatalf("failed to parse params for %s: %v\nraw: %s", req.Method, err, string(req.Params))
	}
}
