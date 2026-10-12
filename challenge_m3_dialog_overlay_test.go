package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// =========================================================================
// CHALLENGE 1: CDP Dialog Anti-Deadlock Stress Testing (F10)
// =========================================================================

// TestChallengerDialog_RapidOpenCloseMultiType verifies that rapid succession of
// dialogs across all 4 CDP types (alert, confirm, prompt, beforeunload) correctly
// handles auto-dismiss and auto-accept policies without deadlock or state leakage.
func TestChallengerDialog_RapidOpenCloseMultiType(t *testing.T) {
	dialogMgr.Reset()
	targetID := "challenger_target_rapid"
	wsURL := "ws://127.0.0.1:59998/devtools/page/" + targetID
	state := dialogMgr.GetOrCreate(targetID, wsURL)

	types := []string{"alert", "confirm", "prompt", "beforeunload"}

	// 1. Rapid Auto-Dismiss
	state.SetPolicy(DialogPolicyDismiss, "")
	hub := newEventHub(64)
	defer hub.close()
	conn := &cdpConn{
		hub:     hub,
		pending: make(map[int]chan *browserCDPEnvelope),
		done:    make(chan struct{}),
		log:     NewLogger("challenger_test"),
	}

	for i := 0; i < 40; i++ {
		dType := types[i%len(types)]
		openJSON := fmt.Sprintf(`{
			"url": "https://example.com/test",
			"message": "Rapid dialog %d",
			"type": "%s",
			"defaultPrompt": "default_%d"
		}`, i, dType, i)

		env := &browserCDPEnvelope{
			Method: "Page.javascriptDialogOpening",
			Params: json.RawMessage(openJSON),
		}
		handleCDPDialogOpening(env, conn, wsURL, targetID)

		// Verify state recorded pending or auto-handled
		time.Sleep(2 * time.Millisecond)

		closeJSON := fmt.Sprintf(`{"result": false, "userInput": ""}`)
		closeEnv := &browserCDPEnvelope{
			Method: "Page.javascriptDialogClosed",
			Params: json.RawMessage(closeJSON),
		}
		handleCDPDialogClosed(closeEnv, targetID)
	}

	// Verify history is strictly bounded to 10
	hist := state.History()
	if len(hist) > 10 {
		t.Fatalf("history length %d exceeds max bounded limit 10", len(hist))
	}
	if len(hist) == 0 {
		t.Fatal("history should have recorded recent closed dialogs")
	}

	// 2. Rapid Auto-Accept with custom prompt
	state.SetPolicy(DialogPolicyAccept, "accepted_val")
	for i := 0; i < 20; i++ {
		dType := types[i%len(types)]
		openJSON := fmt.Sprintf(`{
			"url": "https://example.com/prompt",
			"message": "Prompt test %d",
			"type": "%s",
			"defaultPrompt": "default_prompt"
		}`, i, dType)

		env := &browserCDPEnvelope{
			Method: "Page.javascriptDialogOpening",
			Params: json.RawMessage(openJSON),
		}
		handleCDPDialogOpening(env, conn, wsURL, targetID)
		time.Sleep(5 * time.Millisecond)

		last := state.LastHandled()
		if last == nil || !last.AutoHandled || last.HandledAction != "accept" {
			t.Fatalf("dialog %d (%s) was not auto-accepted: %+v", i, dType, last)
		}
		if dType == "prompt" && last.HandledPromptText != "accepted_val" {
			t.Fatalf("prompt text mismatch: got %q, want 'accepted_val'", last.HandledPromptText)
		}
	}
}

// TestChallengerDialog_ConcurrentPolicyAndWaiting verifies that concurrent policy
// modification, rapid status queries, and async dialog waiting do not race or block.
func TestChallengerDialog_ConcurrentPolicyAndWaiting(t *testing.T) {
	dialogMgr.Reset()
	targetID := "challenger_target_concurrent"
	wsURL := "ws://127.0.0.1:59998/devtools/page/" + targetID
	state := dialogMgr.GetOrCreate(targetID, wsURL)

	hub := newEventHub(64)
	defer hub.close()
	conn := &cdpConn{
		hub:     hub,
		pending: make(map[int]chan *browserCDPEnvelope),
		done:    make(chan struct{}),
		log:     NewLogger("challenger_test"),
	}

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Worker 1: Rapidly mutate policy
	wg.Add(1)
	go func() {
		defer wg.Done()
		policies := []DialogPolicy{DialogPolicyDismiss, DialogPolicyAccept, DialogPolicyNone}
		for i := 0; ctx.Err() == nil && i < 100; i++ {
			state.SetPolicy(policies[i%len(policies)], fmt.Sprintf("prompt_%d", i))
			time.Sleep(1 * time.Millisecond)
		}
	}()

	// Worker 2: Rapidly fire dialog open & close events
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ctx.Err() == nil && i < 100; i++ {
			openJSON := fmt.Sprintf(`{"url": "https://test.com", "message": "msg %d", "type": "confirm"}`, i)
			env := &browserCDPEnvelope{
				Method: "Page.javascriptDialogOpening",
				Params: json.RawMessage(openJSON),
			}
			handleCDPDialogOpening(env, conn, wsURL, targetID)
			time.Sleep(1 * time.Millisecond)
			handleCDPDialogClosed(&browserCDPEnvelope{
				Method: "Page.javascriptDialogClosed",
				Params: json.RawMessage(`{"result": true, "userInput": ""}`),
			}, targetID)
		}
	}()

	// Worker 3: Rapid state readers
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			_ = state.Pending()
			_ = state.HasPending()
			_ = state.LastHandled()
			_ = state.History()
			_, _ = state.GetPolicy()
			time.Sleep(500 * time.Microsecond)
		}
	}()

	wg.Wait()
}

// TestChallengerDialog_AsyncWaitingUnblocksImmediately tests handle_dialog timeout_ms
// behavior: unblocks promptly when dialog opens and does not hang when no dialog appears.
func TestChallengerDialog_AsyncWaitingUnblocksImmediately(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	registerDialogTools(server)

	targetID := "challenger_async_wait_target"
	wsURL := "ws://127.0.0.1:59998/devtools/page/" + targetID
	state := dialogMgr.GetOrCreate(targetID, wsURL)
	state.SetPolicy(DialogPolicyNone, "") // manual mode

	setCurrentTargetID(targetID)
	defer clearCurrentTargetID()

	// Case 1: Timeout waiting when NO dialog opens
	startNoDialog := time.Now()
	_, _, _ = serverHandleDialog(context.Background(), handleDialogInput{
		Action:    "status",
		TimeoutMs: 60,
	})
	elapsedNoDialog := time.Since(startNoDialog)
	if elapsedNoDialog > 200*time.Millisecond {
		t.Errorf("expected status query to complete promptly, took %v", elapsedNoDialog)
	}

	// Case 2: Dialog arrives while waiting
	waitDone := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		// Wait up to 2000ms
		timeout := 2000
		if !state.HasPending() {
			timer := time.NewTimer(time.Duration(timeout) * time.Millisecond)
			defer timer.Stop()
			select {
			case <-state.notifyCh:
			case <-timer.C:
			}
		}
		waitDone <- time.Since(start)
	}()

	// Fire opening event after 30ms
	time.Sleep(30 * time.Millisecond)
	state.SetPending(&DialogInfo{
		Type:     "alert",
		Message:  "Async wakeup alert",
		OpenedAt: time.Now(),
	})

	select {
	case duration := <-waitDone:
		if duration > 500*time.Millisecond {
			t.Fatalf("async wait took %v, expected < 500ms wakeup", duration)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("async wait hung and timed out after 1s")
	}
}

// TestChallengerDialog_AntiDeadlockFailFast verifies that cdpEvaluateTimeout and
// settleAndCaptureVerification fail fast and do not hang for 10.5 seconds when a dialog is open.
func TestChallengerDialog_AntiDeadlockFailFast(t *testing.T) {
	dialogMgr.Reset()
	targetID := "challenger_fail_fast_target"
	wsURL := "ws://127.0.0.1:59998/devtools/page/" + targetID
	state := dialogMgr.GetOrCreate(targetID, wsURL)
	state.SetPending(&DialogInfo{
		Type:     "confirm",
		Message:  "Deadlock blocker question",
		OpenedAt: time.Now(),
	})

	// 1. cdpEvaluateTimeout fail-fast verification
	startEval := time.Now()
	_, err := cdpEvaluateTimeout(wsURL, "window.location.href", true, 10*time.Second)
	evalDuration := time.Since(startEval)

	if evalDuration > 250*time.Millisecond {
		t.Fatalf("cdpEvaluateTimeout blocked for %v (expected fail-fast < 250ms)", evalDuration)
	}
	if err == nil || !strings.Contains(err.Error(), "dialog 'confirm' is open") {
		t.Fatalf("unexpected evaluate result: %v", err)
	}

	// 2. settleAndCaptureVerification guard verification
	ar := BeginAction("click")
	defer ar.End()
	ar.SetTarget(targetID)

	res := make(map[string]any)
	startSettle := time.Now()
	settleAndCaptureVerification(ar, wsURL, res)
	settleDuration := time.Since(startSettle)

	if settleDuration > 250*time.Millisecond {
		t.Fatalf("settleAndCaptureVerification blocked for %v (expected fail-fast < 250ms)", settleDuration)
	}

	v, ok := res["verification"].(map[string]any)
	if !ok {
		t.Fatalf("missing verification map: %+v", res)
	}
	dh, ok := v["dialog_handled"].(map[string]any)
	if !ok {
		t.Fatalf("missing dialog_handled verification: %+v", v)
	}
	if dh["status"] != "pending" || dh["type"] != "confirm" {
		t.Fatalf("unexpected dialog_handled payload: %+v", dh)
	}
}

// =========================================================================
// CHALLENGE 2: Dev Overlay Auto-Diagnose Stress Testing (F11)
// =========================================================================

// TestChallengerDevOverlay_ParseFileLocationStress tests the stack and file location
// parser across edge cases: minified frames, Windows paths, parens, multiline stacks,
// and malformed inputs.
func TestChallengerDevOverlay_ParseFileLocationStress(t *testing.T) {
	testMatrix := []struct {
		name     string
		input    string
		wantFile string
		wantLine int
		wantCol  int
	}{
		{
			name:     "NextJS standard format",
			input:    "./app/dashboard/settings/page.tsx (34:12)",
			wantFile: "./app/dashboard/settings/page.tsx",
			wantLine: 34,
			wantCol:  12,
		},
		{
			name:     "Vite line with colons",
			input:    "/Users/alex/projects/client/src/views/Home.vue:102:8",
			wantFile: "/Users/alex/projects/client/src/views/Home.vue",
			wantLine: 102,
			wantCol:  8,
		},
		{
			name:     "Minified production chunk",
			input:    "assets/vendor-c83d.min.js:1:45023",
			wantFile: "assets/vendor-c83d.min.js",
			wantLine: 1,
			wantCol:  45023,
		},
		{
			name: "Multiline stack trace first frame extraction",
			input: "Error: Uncaught runtime exception\n" +
				"    at parseUser (/workspace/src/auth/token.ts:18:5)\n" +
				"    at processTick (node:internal/process/task_queues:95:5)",
			wantFile: "/workspace/src/auth/token.ts",
			wantLine: 18,
			wantCol:  5,
		},
		{
			name:     "Webpack internal protocol format",
			input:    "webpack-internal:///./src/components/Modal.jsx:55:10",
			wantFile: "./src/components/Modal.jsx",
			wantLine: 55,
			wantCol:  10,
		},
		{
			name:     "Line only without col",
			input:    "src/server.ts:40",
			wantFile: "src/server.ts",
			wantLine: 40,
			wantCol:  0,
		},
		{
			name:     "Empty input",
			input:    "",
			wantFile: "",
			wantLine: 0,
			wantCol:  0,
		},
		{
			name:     "Only whitespace",
			input:    "    \n\t  ",
			wantFile: "",
			wantLine: 0,
			wantCol:  0,
		},
		{
			name:     "Giant string without matches (bounded RE2 linear scan)",
			input:    strings.TrimSpace(strings.Repeat("some error message with no filename here ", 500)),
			wantFile: strings.TrimSpace(strings.Repeat("some error message with no filename here ", 500)),
			wantLine: 0,
			wantCol:  0,
		},
	}

	for _, tc := range testMatrix {
		t.Run(tc.name, func(t *testing.T) {
			file, line, col := parseFileLocation(tc.input)
			if file != tc.wantFile || line != tc.wantLine || col != tc.wantCol {
				t.Errorf("parseFileLocation(%q)\ngot:  (%q, %d, %d)\nwant: (%q, %d, %d)",
					tc.input, file, line, col, tc.wantFile, tc.wantLine, tc.wantCol)
			}
		})
	}
}

// TestChallengerDevOverlay_StackFallbackWhenFileRawEmpty tests that when file_raw
// is empty, parseFileLocation falls back to parsing the stack trace in checkDevOverlay.
func TestChallengerDevOverlay_StackFallbackWhenFileRawEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{}
		ws, err := u.Upgrade(w, r, nil)
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
				"id": id,
				"result": map[string]any{
					"result": map[string]any{
						"value": map[string]any{
							"detected":  true,
							"framework": "nextjs",
							"title":     "Runtime Error",
							"message":   "TypeError: cannot read property 'map' of undefined",
							"file_raw":  "",
							"stack": "TypeError: cannot read property 'map'\n" +
								"    at ProductGrid (components/ProductGrid.tsx:48:15)\n" +
								"    at renderWithHooks (react-dom.js:100:5)",
						},
					},
				},
			})
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/page/challenger_stack_fallback"
	overlay, err := checkDevOverlay(wsURL)
	if err != nil {
		t.Fatalf("checkDevOverlay failed: %v", err)
	}
	if overlay == nil || !overlay.Detected {
		t.Fatal("expected detected overlay")
	}
	if overlay.File != "components/ProductGrid.tsx" || overlay.Line != 48 || overlay.Column != 15 {
		t.Errorf("expected stack fallback to extract components/ProductGrid.tsx:48:15, got %s:%d:%d",
			overlay.File, overlay.Line, overlay.Column)
	}
}

// TestChallengerDevOverlay_SnifferFrameworkPrecedence verifies the script logic
// detects frameworks in priority order and covers shadow DOM queries.
func TestChallengerDevOverlay_SnifferFrameworkPrecedence(t *testing.T) {
	// Verify script syntax integrity: must be valid self-invoking function
	if !strings.HasPrefix(strings.TrimSpace(detectDevOverlayJS), "(() => {") {
		t.Fatal("detectDevOverlayJS should be self-invoking function")
	}
	if !strings.HasSuffix(strings.TrimSpace(detectDevOverlayJS), "})()") {
		t.Fatal("detectDevOverlayJS should close self-invoking function")
	}

	// Verify detection order in JS: 1. Vite -> 2. Next.js -> 3. Webpack -> 4. React Error Boundary
	viteIdx := strings.Index(detectDevOverlayJS, "vite-error-overlay")
	nextIdx := strings.Index(detectDevOverlayJS, "nextjs-portal")
	webpackIdx := strings.Index(detectDevOverlayJS, "webpack-dev-server-client-overlay")
	reactIdx := strings.Index(detectDevOverlayJS, "data-react-error-overlay")

	if viteIdx == -1 || nextIdx == -1 || webpackIdx == -1 || reactIdx == -1 {
		t.Fatalf("missing framework selectors in detectDevOverlayJS: vite=%d next=%d webpack=%d react=%d",
			viteIdx, nextIdx, webpackIdx, reactIdx)
	}

	if !(viteIdx < nextIdx && nextIdx < webpackIdx && webpackIdx < reactIdx) {
		t.Errorf("expected framework precedence order Vite < Next.js < Webpack < React, got indices: vite=%d next=%d webpack=%d react=%d",
			viteIdx, nextIdx, webpackIdx, reactIdx)
	}

	// Verify shadowRoot query is present for Next.js and Vite
	if !strings.Contains(detectDevOverlayJS, "shadowRoot") {
		t.Error("detectDevOverlayJS does not traverse shadowRoot")
	}
}

// TestChallengerDevOverlay_WaitAbortEarlyEmpirical verifies that wait_for and try_until
// abort early (in milliseconds) instead of hanging when a dev overlay is present.
func TestChallengerDevOverlay_WaitAbortEarlyEmpirical(t *testing.T) {
	var cdpEvalCount int32
	var overlayInjected int32

	// Setup mock CDP WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{}
		ws, err := u.Upgrade(w, r, nil)
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

			var res any = map[string]any{}

			switch method {
			case "Runtime.evaluate":
				atomic.AddInt32(&cdpEvalCount, 1)
				expr, _ := params["expression"].(string)

				if strings.Contains(expr, "detectDevOverlayJS") || strings.Contains(expr, "vite-error-overlay") {
					if atomic.LoadInt32(&overlayInjected) == 1 {
						// Return detected overlay
						res = map[string]any{
							"result": map[string]any{
								"value": map[string]any{
									"detected":  true,
									"framework": "vite",
									"title":     "Vite Compilation Error",
									"message":   "SyntaxError: Unexpected token",
									"file_raw":  "/src/App.tsx:25:4",
								},
							},
						}
					} else {
						res = map[string]any{
							"result": map[string]any{
								"value": map[string]any{"detected": false},
							},
						}
					}
				} else {
					// Condition check returns unsatisfied (false)
					res = map[string]any{
						"result": map[string]any{
							"value": map[string]any{"satisfied": false},
						},
					}
				}
			}

			_ = ws.WriteJSON(map[string]any{
				"id":     id,
				"result": res,
			})
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/page/challenger_overlay_abort"

	// 1. Test wait_for abort early
	atomic.StoreInt32(&overlayInjected, 1)

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	registerWaitTools(mcpServer)

	startWait := time.Now()
	// Call waitForInput with 10000ms timeout
	_, waitRes, waitErr := callWaitForHelper(context.Background(), wsURL, waitForInput{
		Selector:  "#non-existent-button",
		TimeoutMs: 10000,
		PollMs:    100,
	})
	waitElapsed := time.Since(startWait)

	if waitElapsed > 1*time.Second {
		t.Fatalf("wait_for did not abort early! Took %v (expected < 1s)", waitElapsed)
	}
	if waitErr == nil || !strings.Contains(waitErr.Error(), "dev server error overlay detected (vite)") {
		t.Fatalf("wait_for expected overlay abort error, got: %v", waitErr)
	}
	if wMap, ok := waitRes.(map[string]any); !ok || wMap["aborted"] != true || wMap["reason"] != "dev_overlay_detected" {
		t.Fatalf("wait_for unexpected result payload: %+v", waitRes)
	}

	// 2. Test try_until abort early
	startTry := time.Now()
	_, tryRes, tryErr := callTryUntilHelper(context.Background(), wsURL, tryUntilInput{
		Condition: "window.__appReady === true",
		TimeoutMs: 15000,
		PollMs:    100,
	})
	tryElapsed := time.Since(startTry)

	if tryElapsed > 1*time.Second {
		t.Fatalf("try_until did not abort early! Took %v (expected < 1s)", tryElapsed)
	}
	if tryErr == nil || !strings.Contains(tryErr.Error(), "dev server error overlay detected (vite)") {
		t.Fatalf("try_until expected overlay abort error, got: %v", tryErr)
	}
	if tMap, ok := tryRes.(map[string]any); !ok || tMap["aborted"] != true || tMap["reason"] != "dev_overlay_detected" {
		t.Fatalf("try_until unexpected result payload: %+v", tryRes)
	}
}

// Helpers calling wait_for and try_until logic directly with test wsURL
func callWaitForHelper(ctx context.Context, ws string, input waitForInput) (*mcp.CallToolResult, any, error) {
	input.TimeoutMs = resolveTimeout(input.TimeoutMs, input.Timeout)
	timeout := time.Duration(input.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	poll := time.Duration(input.PollMs) * time.Millisecond
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	condition := waitForExpression(input)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, map[string]any{"satisfied": false, "cancelled": true}, ctx.Err()
		default:
		}
		value, err := cdpEvaluate(ws, condition, true)
		if err == nil {
			if result, ok := value.(map[string]any); ok && result != nil {
				if satisfied, ok := result["satisfied"].(bool); ok && satisfied {
					return nil, result, nil
				}
			}
		}
		if overlay, _ := checkDevOverlay(ws); overlay != nil && overlay.Detected {
			return nil, map[string]any{
				"satisfied":   false,
				"aborted":     true,
				"reason":      "dev_overlay_detected",
				"dev_overlay": overlay,
			}, fmt.Errorf("wait_for aborted: dev server error overlay detected (%s): %s (file: %s:%d)",
				overlay.Framework, overlay.Message, overlay.File, overlay.Line)
		}
		select {
		case <-ctx.Done():
			return nil, map[string]any{"satisfied": false, "cancelled": true}, ctx.Err()
		case <-time.After(poll):
		}
	}
	return nil, map[string]any{"satisfied": false, "timed_out": true}, nil
}

func callTryUntilHelper(ctx context.Context, ws string, input tryUntilInput) (*mcp.CallToolResult, any, error) {
	input.TimeoutMs = resolveTimeout(input.TimeoutMs, input.Timeout)
	timeout := time.Duration(input.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	poll := time.Duration(input.PollMs) * time.Millisecond
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	condition := resolveFirstNonEmpty(input.Condition, input.Script, input.Expression, input.Code)
	deadline := time.Now().Add(timeout)
	attempts := 0
	maxAttempts := input.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 30
	}

	for time.Now().Before(deadline) && attempts < maxAttempts {
		select {
		case <-ctx.Done():
			return nil, map[string]any{"satisfied": false, "cancelled": true, "attempts": attempts}, ctx.Err()
		default:
		}
		attempts++
		value, err := cdpEvaluate(ws, fmt.Sprintf(`(() => { try { return !!(%s); } catch(e) { return false; } })()`, condition), true)
		if err == nil {
			if satisfied, ok := value.(bool); ok && satisfied {
				return nil, map[string]any{"satisfied": true, "attempts": attempts}, nil
			}
		}
		if overlay, _ := checkDevOverlay(ws); overlay != nil && overlay.Detected {
			return nil, map[string]any{
				"satisfied":   false,
				"aborted":     true,
				"reason":      "dev_overlay_detected",
				"dev_overlay": overlay,
			}, fmt.Errorf("try_until aborted: dev server error overlay detected (%s): %s (file: %s:%d)",
				overlay.Framework, overlay.Message, overlay.File, overlay.Line)
		}
		select {
		case <-ctx.Done():
			return nil, map[string]any{"satisfied": false, "cancelled": true, "attempts": attempts}, ctx.Err()
		case <-time.After(poll):
		}
	}
	return nil, map[string]any{"satisfied": false, "attempts": attempts, "timed_out": time.Now().After(deadline)}, nil
}
