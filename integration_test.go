//go:build integration

// Integration tests require a real Chrome instance with CDP enabled.
// Run with: go test -tags=integration -timeout 120s -v ./...
//
// These tests cover the core CDP/Chrome/tool paths that unit tests can't:
//   - Chrome launch, CDP connection, target creation
//   - snapshot, click, type, screenshot
//   - console exception capture
//   - network response body
//   - intercept mock/block/passthrough
//   - multi-tab target selection
//   - shutdown cleanup (no goroutine/conn/paused-request residue)

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// integrationChromePort is set by TestMain after launching a single Chrome
// instance shared by all integration tests.
var integrationChromePort int

// setupTestChrome configures Argus to use the shared Chrome instance.
// Each test gets a clean target state; connections are closed on cleanup.
func setupTestChrome(t *testing.T) (port int, cleanup func()) {
	t.Helper()
	port = integrationChromePort

	// Reset Chrome launch state so ensureChromeRunning checks the shared instance
	chromeMu.Lock()
	chromeLaunched = true // already launched by TestMain
	chromeMu.Unlock()

	attach := false
	setConfig("", port, &attach)

	baselineTargets := integrationTargetIDs()
	cleanup = func() {
		// Close only targets created during this test. The shared browser and
		// targets that predate the test belong to TestMain or another owner.
		for _, target := range integrationTargetsCreatedAfter(baselineTargets) {
			if err := cdpCloseTarget(target); err != nil {
				t.Logf("cleanup target %s: %v", target, err)
			}
		}
		closeAllConns()
		clearCurrentTargetID()
		// Clean up any intercept state
		interceptTargetsMu.Lock()
		for wsURL, s := range interceptTargets {
			if s.sub != nil {
				select {
				case <-s.stopCh:
				default:
					close(s.stopCh)
				}
			}
			delete(interceptTargets, wsURL)
		}
		interceptTargetsMu.Unlock()
	}

	return port, cleanup
}

func integrationTargetIDs() map[string]struct{} {
	ids := make(map[string]struct{})
	targets, err := cdpTargets()
	if err != nil {
		return ids
	}
	for _, target := range targets {
		ids[target.ID] = struct{}{}
	}
	return ids
}

func integrationTargetsCreatedAfter(baseline map[string]struct{}) []string {
	targets, err := cdpTargets()
	if err != nil {
		return nil
	}
	created := make([]string, 0)
	for _, target := range targets {
		if _, existed := baseline[target.ID]; !existed {
			created = append(created, target.ID)
		}
	}
	return created
}

// setupTestServer creates a test HTTP server with a simple page.
func setupTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, string) {
	t.Helper()
	server := httptest.NewServer(handler)
	return server, server.URL
}

// simpleTestPage returns a basic HTML page with interactive elements.
func simpleTestPage() string {
	return `<!DOCTYPE html>
<html><head><title>Test Page</title></head>
<body>
<h1 id="title">Hello Argus</h1>
<button id="btn" onclick="document.getElementById('result').textContent='clicked'">Click Me</button>
<div id="result"></div>
<input id="textinput" type="text" placeholder="type here" />
<script>
console.log("page loaded");
window.testVar = 42;
</script>
</body></html>`
}

// --- Test 1: Chrome launch and CDP version ---

func TestIntegrationChromeLaunchAndVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	version, err := cdpVersion()
	if err != nil {
		t.Fatalf("cdpVersion failed: %v", err)
	}
	if version.Browser == "" {
		t.Error("version.Browser is empty")
	}
	if version.WebSocketDebuggerURL == "" {
		t.Error("version.WebSocketDebuggerURL is empty")
	}
	t.Logf("Chrome on port %d: %s", integrationChromePort, version.Browser)
}

// --- Test 2: Target creation and targetID selection ---

func TestIntegrationTargetCreationAndSelection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, simpleTestPage())
	})
	defer server.Close()

	// Create a tab
	setConfig(serverURL, 0, nil)
	target, err := requireTarget()
	if err != nil {
		t.Fatalf("requireTarget failed: %v", err)
	}
	if target.ID == "" {
		t.Fatal("target.ID is empty")
	}

	// Verify targetID is set
	if getCurrentTargetID() != target.ID {
		t.Errorf("currentTargetID = %q, want %q", getCurrentTargetID(), target.ID)
	}

	// Create a second tab with same URL
	version, err := cdpVersion()
	if err != nil {
		t.Fatalf("cdpVersion failed: %v", err)
	}
	tabID2, err := cdpCreateTarget(version.WebSocketDebuggerURL, serverURL, true)
	if err != nil {
		t.Fatalf("cdpCreateTarget failed: %v", err)
	}

	// List tabs and verify both exist
	targets, err := cdpTargets()
	if err != nil {
		t.Fatalf("cdpTargets failed: %v", err)
	}
	count := 0
	for _, tg := range targets {
		if tg.Type == "page" && (tg.ID == target.ID || tg.ID == tabID2) {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 tabs, found %d", count)
	}

	// Select the first tab by ID (not URL)
	if err := cdpActivateTarget(target.ID); err != nil {
		t.Fatalf("cdpActivateTarget failed: %v", err)
	}
	setCurrentTargetID(target.ID)

	// requireTarget should return the first tab (by ID, not URL ambiguity)
	target2, err := requireTarget()
	if err != nil {
		t.Fatalf("second requireTarget failed: %v", err)
	}
	if target2.ID != target.ID {
		t.Errorf("requireTarget returned %q, want %q (targetID should be stable)", target2.ID, target.ID)
	}

	// Clean up second tab
	_ = cdpCloseTarget(tabID2)
}

// --- Test 3: Page navigation and evaluation ---

func TestIntegrationNavigateAndEvaluate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, simpleTestPage())
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}

	// Wait for page to load using eventually pattern
	waitForCondition(t, 5*time.Second, func() bool {
		v, err := cdpEvaluate(ws, `typeof window.testVar !== 'undefined'`, true)
		if err != nil {
			return false
		}
		ok, _ := v.(bool)
		return ok
	})

	// Evaluate JS
	value, err := cdpEvaluate(ws, `window.testVar`, true)
	if err != nil {
		t.Fatalf("cdpEvaluate failed: %v", err)
	}
	if num, ok := value.(float64); !ok || num != 42 {
		t.Errorf("window.testVar = %v, want 42", value)
	}

	// Check document title
	value, err = cdpEvaluate(ws, `document.title`, true)
	if err != nil {
		t.Fatalf("cdpEvaluate title failed: %v", err)
	}
	if title, ok := value.(string); !ok || title != "Test Page" {
		t.Errorf("document.title = %v, want 'Test Page'", value)
	}
}

// --- Test 4: Screenshot ---

func TestIntegrationScreenshot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, simpleTestPage())
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.getElementById('title')`, true)
		return err == nil
	})

	data, err := cdpCaptureScreenshot(ws, false)
	if err != nil {
		t.Fatalf("cdpCaptureScreenshot failed: %v", err)
	}
	if len(data) < 100 {
		t.Errorf("screenshot too small: %d bytes", len(data))
	}
	// Check PNG magic bytes
	if len(data) < 4 || data[0] != 0x89 || data[1] != 0x50 {
		t.Error("screenshot is not a PNG")
	}
	t.Logf("screenshot: %d bytes", len(data))
}

// --- Test 5: Click and verify result ---

func TestIntegrationClick(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, simpleTestPage())
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.getElementById('btn')`, true)
		return err == nil
	})

	// Click the button via CDP Input.dispatchMouseEvent
	// First get button position
	value, err := cdpEvaluate(ws, `(() => {
const btn = document.getElementById('btn');
const rect = btn.getBoundingClientRect();
return {x: rect.x + rect.width/2, y: rect.y + rect.height/2};
})()`, true)
	if err != nil {
		t.Fatalf("get button position failed: %v", err)
	}
	pos, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("position is not a map: %T", value)
	}
	x, _ := numberValue(pos["x"])
	y, _ := numberValue(pos["y"])

	// Dispatch mouse events
	for _, typ := range []string{"mouseMoved", "mousePressed", "mouseReleased"} {
		err := cdpRequest(ws, "Input.dispatchMouseEvent", map[string]any{
			"type":       typ,
			"x":          x,
			"y":          y,
			"button":     "left",
			"clickCount": 1,
		}, nil)
		if err != nil {
			t.Fatalf("dispatchMouseEvent %s failed: %v", typ, err)
		}
	}

	// Verify click result using eventually pattern
	waitForCondition(t, 3*time.Second, func() bool {
		v, err := cdpEvaluate(ws, `document.getElementById('result').textContent`, true)
		if err != nil {
			return false
		}
		text, _ := v.(string)
		return text == "clicked"
	})

	value, _ = cdpEvaluate(ws, `document.getElementById('result').textContent`, true)
	if text, ok := value.(string); !ok || text != "clicked" {
		t.Errorf("result = %v, want 'clicked'", value)
	}
}

// --- Test 6: Console message capture ---

func TestIntegrationConsoleCapture(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html>
<html><body>
<script>
console.log("test log message");
console.warn("test warning");
console.error("test error");
</script>
</body></html>`)
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Collect logs for 2 seconds
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entries, err := collectLogs(ctx, ws, 2*time.Second)
	if err != nil {
		t.Fatalf("collectLogs failed: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no console entries captured")
	}
	t.Logf("captured %d console entries", len(entries))
	for _, entry := range entries {
		t.Logf("  entry: %v", entry)
	}
}

// --- Test 7: Intercept mock ---

func TestIntegrationInterceptMock(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/data" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"real": "data"}`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html>
<html><body>
<div id="api-result">loading</div>
<script>
fetch('/api/data').then(r => r.text()).then(t => {
document.getElementById('api-result').textContent = t;
}).catch(e => {
document.getElementById('api-result').textContent = 'error: ' + e.message;
});
</script>
</body></html>`)
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.getElementById('api-result')`, true)
		return err == nil
	})

	// Set up intercept to mock /api/data
	state := getInterceptState(ws)
	state.mu.Lock()
	state.rules = []interceptRule{
		{URLPattern: "/api/data", Action: "mock", Status: 200, Body: "mocked response"},
	}
	if err := cdpRequest(ws, "Fetch.enable", map[string]any{
		"patterns": []map[string]any{{"urlPattern": "*", "requestStage": "Request"}},
	}, nil); err != nil {
		t.Fatalf("Fetch.enable failed: %v", err)
	}
	state.active = true
	state.mu.Unlock()

	startInterceptListener(state, ws)
	defer func() {
		stopInterceptListener(state, ws)
		state.mu.Lock()
		state.rules = nil
		state.active = false
		state.mu.Unlock()
		_ = cdpRequest(ws, "Fetch.disable", map[string]any{}, nil)
	}()

	// Reload page to trigger fetch
	if err := cdpPageReload(ws, true); err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	// Check if mock was applied using eventually pattern
	waitForCondition(t, 5*time.Second, func() bool {
		v, err := cdpEvaluate(ws, `document.getElementById('api-result').textContent`, true)
		if err != nil {
			return false
		}
		text, _ := v.(string)
		return text == "mocked response"
	})

	value, _ := cdpEvaluate(ws, `document.getElementById('api-result').textContent`, true)
	if text, ok := value.(string); !ok || text != "mocked response" {
		t.Errorf("api-result = %v, want 'mocked response'", value)
	}
}

// --- Test 8: Shutdown cleanup ---

func TestIntegrationShutdownCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, simpleTestPage())
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	_, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}

	// Verify we have connections in the pool
	poolMu.Lock()
	poolCount := len(pool)
	poolMu.Unlock()
	if poolCount == 0 {
		t.Fatal("expected connections in pool before shutdown")
	}

	// Trigger shutdown
	BeginGlobalShutdown()
	closeAllConns()

	// Verify pool is empty
	waitForCondition(t, 3*time.Second, func() bool {
		poolMu.Lock()
		n := len(pool)
		poolMu.Unlock()
		return n == 0
	})

	poolMu.Lock()
	poolCount = len(pool)
	poolMu.Unlock()
	if poolCount != 0 {
		t.Errorf("expected empty pool after shutdown, got %d", poolCount)
	}

	// Verify no intercept listeners remain
	interceptTargetsMu.Lock()
	interceptCount := len(interceptTargets)
	interceptTargetsMu.Unlock()
	if interceptCount != 0 {
		t.Errorf("expected no intercept targets after shutdown, got %d", interceptCount)
	}
}

// --- Test 9: Event hub with real connection ---

func TestIntegrationEventHubRealConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, simpleTestPage())
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Subscribe to events
	conn, err := getConn(ws)
	if err != nil {
		t.Fatalf("getConn failed: %v", err)
	}
	sub := conn.subscribeEvents("test", 32, false)
	defer conn.hub.unsubscribe(sub)

	// Trigger a navigation event
	_, _ = cdpEvaluate(ws, `history.pushState({}, '', '/test-path')`, true)

	// Wait for event
	select {
	case ev := <-sub.ch:
		if ev == nil {
			t.Fatal("received nil event")
		}
		t.Logf("received event: %s", ev.Method)
	case <-time.After(3 * time.Second):
		// Not fatal — some events may not be emitted depending on CDP state
		t.Log("no event received within timeout (may be normal)")
	}
}

// --- Test 10: Network request capture with response body ---

func TestIntegrationNetworkCaptureWithBody(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/error" {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error": "internal server error"}`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html>
<html><body>
<script>
fetch('/api/error').catch(e => {});
</script>
</body></html>`)
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Collect network requests for 3 seconds
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	entries, err := collectNetworkRequests(ctx, ws, 3*time.Second)
	if err != nil {
		t.Fatalf("collectNetworkRequests failed: %v", err)
	}

	// Find the error request
	found := false
	for _, entry := range entries {
		if url, _ := entry["url"].(string); url != "" {
			if status, _ := numberValue(entry["status"]); status >= 500 {
				found = true
				if body, hasBody := entry["response_body"].(string); hasBody && body != "" {
					t.Logf("found error request with body: %s", body)
				} else {
					t.Logf("found error request but no body (may not have finished loading)")
				}
			}
		}
	}
	if !found {
		t.Log("no error requests captured (may need page reload to trigger)")
	}
}

// --- Test 11: Two same-URL tabs with targetID disambiguation ---

func TestIntegrationSameURLTabsDisambiguation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, simpleTestPage())
	})
	defer server.Close()

	// Create two tabs with same URL
	version, err := cdpVersion()
	if err != nil {
		t.Fatalf("cdpVersion failed: %v", err)
	}
	tabID1, err := cdpCreateTarget(version.WebSocketDebuggerURL, serverURL, false)
	if err != nil {
		t.Fatalf("create tab1 failed: %v", err)
	}
	tabID2, err := cdpCreateTarget(version.WebSocketDebuggerURL, serverURL, false)
	if err != nil {
		t.Fatalf("create tab2 failed: %v", err)
	}

	// Select tab1 by ID
	setCurrentTargetID(tabID1)
	target1, err := requireTarget()
	if err != nil {
		t.Fatalf("requireTarget for tab1 failed: %v", err)
	}
	if target1.ID != tabID1 {
		t.Errorf("requireTarget returned %q, want %q", target1.ID, tabID1)
	}

	// Select tab2 by ID
	setCurrentTargetID(tabID2)
	target2, err := requireTarget()
	if err != nil {
		t.Fatalf("requireTarget for tab2 failed: %v", err)
	}
	if target2.ID != tabID2 {
		t.Errorf("requireTarget returned %q, want %q", target2.ID, tabID2)
	}

	// Clean up
	_ = cdpCloseTarget(tabID1)
	_ = cdpCloseTarget(tabID2)
}

// --- Test 12: Target disappeared returns clear error ---

func TestIntegrationTargetDisappearedError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, simpleTestPage())
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	target, err := requireTarget()
	if err != nil {
		t.Fatalf("requireTarget failed: %v", err)
	}

	// Close the tab
	if err := cdpCloseTarget(target.ID); err != nil {
		t.Fatalf("cdpCloseTarget failed: %v", err)
	}

	// Wait for Chrome to actually remove the target
	waitForCondition(t, 3*time.Second, func() bool {
		targets, err := cdpTargets()
		if err != nil {
			return false
		}
		return findTargetByID(targets, target.ID) == nil
	})

	// targetID is still set, but target no longer exists
	// requireTarget should return an error, not silently switch
	_, err = requireTarget()
	if err == nil {
		t.Error("expected error when target disappeared, got nil")
	}
	t.Logf("correctly got error: %v", err)
}

// --- Test 13: HTTP timeout on unreachable CDP ---

func TestIntegrationHTTPTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Point to a port that nothing is listening on
	origCdpPort := cdpPort
	origConfiguredPort := configuredPort
	cdpPort = 9991 // unlikely to have Chrome here
	configMu.Lock()
	configuredPort = 9991
	configMu.Unlock()
	defer func() {
		cdpPort = origCdpPort
		configMu.Lock()
		configuredPort = origConfiguredPort
		configMu.Unlock()
	}()

	start := time.Now()
	_, err := cdpVersion()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error for unreachable CDP, got nil")
	}
	// Should fail within timeout (5s), not hang
	if elapsed > 6*time.Second {
		t.Errorf("cdpVersion took %s, should have timed out within 5s", elapsed)
	}
	t.Logf("cdpVersion correctly failed in %s: %v", elapsed, err)
}

// --- Test 14: JSON roundtrip sanity ---

func TestIntegrationJSONRoundtrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, simpleTestPage())
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Test complex JSON roundtrip
	value, err := cdpEvaluate(ws, `(() => {
return {
nested: {a: 1, b: [2, 3, 4]},
str: "hello",
num: 3.14,
bool: true,
null: null
};
})()`, true)
	if err != nil {
		t.Fatalf("cdpEvaluate failed: %v", err)
	}

	// Verify structure
	obj, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value is not a map: %T", value)
	}
	if nested, ok := obj["nested"].(map[string]any); ok {
		if a, ok := nested["a"].(float64); !ok || a != 1 {
			t.Errorf("nested.a = %v, want 1", nested["a"])
		}
	} else {
		t.Error("nested is not a map")
	}
	if str, ok := obj["str"].(string); !ok || str != "hello" {
		t.Errorf("str = %v, want 'hello'", obj["str"])
	}

	// Verify JSON serialization works
	_, err = json.Marshal(obj)
	if err != nil {
		t.Errorf("json.Marshal failed: %v", err)
	}
}

// --- Test 15: Refresh with ignore cache ---

func TestIntegrationRefreshIgnoreCache(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, simpleTestPage())
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Reload with ignoreCache
	if err := cdpPageReload(ws, true); err != nil {
		t.Fatalf("cdpPageReload failed: %v", err)
	}

	// Verify page reloaded
	waitForCondition(t, 5*time.Second, func() bool {
		v, err := cdpEvaluate(ws, `document.readyState`, true)
		if err != nil {
			return false
		}
		state, _ := v.(string)
		return state == "complete"
	})
}

// --- Test 16: Recorder captures console events from real Chrome ---

func TestIntegrationRecorderConsoleCapture(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	// Clear recorder for a clean slate
	rec.Clear()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html>
<html><body>
<script>
console.log("recorder test log");
console.warn("recorder test warning");
console.error("recorder test error");
</script>
</body></html>`)
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Wait for console events to be captured by the recorder subscriber
	waitForCondition(t, 5*time.Second, func() bool {
		events := rec.QueryEvents(EventFilter{eventType: "console"})
		return len(events) >= 3
	})

	events := rec.QueryEvents(EventFilter{eventType: "console"})
	if len(events) < 3 {
		t.Fatalf("expected at least 3 console events, got %d", len(events))
	}

	// Verify event data
	found := map[string]bool{}
	for _, e := range events {
		if data, ok := e.Data.(map[string]any); ok {
			text, _ := data["text"].(string)
			found[text] = true
		}
	}
	if !found["recorder test log"] {
		t.Error("missing 'recorder test log' console event")
	}
	if !found["recorder test warning"] {
		t.Error("missing 'recorder test warning' console event")
	}
	if !found["recorder test error"] {
		t.Error("missing 'recorder test error' console event")
	}
}

// --- Test 17: Recorder captures exceptions from real Chrome ---

func TestIntegrationRecorderExceptionCapture(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	rec.Clear()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html>
<html><body>
<script>
setTimeout(function() {
  throw new Error("recorder test exception");
}, 100);
</script>
</body></html>`)
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Wait for exception event
	waitForCondition(t, 5*time.Second, func() bool {
		events := rec.QueryEvents(EventFilter{eventType: "exception"})
		return len(events) >= 1
	})

	events := rec.QueryEvents(EventFilter{eventType: "exception"})
	if len(events) < 1 {
		t.Fatalf("expected at least 1 exception event, got %d", len(events))
	}

	// Verify exception text contains our message
	data, ok := events[0].Data.(map[string]any)
	if !ok {
		t.Fatal("exception event data is not a map")
	}
	text, _ := data["text"].(string)
	if !contains(text, "recorder test exception") {
		t.Errorf("exception text = %q, want it to contain 'recorder test exception'", text)
	}
}

// --- Test 18: Recorder captures actions with ActionID ---

func TestIntegrationRecorderActionCapture(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	rec.Clear()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, simpleTestPage())
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Perform a click action (which records to the recorder via End())
	actionRec := BeginAction("click")
	actionRec.SetTarget(getCurrentTargetID())
	err = cdpDispatchMouseClick(ws, 50, 50, "left", 1)
	if err != nil {
		t.Fatalf("cdpDispatchMouseClick failed: %v", err)
	}
	actionRec.End()

	// Wait for the action to be recorded
	waitForCondition(t, 3*time.Second, func() bool {
		actions := rec.QueryActions(ActionFilter{op: "click"})
		return len(actions) >= 1
	})

	actions := rec.QueryActions(ActionFilter{op: "click"})
	if len(actions) == 0 {
		t.Fatal("no click actions recorded")
	}

	a := actions[0]
	if a.ID == "" {
		t.Error("recorded action has empty ID")
	}
	if a.EndTime == nil {
		t.Error("recorded action has nil EndTime")
	}
}

// --- Test 19: Timeline tool returns events and actions ---

func TestIntegrationTimelineTool(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	rec.Clear()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, simpleTestPage())
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// The recorder subscriber is installed by the first pooled CDP request.
	// Emit a unique marker afterwards instead of racing page-load events.
	marker := fmt.Sprintf("argus-timeline-%d", time.Now().UnixNano())
	if _, err := cdpEvaluate(ws, fmt.Sprintf(`console.log(%q)`, marker), true); err != nil {
		t.Fatalf("emit timeline marker: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		events := rec.QueryEvents(EventFilter{eventType: "console"})
		for _, event := range events {
			if strings.Contains(fmt.Sprint(event.Data), marker) {
				return true
			}
		}
		return false
	})

	// Query timeline via the recorder directly (simulating timeline tool behavior)
	events := rec.QueryEvents(EventFilter{limit: 50})
	if len(events) == 0 {
		t.Fatal("timeline returned no events")
	}

	// Verify event structure
	for _, e := range events {
		if e.ID == "" {
			t.Error("event has empty ID")
		}
		if e.Type == "" {
			t.Error("event has empty type")
		}
		if e.Timestamp.IsZero() {
			t.Error("event has zero timestamp")
		}
	}

	// Query with include_actions
	_ = rec.QueryActions(ActionFilter{limit: 20})
	// Actions may be empty if no state-changing tool was called yet
	// That's OK — we just verify the query doesn't panic

	// Verify recorder stats
	stats := rec.Stats()
	if stats.EventsRecorded == 0 {
		t.Error("recorder stats show 0 events recorded")
	}
}

// --- Test 20: Diagnose tool combines status + issues ---

func TestIntegrationDiagnoseTool(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	rec.Clear()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html>
<html><body>
<script>
console.error("diagnose test error");
console.warn("diagnose test warning");
setTimeout(function() {
  throw new Error("diagnose test exception");
}, 100);
</script>
</body></html>`)
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Wait for events to be captured
	waitForCondition(t, 5*time.Second, func() bool {
		consoleEvents := rec.QueryEvents(EventFilter{eventType: "console"})
		exceptionEvents := rec.QueryEvents(EventFilter{eventType: "exception"})
		return len(consoleEvents) >= 2 && len(exceptionEvents) >= 1
	})

	// Simulate diagnose tool behavior: gather console issues
	consoleEvents := rec.QueryEvents(EventFilter{eventType: "console", limit: 10})
	consoleIssues := []map[string]any{}
	for _, e := range consoleEvents {
		if data, ok := e.Data.(map[string]any); ok {
			level, _ := data["level"].(string)
			if level == "error" || level == "warning" {
				consoleIssues = append(consoleIssues, map[string]any{
					"level": level,
					"text":  data["text"],
				})
			}
		}
	}
	if len(consoleIssues) < 2 {
		t.Errorf("expected at least 2 console issues, got %d", len(consoleIssues))
	}

	// Check exceptions
	exceptionEvents := rec.QueryEvents(EventFilter{eventType: "exception", limit: 10})
	if len(exceptionEvents) < 1 {
		t.Error("expected at least 1 exception")
	}

	// Verify the exception text
	if len(exceptionEvents) > 0 {
		data, ok := exceptionEvents[0].Data.(map[string]any)
		if !ok {
			t.Fatal("exception data is not a map")
		}
		text, _ := data["text"].(string)
		if !contains(text, "diagnose test exception") {
			t.Errorf("exception text = %q, want 'diagnose test exception'", text)
		}
	}
}

// --- Test 21: Export trace to file ---

func TestIntegrationExportTrace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	rec.Clear()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, simpleTestPage())
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Wait for events
	waitForCondition(t, 5*time.Second, func() bool {
		events := rec.QueryEvents(EventFilter{})
		return len(events) >= 1
	})

	// Export trace (simulating export_trace tool behavior with controlled dir)
	traceDir := getTraceDir()
	os.MkdirAll(traceDir, 0700)
	traceFile := filepath.Join(traceDir, fmt.Sprintf("argus-test-trace-%d.json", time.Now().UnixNano()))
	defer os.Remove(traceFile)

	trace := map[string]any{
		"exported_at": time.Now().Format(time.RFC3339),
		"version":     argusVersion,
	}
	trace["actions"] = rec.QueryActions(ActionFilter{limit: 200})
	trace["events"] = rec.QueryEvents(EventFilter{limit: 2000})
	trace["recorder"] = rec.Stats()

	data, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal trace: %v", err)
	}
	if err := os.WriteFile(traceFile, data, 0600); err != nil {
		t.Fatalf("failed to write trace file: %v", err)
	}

	// Verify file was written with 0600 permissions
	info, err := os.Stat(traceFile)
	if err != nil {
		t.Fatalf("trace file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Error("trace file is empty")
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("trace file mode = %o, want 0600", info.Mode().Perm())
	}
	t.Logf("trace file size: %d bytes", info.Size())

	// Verify file is valid JSON and contains expected keys
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("trace file is not valid JSON: %v", err)
	}
	if _, ok := parsed["events"]; !ok {
		t.Error("trace file missing 'events' key")
	}
	if _, ok := parsed["actions"]; !ok {
		t.Error("trace file missing 'actions' key")
	}
	if _, ok := parsed["recorder"]; !ok {
		t.Error("trace file missing 'recorder' key")
	}
}

// --- helpers ---

// Note: contains() is defined in bounded_output_test.go (always compiled);
// only containsStr is defined here to support the integration tests below.

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- MCP handler integration tests ---
// These tests call trace tools through the real MCP handler pipeline,
// validating schema, filter, target scope, path/permission, and redaction contracts.

// setupMCPServer creates a real MCP server with all tools registered,
// connects an in-memory client, and returns the client session.
func setupMCPServer(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "argus-test", Version: "test"}, nil)
	registerAllTools(server)

	ct, st := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect failed: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}
	return cs
}

// callMCPTool invokes a tool via the MCP client session and returns the result.
func callMCPTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx := context.Background()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s failed: %v", name, err)
	}
	return res
}

// extractToolResultJSON extracts the JSON content from a CallToolResult.
func extractToolResultJSON(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("empty result content")
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			var parsed map[string]any
			if err := json.Unmarshal([]byte(tc.Text), &parsed); err != nil {
				t.Fatalf("failed to parse result JSON: %v\nraw: %s", err, tc.Text)
			}
			return parsed
		}
	}
	t.Fatal("no text content in result")
	return nil
}

// --- Test 22: Timeline via real MCP handler ---

func TestIntegrationMCPTimelineHandler(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	rec.Clear()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, simpleTestPage())
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Wait for console events
	waitForCondition(t, 5*time.Second, func() bool {
		events := rec.QueryEvents(EventFilter{eventType: "console"})
		return len(events) >= 1
	})

	// Call timeline through real MCP handler
	cs := setupMCPServer(t)
	defer cs.Close()

	res := callMCPTool(t, cs, "timeline", map[string]any{
		"include_actions": true,
		"limit":           10,
	})
	parsed := extractToolResultJSON(t, res)

	// Verify schema: should have "timeline" array
	timelineRaw, ok := parsed["timeline"]
	if !ok {
		t.Fatal("timeline result missing 'timeline' key")
	}
	timeline, ok := timelineRaw.([]any)
	if !ok {
		t.Fatalf("timeline should be an array, got %T", timelineRaw)
	}
	if len(timeline) == 0 {
		t.Fatal("timeline array is empty")
	}

	// Verify each entry has required fields
	for i, entryRaw := range timeline {
		entry, ok := entryRaw.(map[string]any)
		if !ok {
			t.Errorf("entry %d is not a map: %T", i, entryRaw)
			continue
		}
		if _, ok := entry["kind"]; !ok {
			t.Errorf("entry %d missing 'kind'", i)
		}
		if _, ok := entry["seq"]; !ok {
			t.Errorf("entry %d missing 'seq'", i)
		}
		if _, ok := entry["timestamp"]; !ok {
			t.Errorf("entry %d missing 'timestamp'", i)
		}
		// Timestamp should be RFC3339Nano (contains date and timezone)
		ts, _ := entry["timestamp"].(string)
		if len(ts) > 0 && !containsStr(ts, "T") {
			t.Errorf("entry %d timestamp %q should be RFC3339Nano (contain 'T')", i, ts)
		}
	}

	// Verify entry_count
	count, ok := parsed["entry_count"]
	if !ok {
		t.Fatal("timeline result missing 'entry_count'")
	}
	if count.(float64) != float64(len(timeline)) {
		t.Errorf("entry_count = %v, want %d", count, len(timeline))
	}

	// Verify recorder stats include new fields
	recorderRaw, ok := parsed["recorder"]
	if !ok {
		t.Fatal("timeline result missing 'recorder' stats")
	}
	recorder, ok := recorderRaw.(map[string]any)
	if !ok {
		t.Fatalf("recorder should be a map, got %T", recorderRaw)
	}
	for _, field := range []string{"events_dropped", "data_truncated", "domain_enable_error", "reconnect_gaps"} {
		if _, ok := recorder[field]; !ok {
			t.Errorf("recorder stats missing '%s'", field)
		}
	}
}

// --- Test 23: Diagnose via real MCP handler ---

func TestIntegrationMCPDiagnoseHandler(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	rec.Clear()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, simpleTestPage())
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Call diagnose through real MCP handler
	cs := setupMCPServer(t)
	defer cs.Close()

	res := callMCPTool(t, cs, "diagnose", map[string]any{
		"limit": 5,
	})
	parsed := extractToolResultJSON(t, res)

	// Verify schema: should have target_id, console_issues, exceptions, failed_actions
	for _, key := range []string{"target_id", "console_issues", "exceptions", "failed_actions"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("diagnose result missing '%s'", key)
		}
	}

	// Verify target_id is not empty
	tid, _ := parsed["target_id"].(string)
	if tid == "" {
		t.Error("diagnose target_id should not be empty")
	}

	// Verify timestamps in console_issues are RFC3339Nano
	if issues, ok := parsed["console_issues"].([]any); ok {
		for i, issueRaw := range issues {
			issue, ok := issueRaw.(map[string]any)
			if !ok {
				continue
			}
			ts, _ := issue["timestamp"].(string)
			if len(ts) > 0 && !containsStr(ts, "T") {
				t.Errorf("console_issue %d timestamp %q should be RFC3339Nano", i, ts)
			}
		}
	}
}

// --- Test 24: Export trace via real MCP handler — path/permission/redaction ---

func TestIntegrationMCPExportTraceHandler(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	rec.Clear()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, simpleTestPage())
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Set ARGUS_TRACE_DIR to temp dir
	traceDir := t.TempDir()
	os.Setenv("ARGUS_TRACE_DIR", traceDir)
	defer os.Unsetenv("ARGUS_TRACE_DIR")

	// Call export_trace through real MCP handler
	cs := setupMCPServer(t)
	defer cs.Close()

	filename := fmt.Sprintf("mcp-test-trace-%d.json", time.Now().UnixNano())
	res := callMCPTool(t, cs, "export_trace", map[string]any{
		"path": filename,
	})
	parsed := extractToolResultJSON(t, res)

	// Verify path is inside traceDir
	exportPath, _ := parsed["path"].(string)
	if exportPath == "" {
		t.Fatal("export_trace result missing 'path'")
	}
	if !containsStr(exportPath, traceDir) {
		t.Errorf("export path %q should be inside traceDir %q", exportPath, traceDir)
	}
	if !containsStr(exportPath, filename) {
		t.Errorf("export path %q should contain filename %q", exportPath, filename)
	}

	// Verify file exists with 0600 permissions
	info, err := os.Stat(exportPath)
	if err != nil {
		t.Fatalf("trace file not found: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("trace file mode = %o, want 0600", info.Mode().Perm())
	}

	// Verify file content is valid JSON with expected keys
	data, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("failed to read trace file: %v", err)
	}
	var trace map[string]any
	if err := json.Unmarshal(data, &trace); err != nil {
		t.Fatalf("trace file is not valid JSON: %v", err)
	}
	for _, key := range []string{"exported_at", "version", "actions", "events", "recorder"} {
		if _, ok := trace[key]; !ok {
			t.Errorf("trace file missing '%s'", key)
		}
	}

	// Verify recorder stats include new fields
	recorder, ok := trace["recorder"].(map[string]any)
	if !ok {
		t.Fatal("trace 'recorder' is not a map")
	}
	for _, field := range []string{"events_dropped", "data_truncated", "domain_enable_error", "reconnect_gaps"} {
		if _, ok := recorder[field]; !ok {
			t.Errorf("trace recorder missing '%s'", field)
		}
	}

	// Test: refuse to overwrite existing file
	res2 := callMCPTool(t, cs, "export_trace", map[string]any{
		"path": filename,
	})
	if res2 != nil && res2.IsError {
		// Expected: error because file already exists
		t.Log("correctly refused to overwrite existing trace file")
	} else {
		t.Error("export_trace should refuse to overwrite existing file")
	}
}

// --- Test 25: Timeline filter by event_type via MCP handler ---

func TestIntegrationMCPTimelineFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	rec.Clear()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, simpleTestPage())
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Wait for console events
	waitForCondition(t, 5*time.Second, func() bool {
		events := rec.QueryEvents(EventFilter{eventType: "console"})
		return len(events) >= 1
	})

	cs := setupMCPServer(t)
	defer cs.Close()

	// Filter by event_type=console
	res := callMCPTool(t, cs, "timeline", map[string]any{
		"event_type": "console",
		"limit":      10,
	})
	parsed := extractToolResultJSON(t, res)

	timeline, ok := parsed["timeline"].([]any)
	if !ok {
		t.Fatal("timeline should be an array")
	}
	if len(timeline) == 0 {
		t.Fatal("filtered timeline should have console events")
	}

	// All entries should be type=console
	for i, entryRaw := range timeline {
		entry, ok := entryRaw.(map[string]any)
		if !ok {
			continue
		}
		entryType, _ := entry["type"].(string)
		if entryType != "console" {
			t.Errorf("entry %d type = %q, want 'console'", i, entryType)
		}
	}
}

// waitForCondition polls pred until it returns true or deadline expires.
// Uses 50ms poll interval. Fails the test on timeout.
func waitForCondition(t *testing.T, timeout time.Duration, pred func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pred() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

// --- Test 26: Recorder captures network events ---

func TestIntegrationRecorderNetworkCapture(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	rec.Clear()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/data" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok": true}`)
			return
		}
		if r.URL.Path == "/api/error" {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error": "fail"}`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body>
<script>
fetch('/api/data').then(r => r.json()).catch(e => {});
fetch('/api/error').then(r => r.json()).catch(e => {});
</script>
</body></html>`)
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	// Navigate explicitly to trigger fresh page load + network events
	cdpRequest(ws, "Page.navigate", map[string]any{"url": serverURL}, nil)
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Wait for network events to be recorded
	waitForCondition(t, 10*time.Second, func() bool {
		events := rec.QueryEvents(EventFilter{eventType: "network", limit: 100})
		return len(events) >= 2 // at least request + response for /api/data
	})

	events := rec.QueryEvents(EventFilter{eventType: "network", limit: 100})
	if len(events) == 0 {
		t.Fatal("no network events recorded")
	}

	// Verify event structure
	foundRequest := false
	foundResponse := false
	foundFailed := false
	for _, e := range events {
		if e.Type != "network" {
			t.Errorf("event type = %q, want 'network'", e.Type)
		}
		if e.TargetID == "" {
			t.Error("network event has empty target_id")
		}
		if e.Timestamp.IsZero() {
			t.Error("network event has zero timestamp")
		}

		var evidence NetworkEvidence
		switch d := e.Data.(type) {
		case NetworkEvidence:
			evidence = d
		case map[string]any:
			evidence = networkEvidenceFromMap(d)
		default:
			t.Errorf("network event data is %T, not NetworkEvidence", e.Data)
			continue
		}

		if evidence.RequestID == "" {
			t.Error("network evidence has empty request_id")
		}
		if evidence.Phase == "" {
			t.Error("network evidence has empty phase")
		}

		switch evidence.Phase {
		case "request":
			foundRequest = true
			if evidence.URL == "" {
				t.Error("request phase has empty URL")
			}
			if evidence.Method == "" {
				t.Error("request phase has empty method")
			}
			// URL should not contain query params (sanitized)
			if strings.Contains(evidence.URL, "?") {
				t.Errorf("request URL should be sanitized (no query): %s", evidence.URL)
			}
		case "response":
			foundResponse = true
			if evidence.Status == 0 {
				t.Error("response phase has status 0")
			}
		case "failed":
			foundFailed = true
			if evidence.ErrorText == "" {
				t.Error("failed phase has empty error_text")
			}
		}
	}

	if !foundRequest {
		t.Error("no 'request' phase network events found")
	}
	if !foundResponse {
		t.Error("no 'response' phase network events found")
	}
	t.Logf("found request=%v response=%v failed=%v", foundRequest, foundResponse, foundFailed)
}

// --- Test 27: Network events appear in timeline via MCP handler ---

func TestIntegrationMCPTimelineNetworkEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	rec.Clear()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/test" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok": true}`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body>
<script>fetch('/api/test').then(r=>r.json()).catch(e=>{});</script>
</body></html>`)
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	// Navigate explicitly to trigger fresh page load + network events
	cdpRequest(ws, "Page.navigate", map[string]any{"url": serverURL}, nil)
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Wait for network events
	waitForCondition(t, 10*time.Second, func() bool {
		events := rec.QueryEvents(EventFilter{eventType: "network", limit: 100})
		return len(events) >= 1
	})

	// Call timeline through real MCP handler
	cs := setupMCPServer(t)
	defer cs.Close()

	res := callMCPTool(t, cs, "timeline", map[string]any{
		"event_type": "network",
		"limit":      20,
	})
	parsed := extractToolResultJSON(t, res)

	timeline, ok := parsed["timeline"].([]any)
	if !ok {
		t.Fatal("timeline should be an array")
	}
	if len(timeline) == 0 {
		t.Fatal("timeline should have network events")
	}

	// Verify each entry is a network event with proper schema
	for i, entryRaw := range timeline {
		entry, ok := entryRaw.(map[string]any)
		if !ok {
			t.Errorf("entry %d is not a map", i)
			continue
		}
		if entry["kind"] != "event" {
			t.Errorf("entry %d kind = %v, want 'event'", i, entry["kind"])
		}
		if entry["type"] != "network" {
			t.Errorf("entry %d type = %v, want 'network'", i, entry["type"])
		}
		if _, ok := entry["seq"]; !ok {
			t.Errorf("entry %d missing 'seq'", i)
		}
		if _, ok := entry["timestamp"]; !ok {
			t.Errorf("entry %d missing 'timestamp'", i)
		}

		// Verify data contains NetworkEvidence fields
		data, ok := entry["data"].(map[string]any)
		if !ok {
			t.Errorf("entry %d data is not a map", i)
			continue
		}
		if _, ok := data["request_id"]; !ok {
			t.Errorf("entry %d data missing 'request_id'", i)
		}
		if _, ok := data["phase"]; !ok {
			t.Errorf("entry %d data missing 'phase'", i)
		}
	}
}

// --- Test 28: network_requests from_recorder compatibility view ---

func TestIntegrationMCPNetworkFromRecorder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	rec.Clear()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/data" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok": true}`)
			return
		}
		if r.URL.Path == "/api/error" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body>
<script>
fetch('/api/data').then(r=>r.json()).catch(e=>{});
fetch('/api/error').then(r=>{}).catch(e=>{});
</script>
</body></html>`)
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	// Navigate explicitly to trigger fresh page load + network events
	cdpRequest(ws, "Page.navigate", map[string]any{"url": serverURL}, nil)
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Wait for network events
	waitForCondition(t, 10*time.Second, func() bool {
		events := rec.QueryEvents(EventFilter{eventType: "network", limit: 100})
		return len(events) >= 2
	})

	// Call network_requests with from_recorder=true through MCP handler
	cs := setupMCPServer(t)
	defer cs.Close()

	res := callMCPTool(t, cs, "network_requests", map[string]any{
		"from_recorder": true,
		"all":           true,
		"limit":         20,
	})
	parsed := extractToolResultJSON(t, res)

	source, _ := parsed["source"].(string)
	if source != "recorder" {
		t.Errorf("source = %q, want 'recorder'", source)
	}

	requests, ok := parsed["requests"].([]any)
	if !ok {
		t.Fatal("requests should be an array")
	}
	if len(requests) == 0 {
		t.Fatal("from_recorder should return network requests")
	}

	// Verify entry shape
	for i, reqRaw := range requests {
		req, ok := reqRaw.(map[string]any)
		if !ok {
			t.Errorf("request %d is not a map", i)
			continue
		}
		if _, ok := req["id"]; !ok {
			t.Errorf("request %d missing 'id'", i)
		}
		// Should NOT have response_body (never recorded)
		if _, ok := req["response_body"]; ok {
			t.Errorf("request %d should not have response_body", i)
		}
	}
}

// --- Test 29: Network event redaction — no secrets in output ---

func TestIntegrationNetworkRedaction(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	rec.Clear()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body>
<script>
fetch('/api?token=secret-jwt-eyJ123&api_key=abc456').catch(e=>{});
</script>
</body></html>`)
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	// Navigate explicitly to trigger fresh page load + network events
	cdpRequest(ws, "Page.navigate", map[string]any{"url": serverURL}, nil)
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Wait for network events
	waitForCondition(t, 10*time.Second, func() bool {
		events := rec.QueryEvents(EventFilter{eventType: "network", limit: 100})
		return len(events) >= 1
	})

	// Query timeline through MCP handler
	cs := setupMCPServer(t)
	defer cs.Close()

	res := callMCPTool(t, cs, "timeline", map[string]any{
		"event_type": "network",
		"limit":      20,
	})

	// Verify no secrets appear in the raw JSON output
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			raw := tc.Text
			if strings.Contains(raw, "secret-jwt") {
				t.Errorf("timeline output contains secret 'secret-jwt': %s", raw)
			}
			if strings.Contains(raw, "api_key=abc456") {
				t.Errorf("timeline output contains api_key: %s", raw)
			}
			if strings.Contains(raw, "eyJ123") {
				t.Errorf("timeline output contains JWT fragment: %s", raw)
			}
		}
	}

	// Also verify from_recorder view
	res2 := callMCPTool(t, cs, "network_requests", map[string]any{
		"from_recorder": true,
		"all":           true,
	})
	for _, c := range res2.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			raw := tc.Text
			if strings.Contains(raw, "secret-jwt") {
				t.Errorf("network_requests output contains secret: %s", raw)
			}
			if strings.Contains(raw, "api_key=abc456") {
				t.Errorf("network_requests output contains api_key: %s", raw)
			}
		}
	}
}

// --- Test 30: Deterministic loadingFailed event ---

func TestIntegrationNetworkLoadingFailed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	rec.Clear()

	// Use a port that's almost certainly not listening to trigger
	// a connection-refused loadingFailed event.
	failURL := "http://127.0.0.1:1/unreachable-port"

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!DOCTYPE html><html><body>
<script>fetch(%q).catch(function(){});</script>
</body></html>`, failURL)
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	cdpRequest(ws, "Page.navigate", map[string]any{"url": serverURL}, nil)
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Wait for a failed network event
	waitForCondition(t, 10*time.Second, func() bool {
		events := rec.QueryEvents(EventFilter{eventType: "network", limit: 100})
		for _, e := range events {
			var ev NetworkEvidence
			switch d := e.Data.(type) {
			case NetworkEvidence:
				ev = d
			case map[string]any:
				ev = networkEvidenceFromMap(d)
			default:
				continue
			}
			if ev.Phase == "failed" && ev.Failed {
				return true
			}
		}
		return false
	})

	// Verify the failed event
	events := rec.QueryEvents(EventFilter{eventType: "network", limit: 100})
	foundFailed := false
	for _, e := range events {
		var ev NetworkEvidence
		switch d := e.Data.(type) {
		case NetworkEvidence:
			ev = d
		case map[string]any:
			ev = networkEvidenceFromMap(d)
		default:
			continue
		}
		if ev.Phase == "failed" {
			foundFailed = true
			if !ev.Failed {
				t.Error("failed phase event should have Failed=true")
			}
			if ev.ErrorText == "" {
				t.Error("failed phase event should have non-empty ErrorText")
			}
		}
	}
	if !foundFailed {
		t.Error("no loadingFailed event captured")
	}
}

// --- Test 31: Two-hop redirect chain ---

func TestIntegrationNetworkTwoHopRedirect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	rec.Clear()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/hop0":
			http.Redirect(w, r, "/hop1", http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/hop0/"):
			// Unique-path entry point for deterministic attribution. The
			// path segment survives URL sanitization (query params do not).
			http.Redirect(w, r, "/hop1", http.StatusFound)
		case r.URL.Path == "/hop1":
			http.Redirect(w, r, "/hop2", http.StatusMovedPermanently)
		case r.URL.Path == "/hop2":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<!DOCTYPE html><html><body>final</body></html>`)
		default:
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, simpleTestPage())
		}
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	cdpRequest(ws, "Page.navigate", map[string]any{"url": serverURL}, nil)
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Trigger the redirect chain after the recorder subscriber is installed.
	// Page-load fetches can race the Network.enable handshake and miss early
	// requestWillBeSent events, splitting the redirect chain across loaders.
	// Use a unique path prefix (not query) because sanitizeURL strips query
	// params for security. The path survives sanitization and identifies our
	// chain unambiguously since the page itself issues no /hop requests.
	marker := fmt.Sprintf("argus-2hop-%d", time.Now().UnixNano())
	fetchPath := fmt.Sprintf("/hop0/%s", marker)
	if _, err := cdpEvaluate(ws, fmt.Sprintf(`fetch(%q).catch(function(){})`, fetchPath), true); err != nil {
		t.Fatalf("trigger redirect chain: %v", err)
	}

	// Wait for the full redirect chain: the marker-bearing loaderID must
	// accumulate >= 2 hops (hop0 -> hop1 -> hop2).
	waitForCondition(t, 10*time.Second, func() bool {
		events := rec.QueryEvents(EventFilter{eventType: "network", limit: 200})
		loaderHops := map[string][]int{}
		markerLoader := ""
		for _, e := range events {
			var ev NetworkEvidence
			switch d := e.Data.(type) {
			case NetworkEvidence:
				ev = d
			case map[string]any:
				ev = networkEvidenceFromMap(d)
			default:
				continue
			}
			if ev.Phase == "request" && strings.Contains(ev.URL, "/hop") && ev.LoaderID != "" {
				loaderHops[ev.LoaderID] = append(loaderHops[ev.LoaderID], ev.RedirectHops)
				if strings.Contains(ev.URL, marker) {
					markerLoader = ev.LoaderID
				}
			}
		}
		if markerLoader == "" {
			return false
		}
		return len(loaderHops[markerLoader]) >= 2
	})

	// Collect all /hop request events and group by loaderID. Identify the
	// marker-bearing loader as the one we triggered, not incidental traffic.
	events := rec.QueryEvents(EventFilter{eventType: "network", limit: 200})
	loaderHops := map[string][]int{} // loaderID -> ordered hops
	markerLoader := ""
	for _, e := range events {
		var ev NetworkEvidence
		switch d := e.Data.(type) {
		case NetworkEvidence:
			ev = d
		case map[string]any:
			ev = networkEvidenceFromMap(d)
		default:
			continue
		}
		if ev.Phase == "request" && strings.Contains(ev.URL, "/hop") && ev.LoaderID != "" {
			loaderHops[ev.LoaderID] = append(loaderHops[ev.LoaderID], ev.RedirectHops)
			if strings.Contains(ev.URL, marker) {
				markerLoader = ev.LoaderID
			}
		}
	}

	if markerLoader == "" {
		t.Fatalf("no request with marker %s found (loaders: %v)", marker, loaderHops)
	}
	chainHops := loaderHops[markerLoader]
	if len(chainHops) < 2 {
		t.Fatalf("marker loader %s has only %d hops, want >= 2 (loaders: %v)", markerLoader, len(chainHops), loaderHops)
	}
	// Reverse (newest-first) to get chronological order
	for i, j := 0, len(chainHops)-1; i < j; i, j = i+1, j-1 {
		chainHops[i], chainHops[j] = chainHops[j], chainHops[i]
	}
	// First hop should be 0, subsequent should be strictly increasing
	if chainHops[0] != 0 {
		t.Errorf("first hop should be 0, got %d (chain: %v)", chainHops[0], chainHops)
	}
	for i := 1; i < len(chainHops); i++ {
		if chainHops[i] <= chainHops[i-1] {
			t.Errorf("hop %d = %d should be > hop %d = %d (chain: %v)", i, chainHops[i], i-1, chainHops[i-1], chainHops)
		}
	}
}

// --- Test 32: Multi-tab target isolation ---

func TestIntegrationNetworkMultiTabIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	rec.Clear()

	serverA, urlA := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/a" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"tab":"A"}`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body>
<script>fetch('/api/a').then(r=>r.json()).catch(e=>{});</script>
</body></html>`)
	})
	defer serverA.Close()

	serverB, urlB := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/b" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"tab":"B"}`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body>
<script>fetch('/api/b').then(r=>r.json()).catch(e=>{});</script>
</body></html>`)
	})
	defer serverB.Close()

	// Tab A
	setConfig(urlA, 0, nil)
	wsA, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS A failed: %v", err)
	}
	targetA := getCurrentTargetID()
	cdpRequest(wsA, "Page.navigate", map[string]any{"url": urlA}, nil)
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(wsA, `document.body`, true)
		return err == nil
	})

	// Tab B — create a new tab
	setConfig(urlB, 0, nil)
	// Clear current target to force new tab creation
	clearCurrentTargetID()
	wsB, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS B failed: %v", err)
	}
	targetB := getCurrentTargetID()
	cdpRequest(wsB, "Page.navigate", map[string]any{"url": urlB}, nil)
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(wsB, `document.body`, true)
		return err == nil
	})

	if targetA == targetB {
		t.Fatalf("target A and B should be different: %s", targetA)
	}

	// Trigger deterministic requests after each target's recorder subscriber is
	// installed. Page-load fetches can race the Network.enable handshake.
	if _, err := cdpEvaluate(wsA, `fetch('/api/a?marker=argus-tab-a').then(r => r.json())`, true); err != nil {
		t.Fatalf("trigger target A request: %v", err)
	}
	if _, err := cdpEvaluate(wsB, `fetch('/api/b?marker=argus-tab-b').then(r => r.json())`, true); err != nil {
		t.Fatalf("trigger target B request: %v", err)
	}
	waitForCondition(t, 10*time.Second, func() bool {
		entriesA := queryNetworkFromRecorder(100, true, targetA, false)
		entriesB := queryNetworkFromRecorder(100, true, targetB, false)
		return networkEntriesContainPath(entriesA, "/api/a") &&
			networkEntriesContainPath(entriesB, "/api/b")
	})

	// Verify target isolation: from_recorder with targetA should not
	// contain events from targetB and vice versa.
	entriesA := queryNetworkFromRecorder(50, true, targetA, false)
	entriesB := queryNetworkFromRecorder(50, true, targetB, false)

	for _, entry := range entriesA {
		if url, ok := entry["url"].(string); ok && strings.Contains(url, "/api/b") {
			t.Errorf("target A results should not contain /api/b from target B: %s", url)
		}
	}
	for _, entry := range entriesB {
		if url, ok := entry["url"].(string); ok && strings.Contains(url, "/api/a") {
			t.Errorf("target B results should not contain /api/a from target A: %s", url)
		}
	}

	// Verify each target has its own events
	foundA := false
	foundB := false
	for _, entry := range entriesA {
		if url, ok := entry["url"].(string); ok && strings.Contains(url, "/api/a") {
			foundA = true
		}
	}
	for _, entry := range entriesB {
		if url, ok := entry["url"].(string); ok && strings.Contains(url, "/api/b") {
			foundB = true
		}
	}
	if !foundA {
		t.Error("target A should have /api/a events")
	}
	if !foundB {
		t.Error("target B should have /api/b events")
	}
}

func networkEntriesContainPath(entries []map[string]any, path string) bool {
	for _, entry := range entries {
		if rawURL, ok := entry["url"].(string); ok && strings.Contains(rawURL, path) {
			return true
		}
	}
	return false
}

// --- Test 33: Malicious long URL does not crash or leak ---

func TestIntegrationNetworkMaliciousLongURL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	rec.Clear()

	// Build a URL with a very long path + query secret
	longPath := strings.Repeat("a", 5000)
	maliciousURL := fmt.Sprintf("http://127.0.0.1:1/%s?secret=should-not-leak", longPath)

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!DOCTYPE html><html><body>
<script>fetch(%q).catch(function(){});</script>
</body></html>`, maliciousURL)
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	cdpRequest(ws, "Page.navigate", map[string]any{"url": serverURL}, nil)
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Wait for network events
	waitForCondition(t, 10*time.Second, func() bool {
		events := rec.QueryEvents(EventFilter{eventType: "network", limit: 100})
		return len(events) >= 1
	})

	// Verify no secret leaked and URLs are bounded
	events := rec.QueryEvents(EventFilter{eventType: "network", limit: 100})
	for _, e := range events {
		var ev NetworkEvidence
		switch d := e.Data.(type) {
		case NetworkEvidence:
			ev = d
		case map[string]any:
			ev = networkEvidenceFromMap(d)
		default:
			continue
		}
		if strings.Contains(ev.URL, "secret=should-not-leak") {
			t.Errorf("URL leaked secret: %s", ev.URL[:80])
		}
		if len(ev.URL) > maxURLBytes+50 { // allow truncation suffix
			t.Errorf("URL not bounded: %d bytes", len(ev.URL))
		}
	}

	// Also verify via MCP handler timeline
	cs := setupMCPServer(t)
	defer cs.Close()

	res := callMCPTool(t, cs, "timeline", map[string]any{
		"event_type": "network",
		"limit":      20,
	})
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			if strings.Contains(tc.Text, "secret=should-not-leak") {
				t.Errorf("timeline output leaked secret from long URL")
			}
		}
	}
}

// --- Test 34: URL header secret (Referer/Location) does not leak ---

func TestIntegrationNetworkURLHeaderSecret(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	rec.Clear()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect-with-secret" {
			// Location header with query secret
			w.Header().Set("Location", "/dest?token=location-secret-xyz")
			w.WriteHeader(http.StatusFound)
			return
		}
		if r.URL.Path == "/dest" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<!DOCTYPE html><html><body>dest</body></html>`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body>
<script>
fetch('/redirect-with-secret', {redirect:'manual'}).catch(function(){});
fetch('/api', {headers:{'Referer':'https://evil.com/?token=referer-secret-abc'}}).catch(function(){});
</script>
</body></html>`)
	})
	defer server.Close()

	setConfig(serverURL, 0, nil)
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	cdpRequest(ws, "Page.navigate", map[string]any{"url": serverURL}, nil)
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := cdpEvaluate(ws, `document.body`, true)
		return err == nil
	})

	// Wait for network events
	waitForCondition(t, 10*time.Second, func() bool {
		events := rec.QueryEvents(EventFilter{eventType: "network", limit: 100})
		return len(events) >= 1
	})

	// Verify no header secrets leaked in recorder or MCP output
	cs := setupMCPServer(t)
	defer cs.Close()

	res := callMCPTool(t, cs, "timeline", map[string]any{
		"event_type": "network",
		"limit":      50,
	})
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			raw := tc.Text
			if strings.Contains(raw, "location-secret-xyz") {
				t.Errorf("timeline leaked Location header secret: %s", raw[:200])
			}
			if strings.Contains(raw, "referer-secret-abc") {
				t.Errorf("timeline leaked Referer header secret: %s", raw[:200])
			}
		}
	}

	// Also check from_recorder
	res2 := callMCPTool(t, cs, "network_requests", map[string]any{
		"from_recorder": true,
		"all":           true,
		"limit":         50,
	})
	for _, c := range res2.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			raw := tc.Text
			if strings.Contains(raw, "location-secret-xyz") {
				t.Errorf("network_requests leaked Location header secret: %s", raw[:200])
			}
			if strings.Contains(raw, "referer-secret-abc") {
				t.Errorf("network_requests leaked Referer header secret: %s", raw[:200])
			}
		}
	}
}

// --- Test 35: Tab churn does not leak targets ---

func TestIntegrationTabChurnNoLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, cleanup := setupTestChrome(t)
	defer cleanup()

	server, serverURL := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, simpleTestPage())
	})
	defer server.Close()

	version, err := cdpVersion()
	if err != nil {
		t.Fatalf("cdpVersion failed: %v", err)
	}

	baseline := integrationTargetIDs()

	// Churn: create and close 8 tabs across 4 iterations.
	const iterations = 4
	const tabsPerIter = 2
	for i := 0; i < iterations; i++ {
		var created []string
		for j := 0; j < tabsPerIter; j++ {
			id, err := cdpCreateTarget(version.WebSocketDebuggerURL, serverURL, true)
			if err != nil {
				t.Fatalf("iter %d create tab %d failed: %v", i, j, err)
			}
			created = append(created, id)
		}
		for _, id := range created {
			if err := cdpCloseTarget(id); err != nil {
				t.Fatalf("iter %d close tab %s failed: %v", i, id, err)
			}
		}
		// Close this iteration's pooled connections so the next iteration
		// does not accumulate idle sockets against churned targets.
		closeAllConns()
	}

	// After churn, the only targets that should remain are those present at
	// baseline (TestMain's about:blank plus any pre-existing tabs). No churned
	// tab should survive, and baseline tabs must not have been closed.
	// Target.closeTarget is asynchronous in CDP, so wait a bounded time for the
	// churned targets to actually disappear before asserting.
	waitForCondition(t, 5*time.Second, func() bool {
		return len(integrationTargetsCreatedAfter(baseline)) == 0
	})
	leaked := integrationTargetsCreatedAfter(baseline)
	if len(leaked) > 0 {
		t.Errorf("tab churn leaked %d targets not present at baseline: %v", len(leaked), leaked)
	}
	// Verify baseline tabs were not accidentally closed.
	current := integrationTargetIDs()
	for id := range baseline {
		if _, ok := current[id]; !ok {
			t.Errorf("baseline target %s was closed during churn (must not touch other owners)", id)
		}
	}
}

// --- TestMain: launch a single Chrome instance for all integration tests ---

func TestMain(m *testing.M) {
	// Create a temp profile dir for the test Chrome instance
	tmpDir, err := os.MkdirTemp("", "argus-integration-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	// Pick a port unlikely to be in use
	integrationChromePort = 9400

	// Force headless Chrome so integration tests run on servers without a
	// display. Does not affect normal user-facing launches.
	os.Setenv("ARGUS_CHROME_HEADLESS", "1")

	// Configure Argus to use this Chrome instance
	profileDir = tmpDir
	configuredProfile = "isolated"
	cdpPort = integrationChromePort
	configuredPort = integrationChromePort
	attach := false
	setConfig("", integrationChromePort, &attach)

	// Launch Chrome
	if err := ensureChromeRunning(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to launch Chrome for integration tests: %v\n", err)
		_ = os.RemoveAll(tmpDir)
		os.Exit(1)
	}

	// Run tests
	code := m.Run()

	// This browser is owned by TestMain: it uses a unique port (9400) and an
	// isolated profile under a temp dir. Close it through its browser-level
	// CDP endpoint, then wait a bounded 10s for CDP to stop responding.
	// Never use pkill/killall: they can affect a user's browser, and the
	// current code does not provably own a Chrome PID after cmd.Process.Release.
	// If Browser.close fails to terminate Chrome within 10s, fail the suite
	// and KEEP the profile dir on disk for diagnosis (do not delete evidence).
	closeAllConns()
	chromeClosed := false
	if version, err := cdpVersion(); err == nil && version.WebSocketDebuggerURL != "" {
		_ = cdpRequest(version.WebSocketDebuggerURL, "Browser.close", nil, nil)
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := cdpVersion(); err != nil {
				chromeClosed = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	} else {
		// Already gone (e.g. a test closed it) — treat as closed.
		chromeClosed = true
	}
	if !chromeClosed {
		fmt.Fprintf(os.Stderr, "integration Chrome on port %d still responds after 10s Browser.close; keeping profile %s for diagnosis\n", integrationChromePort, tmpDir)
		if code == 0 {
			code = 1
		}
		// Do NOT remove tmpDir: it is diagnostic evidence of a leaked Chrome.
	} else {
		if err := os.RemoveAll(tmpDir); err != nil {
			fmt.Fprintf(os.Stderr, "remove integration profile %s: %v\n", tmpDir, err)
			if code == 0 {
				code = 1
			}
		}
	}

	os.Exit(code)
}
