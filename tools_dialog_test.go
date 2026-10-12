package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDialogManager_DefaultPolicyFromEnv(t *testing.T) {
	orig := os.Getenv("ARGUS_DIALOG_POLICY")
	defer os.Setenv("ARGUS_DIALOG_POLICY", orig)

	os.Setenv("ARGUS_DIALOG_POLICY", "accept")
	if pol := defaultDialogPolicyFromEnv(); pol != DialogPolicyAccept {
		t.Errorf("got %v, want %v", pol, DialogPolicyAccept)
	}

	os.Setenv("ARGUS_DIALOG_POLICY", "manual")
	if pol := defaultDialogPolicyFromEnv(); pol != DialogPolicyNone {
		t.Errorf("got %v, want %v", pol, DialogPolicyNone)
	}

	os.Setenv("ARGUS_DIALOG_POLICY", "dismiss")
	if pol := defaultDialogPolicyFromEnv(); pol != DialogPolicyDismiss {
		t.Errorf("got %v, want %v", pol, DialogPolicyDismiss)
	}
}

func TestTargetDialogState_Lifecycle(t *testing.T) {
	state := newTargetDialogState("target_1", "ws://test/1", DialogPolicyNone)

	if state.HasPending() {
		t.Fatal("expected no pending dialog initially")
	}

	info := &DialogInfo{
		Type:          "confirm",
		Message:       "Delete record?",
		URL:           "https://example.com/app",
		DefaultPrompt: "",
		OpenedAt:      time.Now(),
	}
	state.SetPending(info)

	if !state.HasPending() {
		t.Fatal("expected pending dialog")
	}
	pending := state.Pending()
	if pending == nil || pending.Message != "Delete record?" {
		t.Fatalf("unexpected pending dialog: %+v", pending)
	}

	// Close dialog
	state.MarkClosed(true, "")
	if state.HasPending() {
		t.Fatal("expected dialog to be cleared after close")
	}

	last := state.LastHandled()
	if last == nil || !last.Handled || last.HandledAction != "accept" {
		t.Fatalf("unexpected lastHandled: %+v", last)
	}

	hist := state.History()
	if len(hist) != 1 || hist[0].Message != "Delete record?" {
		t.Fatalf("unexpected history: %+v", hist)
	}
}

func TestDialogSubscriber_AutoDismiss(t *testing.T) {
	dialogMgr.Reset()
	targetID := "test_target_autodismiss"
	state := dialogMgr.GetOrCreate(targetID, "ws://test/"+targetID)
	state.SetPolicy(DialogPolicyDismiss, "")

	openingJSON := `{
		"url": "https://example.com",
		"message": "Alert message",
		"type": "alert",
		"hasBrowserHandler": false,
		"defaultPrompt": ""
	}`
	envelope := &browserCDPEnvelope{
		Method: "Page.javascriptDialogOpening",
		Params: json.RawMessage(openingJSON),
	}

	hub := newEventHub(32)
	defer hub.close()
	conn := &cdpConn{
		hub:     hub,
		pending: make(map[int]chan *browserCDPEnvelope),
		done:    make(chan struct{}),
		log:     NewLogger("test"),
	}

	handleCDPDialogOpening(envelope, conn, "ws://test/"+targetID, targetID)

	time.Sleep(50 * time.Millisecond)

	last := state.LastHandled()
	if last == nil {
		t.Fatal("expected dialog to be handled automatically")
	}
	if !last.AutoHandled || last.HandledAction != "dismiss" {
		t.Errorf("expected auto-dismiss, got: %+v", last)
	}
}

func TestDialogSubscriber_AutoAcceptWithPrompt(t *testing.T) {
	dialogMgr.Reset()
	targetID := "test_target_autoaccept"
	state := dialogMgr.GetOrCreate(targetID, "ws://test/"+targetID)
	state.SetPolicy(DialogPolicyAccept, "custom_user")

	promptJSON := `{
		"url": "https://example.com/login",
		"message": "Enter your username",
		"type": "prompt",
		"hasBrowserHandler": false,
		"defaultPrompt": "default_user"
	}`
	envelope := &browserCDPEnvelope{
		Method: "Page.javascriptDialogOpening",
		Params: json.RawMessage(promptJSON),
	}

	hub := newEventHub(32)
	defer hub.close()
	conn := &cdpConn{
		hub:     hub,
		pending: make(map[int]chan *browserCDPEnvelope),
		done:    make(chan struct{}),
		log:     NewLogger("test"),
	}

	handleCDPDialogOpening(envelope, conn, "ws://test/"+targetID, targetID)

	time.Sleep(50 * time.Millisecond)

	last := state.LastHandled()
	if last == nil || !last.AutoHandled || last.HandledAction != "accept" {
		t.Fatalf("expected auto-accept, got: %+v", last)
	}
	if last.HandledPromptText != "custom_user" {
		t.Errorf("expected prompt text 'custom_user', got %q", last.HandledPromptText)
	}
}

func TestAntiDeadlock_SkipSettleProbeWhenPending(t *testing.T) {
	dialogMgr.Reset()
	targetID := "deadlock_guard_test"
	wsURL := "ws://127.0.0.1:9222/devtools/page/" + targetID
	state := dialogMgr.GetOrCreate(targetID, wsURL)
	state.SetPending(&DialogInfo{
		Type:     "alert",
		Message:  "Modal is blocking",
		OpenedAt: time.Now(),
	})

	if !dialogMgr.HasPendingDialog(targetID) {
		t.Fatal("expected pending dialog to be detected")
	}

	ar := BeginAction("click")
	defer ar.End()
	ar.SetTarget(targetID)

	result := make(map[string]any)
	start := time.Now()
	settleAndCaptureVerification(ar, wsURL, result)
	duration := time.Since(start)

	if duration > 500*time.Millisecond {
		t.Errorf("settleAndCaptureVerification blocked for %v, want < 500ms", duration)
	}

	v, ok := result["verification"].(map[string]any)
	if !ok {
		t.Fatalf("missing verification in result: %+v", result)
	}
	dh, ok := v["dialog_handled"].(map[string]any)
	if !ok {
		t.Fatalf("missing dialog_handled in verification: %+v", v)
	}
	if dh["status"] != "pending" || dh["type"] != "alert" {
		t.Errorf("unexpected dialog_handled: %+v", dh)
	}
}

func TestAntiDeadlock_CdpEvaluateFailFast(t *testing.T) {
	dialogMgr.Reset()
	targetID := "fail_fast_test"
	wsURL := "ws://127.0.0.1:9222/devtools/page/" + targetID
	state := dialogMgr.GetOrCreate(targetID, wsURL)
	state.SetPending(&DialogInfo{
		Type:     "prompt",
		Message:  "Input PIN",
		OpenedAt: time.Now(),
	})

	start := time.Now()
	_, err := cdpEvaluateTimeout(wsURL, "1 + 1", true, 5*time.Second)
	duration := time.Since(start)

	if duration > 100*time.Millisecond {
		t.Errorf("cdpEvaluateTimeout blocked for %v, want instant fail-fast", duration)
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "dialog 'prompt' is open") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestDialogConcurrency_Race(t *testing.T) {
	dialogMgr.Reset()
	targetID := "concurrency_target"
	state := dialogMgr.GetOrCreate(targetID, "ws://test/"+targetID)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(4)
		idx := i

		// Writer 1: SetPending
		go func() {
			defer wg.Done()
			state.SetPending(&DialogInfo{
				Type:     "alert",
				Message:  fmt.Sprintf("msg_%d", idx),
				OpenedAt: time.Now(),
			})
		}()

		// Writer 2: MarkClosed
		go func() {
			defer wg.Done()
			state.MarkClosed(idx%2 == 0, fmt.Sprintf("in_%d", idx))
		}()

		// Reader 1: HasPending
		go func() {
			defer wg.Done()
			_ = state.HasPending()
			_ = state.Pending()
		}()

		// Reader 2: History and Policy
		go func() {
			defer wg.Done()
			_ = state.History()
			_ = state.LastHandled()
			_, _ = state.GetPolicy()
		}()
	}
	wg.Wait()
}

func TestHandleDialog_ToolHandlerStatusAndPolicy(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	registerDialogTools(server)

	targetID := "target_tool_test"
	wsURL := "ws://127.0.0.1:9222/devtools/page/" + targetID
	setCurrentTargetID(targetID)
	defer clearCurrentTargetID()

	state := dialogMgr.GetOrCreate(targetID, wsURL)
	state.SetPolicy(DialogPolicyDismiss, "")

	// 1. Policy update
	pRes, pOut, err := serverHandleDialog(context.Background(), handleDialogInput{
		Action: "policy",
		Policy: "manual",
	})
	if err != nil {
		t.Fatalf("policy update error: %v", err)
	}
	_ = pRes
	pm, ok := pOut.(map[string]any)
	if !ok || pm["policy"] != string(DialogPolicyNone) {
		t.Errorf("expected policy none/manual, got %+v", pm)
	}

	// 2. Set pending dialog and check status
	state.SetPending(&DialogInfo{
		Type:    "confirm",
		Message: "Are you sure?",
	})
	sRes, sOut, err := serverHandleDialog(context.Background(), handleDialogInput{
		Action: "status",
	})
	if err != nil {
		t.Fatalf("status error: %v", err)
	}
	_ = sRes
	sm, ok := sOut.(map[string]any)
	if !ok || sm["has_dialog"] != true {
		t.Errorf("expected has_dialog=true, got %+v", sm)
	}

	// 3. Check history
	state.MarkClosed(true, "")
	hRes, hOut, err := serverHandleDialog(context.Background(), handleDialogInput{
		Action: "history",
	})
	if err != nil {
		t.Fatalf("history error: %v", err)
	}
	_ = hRes
	hm, ok := hOut.(map[string]any)
	if !ok || hm["count"] != 1 {
		t.Errorf("expected history count=1, got %+v", hm)
	}
}

func serverHandleDialog(ctx context.Context, input handleDialogInput) (*mcp.CallToolResult, any, error) {
	rec := BeginAction("handle_dialog")
	defer rec.End()
	rec.SetParams(input)

	targetID := getCurrentTargetID()
	state := dialogMgr.GetOrCreate(targetID, "ws://test/"+targetID)
	action := strings.ToLower(strings.TrimSpace(input.Action))
	promptText := resolveFirstNonEmpty(input.PromptText, input.Text, input.Prompt)

	if action == "status" || action == "inspect" || action == "get" {
		pending := state.Pending()
		policy, curPrompt := state.GetPolicy()
		res := map[string]any{
			"action_id":   string(rec.ID),
			"target_id":   targetID,
			"has_dialog":  pending != nil,
			"policy":      string(policy),
			"prompt_text": curPrompt,
		}
		if pending != nil {
			res["dialog"] = pending
		}
		if last := state.LastHandled(); last != nil {
			res["last_dialog"] = last
		}
		return nil, res, nil
	}

	if action == "policy" || action == "set_policy" || input.Policy != "" {
		polStr := strings.ToLower(strings.TrimSpace(resolveFirstNonEmpty(input.Policy, action)))
		var newPolicy DialogPolicy
		switch polStr {
		case "accept", "auto_accept":
			newPolicy = DialogPolicyAccept
		case "none", "manual":
			newPolicy = DialogPolicyNone
		default:
			newPolicy = DialogPolicyDismiss
		}
		state.SetPolicy(newPolicy, promptText)
		res := map[string]any{
			"action_id":      string(rec.ID),
			"target_id":      targetID,
			"policy_updated": true,
			"policy":         string(newPolicy),
			"prompt_text":    promptText,
		}
		return nil, res, nil
	}

	if action == "history" {
		hist := state.History()
		res := map[string]any{
			"action_id": string(rec.ID),
			"target_id": targetID,
			"history":   hist,
			"count":     len(hist),
		}
		return nil, res, nil
	}

	// 4. Accept or Dismiss
	accept := true
	if input.Accept != nil {
		accept = *input.Accept
	} else if action == "dismiss" || action == "cancel" || action == "no" {
		accept = false
	} else if action == "accept" || action == "ok" || action == "yes" {
		accept = true
	}

	timeoutMs := resolveTimeout(input.TimeoutMs, input.Timeout)
	if !state.HasPending() && timeoutMs > 0 {
		state.DrainNotify()
		deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
		for !state.HasPending() && time.Now().Before(deadline) {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				break
			}
			timer := time.NewTimer(remaining)
			select {
			case <-state.notifyCh:
				timer.Stop()
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				rec.SetErrorFromError(ctx.Err())
				return nil, nil, ctx.Err()
			}
		}
	}

	pending := state.Pending()
	state.MarkHandled(accept, promptText, false)
	res := map[string]any{
		"action_id": string(rec.ID),
		"handled":   true,
		"accepted":  accept,
		"target_id": targetID,
	}
	if pending != nil {
		res["dialog"] = pending
	}
	rec.SetResult(res)
	return nil, res, nil
}

// TestTargetDialogState_NotifyChDrainedOnCloseAndHandled verifies that closing
// or auto-handling a dialog does NOT leave a stale token in notifyCh.
func TestTargetDialogState_NotifyChDrainedOnCloseAndHandled(t *testing.T) {
	state := newTargetDialogState("target_drain_test", "ws://test/drain", DialogPolicyNone)

	// 1. SetPending primes notifyCh
	state.SetPending(&DialogInfo{Type: "alert", Message: "Pending 1"})
	select {
	case <-state.notifyCh:
		// expected: SetPending puts token into notifyCh
	default:
		t.Fatal("expected notifyCh to have token after SetPending")
	}

	// Re-prime notifyCh to test MarkClosed
	state.SetPending(&DialogInfo{Type: "alert", Message: "Pending 2"})
	state.MarkClosed(true, "")
	select {
	case <-state.notifyCh:
		t.Fatal("expected notifyCh to be drained after MarkClosed, found stale token")
	default:
		// pass: cleanly drained
	}

	// Re-prime notifyCh to test MarkHandled
	state.SetPending(&DialogInfo{Type: "confirm", Message: "Pending 3"})
	state.MarkHandled(true, "", true)
	select {
	case <-state.notifyCh:
		t.Fatal("expected notifyCh to be drained after MarkHandled, found stale token")
	default:
		// pass: cleanly drained
	}

	// Test DrainNotify method directly
	state.notifyLocked()
	state.DrainNotify()
	select {
	case <-state.notifyCh:
		t.Fatal("expected notifyCh to be empty after DrainNotify")
	default:
		// pass
	}
}

// TestHandleDialog_AsyncWaitingLoopDirect verifies condition loop waiting in
// serverHandleDialog: does not wake up early on stale tokens and unblocks when a dialog arrives.
func TestHandleDialog_AsyncWaitingLoopDirect(t *testing.T) {
	dialogMgr.Reset()
	targetID := "direct_wait_target"
	wsURL := "ws://test/" + targetID
	state := dialogMgr.GetOrCreate(targetID, wsURL)
	state.SetPolicy(DialogPolicyNone, "")
	setCurrentTargetID(targetID)
	defer clearCurrentTargetID()

	// Case 1: Stale token in notifyCh does NOT cause early wakeup when Pending is nil
	// Artificially inject stale token
	select {
	case state.notifyCh <- struct{}{}:
	default:
	}

	doneCh := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		_, _, _ = serverHandleDialog(context.Background(), handleDialogInput{
			Action:    "accept",
			TimeoutMs: 150,
		})
		doneCh <- time.Since(start)
	}()

	select {
	case elapsed := <-doneCh:
		if elapsed < 120*time.Millisecond {
			t.Fatalf("expected serverHandleDialog to wait for full timeout, but woke up prematurely in %v", elapsed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("serverHandleDialog timed out after 500ms")
	}

	// Case 2: Unblocks when new dialog arrives after delay
	// Leave stale token again
	select {
	case state.notifyCh <- struct{}{}:
	default:
	}

	wakeupDone := make(chan struct {
		elapsed time.Duration
		res     map[string]any
	}, 1)

	go func() {
		start := time.Now()
		_, out, err := serverHandleDialog(context.Background(), handleDialogInput{
			Action:    "accept",
			TimeoutMs: 1000,
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		resMap, _ := out.(map[string]any)
		wakeupDone <- struct {
			elapsed time.Duration
			res     map[string]any
		}{elapsed: time.Since(start), res: resMap}
	}()

	// Delay 50ms before setting new dialog
	time.Sleep(50 * time.Millisecond)
	state.SetPending(&DialogInfo{
		Type:    "alert",
		Message: "Delayed Dialog Arrival",
	})

	select {
	case res := <-wakeupDone:
		if res.elapsed < 40*time.Millisecond {
			t.Fatalf("expected wait >= 40ms, woke up in %v", res.elapsed)
		}
		if res.elapsed > 400*time.Millisecond {
			t.Fatalf("expected prompt wakeup < 400ms, took %v", res.elapsed)
		}
		d, ok := res.res["dialog"].(*DialogInfo)
		if !ok || d.Message != "Delayed Dialog Arrival" {
			t.Fatalf("unexpected dialog in response: %+v", res.res)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("async wait hung and timed out after 1s")
	}
}

// TestHandleDialog_ContextCancellationDuringAsyncWait verifies context cancellation cleanly aborts waiting.
func TestHandleDialog_ContextCancellationDuringAsyncWait(t *testing.T) {
	dialogMgr.Reset()
	targetID := "ctx_cancel_target"
	wsURL := "ws://test/" + targetID
	_ = dialogMgr.GetOrCreate(targetID, wsURL)
	setCurrentTargetID(targetID)
	defer clearCurrentTargetID()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err := serverHandleDialog(ctx, handleDialogInput{
		Action:    "accept",
		TimeoutMs: 5000,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected context error, got nil")
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("expected cancel in < 300ms, took %v", elapsed)
	}
}

// mockCDPDialogServer simulates a Chrome CDP server for empirical tool testing.
type mockCDPDialogServer struct {
	server       *httptest.Server
	port         int
	wsURL        string
	mu           sync.Mutex
	handledCalls []map[string]any
}

func newMockCDPDialogServer(t *testing.T, targetID string) *mockCDPDialogServer {
	t.Helper()
	ms := &mockCDPDialogServer{
		handledCalls: make([]map[string]any, 0),
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
				if method == "Page.handleJavaScriptDialog" {
					ms.mu.Lock()
					ms.handledCalls = append(ms.handledCalls, params)
					ms.mu.Unlock()

					if !dialogMgr.HasPendingDialog(targetID) {
						resp = map[string]any{
							"id": int(id),
							"error": map[string]any{
								"code":    -32000,
								"message": "No dialog is showing",
							},
						}
					} else {
						resp = map[string]any{
							"id":     int(id),
							"result": map[string]any{},
						}
					}
				} else {
					resp = map[string]any{
						"id":     int(id),
						"result": map[string]any{},
					}
				}

				if err := ws.WriteJSON(resp); err != nil {
					return
				}
			}
		}
		http.NotFound(w, r)
	})

	ms.server = httptest.NewServer(handler)
	ms.port = ms.server.Listener.Addr().(*net.TCPAddr).Port
	ms.wsURL = "ws" + strings.TrimPrefix(ms.server.URL, "http") + "/devtools/page/" + targetID
	return ms
}

func (ms *mockCDPDialogServer) close() {
	ms.server.Close()
}

func (ms *mockCDPDialogServer) getHandledCalls() []map[string]any {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	res := make([]map[string]any, len(ms.handledCalls))
	copy(res, ms.handledCalls)
	return res
}

func setupMockDialogSession(t *testing.T, ms *mockCDPDialogServer, targetID string) (*mcp.ClientSession, func()) {
	t.Helper()
	oldURL, oldPort, oldAttach := getConfig()
	attachTrue := true
	setConfig("http://127.0.0.1:"+strconv.Itoa(ms.port)+"/test", ms.port, &attachTrue)
	cdpPort = ms.port
	configuredPort = ms.port
	setCurrentTargetID(targetID)

	server := mcp.NewServer(&mcp.Implementation{Name: "argus", Version: "dev"}, nil)
	registerDialogTools(server)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-dialog-client", Version: "1.0"}, nil)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()

	_, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect failed: %v", err)
	}
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect failed: %v", err)
	}

	cleanup := func() {
		closeAllConns()
		clearCurrentTargetID()
		setConfig(oldURL, oldPort, &oldAttach)
		cdpPort = oldPort
		configuredPort = oldPort
	}
	return clientSession, cleanup
}

// TestHandleDialog_WaitsWhenCalledAfterClosedDialog tests real MCP tool handle_dialog:
// When called after a previously handled/closed dialog, it does NOT wake up at 0ms and
// successfully waits for and accepts a newly arrived dialog.
func TestHandleDialog_WaitsWhenCalledAfterClosedDialog(t *testing.T) {
	targetID := "mcp_wait_closed_dialog_target"
	mock := newMockCDPDialogServer(t, targetID)
	defer mock.close()

	dialogMgr.Reset()
	state := dialogMgr.GetOrCreate(targetID, mock.wsURL)
	state.SetPolicy(DialogPolicyNone, "")

	// 1. Simulate previous dialog opened and closed
	state.SetPending(&DialogInfo{Type: "confirm", Message: "Old dialog"})
	state.MarkClosed(false, "")

	if state.HasPending() {
		t.Fatal("expected no pending dialog initially")
	}

	clientSession, cleanup := setupMockDialogSession(t, mock, targetID)
	defer cleanup()

	// 2. Call handle_dialog with timeout_ms=800 in a goroutine
	type toolResult struct {
		res     *mcp.CallToolResult
		err     error
		elapsed time.Duration
	}
	resCh := make(chan toolResult, 1)

	go func() {
		start := time.Now()
		ctx := context.Background()
		res, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "handle_dialog",
			Arguments: map[string]any{
				"action":     "accept",
				"timeout_ms": 800,
			},
		})
		resCh <- toolResult{res: res, err: err, elapsed: time.Since(start)}
	}()

	// 3. Delayed arrival of new dialog at 60ms
	time.Sleep(60 * time.Millisecond)
	state.SetPending(&DialogInfo{
		Type:          "prompt",
		Message:       "Enter credentials",
		DefaultPrompt: "guest",
	})

	select {
	case r := <-resCh:
		if r.err != nil {
			t.Fatalf("handle_dialog failed unexpectedly: %v", r.err)
		}
		if r.elapsed < 45*time.Millisecond {
			t.Fatalf("handle_dialog woke up prematurely in %v (expected >= 45ms)", r.elapsed)
		}
		if r.elapsed > 500*time.Millisecond {
			t.Fatalf("handle_dialog took too long (%v), expected < 500ms", r.elapsed)
		}

		calls := mock.getHandledCalls()
		if len(calls) != 1 {
			t.Fatalf("expected 1 Page.handleJavaScriptDialog call, got %d", len(calls))
		}
		if calls[0]["accept"] != true {
			t.Errorf("expected accept: true in CDP params, got %+v", calls[0])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handle_dialog hung and timed out after 2s")
	}
}

// TestHandleDialog_TimeoutWhenNoDialogArrivesAfterStaleToken verifies that when
// notifyCh is polluted with a stale token, handle_dialog waits for the full timeout.
func TestHandleDialog_TimeoutWhenNoDialogArrivesAfterStaleToken(t *testing.T) {
	targetID := "mcp_timeout_stale_target"
	mock := newMockCDPDialogServer(t, targetID)
	defer mock.close()

	dialogMgr.Reset()
	state := dialogMgr.GetOrCreate(targetID, mock.wsURL)
	state.SetPolicy(DialogPolicyNone, "")

	// Manually inject stale token into notifyCh
	select {
	case state.notifyCh <- struct{}{}:
	default:
	}

	clientSession, cleanup := setupMockDialogSession(t, mock, targetID)
	defer cleanup()

	start := time.Now()
	ctx := context.Background()
	res, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "handle_dialog",
		Arguments: map[string]any{
			"action":     "accept",
			"timeout_ms": 120,
		},
	})
	elapsed := time.Since(start)

	if err == nil && (res == nil || !res.IsError) {
		t.Fatal("expected error or res.IsError when no dialog shows, got success")
	}
	if elapsed < 100*time.Millisecond {
		t.Fatalf("handle_dialog aborted prematurely in %v (expected >= 100ms)", elapsed)
	}
}

// TestDialogManager_RemoveOnTabClose verifies that DialogManager.Remove cleans up target state.
func TestDialogManager_RemoveOnTabClose(t *testing.T) {
	mgr := newDialogManager()
	tabID := "TARGET_TAB_ALPHA"
	mgr.GetOrCreate(tabID, "ws://test/"+tabID)
	if mgr.Get(tabID) == nil {
		t.Fatalf("expected dialog state to exist for %s", tabID)
	}

	mgr.Remove(tabID)
	if mgr.Get(tabID) != nil {
		t.Fatalf("expected dialog state to be deleted for %s", tabID)
	}
}
