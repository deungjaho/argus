package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestParseFileLocation(t *testing.T) {
	tests := []struct {
		input    string
		wantFile string
		wantLine int
		wantCol  int
	}{
		{"src/App.tsx:42:15", "src/App.tsx", 42, 15},
		{"./app/dashboard/page.tsx:28:9", "./app/dashboard/page.tsx", 28, 9},
		{"src/utils.js (105:4)", "src/utils.js", 105, 4},
		{"/components/Button.jsx:12", "/components/Button.jsx", 12, 0},
		{"webpack-internal:///./src/index.js:25:3", "./src/index.js", 25, 3},
		{"", "", 0, 0},
		{"   ", "", 0, 0},
	}

	for _, tt := range tests {
		file, line, col := parseFileLocation(tt.input)
		if file != tt.wantFile || line != tt.wantLine || col != tt.wantCol {
			t.Errorf("parseFileLocation(%q) = (%q, %d, %d), want (%q, %d, %d)",
				tt.input, file, line, col, tt.wantFile, tt.wantLine, tt.wantCol)
		}
	}
}

func TestDevOverlaySnifferJS(t *testing.T) {
	requiredFrameworks := []string{
		"vite-error-overlay",
		"nextjs-portal",
		"webpack-dev-server-client-overlay",
		"data-react-error-overlay",
		"data-error-boundary",
		"remix-error-boundary",
		"__nuxt-error",
	}
	for _, f := range requiredFrameworks {
		if !strings.Contains(detectDevOverlayJS, f) {
			t.Errorf("detectDevOverlayJS missing required framework selector: %s", f)
		}
	}

	// Finding 1 regression check: Verify overly broad form alert selectors are NOT present
	forbiddenSelectors := []string{
		`role="alert"`,
		`role=\"alert\"`,
		`[role="alert"]`,
		`[class*="error" i]`,
	}
	for _, s := range forbiddenSelectors {
		if strings.Contains(detectDevOverlayJS, s) {
			t.Errorf("REGRESSION: detectDevOverlayJS must NOT contain overly broad selector %q", s)
		}
	}
}

// TestDevOverlay_FormValidationRegression_Node empirically validates in Node.js
// that detectDevOverlayJS does NOT detect standard WAI-ARIA form validation elements.
func TestDevOverlay_FormValidationRegression_Node(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node executable not found in PATH; skipping node empirical DOM test")
	}

	script := fmt.Sprintf(`
const detectDevOverlayJS = %s;

function evaluateOnElements(elements) {
  const document = {
    querySelector(sel) {
      const selectors = sel.split(',').map(s => s.trim());
      for (const el of elements) {
        for (const s of selectors) {
          if (s === 'vite-error-overlay' && el.tag === 'vite-error-overlay') return el;
          if (s === 'nextjs-portal' && el.tag === 'nextjs-portal') return el;
          if (s === 'div[data-react-error-overlay="true"]' && el.tag === 'div' && el.attrs?.['data-react-error-overlay'] === 'true') return el;
          if (s === '[data-error-boundary="true"]' && el.attrs?.['data-error-boundary'] === 'true') return el;
          if (s === 'div.remix-error-boundary' && el.tag === 'div' && el.classes?.includes('remix-error-boundary')) return el;
          if (s === '#__nuxt-error' && el.id === '__nuxt-error') return el;
          if (s.includes('[role="alert"]') && el.attrs?.role === 'alert') return el;
        }
      }
      return null;
    },
    querySelectorAll(sel) { return []; },
    getElementById(id) { return elements.find(e => e.id === id) || null; }
  };
  const fn = new Function('document', 'return ' + detectDevOverlayJS);
  return fn(document);
}

// 1. Standard Accessible Form Validation (Bootstrap / AntD / Element / Native WAI-ARIA)
const formAlert = evaluateOnElements([{
  tag: 'div',
  attrs: { role: 'alert' },
  classes: ['error-message', 'form-error'],
  innerText: 'Please enter your password'
}]);
if (formAlert.detected) {
  console.error("FAIL: form validation error was detected as dev overlay!");
  process.exit(1);
}

// 2. Ant Design form validation alert
const antdAlert = evaluateOnElements([{
  tag: 'div',
  attrs: { role: 'alert' },
  classes: ['ant-alert', 'ant-alert-error'],
  innerText: 'Please input your username!'
}]);
if (antdAlert.detected) {
  console.error("FAIL: Ant Design form alert was detected as dev overlay!");
  process.exit(2);
}

// 3. Genuine React error boundary
const reactBoundary = evaluateOnElements([{
  tag: 'div',
  attrs: { 'data-error-boundary': 'true' },
  innerText: 'Application crashed: Uncaught TypeError: foo is not a function'
}]);
if (!reactBoundary.detected || reactBoundary.framework !== 'react_error_boundary') {
  console.error("FAIL: genuine react error boundary was NOT detected!");
  process.exit(3);
}

console.log("PASS");
`, jsString(detectDevOverlayJS))

	cmd := exec.Command(nodePath, "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Node regression test failed: %v\nOutput: %s", err, string(out))
	}
}

// TestDevOverlay_FormValidationRegression_IsolatedChrome verifies against a real isolated headless browser
// that normal form validation error messages do not trigger dev overlay detection.
func TestDevOverlay_FormValidationRegression_IsolatedChrome(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real Chrome test in short mode")
	}
	inst, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	targets, err := inst.Targets()
	if err != nil || len(targets) == 0 {
		t.Fatalf("failed to query targets from isolated Chrome: %v", err)
	}
	wsURL := targets[0].WebSocketDebuggerURL

	// 1. Navigate to page with standard form validation error
	htmlForm := "data:text/html,<html><body><form><div role=\"alert\" class=\"error-message\">Please enter your password</div></form></body></html>"
	_, err = cdpEvaluate(wsURL, fmt.Sprintf("window.location.href = %q", htmlForm), false)
	if err != nil {
		t.Fatalf("failed to navigate to form alert page: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	overlay, err := checkDevOverlay(wsURL)
	if err != nil {
		t.Fatalf("checkDevOverlay failed: %v", err)
	}
	if overlay != nil && overlay.Detected {
		t.Fatalf("REGRESSION DETECTED: form validation message was falsely identified as dev overlay: %+v", overlay)
	}

	// 2. Navigate to page with genuine React Error Overlay
	htmlOverlay := "data:text/html,<html><body><div data-react-error-overlay=\"true\">RenderError in src/App.tsx:42:10</div></body></html>"
	_, err = cdpEvaluate(wsURL, fmt.Sprintf("window.location.href = %q", htmlOverlay), false)
	if err != nil {
		t.Fatalf("failed to navigate to genuine overlay page: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	overlay2, err := checkDevOverlay(wsURL)
	if err != nil {
		t.Fatalf("checkDevOverlay failed: %v", err)
	}
	if overlay2 == nil || !overlay2.Detected {
		t.Fatalf("expected genuine overlay to be detected, got nil")
	}
	if overlay2.Framework != "react_error_boundary" {
		t.Errorf("expected framework react_error_boundary, got %s", overlay2.Framework)
	}
	if overlay2.File != "src/App.tsx" || overlay2.Line != 42 || overlay2.Column != 10 {
		t.Errorf("expected parsed location src/App.tsx:42:10, got %s:%d:%d", overlay2.File, overlay2.Line, overlay2.Column)
	}
}

func TestCheckDevOverlay_RealFunction(t *testing.T) {
	// 1. Error on empty WebSocket URL
	_, err := checkDevOverlay("")
	if err == nil {
		t.Error("expected error for empty ws url")
	}

	// 2. Mock WebSocket server returning detected Vite overlay
	var mockDetected atomic.Bool
	mockDetected.Store(true)

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

			var val any
			if mockDetected.Load() {
				val = map[string]any{
					"detected":   true,
					"framework":  "vite",
					"title":      "Vite Error [vite:react-babel]",
					"message":    "Failed to parse source file",
					"file_raw":   "/src/Broken.tsx:14:2",
					"code_frame": "const x = ;",
					"stack":      "Error at /src/Broken.tsx:14:2",
				}
			} else {
				val = map[string]any{"detected": false}
			}

			_ = ws.WriteJSON(map[string]any{
				"id": int(id),
				"result": map[string]any{
					"result": map[string]any{
						"value": val,
					},
				},
			})
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/page/test_real"

	// Subtest A: Detected
	overlay, err := checkDevOverlay(wsURL)
	if err != nil {
		t.Fatalf("checkDevOverlay failed: %v", err)
	}
	if overlay == nil || !overlay.Detected {
		t.Fatal("expected detected overlay")
	}
	if overlay.Framework != "vite" || overlay.File != "/src/Broken.tsx" || overlay.Line != 14 || overlay.Column != 2 {
		t.Errorf("unexpected overlay data: %+v", overlay)
	}

	// Subtest B: Not detected
	mockDetected.Store(false)
	overlay2, err := checkDevOverlay(wsURL)
	if err != nil {
		t.Fatalf("checkDevOverlay failed on clean page: %v", err)
	}
	if overlay2 != nil {
		t.Errorf("expected nil overlay when detected is false, got: %+v", overlay2)
	}
}

func TestDevOverlayInVerification(t *testing.T) {
	ar := BeginAction("click")
	defer ar.End()
	ar.SetTarget("test_target")

	// Test collectVerification when no overlay exists
	v := ar.collectVerification("", nil, "https://example.com")
	if v == nil {
		t.Fatal("expected non-nil verification")
	}
	if _, hasOverlay := v["dev_overlay"]; hasOverlay {
		t.Errorf("expected no dev_overlay in clean verification")
	}
}

func TestWaitForExpression_Conditions(t *testing.T) {
	// 1. Selector condition
	expr := waitForExpression(waitForInput{Selector: "#submit-btn"})
	if !strings.Contains(expr, `selector_present: !!document.querySelector("#submit-btn")`) {
		t.Errorf("waitForExpression selector check missing: %s", expr)
	}

	// 2. Text condition
	expr = waitForExpression(waitForInput{Text: "Welcome Back"})
	if !strings.Contains(expr, `text_present: (document.body && document.body.innerText || '').includes("Welcome Back")`) {
		t.Errorf("waitForExpression text check missing: %s", expr)
	}

	// 3. Text absent condition
	expr = waitForExpression(waitForInput{TextAbsent: "Loading..."})
	if !strings.Contains(expr, `text_absent: !(document.body && document.body.innerText || '').includes("Loading...")`) {
		t.Errorf("waitForExpression text_absent check missing: %s", expr)
	}

	// 4. URL matches condition
	expr = waitForExpression(waitForInput{URLMatches: "/dashboard"})
	if !strings.Contains(expr, `url_matches: location.href.includes("/dashboard")`) {
		t.Errorf("waitForExpression url_matches check missing: %s", expr)
	}

	// 5. Empty condition
	if expr = waitForExpression(waitForInput{}); expr != "" {
		t.Errorf("expected empty string for blank input, got: %s", expr)
	}
}

func TestWaitForAbortsEarlyOnDevOverlay(t *testing.T) {
	// Setup mock CDP server that responds with unsatisfied condition, but detects overlay
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			params, _ := req["params"].(map[string]any)
			expr, _ := params["expression"].(string)

			var val any
			if strings.Contains(expr, "detectDevOverlayJS") || strings.Contains(expr, "vite-error-overlay") {
				val = map[string]any{
					"detected":   true,
					"framework":  "vite",
					"title":      "Vite Error [vite:react-babel]",
					"message":    "Failed to parse source file",
					"file_raw":   "/src/Broken.tsx:14:2",
					"code_frame": "const a = ;",
					"stack":      "Error at /src/Broken.tsx:14:2",
				}
			} else {
				val = map[string]any{"satisfied": false}
			}

			_ = ws.WriteJSON(map[string]any{
				"id": int(id),
				"result": map[string]any{
					"result": map[string]any{
						"value": val,
					},
				},
			})
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/page/test_abort"

	// 1. Real wait_for helper test
	startWait := time.Now()
	_, waitRes, waitErr := callWaitForHelper(context.Background(), wsURL, waitForInput{
		Selector:  "#never-appearing",
		TimeoutMs: 5000,
		PollMs:    50,
	})
	elapsed := time.Since(startWait)

	if elapsed > 1*time.Second {
		t.Fatalf("wait_for did not abort early! Took %v", elapsed)
	}
	if waitErr == nil || !strings.Contains(waitErr.Error(), "dev server error overlay detected (vite)") {
		t.Fatalf("expected overlay abort error, got: %v", waitErr)
	}
	wMap, ok := waitRes.(map[string]any)
	if !ok || wMap["aborted"] != true || wMap["reason"] != "dev_overlay_detected" {
		t.Fatalf("unexpected wait_for result payload: %+v", waitRes)
	}
	overlayObj, ok := wMap["dev_overlay"].(*DevOverlayError)
	if !ok || overlayObj == nil || overlayObj.Framework != "vite" {
		t.Fatalf("unexpected dev_overlay in payload: %+v", wMap["dev_overlay"])
	}

	// 2. Real try_until helper test
	startTry := time.Now()
	_, tryRes, tryErr := callTryUntilHelper(context.Background(), wsURL, tryUntilInput{
		Condition: "window.__neverTrue === true",
		TimeoutMs: 5000,
		PollMs:    50,
	})
	tryElapsed := time.Since(startTry)

	if tryElapsed > 1*time.Second {
		t.Fatalf("try_until did not abort early! Took %v", tryElapsed)
	}
	if tryErr == nil || !strings.Contains(tryErr.Error(), "dev server error overlay detected (vite)") {
		t.Fatalf("expected overlay abort error, got: %v", tryErr)
	}
	tMap, ok := tryRes.(map[string]any)
	if !ok || tMap["aborted"] != true || tMap["reason"] != "dev_overlay_detected" {
		t.Fatalf("unexpected try_until result payload: %+v", tryRes)
	}
}

func TestDiagnoseToolWithDevOverlay(t *testing.T) {
	// Setup mock CDP HTTP & WebSocket server
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	var wsURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Browser":              "MockChrome/120.0",
				"Protocol-Version":     "1.3",
				"webSocketDebuggerUrl": wsURL,
			})
			return
		}
		if r.URL.Path == "/json/list" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":                   "test_diag_target",
					"type":                 "page",
					"url":                  "http://example.com/test",
					"webSocketDebuggerUrl": wsURL,
				},
			})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/devtools/") {
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
				_ = ws.WriteJSON(map[string]any{
					"id": int(id),
					"result": map[string]any{
						"result": map[string]any{
							"value": map[string]any{
								"detected":  true,
								"framework": "nextjs",
								"title":     "Build Error",
								"message":   "Module not found: Can't resolve './missing'",
								"file_raw":  "app/page.tsx:5:1",
								"stack":     "Error in app/page.tsx:5:1",
							},
						},
					},
				})
			}
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	wsURL = "ws://" + server.Listener.Addr().String() + "/devtools/page/test_diag_target"

	// Configure Argus target routing
	oldURL, oldPort, oldAttach := getConfig()
	defer func() {
		attachVal := oldAttach
		setConfig(oldURL, oldPort, &attachVal)
		cdpPort = oldPort
		configuredPort = oldPort
	}()

	attachTrue := true
	setConfig("http://127.0.0.1:"+strconv.Itoa(port)+"/test", port, &attachTrue)
	cdpPort = port
	configuredPort = port
	setCurrentTargetID("test_diag_target")

	// Set up real MCP Server and Client Session
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "argus", Version: "dev"}, nil)
	registerTraceTools(mcpServer)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0"}, nil)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	_, err := mcpServer.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect MCP server: %v", err)
	}
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect MCP client session: %v", err)
	}
	defer cs.Close()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "diagnose",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("diagnose tool execution failed: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatal("empty response from diagnose tool")
	}

	var resData map[string]any
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			if err := json.Unmarshal([]byte(tc.Text), &resData); err != nil {
				t.Fatalf("failed to unmarshal diagnose response: %v\nraw: %s", err, tc.Text)
			}
			break
		}
	}

	// Verify dev_overlay payload enrichment
	devOverlay, ok := resData["dev_overlay"].(map[string]any)
	if !ok || devOverlay == nil {
		t.Fatalf("expected dev_overlay in diagnose response, got: %+v", resData)
	}
	if devOverlay["framework"] != "nextjs" {
		t.Errorf("expected framework nextjs, got: %v", devOverlay["framework"])
	}
	if devOverlay["file"] != "app/page.tsx" || devOverlay["line"] != float64(5) || devOverlay["column"] != float64(1) {
		t.Errorf("expected parsed file app/page.tsx:5:1, got: %v:%v:%v",
			devOverlay["file"], devOverlay["line"], devOverlay["column"])
	}

	// Verify errors list contains dev_overlay
	errorsList, ok := resData["errors"].([]any)
	if !ok {
		t.Fatalf("expected errors list, got: %v", resData["errors"])
	}
	foundOverlay := false
	for _, e := range errorsList {
		if em, ok := e.(map[string]any); ok && em["type"] == "dev_overlay" {
			foundOverlay = true
			if em["framework"] != "nextjs" {
				t.Errorf("expected dev_overlay error framework nextjs, got %v", em["framework"])
			}
			break
		}
	}
	if !foundOverlay {
		t.Errorf("dev_overlay entry not found in diagnose errors: %+v", errorsList)
	}

	// Verify summary text enrichment
	summary, _ := resData["summary"].(string)
	if !strings.Contains(summary, "Dev server error overlay detected (nextjs)") {
		t.Errorf("summary does not contain dev overlay notice: %s", summary)
	}
}
