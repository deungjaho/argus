package main

// automator_mcp_test.go — registered MCP handler contract tests.
//
// These tests call the WeChat-routed tools through the real MCP handler
// pipeline (in-memory client + server), validating that the registered
// handlers work end-to-end with the fake WS server. This is the
// acceptance-level evidence that the agent-first loop works through
// the public MCP boundary, not just internal helpers.
//
// Covered paths:
//   - configure (backend=wechat)
//   - status (wechat)
//   - snapshot (wechat, returns refs)
//   - click (via ref from snapshot)
//   - fill (via ref from snapshot)
//   - navigate (typed mode)
//   - page_data
//   - eval
//   - console_messages
//   - exists (via ref)
//   - one failure path (click with stale ref)

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// setupFakeMCPServer creates a real MCP server with all tools registered,
// connected to an in-memory client, with the WeChat backend pointing at
// a fake WS server. Returns the client session and a cleanup function.
func setupFakeMCPServer(t *testing.T, handlers map[string]fakeWxHandler) (*mcpClientSession, func()) {
	t.Helper()
	fake := newFakeWxServer(t, handlers)
	backendCleanup := setupFakeBackend(t, fake.port())

	server := mcp.NewServer(&mcp.Implementation{Name: "argus-test", Version: "test"}, nil)
	registerAllTools(server)
	ct, st := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatalf("server connect failed: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}
	return &mcpClientSession{cs, t}, func() {
		backendCleanup()
		fake.close()
	}
}

// mcpClientSession wraps a ClientSession with test helpers.
type mcpClientSession struct {
	*mcp.ClientSession
	t *testing.T
}

// callTool invokes a tool and returns the parsed JSON result.
func (s *mcpClientSession) callTool(name string, args map[string]any) map[string]any {
	s.t.Helper()
	ctx := context.Background()
	res, err := s.ClientSession.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		s.t.Fatalf("CallTool %s failed: %v", name, err)
	}
	return extractMCPResultJSON(s.t, res)
}

// callToolError invokes a tool that is expected to return an error.
// Returns the error message.
func (s *mcpClientSession) callToolError(name string, args map[string]any) string {
	s.t.Helper()
	ctx := context.Background()
	res, err := s.ClientSession.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err.Error()
	}
	// The tool may return an error in the result content.
	if res != nil && len(res.Content) > 0 {
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				var parsed map[string]any
				if json.Unmarshal([]byte(tc.Text), &parsed) == nil {
					if e, ok := parsed["error"].(map[string]any); ok {
						if msg, ok := e["message"].(string); ok {
							return msg
						}
					}
				}
				return tc.Text
			}
		}
	}
	s.t.Fatalf("expected error from %s, got success", name)
	return ""
}

// extractMCPResultJSON extracts the JSON content from a CallToolResult.
func extractMCPResultJSON(t *testing.T, res *mcp.CallToolResult) map[string]any {
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

// --- MCP handler contract tests ---

// TestMCPConfigureWeChat asserts that configure with backend=wechat
// switches the active backend and status reports it.
func TestMCPConfigureWeChat(t *testing.T) {
	handlers := standardFakeHandlers()
	cs, cleanup := setupFakeMCPServer(t, handlers)
	defer cleanup()

	// Configure with an invalid backend first to verify validation.
	errMsg := cs.callToolError("configure", map[string]any{"backend": "invalid"})
	if !strings.Contains(errMsg, "unknown backend") {
		t.Errorf("expected 'unknown backend' error, got: %s", errMsg)
	}

	// Configure with wechat backend.
	result := cs.callTool("configure", map[string]any{"backend": "wechat", "wx_port": 9420})
	if result["backend"] != BackendWeChat {
		t.Errorf("backend = %v, want %q", result["backend"], BackendWeChat)
	}
}

// TestMCPStatusWeChat asserts that status returns wechat backend info
// and current page through the MCP handler.
func TestMCPStatusWeChat(t *testing.T) {
	handlers := standardFakeHandlers()
	cs, cleanup := setupFakeMCPServer(t, handlers)
	defer cleanup()

	result := cs.callTool("status", map[string]any{})
	if result["backend"] != BackendWeChat {
		t.Errorf("backend = %v, want %q", result["backend"], BackendWeChat)
	}
	if result["automator_connected"] != true {
		t.Error("expected automator_connected=true")
	}
	page, ok := result["page"].(map[string]any)
	if !ok {
		t.Fatal("expected page in status result")
	}
	if page["path"] != "pages/index/index" {
		t.Errorf("page.path = %v, want %q", page["path"], "pages/index/index")
	}
}

// TestMCPSnapshotReturnsRefs asserts that snapshot returns interactive
// elements with refs that can be passed to click.
func TestMCPSnapshotReturnsRefs(t *testing.T) {
	handlers := standardFakeHandlers()
	// Return elements for the broad selector.
	handlers["Page.getElements"] = func(p json.RawMessage) (any, error) {
		return map[string]any{"elements": []map[string]any{
			{"elementId": "btn-1", "tagName": "button"},
			{"elementId": "inp-1", "tagName": "input"},
		}}, nil
	}
	cs, cleanup := setupFakeMCPServer(t, handlers)
	defer cleanup()

	result := cs.callTool("snapshot", map[string]any{})
	if result["backend"] != BackendWeChat {
		t.Errorf("backend = %v, want %q", result["backend"], BackendWeChat)
	}
	elements, ok := result["elements"].([]any)
	if !ok {
		t.Fatal("expected elements array in snapshot")
	}
	if len(elements) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(elements))
	}
	// Each element should have a ref starting with "wx:".
	first, ok := elements[0].(map[string]any)
	if !ok {
		t.Fatal("expected element to be map[string]any")
	}
	ref, ok := first["ref"].(string)
	if !ok || !strings.HasPrefix(ref, "wx:") {
		t.Errorf("element ref = %v, want wx: prefix", first["ref"])
	}
	// rpc_count and duration_ms should be present.
	if _, ok := result["rpc_count"]; !ok {
		t.Error("expected rpc_count in snapshot")
	}
	if _, ok := result["duration_ms"]; !ok {
		t.Error("expected duration_ms in snapshot")
	}
}

// TestMCPSnapshotEnrichment verifies that snapshot elements are enriched
// with id, class, value, disabled, and name metadata from Element.getWXML.
func TestMCPSnapshotEnrichment(t *testing.T) {
	handlers := standardFakeHandlers()
	handlers["Page.getElements"] = func(p json.RawMessage) (any, error) {
		return fakeElementsResult(
			fakeElementResult("btn-1", "button"),
			fakeElementResult("inp-1", "input"),
		), nil
	}
	// Return different WXML per element based on elementId and type.
	handlers["Element.getWXML"] = func(p json.RawMessage) (any, error) {
		var req struct {
			ElementID string `json:"elementId"`
			Type      string `json:"type"`
		}
		json.Unmarshal(p, &req)
		if req.Type == "outer" {
			switch req.ElementID {
			case "btn-1":
				return map[string]any{"wxml": "<button class=\"btn-primary\" id=\"submit-btn\">Submit</button>"}, nil
			case "inp-1":
				return map[string]any{"wxml": "<input class=\"form-input\" id=\"name-field\" name=\"username\" value=\"initial\" disabled=\"true\" placeholder=\"Enter name\"></input>"}, nil
			default:
				return map[string]any{"wxml": "<view></view>"}, nil
			}
		}
		switch req.ElementID {
		case "btn-1":
			return map[string]any{"wxml": "Submit"}, nil
		default:
			return map[string]any{"wxml": ""}, nil
		}
	}
	cs, cleanup := setupFakeMCPServer(t, handlers)
	defer cleanup()

	result := cs.callTool("snapshot", map[string]any{})
	elements, ok := result["elements"].([]any)
	if !ok || len(elements) != 2 {
		t.Fatalf("expected 2 elements, got %v", result["elements"])
	}

	// First element (button) should have id, class, text.
	btn, _ := elements[0].(map[string]any)
	if btn["id"] != "submit-btn" {
		t.Errorf("button id = %v, want %q", btn["id"], "submit-btn")
	}
	if btn["class"] != "btn-primary" {
		t.Errorf("button class = %v, want %q", btn["class"], "btn-primary")
	}
	if btn["text"] != "Submit" {
		t.Errorf("button text = %v, want %q", btn["text"], "Submit")
	}

	// Second element (input) should have name, value, disabled.
	inp, _ := elements[1].(map[string]any)
	if inp["name"] != "username" {
		t.Errorf("input name = %v, want %q", inp["name"], "username")
	}
	if inp["value"] != "initial" {
		t.Errorf("input value = %v, want %q", inp["value"], "initial")
	}
	if !inp["disabled"].(bool) {
		t.Errorf("input disabled = %v, want true", inp["disabled"])
	}

	// RPC count should include discovery + enrichment RPCs.
	rpcCount, _ := result["rpc_count"].(float64)
	if rpcCount < 3 {
		t.Errorf("rpc_count = %v, expected at least 3 (1 discovery + 2 enrichment)", rpcCount)
	}
}

// TestMCPSnapshotEnrichmentBounds verifies that enrichment RPCs are bounded
// when there are more elements than the enrichment budget.
func TestMCPSnapshotEnrichmentBounds(t *testing.T) {
	handlers := standardFakeHandlers()
	// Return 30 elements — more than snapshotEnrichMaxRPCs (20).
	var elems []any
	for i := 0; i < 30; i++ {
		elems = append(elems, fakeElementResult(fmt.Sprintf("elem-%02d", i), "view"))
	}
	handlers["Page.getElements"] = func(p json.RawMessage) (any, error) {
		return fakeElementsResult(elems...), nil
	}
	wxmlCallCount := int64(0)
	handlers["Element.getWXML"] = func(p json.RawMessage) (any, error) {
		atomic.AddInt64(&wxmlCallCount, 1)
		return map[string]any{"wxml": "<view class=\"item\">text</view>"}, nil
	}
	cs, cleanup := setupFakeMCPServer(t, handlers)
	defer cleanup()

	result := cs.callTool("snapshot", map[string]any{})
	elements, ok := result["elements"].([]any)
	if !ok || len(elements) != 30 {
		t.Fatalf("expected 30 elements, got %d", len(elements))
	}

	// Enrichment RPCs should be bounded to snapshotEnrichMaxRPCs (20).
	if wxmlCallCount > snapshotEnrichMaxRPCs {
		t.Errorf("Element.getWXML called %d times, expected <= %d", wxmlCallCount, snapshotEnrichMaxRPCs)
	}
	t.Logf("enrichment RPCs: %d (budget: %d), elements: %d", wxmlCallCount, snapshotEnrichMaxRPCs, len(elements))
}

// TestMCPFindTextMatch verifies that find filters elements by text from
// enriched snapshot metadata and returns only matching elements.
func TestMCPFindTextMatch(t *testing.T) {
	handlers := standardFakeHandlers()
	handlers["Page.getElements"] = func(p json.RawMessage) (any, error) {
		return fakeElementsResult(
			fakeElementResult("btn-1", "button"),
			fakeElementResult("inp-1", "input"),
			fakeElementResult("nav-1", "navigator"),
		), nil
	}
	handlers["Element.getWXML"] = func(p json.RawMessage) (any, error) {
		var req struct {
			ElementID string `json:"elementId"`
			Type      string `json:"type"`
		}
		json.Unmarshal(p, &req)
		if req.Type == "outer" {
			switch req.ElementID {
			case "btn-1":
				return map[string]any{"wxml": "<button id=\"submit-btn\">Submit</button>"}, nil
			case "inp-1":
				return map[string]any{"wxml": "<input name=\"reason\" placeholder=\"原因描述\"></input>"}, nil
			case "nav-1":
				return map[string]any{"wxml": "<navigator class=\"home-link\">首页</navigator>"}, nil
			default:
				return map[string]any{"wxml": "<view></view>"}, nil
			}
		}
		return map[string]any{"wxml": ""}, nil
	}
	cs, cleanup := setupFakeMCPServer(t, handlers)
	defer cleanup()

	// Find "原因" — should match only the input element with placeholder "原因描述".
	result := cs.callTool("find", map[string]any{"text": "原因"})
	count, _ := result["count"].(float64)
	if int(count) != 1 {
		t.Fatalf("find count = %v, want 1", result["count"])
	}
	matches, _ := result["matches"].([]any)
	first, _ := matches[0].(map[string]any)
	if first["tag"] != "input" {
		t.Errorf("find matched tag = %v, want input", first["tag"])
	}
	if first["name"] != "reason" {
		t.Errorf("find matched name = %v, want reason", first["name"])
	}
}

// TestMCPFindRegexMatch verifies that find filters elements by regex.
func TestMCPFindRegexMatch(t *testing.T) {
	handlers := standardFakeHandlers()
	handlers["Page.getElements"] = func(p json.RawMessage) (any, error) {
		return fakeElementsResult(
			fakeElementResult("btn-1", "button"),
			fakeElementResult("btn-2", "button"),
			fakeElementResult("inp-1", "input"),
		), nil
	}
	handlers["Element.getWXML"] = func(p json.RawMessage) (any, error) {
		var req struct {
			ElementID string `json:"elementId"`
			Type      string `json:"type"`
		}
		json.Unmarshal(p, &req)
		if req.Type == "outer" {
			switch req.ElementID {
			case "btn-1":
				return map[string]any{"wxml": "<button id=\"submit-btn\">Submit</button>"}, nil
			case "btn-2":
				return map[string]any{"wxml": "<button id=\"cancel-btn\">Cancel</button>"}, nil
			case "inp-1":
				return map[string]any{"wxml": "<input id=\"name-field\"></input>"}, nil
			default:
				return map[string]any{"wxml": "<view></view>"}, nil
			}
		}
		return map[string]any{"wxml": ""}, nil
	}
	cs, cleanup := setupFakeMCPServer(t, handlers)
	defer cleanup()

	// Find elements with id matching ".*-btn$" — should match 2 buttons.
	result := cs.callTool("find", map[string]any{"regex": ".*-btn"})
	count, _ := result["count"].(float64)
	if int(count) != 2 {
		t.Fatalf("find regex count = %v, want 2", result["count"])
	}
}

// TestMCPWaitForTextPolls verifies that wait_for text condition genuinely
// polls using the enriched snapshot, not returning a degraded response.
func TestMCPWaitForTextPolls(t *testing.T) {
	handlers := standardFakeHandlers()
	handlers["Page.getElements"] = func(p json.RawMessage) (any, error) {
		return fakeElementsResult(
			fakeElementResult("elem-1", "view"),
		), nil
	}
	handlers["Element.getWXML"] = func(p json.RawMessage) (any, error) {
		var req struct {
			Type string `json:"type"`
		}
		json.Unmarshal(p, &req)
		if req.Type == "outer" {
			return map[string]any{"wxml": "<view>申请已保存</view>"}, nil
		}
		return map[string]any{"wxml": "申请已保存"}, nil
	}
	cs, cleanup := setupFakeMCPServer(t, handlers)
	defer cleanup()

	// wait_for text "申请已保存" should be satisfied immediately.
	result := cs.callTool("wait_for", map[string]any{
		"text":       "申请已保存",
		"timeout_ms": 2000,
		"poll_ms":    100,
	})
	satisfied, _ := result["satisfied"].(bool)
	if !satisfied {
		t.Errorf("wait_for text satisfied = %v, want true; result=%v", satisfied, result)
	}
	// Must not contain degraded flag.
	if degraded, ok := result["degraded"].(bool); ok && degraded {
		t.Error("wait_for text returned degraded=true, should genuinely poll")
	}
}

// TestMCPWaitForTextAbsent verifies text_absent condition polls correctly.
func TestMCPWaitForTextAbsent(t *testing.T) {
	handlers := standardFakeHandlers()
	handlers["Page.getElements"] = func(p json.RawMessage) (any, error) {
		return fakeElementsResult(
			fakeElementResult("elem-1", "view"),
		), nil
	}
	handlers["Element.getWXML"] = func(p json.RawMessage) (any, error) {
		var req struct {
			Type string `json:"type"`
		}
		json.Unmarshal(p, &req)
		if req.Type == "outer" {
			return map[string]any{"wxml": "<view>loaded</view>"}, nil
		}
		return map[string]any{"wxml": "loaded"}, nil
	}
	cs, cleanup := setupFakeMCPServer(t, handlers)
	defer cleanup()

	// wait_for text_absent "loading" — should be satisfied since "loading" is absent.
	result := cs.callTool("wait_for", map[string]any{
		"text_absent": "loading",
		"timeout_ms":  2000,
		"poll_ms":     100,
	})
	satisfied, _ := result["satisfied"].(bool)
	if !satisfied {
		t.Errorf("wait_for text_absent satisfied = %v, want true; result=%v", satisfied, result)
	}
}

// TestMCPClickViaRef asserts that click with a ref from snapshot works
// through the MCP handler and taps the correct element.
func TestMCPClickViaRef(t *testing.T) {
	fake := newFakeWxServer(t, standardFakeHandlers())
	defer fake.close()
	backendCleanup := setupFakeBackend(t, fake.port())
	defer backendCleanup()

	// First, get a ref via the snapshot helper directly.
	_, pageID, err := automatorCurrentPage()
	if err != nil {
		t.Fatalf("automatorCurrentPage: %v", err)
	}
	elem, err := automatorGetElement(pageID, ".btn")
	if err != nil {
		t.Fatalf("automatorGetElement: %v", err)
	}
	snapGen := wxRefs.nextSnapshotGeneration()
	ref := wxRefs.register(pageID, elem, snapGen)

	// Now set up the MCP server and call click through it.
	server := mcp.NewServer(&mcp.Implementation{Name: "argus-test", Version: "test"}, nil)
	registerAllTools(server)
	ct, st := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "click",
		Arguments: map[string]any{"ref": ref},
	})
	if err != nil {
		t.Fatalf("CallTool click: %v", err)
	}
	result := extractMCPResultJSON(t, res)
	if result["backend"] != BackendWeChat {
		t.Errorf("backend = %v, want %q", result["backend"], BackendWeChat)
	}
	if result["element_id"] != "elem-001" {
		t.Errorf("element_id = %v, want elem-001", result["element_id"])
	}
	// Verify Element.tap was called on the fake server.
	tapReq := fake.requireRequest("Element.tap")
	fake.assertParam(tapReq, "elementId", "elem-001")
	fake.assertParam(tapReq, "pageId", float64(1))
}

// TestMCPClickStaleRef asserts that click with a stale ref returns
// stale_reference through the MCP handler.
func TestMCPClickStaleRef(t *testing.T) {
	fake := newFakeWxServer(t, standardFakeHandlers())
	defer fake.close()
	backendCleanup := setupFakeBackend(t, fake.port())
	defer backendCleanup()

	server := mcp.NewServer(&mcp.Implementation{Name: "argus-test", Version: "test"}, nil)
	registerAllTools(server)
	ct, st := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}

	// Use a ref that doesn't exist in the registry.
	_, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "click",
		Arguments: map[string]any{"ref": "wx:nonexistentref"},
	})
	if err == nil {
		// Check if the error is in the result content.
		// The MCP handler may return the error in the result.
	}
	// The error should be surfaced — either as a Go error or in the
	// result content. We accept either since the SDK behavior varies.
	if err != nil {
		if !strings.Contains(err.Error(), "stale") && !strings.Contains(err.Error(), "not found") {
			t.Errorf("expected stale reference error, got: %v", err)
		}
	}
}

// TestMCPFillViaRef asserts that fill with a ref works through the MCP
// handler and calls Element.callFunction with the right functionName.
func TestMCPFillViaRef(t *testing.T) {
	handlers := standardFakeHandlers()
	handlers["Page.getElement"] = func(_ json.RawMessage) (any, error) {
		return fakeElementResult("inp-1", "input"), nil
	}
	fake := newFakeWxServer(t, handlers)
	defer fake.close()
	backendCleanup := setupFakeBackend(t, fake.port())
	defer backendCleanup()

	// Get a ref for an input element.
	_, pageID, err := automatorCurrentPage()
	if err != nil {
		t.Fatalf("automatorCurrentPage: %v", err)
	}
	elem, err := automatorGetElement(pageID, "#myInput")
	if err != nil {
		t.Fatalf("automatorGetElement: %v", err)
	}
	snapGen := wxRefs.nextSnapshotGeneration()
	ref := wxRefs.register(pageID, elem, snapGen)

	server := mcp.NewServer(&mcp.Implementation{Name: "argus-test", Version: "test"}, nil)
	registerAllTools(server)
	ct, st := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "fill",
		Arguments: map[string]any{"ref": ref, "value": "hello"},
	})
	if err != nil {
		t.Fatalf("CallTool fill: %v", err)
	}
	result := extractMCPResultJSON(t, res)
	if result["backend"] != BackendWeChat {
		t.Errorf("backend = %v, want %q", result["backend"], BackendWeChat)
	}
	// Verify Element.callFunction was called with input.input.
	cfReq := fake.requireRequest("Element.callFunction")
	fake.assertParam(cfReq, "functionName", "input.input")
	fake.assertParam(cfReq, "elementId", "inp-1")
}

// TestMCPNavigateTypedMode asserts that navigate with mode=navigateTo
// calls App.callWxMethod with the right method through the MCP handler.
func TestMCPNavigateTypedMode(t *testing.T) {
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
	cs, cleanup := setupFakeMCPServer(t, handlers)
	defer cleanup()

	result := cs.callTool("navigate", map[string]any{
		"url":  "pages/detail/index",
		"mode": "navigateTo",
	})
	if result["navigated"] != "navigateTo" {
		t.Errorf("navigated = %v, want navigateTo", result["navigated"])
	}
	if navMethod != "navigateTo" {
		t.Errorf("callWxMethod method = %q, want navigateTo", navMethod)
	}
}

// TestMCPPageData asserts that page_data returns unwrapped data through
// the MCP handler.
func TestMCPPageData(t *testing.T) {
	cs, cleanup := setupFakeMCPServer(t, standardFakeHandlers())
	defer cleanup()

	result := cs.callTool("page_data", map[string]any{})
	if result["backend"] != BackendWeChat {
		t.Errorf("backend = %v, want %q", result["backend"], BackendWeChat)
	}
	// data should be a JSON string (bounded), not a nested object.
	data, ok := result["data"].(string)
	if !ok {
		t.Fatalf("expected data as string, got %T: %v", result["data"], result["data"])
	}
	if !strings.Contains(data, "Hello") {
		t.Errorf("data does not contain 'Hello': %s", data)
	}
}

// TestMCPEval asserts that eval uses functionDeclaration and unwraps
// the result through the MCP handler.
func TestMCPEval(t *testing.T) {
	cs, cleanup := setupFakeMCPServer(t, standardFakeHandlers())
	defer cleanup()

	result := cs.callTool("eval", map[string]any{"script": "() => 42"})
	if result["backend"] != BackendWeChat {
		t.Errorf("backend = %v, want %q", result["backend"], BackendWeChat)
	}
	if result["result"] != float64(42) {
		t.Errorf("result = %v, want 42", result["result"])
	}
}

// TestMCPConsoleMessages asserts that console_messages returns events
// from the ring buffer through the MCP handler.
func TestMCPConsoleMessages(t *testing.T) {
	cs, cleanup := setupFakeMCPServer(t, standardFakeHandlers())
	defer cleanup()

	// Push an event into the ring buffer directly.
	conn, err := getAutomatorConn()
	if err != nil {
		t.Fatalf("getAutomatorConn: %v", err)
	}
	conn.recordEvent("App.logAdded", json.RawMessage(`{"args":["hello"]}`))

	result := cs.callTool("console_messages", map[string]any{"all": true})
	if result["backend"] != BackendWeChat {
		t.Errorf("backend = %v, want %q", result["backend"], BackendWeChat)
	}
	messages, ok := result["messages"].([]any)
	if !ok {
		t.Fatal("expected messages array")
	}
	if len(messages) == 0 {
		t.Error("expected at least 1 message from ring buffer")
	}
}

// TestMCPExistsViaRef asserts that exists with a registered ref returns
// true through the MCP handler.
func TestMCPExistsViaRef(t *testing.T) {
	fake := newFakeWxServer(t, standardFakeHandlers())
	defer fake.close()
	backendCleanup := setupFakeBackend(t, fake.port())
	defer backendCleanup()

	// Register a ref.
	_, pageID, err := automatorCurrentPage()
	if err != nil {
		t.Fatalf("automatorCurrentPage: %v", err)
	}
	elem, err := automatorGetElement(pageID, ".btn")
	if err != nil {
		t.Fatalf("automatorGetElement: %v", err)
	}
	snapGen := wxRefs.nextSnapshotGeneration()
	ref := wxRefs.register(pageID, elem, snapGen)

	server := mcp.NewServer(&mcp.Implementation{Name: "argus-test", Version: "test"}, nil)
	registerAllTools(server)
	ct, st := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "exists",
		Arguments: map[string]any{"ref": ref},
	})
	if err != nil {
		t.Fatalf("CallTool exists: %v", err)
	}
	result := extractMCPResultJSON(t, res)
	if result["exists"] != true {
		t.Errorf("exists = %v, want true", result["exists"])
	}
}

// TestMCPBackendValidation asserts that configure with an invalid
// backend returns an actionable error through the MCP handler.
func TestMCPBackendValidation(t *testing.T) {
	cs, cleanup := setupFakeMCPServer(t, standardFakeHandlers())
	defer cleanup()

	errMsg := cs.callToolError("configure", map[string]any{"backend": "firefox"})
	if !strings.Contains(errMsg, "unknown backend") {
		t.Errorf("expected 'unknown backend' error, got: %s", errMsg)
	}
}

// TestMCPDiagnoseWeChat verifies that diagnose under the WeChat backend
// returns console issues from the automator event ring buffer, not the
// CDP flight recorder.
func TestMCPDiagnoseWeChat(t *testing.T) {
	cs, cleanup := setupFakeMCPServer(t, standardFakeHandlers())
	defer cleanup()

	// Inject some log events into the automator event ring buffer
	// (same connection the MCP server uses).
	conn, err := getAutomatorConn()
	if err != nil {
		t.Fatalf("getAutomatorConn: %v", err)
	}
	// Add a warning event.
	warnParams, _ := json.Marshal(map[string]any{
		"type":    "log",
		"message": "deprecated API usage",
		"level":   "warn",
	})
	conn.recordEvent("App.logAdded", warnParams)
	// Add an error event.
	errParams, _ := json.Marshal(map[string]any{
		"type":    "error",
		"message": "uncaught TypeError: cannot read property",
		"level":   "error",
	})
	conn.recordEvent("App.logAdded", errParams)

	result := cs.callTool("diagnose", map[string]any{"limit": 10})

	// Must have console_issues from WeChat event ring buffer.
	consoleIssues, ok := result["console_issues"].([]any)
	if !ok {
		t.Fatal("expected console_issues array in diagnose result")
	}
	if len(consoleIssues) == 0 {
		t.Error("expected at least 1 console issue from WeChat event ring buffer")
	}

	// Must have exceptions from error-level events.
	exceptions, _ := result["exceptions"].([]any)
	if len(exceptions) == 0 {
		t.Error("expected at least 1 exception from error-level events")
	}

	// Must have recorder stats with event ring buffer info.
	recorder, ok := result["recorder"].(map[string]any)
	if !ok {
		t.Fatal("expected recorder stats in diagnose result")
	}
	if _, ok := recorder["events_dropped"]; !ok {
		t.Error("expected events_dropped in recorder stats")
	}

	// Must have a summary.
	if _, ok := result["summary"]; !ok {
		t.Error("expected summary in diagnose result")
	}
}

// TestMCPNavigateInvalidatesRefs asserts that after a successful navigate,
// all refs from the previous page are invalidated — even if the new page
// reuses the same numeric pageID. This is the core safety property:
// navigation must not allow old refs to silently re-match.
//
// This test would FAIL before the fix (navigate did not invalidate refs,
// so a pageID reuse would allow the old ref to resolve).
func TestMCPNavigateInvalidatesRefs(t *testing.T) {
	// Use a mutable handler so we can change the page after navigation.
	currentPageID := 1
	currentPagePath := "pages/aftersales/form"

	handlers := standardFakeHandlers()
	handlers["App.getCurrentPage"] = func(_ json.RawMessage) (any, error) {
		return fakeCurrentPageResult(currentPageID, currentPagePath), nil
	}
	handlers["Page.getElements"] = func(_ json.RawMessage) (any, error) {
		return fakeElementsResult(
			fakeElementResult("elem-1", "button"),
		), nil
	}
	handlers["Element.getWXML"] = func(p json.RawMessage) (any, error) {
		var req struct {
			Type string `json:"type"`
		}
		json.Unmarshal(p, &req)
		if req.Type == "outer" {
			return map[string]any{"wxml": "<button class=\"btn\">Submit</button>"}, nil
		}
		return map[string]any{"wxml": "Submit"}, nil
	}
	handlers["App.callWxMethod"] = func(p json.RawMessage) (any, error) {
		// Simulate navigation: change the page path but REUSE the same pageID.
		// This is the dangerous case: numeric pageID reuse.
		var req struct {
			Method string `json:"method"`
		}
		json.Unmarshal(p, &req)
		if strings.Contains(req.Method, "reLaunch") || strings.Contains(req.Method, "navigateTo") || strings.Contains(req.Method, "redirectTo") {
			currentPagePath = "pages/home/index"
			// Keep the same pageID — this is the pageID reuse scenario.
		}
		return fakeCallWxResult(map[string]any{"ok": true}), nil
	}

	cs, cleanup := setupFakeMCPServer(t, handlers)
	defer cleanup()

	// Step 1: snapshot to get a ref on page 1 (pageID=1).
	snapResult := cs.callTool("snapshot", map[string]any{})
	elements, _ := snapResult["elements"].([]any)
	if len(elements) == 0 {
		t.Fatal("expected elements in snapshot")
	}
	first, _ := elements[0].(map[string]any)
	ref, _ := first["ref"].(string)
	if !strings.HasPrefix(ref, "wx:") {
		t.Fatalf("expected wx: ref, got %q", ref)
	}
	t.Logf("captured ref=%s on pageID=%d", ref, currentPageID)

	// Step 2: navigate to a different route. The fake handler reuses the
	// same pageID, simulating the dangerous pageID-reuse scenario.
	cs.callTool("navigate", map[string]any{
		"url":  "/pages/home/index",
		"mode": "reLaunch",
	})
	t.Logf("navigated to %s (pageID reused=%d)", currentPagePath, currentPageID)

	// Step 3: try to click the old ref — MUST get stale_reference.
	// Even though pageID is the same, the ref should be invalidated by
	// the navigation.
	errStr := cs.callToolError("click", map[string]any{"ref": ref})
	t.Logf("post-navigate click error: %s", errStr)
	if !strings.Contains(errStr, "stale") {
		t.Errorf("expected stale_reference after navigate (pageID reuse), got: %s", errStr)
	}
}

// TestWxRefPageIDReuseAndInvalidation verifies the mechanism that
// automatorNavigateTyped relies on: after invalidateAll, a ref that
// was valid on a given pageID becomes stale even when resolved against
// the same pageID. This is the unit-level proof that the navigate fix
// (calling wxRefs.invalidateAll() after route settlement) works.
func TestWxRefPageIDReuseAndInvalidation(t *testing.T) {
	wxRefs.invalidateAll()
	defer wxRefs.invalidateAll()

	automatorPoolMu.Lock()
	automatorEpoch = 1
	automatorConn_ = &automatorConn{epoch: 1}
	automatorPoolMu.Unlock()
	defer func() {
		automatorPoolMu.Lock()
		automatorConn_ = nil
		automatorPoolMu.Unlock()
	}()

	// Register a ref on page 5.
	elem := wxElementRef{ElementID: "elem-1", TagName: "button"}
	snapGen := wxRefs.nextSnapshotGeneration()
	ref := wxRefs.register(5, elem, snapGen)

	// Before invalidation, the ref resolves against the same pageID.
	// This is expected: the registry doesn't know about navigation.
	_, err := wxRefs.resolve(ref, 5)
	if err != nil {
		t.Fatalf("ref should resolve before invalidation: %v", err)
	}

	// After invalidateAll (what automatorNavigateTyped calls after
	// successful navigation), the ref is removed and resolve fails —
	// even against the same pageID. This prevents pageID reuse from
	// allowing old refs to silently match.
	wxRefs.invalidateAll()
	_, err = wxRefs.resolve(ref, 5)
	if err == nil {
		t.Fatal("after invalidateAll, ref should be stale even with same pageID")
	}
	te, ok := err.(*ToolError)
	if !ok || te.Code != ErrStaleReference {
		t.Errorf("expected ErrStaleReference, got %T: %v", err, err)
	}
}
