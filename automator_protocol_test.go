package main

// automator_protocol_test.go — protocol contract tests.
//
// These tests assert the exact JSON-RPC method names, parameter fields,
// and response unwrapping that the WeChat automator backend must use,
// as derived from the official miniprogram-automator@0.12.1 package.
//
// They use the fakeWxServer harness so they run without a real WeChat
// DevTools instance. They are the single source of protocol truth in
// the test suite.
//
// Key protocol facts (from out/Page.js, out/Element.js, out/MiniProgram.js):
//   - Page.getElement returns {elementId, tagName, nodeId?, videoId?}, NOT {id}.
//   - Element calls carry elementId + pageId (+ nodeId/videoId when present).
//   - Element.tap sends {elementId, pageId} (no other params).
//   - Input/textarea use Element.callFunction with functionName =
//     "input.input" / "textarea.input" and args = [value].
//   - App.callFunction uses functionDeclaration (NOT function), unwraps result.
//   - Page.getData returns {data: ...}, must unwrap .data.
//   - App.callWxMethod returns {result: ...}, must unwrap .result.
//   - App.getCurrentPage returns {pageId, path, query}.
//   - App.getPageStack returns {pageStack: [{pageId, path, query}]}.
//   - Navigation is callWxMethod(method, {url}) then wait then re-read page.

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// --- Fake handler helpers ---

// fakeElementResult returns the correct Page.getElement response shape.
func fakeElementResult(elementID, tagName string) any {
	return map[string]any{
		"elementId": elementID,
		"tagName":   tagName,
	}
}

// fakeElementsResult returns the correct Page.getElements response shape.
func fakeElementsResult(elements ...any) any {
	return map[string]any{"elements": elements}
}

// fakeAttributesResult returns the correct Element.getAttributes response.
func fakeAttributesResult(attrs map[string]string) any {
	return map[string]any{"attributes": attrs}
}

// fakeDataResult returns the correct Page.getData response shape.
func fakeDataResult(data any) any {
	return map[string]any{"data": data}
}

// fakeCallWxResult returns the correct App.callWxMethod response shape.
func fakeCallWxResult(result any) any {
	return map[string]any{"result": result}
}

// fakeCallFunctionResult returns the correct App.callFunction response shape.
func fakeCallFunctionResult(result any) any {
	return map[string]any{"result": result}
}

// fakeCurrentPageResult returns the correct App.getCurrentPage response.
func fakeCurrentPageResult(pageID int, path string) any {
	return map[string]any{
		"pageId": pageID,
		"path":   path,
		"query":  map[string]any{},
	}
}

// fakePageStackResult returns the correct App.getPageStack response.
func fakePageStackResult(pages ...any) any {
	return map[string]any{"pageStack": pages}
}

// fakeScreenshotResult returns the correct App.captureScreenshot response.
func fakeScreenshotResult(b64 string) any {
	return map[string]any{"data": b64}
}

// --- Standard handler sets ---

// standardFakeHandlers returns a handler map that covers the core protocol
// methods. Individual tests can override specific handlers.
func standardFakeHandlers() map[string]fakeWxHandler {
	return map[string]fakeWxHandler{
		"Tool.getInfo": func(_ json.RawMessage) (any, error) {
			return map[string]any{"SDKVersion": "3.17.1"}, nil
		},
		"App.getCurrentPage": func(_ json.RawMessage) (any, error) {
			return fakeCurrentPageResult(1, "pages/index/index"), nil
		},
		"App.getPageStack": func(_ json.RawMessage) (any, error) {
			return fakePageStackResult(fakeCurrentPageResult(1, "pages/index/index")), nil
		},
		"Page.getData": func(p json.RawMessage) (any, error) {
			return fakeDataResult(map[string]any{"title": "Hello", "count": 42}), nil
		},
		"Page.getElement": func(p json.RawMessage) (any, error) {
			return fakeElementResult("elem-001", "view"), nil
		},
		"Page.getElements": func(p json.RawMessage) (any, error) {
			return fakeElementsResult(
				fakeElementResult("elem-001", "view"),
				fakeElementResult("elem-002", "button"),
			), nil
		},
		"Element.getAttributes": func(p json.RawMessage) (any, error) {
			return fakeAttributesResult(map[string]string{
				"class": "container",
				"id":    "elem-001",
			}), nil
		},
		"Element.tap": func(_ json.RawMessage) (any, error) {
			return map[string]any{}, nil
		},
		"Element.callFunction": func(p json.RawMessage) (any, error) {
			return fakeCallFunctionResult(nil), nil
		},
		"Element.getWXML": func(p json.RawMessage) (any, error) {
			var req struct {
				ElementID string `json:"elementId"`
				Type      string `json:"type"`
			}
			json.Unmarshal(p, &req)
			if req.Type == "outer" {
				return map[string]any{"wxml": "<view class=\"container\">hello</view>"}, nil
			}
			return map[string]any{"wxml": "hello"}, nil
		},
		"App.callWxMethod": func(p json.RawMessage) (any, error) {
			return fakeCallWxResult(map[string]any{"ok": true}), nil
		},
		"App.callFunction": func(p json.RawMessage) (any, error) {
			return fakeCallFunctionResult(42), nil
		},
		"App.captureScreenshot": func(_ json.RawMessage) (any, error) {
			return fakeScreenshotResult("iVBORw0KGgo="), nil
		},
		"App.enableLog": func(_ json.RawMessage) (any, error) {
			return map[string]any{}, nil
		},
	}
}

// setupFakeBackend configures the wechat backend to point at the fake server
// and returns a cleanup function.
func setupFakeBackend(t *testing.T, port int) func() {
	t.Helper()
	backendMu.Lock()
	origBackend := configuredBackend
	origPort := configuredWxPort
	origProject := configuredWxProject
	configuredBackend = BackendWeChat
	configuredWxPort = port
	configuredWxProject = ""
	backendMu.Unlock()
	closeAutomatorConn()
	return func() {
		closeAutomatorConn()
		backendMu.Lock()
		configuredBackend = origBackend
		configuredWxPort = origPort
		configuredWxProject = origProject
		backendMu.Unlock()
	}
}

// --- Contract tests ---

// TestContractGetElementReturnsElementID asserts that Page.getElement
// response is decoded using the "elementId" field, not "id".
func TestContractGetElementReturnsElementID(t *testing.T) {
	fake := newFakeWxServer(t, standardFakeHandlers())
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	_, pageID, err := automatorCurrentPage()
	if err != nil {
		t.Fatalf("automatorCurrentPage: %v", err)
	}
	// Use the typed getElement helper.
	elem, err := automatorGetElement(pageID, ".btn")
	if err != nil {
		t.Fatalf("automatorGetElement: %v", err)
	}
	if elem.ElementID != "elem-001" {
		t.Errorf("elementId = %q, want %q", elem.ElementID, "elem-001")
	}
	if elem.TagName != "view" {
		t.Errorf("tagName = %q, want %q", elem.TagName, "view")
	}
}

// TestContractTapSendsElementIDAndPageID asserts that Element.tap sends
// {elementId, pageId} — not {id}, and not missing pageId.
func TestContractTapSendsElementIDAndPageID(t *testing.T) {
	fake := newFakeWxServer(t, standardFakeHandlers())
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	_, pageID, err := automatorCurrentPage()
	if err != nil {
		t.Fatalf("automatorCurrentPage: %v", err)
	}
	elem, err := automatorGetElement(pageID, ".btn")
	if err != nil {
		t.Fatalf("automatorGetElement: %v", err)
	}
	if err := automatorTapElement(pageID, elem); err != nil {
		t.Fatalf("automatorTapElement: %v", err)
	}
	// Verify the Element.tap request had elementId and pageId.
	tapReq := fake.requireRequest("Element.tap")
	fake.assertParam(tapReq, "elementId", "elem-001")
	fake.assertParam(tapReq, "pageId", float64(1))
	fake.assertNoParam(tapReq, "id")
}

// TestContractFillUsesCallFunctionInputInput asserts that fill on an input
// element uses Element.callFunction with functionName="input.input".
func TestContractFillUsesCallFunctionInputInput(t *testing.T) {
	handlers := standardFakeHandlers()
	handlers["Page.getElement"] = func(_ json.RawMessage) (any, error) {
		return fakeElementResult("elem-002", "input"), nil
	}
	fake := newFakeWxServer(t, handlers)
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	_, pageID, err := automatorCurrentPage()
	if err != nil {
		t.Fatalf("automatorCurrentPage: %v", err)
	}
	elem, err := automatorGetElement(pageID, "#myInput")
	if err != nil {
		t.Fatalf("automatorGetElement: %v", err)
	}
	if err := automatorFillElement(pageID, elem, "hello world"); err != nil {
		t.Fatalf("automatorFillElement: %v", err)
	}
	// Verify Element.callFunction was called with the right params.
	cfReq := fake.requireRequest("Element.callFunction")
	fake.assertParam(cfReq, "functionName", "input.input")
	fake.assertParam(cfReq, "elementId", "elem-002")
	fake.assertParam(cfReq, "pageId", float64(1))
	fake.assertNoParam(cfReq, "id")
	fake.assertNoParam(cfReq, "function")
	// args should be ["hello world"]
	var params struct {
		Args []any `json:"args"`
	}
	parseParams(t, cfReq, &params)
	if len(params.Args) != 1 {
		t.Fatalf("expected 1 arg, got %d: %v", len(params.Args), params.Args)
	}
	if params.Args[0] != "hello world" {
		t.Errorf("args[0] = %v, want %q", params.Args[0], "hello world")
	}
}

// TestContractFillTextareaUsesTextareaInput asserts that fill on a textarea
// uses Element.callFunction with functionName="textarea.input".
func TestContractFillTextareaUsesTextareaInput(t *testing.T) {
	handlers := standardFakeHandlers()
	handlers["Page.getElement"] = func(_ json.RawMessage) (any, error) {
		return fakeElementResult("elem-003", "textarea"), nil
	}
	fake := newFakeWxServer(t, handlers)
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	_, pageID, err := automatorCurrentPage()
	if err != nil {
		t.Fatalf("automatorCurrentPage: %v", err)
	}
	elem, err := automatorGetElement(pageID, "#myTextarea")
	if err != nil {
		t.Fatalf("automatorGetElement: %v", err)
	}
	if err := automatorFillElement(pageID, elem, "multi\nline"); err != nil {
		t.Fatalf("automatorFillElement: %v", err)
	}
	cfReq := fake.requireRequest("Element.callFunction")
	fake.assertParam(cfReq, "functionName", "textarea.input")
}

// TestContractFillRejectsNonInput asserts that fill on a non-input/textarea
// element returns an actionable error.
func TestContractFillRejectsNonInput(t *testing.T) {
	handlers := standardFakeHandlers()
	handlers["Page.getElement"] = func(_ json.RawMessage) (any, error) {
		return fakeElementResult("elem-004", "view"), nil
	}
	fake := newFakeWxServer(t, handlers)
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	_, pageID, err := automatorCurrentPage()
	if err != nil {
		t.Fatalf("automatorCurrentPage: %v", err)
	}
	elem, err := automatorGetElement(pageID, ".notinput")
	if err != nil {
		t.Fatalf("automatorGetElement: %v", err)
	}
	err = automatorFillElement(pageID, elem, "value")
	if err == nil {
		t.Fatal("expected error for fill on non-input/textarea, got nil")
	}
	te, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("expected *ToolError, got %T: %v", err, err)
	}
	if te.Code != ErrInvalidInput {
		t.Errorf("error code = %s, want %s", te.Code, ErrInvalidInput)
	}
}

// TestContractGetDataUnwrapsDataEnvelope asserts that Page.getData response
// is unwrapped: the caller receives the inner data, not {data: ...}.
func TestContractGetDataUnwrapsDataEnvelope(t *testing.T) {
	fake := newFakeWxServer(t, standardFakeHandlers())
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	_, pageID, err := automatorCurrentPage()
	if err != nil {
		t.Fatalf("automatorCurrentPage: %v", err)
	}
	data, err := automatorPageData(pageID, "")
	if err != nil {
		t.Fatalf("automatorPageData: %v", err)
	}
	m, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T: %v", data, data)
	}
	// The unwrapped data should have "title" and "count" directly,
	// NOT a nested "data" key.
	if _, ok := m["data"]; ok {
		t.Error("Page.getData result was not unwrapped: found nested 'data' key")
	}
	if m["title"] != "Hello" {
		t.Errorf("data.title = %v, want %q", m["title"], "Hello")
	}
}

// TestContractCallWxMethodUnwrapsResult asserts that App.callWxMethod
// response is unwrapped: the caller receives the inner result.
func TestContractCallWxMethodUnwrapsResult(t *testing.T) {
	fake := newFakeWxServer(t, standardFakeHandlers())
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	result, err := automatorCallWxMethod("getStorageSync", []any{"token"})
	if err != nil {
		t.Fatalf("automatorCallWxMethod: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T: %v", result, result)
	}
	if _, ok := m["result"]; ok {
		t.Error("App.callWxMethod result was not unwrapped: found nested 'result' key")
	}
	if m["ok"] != true {
		t.Errorf("result.ok = %v, want true", m["ok"])
	}
}

// TestContractEvaluateUsesFunctionDeclaration asserts that App.callFunction
// sends "functionDeclaration" (not "function") and unwraps result.
func TestContractEvaluateUsesFunctionDeclaration(t *testing.T) {
	fake := newFakeWxServer(t, standardFakeHandlers())
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	result, err := automatorEvaluate("() => 42")
	if err != nil {
		t.Fatalf("automatorEvaluate: %v", err)
	}
	// Result should be unwrapped (42, not {result: 42}).
	if result != float64(42) {
		t.Errorf("evaluate result = %v (%T), want 42", result, result)
	}
	// Verify the request used functionDeclaration.
	req := fake.requireRequest("App.callFunction")
	fake.assertParam(req, "functionDeclaration", "() => 42")
	fake.assertNoParam(req, "function")
}

// TestContractNavigateCallsWxMethodAndReReads asserts that navigation
// calls App.callWxMethod with the right method name and url, then
// re-reads the current page (App.getCurrentPage) to confirm settlement.
func TestContractNavigateCallsWxMethodAndReReads(t *testing.T) {
	handlers := standardFakeHandlers()
	var navMethod string
	var navURL string
	handlers["App.callWxMethod"] = func(p json.RawMessage) (any, error) {
		var params struct {
			Method string `json:"method"`
			Args   []any  `json:"args"`
		}
		json.Unmarshal(p, &params)
		navMethod = params.Method
		if len(params.Args) > 0 {
			if argMap, ok := params.Args[0].(map[string]any); ok {
				navURL, _ = argMap["url"].(string)
			}
		}
		return fakeCallWxResult(nil), nil
	}
	fake := newFakeWxServer(t, handlers)
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	before, after, err := automatorNavigateTyped("navigateTo", "pages/detail/index")
	if err != nil {
		t.Fatalf("automatorNavigateTyped: %v", err)
	}
	if before != "pages/index/index" {
		t.Errorf("before route = %q, want %q", before, "pages/index/index")
	}
	if after != "pages/index/index" {
		t.Errorf("after route = %q, want %q", after, "pages/index/index")
	}
	if navMethod != "navigateTo" {
		t.Errorf("callWxMethod method = %q, want %q", navMethod, "navigateTo")
	}
	if navURL != "/pages/detail/index" {
		t.Errorf("callWxMethod url = %q, want %q", navURL, "/pages/detail/index")
	}
	// Verify getCurrentPage was called at least twice (before + after).
	count := 0
	for _, r := range fake.recordedRequests() {
		if r.Method == "App.getCurrentPage" {
			count++
		}
	}
	if count < 2 {
		t.Errorf("expected getCurrentPage called >= 2 times (before+after), got %d", count)
	}
}

// TestContractNavigateDefaultRelaunch asserts that navigate without an
// explicit mode defaults to reLaunch (as the prototype intended).
func TestContractNavigateDefaultRelaunch(t *testing.T) {
	handlers := standardFakeHandlers()
	var navMethod string
	handlers["App.callWxMethod"] = func(p json.RawMessage) (any, error) {
		var params struct {
			Method string `json:"method"`
		}
		json.Unmarshal(p, &params)
		navMethod = params.Method
		return fakeCallWxResult(nil), nil
	}
	fake := newFakeWxServer(t, handlers)
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	_, _, err := automatorNavigateTyped("", "pages/home/index")
	if err != nil {
		t.Fatalf("automatorNavigateTyped: %v", err)
	}
	if navMethod != "reLaunch" {
		t.Errorf("default navigate method = %q, want %q", navMethod, "reLaunch")
	}
}

// TestContractNavigateRejectsInvalidMode asserts that an invalid navigation
// mode returns an actionable error.
func TestContractNavigateRejectsInvalidMode(t *testing.T) {
	fake := newFakeWxServer(t, standardFakeHandlers())
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	_, _, err := automatorNavigateTyped("invalidMode", "pages/x")
	if err == nil {
		t.Fatal("expected error for invalid navigation mode, got nil")
	}
	te, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("expected *ToolError, got %T: %v", err, err)
	}
	if te.Code != ErrInvalidInput {
		t.Errorf("error code = %s, want %s", te.Code, ErrInvalidInput)
	}
}

// TestContractScreenshotReturnsBase64 asserts that App.captureScreenshot
// response is decoded correctly (base64 data field).
func TestContractScreenshotReturnsBase64(t *testing.T) {
	fake := newFakeWxServer(t, standardFakeHandlers())
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	data, err := automatorScreenshot()
	if err != nil {
		t.Fatalf("automatorScreenshot: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty screenshot data")
	}
}

// TestContractGetCurrentPageReturnsPageID asserts that App.getCurrentPage
// returns pageId as a number, not a string.
func TestContractGetCurrentPageReturnsPageID(t *testing.T) {
	fake := newFakeWxServer(t, standardFakeHandlers())
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	path, pageID, err := automatorCurrentPage()
	if err != nil {
		t.Fatalf("automatorCurrentPage: %v", err)
	}
	if path != "pages/index/index" {
		t.Errorf("path = %q, want %q", path, "pages/index/index")
	}
	if pageID != 1 {
		t.Errorf("pageID = %d, want 1", pageID)
	}
}

// --- Context-aware call tests ---

// TestContractCallCancellation asserts that a call respects context
// cancellation and returns ErrCanceled.
func TestContractCallCancellation(t *testing.T) {
	handlers := standardFakeHandlers()
	// Handler that blocks forever.
	handlers["App.getCurrentPage"] = func(_ json.RawMessage) (any, error) {
		select {} // block forever
	}
	fake := newFakeWxServer(t, handlers)
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	err := automatorCallContext(ctx, "App.getCurrentPage", map[string]any{}, nil)
	if err == nil {
		t.Fatal("expected error from canceled call, got nil")
	}
	te, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("expected *ToolError, got %T: %v", err, err)
	}
	if te.Code != ErrCanceled {
		t.Errorf("error code = %s, want %s", te.Code, ErrCanceled)
	}
}

// TestContractCallTimeout asserts that a call respects a deadline and
// returns ErrTimeout.
func TestContractCallTimeout(t *testing.T) {
	handlers := standardFakeHandlers()
	handlers["App.getCurrentPage"] = func(_ json.RawMessage) (any, error) {
		time.Sleep(5 * time.Second)
		return fakeCurrentPageResult(1, "pages/index"), nil
	}
	fake := newFakeWxServer(t, handlers)
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := automatorCallContext(ctx, "App.getCurrentPage", map[string]any{}, nil)
	if err == nil {
		t.Fatal("expected error from timed-out call, got nil")
	}
	te, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("expected *ToolError, got %T: %v", err, err)
	}
	if te.Code != ErrTimeout {
		t.Errorf("error code = %s, want %s", te.Code, ErrTimeout)
	}
}

// TestContractCallDisconnect asserts that a call returns
// ErrBrowserDisconnected when the connection closes during a call.
func TestContractCallDisconnect(t *testing.T) {
	handlers := standardFakeHandlers()
	handlers["App.getCurrentPage"] = func(_ json.RawMessage) (any, error) {
		time.Sleep(5 * time.Second)
		return fakeCurrentPageResult(1, "pages/index"), nil
	}
	fake := newFakeWxServer(t, handlers)
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	// Get a connection, then close it during a call.
	conn, err := getAutomatorConn()
	if err != nil {
		t.Fatalf("getAutomatorConn: %v", err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		conn.close()
	}()
	ctx := context.Background()
	err = automatorCallContext(ctx, "App.getCurrentPage", map[string]any{}, nil)
	if err == nil {
		t.Fatal("expected error from disconnected call, got nil")
	}
	te, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("expected *ToolError, got %T: %v", err, err)
	}
	if te.Code != ErrBrowserDisconnected && te.Code != ErrCanceled {
		t.Errorf("error code = %s, want %s or %s", te.Code, ErrBrowserDisconnected, ErrCanceled)
	}
}

// --- Concurrency tests ---

// TestContractConcurrentCalls asserts that multiple concurrent calls
// are correctly correlated by ID and do not cross-wire responses.
func TestContractConcurrentCalls(t *testing.T) {
	handlers := standardFakeHandlers()
	handlers["Page.getData"] = func(p json.RawMessage) (any, error) {
		var params struct {
			PageID int `json:"pageId"`
		}
		json.Unmarshal(p, &params)
		// Return different data per pageId so we can detect cross-wiring.
		return fakeDataResult(map[string]any{"page": params.PageID}), nil
	}
	fake := newFakeWxServer(t, handlers)
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	const N = 20
	var wg sync.WaitGroup
	errs := make([]error, N)
	results := make([]int, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			pageID := idx + 1
			data, err := automatorPageData(pageID, "")
			if err != nil {
				errs[idx] = err
				return
			}
			m, ok := data.(map[string]any)
			if !ok {
				errs[idx] = fmt.Errorf("expected map, got %T", data)
				return
			}
			v, ok := numberValue(m["page"])
			if !ok {
				errs[idx] = fmt.Errorf("missing page field")
				return
			}
			results[idx] = int(v)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d error: %v", i, err)
		}
	}
	for i, got := range results {
		want := i + 1
		if got != want {
			t.Errorf("call %d: page = %d, want %d (response cross-wired!)", i, got, want)
		}
	}
}
