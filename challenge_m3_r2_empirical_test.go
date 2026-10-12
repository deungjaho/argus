package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ============================================================================
// PART 1: MOCK ROUTE CONCURRENCY & LOCK STRESS
// ============================================================================

// TestChallenger_M3_R2_RemoveInterceptState_HeavyConcurrentRequests_NoDeadlock
// Empirically stress-tests that releasing s.mu.Unlock() before calling stopInterceptListener
// completely eliminates the 2-second timeout stall/deadlock when concurrent requestPaused
// handlers are actively acquiring s.mu.RLock().
func TestChallenger_M3_R2_RemoveInterceptState_HeavyConcurrentRequests_NoDeadlock(t *testing.T) {
	wsURL := "ws://127.0.0.1:59999/devtools/page/MOCK_ROUTE_STRESS_TARGET"

	state := getInterceptState(wsURL)
	re1, _, _ := compileRoutePattern("*/api/v1/*")
	re2, _, _ := compileRoutePattern("*/api/v2/*")

	state.mu.Lock()
	state.rules = []interceptRule{
		{URLPattern: "*/api/v1/*", Method: "GET", Action: "mock", Status: 200, Body: `{"v":1}`, matcher: re1},
		{URLPattern: "*/api/v2/*", Method: "POST", Action: "block", matcher: re2},
	}
	state.active = true
	state.stopCh = make(chan struct{})
	state.listenerDone = make(chan struct{})
	state.mu.Unlock()

	stopTraffic := make(chan struct{})
	var inFlightRequests int64
	var completedRequests int64

	// 50 concurrent reader goroutines continuously pumping requests that hold RLock
	var readersWg sync.WaitGroup
	for i := 0; i < 50; i++ {
		readersWg.Add(1)
		go func(id int) {
			defer readersWg.Done()
			reqID := 0
			log := NewLogger("stress")
			for {
				select {
				case <-stopTraffic:
					return
				default:
					atomic.AddInt64(&inFlightRequests, 1)
					reqID++
					envelope := &browserCDPEnvelope{
						Params: json.RawMessage(fmt.Sprintf(`{"requestId":"req-%d-%d","request":{"url":"https://example.com/api/v1/items","method":"GET"}}`, id, reqID)),
					}
					// Simulate request processing under RLock
					handleRequestPaused(state, wsURL, envelope, log)
					atomic.AddInt64(&completedRequests, 1)
					// Tiny yield to allow scheduler rotation
					time.Sleep(10 * time.Microsecond)
				}
			}
		}(i)
	}

	// 10 concurrent writer goroutines continuously mutating rules under Lock
	for i := 0; i < 10; i++ {
		readersWg.Add(1)
		go func(id int) {
			defer readersWg.Done()
			step := 0
			for {
				select {
				case <-stopTraffic:
					return
				default:
					step++
					state.mu.Lock()
					if len(state.rules) > 5 {
						state.rules = state.rules[:2]
					} else {
						pat := fmt.Sprintf("*/dyn/%d/%d", id, step)
						re, _, _ := compileRoutePattern(pat)
						state.rules = append(state.rules, interceptRule{
							URLPattern: pat,
							Action:     "mock",
							Status:     200,
							matcher:    re,
						})
					}
					state.mu.Unlock()
					time.Sleep(50 * time.Microsecond)
				}
			}
		}(i)
	}

	// Wait for traffic to ramp up
	time.Sleep(20 * time.Millisecond)

	// In a separate goroutine simulating listener exit when stopCh closes
	go func() {
		<-state.stopCh
		// Allow any in-flight handleRequestPaused to finish, then close listenerDone
		time.Sleep(5 * time.Millisecond)
		close(state.listenerDone)
	}()

	// Now empirically benchmark removeInterceptState while heavy traffic is running
	removeStart := time.Now()
	removeDone := make(chan struct{})

	go func() {
		removeInterceptState(wsURL)
		close(removeDone)
	}()

	select {
	case <-removeDone:
		removeDuration := time.Since(removeStart)
		if removeDuration > 200*time.Millisecond {
			t.Fatalf("removeInterceptState took %v, exceeding 200ms threshold (stalled or lock inversion!)", removeDuration)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("CRITICAL DEADLOCK: removeInterceptState failed to complete within 1s under concurrent requests!")
	}

	// Stop background traffic
	close(stopTraffic)
	readersWg.Wait()

	// Verify state is clean
	interceptTargetsMu.Lock()
	_, stillPresent := interceptTargets[wsURL]
	interceptTargetsMu.Unlock()

	if stillPresent {
		t.Fatalf("expected wsURL to be removed from interceptTargets map")
	}

	state.mu.RLock()
	active := state.active
	state.mu.RUnlock()
	if active {
		t.Fatalf("expected state.active to be false after removeInterceptState")
	}
}

// TestChallenger_M3_R2_RouteMatcher_HighConcurrency_UnderTraffic
// Empirically verifies thread safety of pre-compiled route matchers across 20,000 requests.
func TestChallenger_M3_R2_RouteMatcher_HighConcurrency_UnderTraffic(t *testing.T) {
	state := &interceptState{}

	// Setup 10 diverse pre-compiled rules
	patterns := []struct {
		pat    string
		method string
		action string
		status int
	}{
		{"*/api/v1/users/*", "GET", "mock", 200},
		{"*/api/v1/orders", "POST", "mock", 201},
		{"^https://auth\\.example\\.com/oauth/.*", "POST", "mock", 200},
		{"*/static/img/*.png", "GET", "block", 404},
		{"/(?i)healthz|ready/", "GET", "mock", 200},
		{"*/metrics", "GET", "block", 403},
		{"*/v2/graphql", "POST", "mock", 200},
		{"*/admin/**", "", "block", 401},
		{"*/data.[0-9].json", "GET", "mock", 200},
		{"*/checkout/payment", "POST", "mock", 200},
	}

	for _, p := range patterns {
		re, _, err := compileRoutePattern(p.pat)
		if err != nil {
			t.Fatalf("compileRoutePattern failed: %v", err)
		}
		state.rules = append(state.rules, interceptRule{
			URLPattern: p.pat,
			Method:     p.method,
			Action:     p.action,
			Status:     p.status,
			matcher:    re,
		})
	}

	var wg sync.WaitGroup
	var matchedCount int64
	var unmatchedCount int64

	// 80 concurrent workers evaluating 250 requests each (20,000 requests)
	for w := 0; w < 80; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for req := 0; req < 250; req++ {
				var url string
				var method string
				switch req % 5 {
				case 0:
					url = fmt.Sprintf("https://api.example.com/api/v1/users/%d", req)
					method = "GET"
				case 1:
					url = "https://auth.example.com/oauth/token"
					method = "POST"
				case 2:
					url = "https://cdn.example.com/static/img/logo.png"
					method = "GET"
				case 3:
					url = "https://example.com/v2/graphql"
					method = "POST"
				default:
					url = fmt.Sprintf("https://random.com/unmatched/%d", req)
					method = "GET"
				}

				envelope := &browserCDPEnvelope{
					Params: json.RawMessage(fmt.Sprintf(`{"requestId":"w%d-r%d","request":{"url":%q,"method":%q}}`, workerID, req, url, method)),
				}

				state.mu.RLock()
				matched := false
				for i := range state.rules {
					r := &state.rules[i]
					if r.matcher != nil && r.matcher.MatchString(url) {
						if r.Method == "" || strings.EqualFold(r.Method, method) {
							matched = true
							break
						}
					}
				}
				state.mu.RUnlock()

				// Verify handleRequestPaused integration on first request per worker
				if req == 0 {
					handleRequestPaused(state, "ws://127.0.0.1:59999/devtools/page/test", envelope, NewLogger("stress"))
				}

				if matched {
					atomic.AddInt64(&matchedCount, 1)
				} else {
					atomic.AddInt64(&unmatchedCount, 1)
				}
			}
		}(w)
	}

	wg.Wait()

	total := atomic.LoadInt64(&matchedCount) + atomic.LoadInt64(&unmatchedCount)
	if total != 80*250 {
		t.Fatalf("expected 20,000 evaluations, got %d", total)
	}
}

// ============================================================================
// PART 2: SCREENSHOT INPUT DISAMBIGUATION & ACCURATE CLIPPING
// ============================================================================

// mockCDPScreenshotServer simulates CDP responses for screenshot tests
type mockCDPScreenshotServer struct {
	server       *httptest.Server
	port         int
	wsURL        string
	mu           sync.Mutex
	evalQueries  []string
	captureCalls []map[string]any
}

func newMockCDPScreenshotServer(t *testing.T, targetID string) *mockCDPScreenshotServer {
	t.Helper()
	ms := &mockCDPScreenshotServer{
		evalQueries:  make([]string, 0),
		captureCalls: make([]map[string]any, 0),
	}

	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Browser":              "MockChrome/120.0",
				"Protocol-Version":     "1.3",
				"webSocketDebuggerUrl": ms.wsURL,
			})
			return
		}
		if r.URL.Path == "/json/list" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":                   targetID,
					"type":                 "page",
					"url":                  "http://example.com/test",
					"webSocketDebuggerUrl": ms.wsURL,
				},
			})
			return
		}
		if strings.Contains(r.URL.Path, "/devtools/") {
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer ws.Close()

			for {
				var req map[string]any
				if err := ws.ReadJSON(&req); err != nil {
					return
				}
				id, _ := req["id"].(float64)
				method, _ := req["method"].(string)
				params, _ := req["params"].(map[string]any)

				var resp map[string]any
				switch method {
				case "Page.enable", "DOM.enable", "Accessibility.enable":
					resp = map[string]any{"id": id, "result": map[string]any{}}

				case "Runtime.evaluate":
					expr, _ := params["expression"].(string)
					ms.mu.Lock()
					ms.evalQueries = append(ms.evalQueries, expr)
					ms.mu.Unlock()

					// Return mock box model coordinates for element evaluation
					if strings.Contains(expr, "getBoundingClientRect") {
						resp = map[string]any{
							"id": id,
							"result": map[string]any{
								"result": map[string]any{
									"type": "object",
									"value": map[string]any{
										"found":  true,
										"x":      100.0,
										"y":      150.0,
										"width":  320.0,
										"height": 240.0,
										"tag":    "div",
									},
								},
							},
						}
					} else {
						resp = map[string]any{
							"id": id,
							"result": map[string]any{
								"result": map[string]any{"type": "boolean", "value": true},
							},
						}
					}

				case "Page.getLayoutMetrics":
					resp = map[string]any{
						"id": id,
						"result": map[string]any{
							"cssVisualViewport": map[string]any{
								"pageX": 0, "pageY": 0, "clientWidth": 1280, "clientHeight": 720, "scale": 1,
							},
							"contentSize": map[string]any{
								"x": 0, "y": 0, "width": 1280, "height": 720,
							},
						},
					}

				case "Page.captureScreenshot":
					ms.mu.Lock()
					ms.captureCalls = append(ms.captureCalls, params)
					ms.mu.Unlock()

					// Return a minimal valid 10x10 PNG in base64
					png1x1 := createTestPNG(10, 10)
					b64 := base64.StdEncoding.EncodeToString(png1x1)
					resp = map[string]any{
						"id": id,
						"result": map[string]any{
							"data": b64,
						},
					}

				default:
					resp = map[string]any{"id": id, "result": map[string]any{}}
				}

				if err := ws.WriteJSON(resp); err != nil {
					return
				}
			}
		}
	})

	server := httptest.NewServer(handler)
	ms.server = server
	ms.port = server.Listener.Addr().(*net.TCPAddr).Port
	ms.wsURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/page/" + targetID
	return ms
}

// TestChallenger_M3_R2_Screenshot_TargetAsTabID_NoCSSQueryError
// Empirically tests invoking screenshot with target: "tab_xxx" (meaning tab ID)
// to verify it does NOT trigger document.querySelector("tab_xxx") and does NOT fail.
func TestChallenger_M3_R2_Screenshot_TargetAsTabID_NoCSSQueryError(t *testing.T) {
	tabID := "tab_target_id_123"
	ms := newMockCDPScreenshotServer(t, tabID)
	defer ms.server.Close()

	// Configure Argus to point to mock server
	oldURL, oldPort, oldAttach := getConfig()
	attachTrue := true
	setConfig("http://127.0.0.1:"+strconv.Itoa(ms.port)+"/test", ms.port, &attachTrue)
	cdpPort = ms.port
	configuredPort = ms.port
	defer func() {
		setConfig(oldURL, oldPort, &oldAttach)
		cdpPort = oldPort
		configuredPort = oldPort
		closeAllConns()
	}()

	// Call screenshot with target: "tab_target_id_123"
	res, _, err := screenshotResult(screenshotInput{
		Target: tabID,
	})
	if err != nil {
		t.Fatalf("screenshotResult failed with target as tab ID: %v", err)
	}

	if res == nil || len(res.Content) == 0 {
		t.Fatalf("expected valid CallToolResult, got empty")
	}

	// Verify that NO querySelector was evaluated searching for "tab_target_id_123" as CSS selector
	ms.mu.Lock()
	evals := append([]string{}, ms.evalQueries...)
	captures := append([]map[string]any{}, ms.captureCalls...)
	ms.mu.Unlock()

	for _, expr := range evals {
		if strings.Contains(expr, tabID) {
			t.Fatalf("REGRESSION: target ID %q was erroneously passed to browser JavaScript eval:\n%s", tabID, expr)
		}
	}

	// Verify captureScreenshot was called without clip (full viewport)
	if len(captures) == 0 {
		t.Fatalf("expected Page.captureScreenshot to be called")
	}
	clipParam := captures[0]["clip"]
	if clipParam != nil {
		t.Fatalf("expected clip to be nil for viewport screenshot with target as tab ID, got: %+v", clipParam)
	}
}

// TestChallenger_M3_R2_Screenshot_SelectorAndRefClipping
// Empirically tests element clipping using selector: "#my-element" and target_id + target: "#my-element".
func TestChallenger_M3_R2_Screenshot_SelectorAndRefClipping(t *testing.T) {
	tabID := "tab_clip_test"
	ms := newMockCDPScreenshotServer(t, tabID)
	defer ms.server.Close()

	oldURL, oldPort, oldAttach := getConfig()
	attachTrue := true
	setConfig("http://127.0.0.1:"+strconv.Itoa(ms.port)+"/test", ms.port, &attachTrue)
	cdpPort = ms.port
	configuredPort = ms.port
	defer func() {
		setConfig(oldURL, oldPort, &oldAttach)
		cdpPort = oldPort
		configuredPort = oldPort
		closeAllConns()
	}()

	// Case 1: selector: "#my-element"
	ms.mu.Lock()
	ms.captureCalls = nil
	ms.mu.Unlock()

	res1, _, err := screenshotResult(screenshotInput{
		TargetID: tabID,
		Selector: "#my-element",
	})
	if err != nil {
		t.Fatalf("screenshot with selector failed: %v", err)
	}
	if res1 == nil {
		t.Fatalf("res1 is nil")
	}

	ms.mu.Lock()
	captures1 := append([]map[string]any{}, ms.captureCalls...)
	ms.mu.Unlock()

	if len(captures1) == 0 {
		t.Fatalf("expected Page.captureScreenshot call for Case 1")
	}
	clip1, ok := captures1[0]["clip"].(map[string]any)
	if !ok || clip1 == nil {
		t.Fatalf("expected clip parameter in Page.captureScreenshot for element screenshot, got: %+v", captures1[0])
	}
	if clip1["width"] != float64(320) || clip1["height"] != float64(240) {
		t.Fatalf("expected width=320, height=240, got: %+v", clip1)
	}

	// Case 2: target_id: tabID and target: "#my-element"
	ms.mu.Lock()
	ms.captureCalls = nil
	ms.mu.Unlock()

	res2, _, err := screenshotResult(screenshotInput{
		TargetID: tabID,
		Target:   "#my-element",
	})
	if err != nil {
		t.Fatalf("screenshot with target_id + target element failed: %v", err)
	}
	if res2 == nil {
		t.Fatalf("res2 is nil")
	}

	ms.mu.Lock()
	captures2 := append([]map[string]any{}, ms.captureCalls...)
	ms.mu.Unlock()

	if len(captures2) == 0 {
		t.Fatalf("expected Page.captureScreenshot call for Case 2")
	}
	clip2, ok := captures2[0]["clip"].(map[string]any)
	if !ok || clip2 == nil {
		t.Fatalf("expected clip parameter for Case 2, got: %+v", captures2[0])
	}
	if clip2["width"] != float64(320) || clip2["height"] != float64(240) {
		t.Fatalf("expected width=320, height=240, got: %+v", clip2)
	}
}

// TestChallenger_M3_R2_Screenshot_DiskMode_SavedToAlias
// Empirically verifies save_to_disk=true omits base64 and populates saved_to, path, file_path.
func TestChallenger_M3_R2_Screenshot_DiskMode_SavedToAlias(t *testing.T) {
	tabID := "tab_disk_test"
	ms := newMockCDPScreenshotServer(t, tabID)
	defer ms.server.Close()

	oldURL, oldPort, oldAttach := getConfig()
	attachTrue := true
	setConfig("http://127.0.0.1:"+strconv.Itoa(ms.port)+"/test", ms.port, &attachTrue)
	cdpPort = ms.port
	configuredPort = ms.port
	defer func() {
		setConfig(oldURL, oldPort, &oldAttach)
		cdpPort = oldPort
		configuredPort = oldPort
		closeAllConns()
	}()

	tmpDir, err := os.MkdirTemp("", "argus-disk-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	outPath := filepath.Join(tmpDir, "disk_shot.png")

	res, _, err := screenshotResult(screenshotInput{
		TargetID:   tabID,
		SaveToDisk: true,
		Path:       outPath,
	})
	if err != nil {
		t.Fatalf("screenshotResult with SaveToDisk failed: %v", err)
	}

	// Verify only 1 content item and it's TextContent (no ImageContent)
	if len(res.Content) != 1 {
		t.Fatalf("expected exactly 1 content item in disk mode, got %d", len(res.Content))
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}

	var meta map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &meta); err != nil {
		t.Fatalf("unmarshal meta text failed: %v", err)
	}

	if meta["saved_to_disk"] != true {
		t.Errorf("expected saved_to_disk=true, got %v", meta["saved_to_disk"])
	}
	if meta["saved_to"] != outPath {
		t.Errorf("expected saved_to=%q, got %v", outPath, meta["saved_to"])
	}
	if meta["file_path"] != outPath {
		t.Errorf("expected file_path=%q, got %v", outPath, meta["file_path"])
	}
	if meta["path"] != outPath {
		t.Errorf("expected path=%q, got %v", outPath, meta["path"])
	}

	// Verify file was written to disk
	fi, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("file not found on disk: %v", err)
	}
	if fi.Size() == 0 {
		t.Errorf("written file is 0 bytes")
	}
}

// ============================================================================
// PART 3: TAB LIFECYCLE STATE CLEANLINESS (ZERO LEAKS)
// ============================================================================

// TestChallenger_M3_R2_TabClose_ZeroLeak_InterceptAndDialog
// Empirically verifies opening and closing multiple tabs leaves 0 state entries
// in dialogMgr and interceptTargets.
func TestChallenger_M3_R2_TabClose_ZeroLeak_InterceptAndDialog(t *testing.T) {
	dialogMgr.Reset()

	const numTabs = 25
	tabIDs := make([]string, numTabs)
	wsURLs := make([]string, numTabs)

	for i := 0; i < numTabs; i++ {
		tabIDs[i] = fmt.Sprintf("LIFECYCLE_TAB_%03d", i)
		wsURLs[i] = fmt.Sprintf("ws://127.0.0.1:54321/devtools/page/%s", tabIDs[i])

		// Populate dialog state
		_ = dialogMgr.GetOrCreate(tabIDs[i], wsURLs[i])

		// Populate intercept state
		interceptTargetsMu.Lock()
		re, _, _ := compileRoutePattern("*/api/*")
		interceptTargets[wsURLs[i]] = &interceptState{
			rules: []interceptRule{
				{URLPattern: "*/api/*", Action: "mock", Status: 200, matcher: re},
			},
		}
		interceptTargetsMu.Unlock()
	}

	// Assert pre-condition: all 25 tabs registered
	dialogMgr.mu.Lock()
	if len(dialogMgr.targets) != numTabs {
		t.Fatalf("expected %d dialog targets, got %d", numTabs, len(dialogMgr.targets))
	}
	dialogMgr.mu.Unlock()

	interceptTargetsMu.Lock()
	for _, ws := range wsURLs {
		if _, ok := interceptTargets[ws]; !ok {
			t.Fatalf("expected %s in interceptTargets", ws)
		}
	}
	interceptTargetsMu.Unlock()

	// Simulate closing all tabs sequentially and checking cleanup
	for i, tabID := range tabIDs {
		// Both calls executed on tab close in tools_tabs.go:close_tab
		removeInterceptStateForTarget(tabID)
		dialogMgr.Remove(tabID)

		// Verify this tab was removed from dialogMgr
		dialogMgr.mu.Lock()
		if _, exists := dialogMgr.targets[tabID]; exists {
			dialogMgr.mu.Unlock()
			t.Fatalf("dialogMgr leaked tab %s", tabID)
		}
		dialogMgr.mu.Unlock()

		// Verify this tab was removed from interceptTargets
		interceptTargetsMu.Lock()
		if _, exists := interceptTargets[wsURLs[i]]; exists {
			interceptTargetsMu.Unlock()
			t.Fatalf("interceptTargets leaked wsURL %s", wsURLs[i])
		}
		interceptTargetsMu.Unlock()
	}

	// Final zero-leak assertion
	dialogMgr.mu.Lock()
	dialogRemaining := len(dialogMgr.targets)
	dialogMgr.mu.Unlock()
	if dialogRemaining != 0 {
		t.Fatalf("ZERO LEAK VIOLATION: dialogMgr retained %d targets", dialogRemaining)
	}

	interceptTargetsMu.Lock()
	interceptRemaining := len(interceptTargets)
	interceptTargetsMu.Unlock()
	if interceptRemaining != 0 {
		t.Fatalf("ZERO LEAK VIOLATION: interceptTargets retained %d targets", interceptRemaining)
	}
}

// TestChallenger_M3_R2_ConcurrentTabOpenClose_RaceCheck
// Empirically checks concurrent tab registration and tab closing under race detector.
func TestChallenger_M3_R2_ConcurrentTabOpenClose_RaceCheck(t *testing.T) {
	var wg sync.WaitGroup

	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for step := 0; step < 20; step++ {
				tabID := fmt.Sprintf("CONCURRENT_TAB_%d_%d", id, step)
				wsURL := fmt.Sprintf("ws://127.0.0.1:54321/devtools/page/%s", tabID)

				// Register
				_ = dialogMgr.GetOrCreate(tabID, wsURL)
				state := getInterceptState(wsURL)
				state.mu.Lock()
				state.rules = []interceptRule{{URLPattern: "/test", Action: "mock", Status: 200}}
				state.mu.Unlock()

				// Immediate or slightly delayed close
				if step%2 == 0 {
					time.Sleep(10 * time.Microsecond)
				}

				removeInterceptStateForTarget(tabID)
				dialogMgr.Remove(tabID)
			}
		}(i)
	}

	wg.Wait()
}
