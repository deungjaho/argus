package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// mockCDPServer is a minimal WebSocket server that mimics a CDP page
// endpoint. It accepts connections and responds to CDP commands with
// empty success results so that domain-enable calls don't fail.
type mockCDPServer struct {
	t        *testing.T
	server   *httptest.Server
	wsURL    string
	upgrader websocket.Upgrader
}

func newMockCDPServer(t *testing.T) *mockCDPServer {
	m := &mockCDPServer{
		t:        t,
		upgrader: websocket.Upgrader{},
	}
	m.server = httptest.NewServer(http.HandlerFunc(m.handle))
	m.wsURL = "ws" + strings.TrimPrefix(m.server.URL, "http") + "/devtools/page/mock-target-1"
	return m
}

// newMockCDPServerWithTarget creates a mock server with a specific
// target ID in the WebSocket URL path.
func newMockCDPServerWithTarget(t *testing.T, targetID string) *mockCDPServer {
	m := &mockCDPServer{
		t:        t,
		upgrader: websocket.Upgrader{},
	}
	m.server = httptest.NewServer(http.HandlerFunc(m.handle))
	m.wsURL = "ws" + strings.TrimPrefix(m.server.URL, "http") + "/devtools/page/" + targetID
	return m
}

func (m *mockCDPServer) handle(w http.ResponseWriter, r *http.Request) {
	// Accept both page and browser endpoint paths for testing.
	if !strings.Contains(r.URL.Path, "/devtools/") {
		http.NotFound(w, r)
		return
	}
	ws, err := m.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	// Read requests and respond with empty success results.
	for {
		var req map[string]any
		if err := ws.ReadJSON(&req); err != nil {
			return
		}
		id, _ := req["id"].(float64)
		resp := map[string]any{
			"id":     int(id),
			"result": map[string]any{},
		}
		if err := ws.WriteJSON(resp); err != nil {
			return
		}
	}
}

func (m *mockCDPServer) close() {
	m.server.Close()
}

// TestGetConnFirstConnectionMintsEpochZero verifies that the first
// connection to a page endpoint mints epoch_0000 and does NOT count
// as a reconnect.
func TestGetConnFirstConnectionMintsEpochZero(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()
	closeAllConns()
	defer closeAllConns()

	mock := newMockCDPServer(t)
	defer mock.close()

	conn, err := getConn(mock.wsURL)
	if err != nil {
		t.Fatalf("getConn failed: %v", err)
	}

	if conn.epoch != "epoch_0000" {
		t.Errorf("first connection epoch = %q, want epoch_0000", conn.epoch)
	}

	stats := rec.Stats()
	if stats.ReconnectGaps != 0 {
		t.Errorf("reconnect gaps = %d, want 0 for first connection", stats.ReconnectGaps)
	}
}

// TestGetConnReconnectMintsNewEpoch verifies that when a dead
// connection is replaced, a reconnect is recorded and a new epoch is
// minted on the new connection.
func TestGetConnReconnectMintsNewEpoch(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()
	closeAllConns()
	defer closeAllConns()

	mock := newMockCDPServer(t)
	defer mock.close()

	// First connection — epoch_0000
	conn1, err := getConn(mock.wsURL)
	if err != nil {
		t.Fatalf("first getConn failed: %v", err)
	}
	if conn1.epoch != "epoch_0000" {
		t.Fatalf("first connection epoch = %q, want epoch_0000", conn1.epoch)
	}

	// Kill the first connection to simulate a dead connection.
	conn1.close()

	// Second connection — should detect dead conn, record reconnect,
	// and mint epoch_0001.
	conn2, err := getConn(mock.wsURL)
	if err != nil {
		t.Fatalf("second getConn failed: %v", err)
	}
	if conn2.epoch != "epoch_0001" {
		t.Errorf("reconnect epoch = %q, want epoch_0001", conn2.epoch)
	}

	stats := rec.Stats()
	if stats.ReconnectGaps != 1 {
		t.Errorf("reconnect gaps = %d, want 1", stats.ReconnectGaps)
	}

	// Verify a reconnect event was recorded in the timeline.
	events := rec.QueryEvents(EventFilter{eventType: "reconnect"})
	if len(events) != 1 {
		t.Fatalf("expected 1 reconnect event, got %d", len(events))
	}
	data, ok := events[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("reconnect event data is %T, want map", events[0].Data)
	}
	if data["old_epoch"] != "epoch_0000" {
		t.Errorf("old_epoch = %v, want epoch_0000", data["old_epoch"])
	}
	if data["new_epoch"] != "epoch_0001" {
		t.Errorf("new_epoch = %v, want epoch_0001", data["new_epoch"])
	}
}

// TestGetConnReconnectDoesNotAffectOtherTargets verifies that a
// reconnect on one target does not mint a new epoch for a different
// target.
func TestGetConnReconnectDoesNotAffectOtherTargets(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()
	closeAllConns()
	defer closeAllConns()

	mock1 := newMockCDPServerWithTarget(t, "target-A")
	defer mock1.close()
	mock2 := newMockCDPServerWithTarget(t, "target-B")
	defer mock2.close()

	// Connect both targets.
	conn1, err := getConn(mock1.wsURL)
	if err != nil {
		t.Fatalf("getConn mock1 failed: %v", err)
	}
	conn2, err := getConn(mock2.wsURL)
	if err != nil {
		t.Fatalf("getConn mock2 failed: %v", err)
	}
	if conn1.epoch != "epoch_0000" || conn2.epoch != "epoch_0000" {
		t.Fatalf("initial epochs: conn1=%q conn2=%q, want epoch_0000 for both", conn1.epoch, conn2.epoch)
	}

	// Kill conn1 and reconnect.
	conn1.close()
	conn1b, err := getConn(mock1.wsURL)
	if err != nil {
		t.Fatalf("reconnect getConn mock1 failed: %v", err)
	}
	if conn1b.epoch != "epoch_0001" {
		t.Errorf("reconnected conn1 epoch = %q, want epoch_0001", conn1b.epoch)
	}

	// conn2's epoch should be unchanged.
	if rec.CurrentEpoch(shortID(mock2.wsURL)) != "epoch_0000" {
		t.Errorf("conn2 epoch after conn1 reconnect = %q, want epoch_0000", rec.CurrentEpoch(shortID(mock2.wsURL)))
	}

	// Only 1 reconnect gap should be counted.
	stats := rec.Stats()
	if stats.ReconnectGaps != 1 {
		t.Errorf("reconnect gaps = %d, want 1", stats.ReconnectGaps)
	}
}

// TestGetConnBrowserEndpointNoEpoch verifies that browser-level
// endpoints do not mint epochs (they don't have recorder subscribers).
func TestGetConnBrowserEndpointNoEpoch(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()
	closeAllConns()
	defer closeAllConns()

	mock := newMockCDPServer(t)
	defer mock.close()

	// Use a browser endpoint URL instead of page.
	browserURL := "ws" + strings.TrimPrefix(mock.server.URL, "http") + "/devtools/browser/mock-browser-1"
	conn, err := getConn(browserURL)
	if err != nil {
		t.Fatalf("getConn failed: %v", err)
	}
	if conn.epoch != "" {
		t.Errorf("browser endpoint epoch = %q, want empty (no epoch for browser endpoint)", conn.epoch)
	}

	stats := rec.Stats()
	if stats.ReconnectGaps != 0 {
		t.Errorf("reconnect gaps = %d, want 0 for browser endpoint", stats.ReconnectGaps)
	}
}
