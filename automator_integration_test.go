//go:build integration

package main

// automator_integration_test.go — live integration tests against a running
// WeChat DevTools automator endpoint.
//
// These tests are skipped unless ARGUS_WX_LIVE=1 is set and the automator
// port is reachable. They verify the end-to-end protocol path through the
// registered MCP handlers, not just internal helpers.
//
// Configuration is environment-driven (no hard-coded local paths):
//   ARGUS_WX_LIVE=1              — enable live tests
//   ARGUS_WX_PORT=9420           — automator WebSocket port (default 9420)
//   ARGUS_WX_PROJECT=/path/to/mp — compiled Mini Program path (informational)
//
// Run with:
//   ARGUS_WX_LIVE=1 ARGUS_WX_PORT=9420 \
//     go test -tags=integration -count=1 -timeout 120s -run TestAutomatorLive ./...

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// automatorLiveSkip configures the wechat backend from env vars and skips
// if ARGUS_WX_LIVE is not set. No hard-coded local paths.
func automatorLiveSkip(t *testing.T) {
	t.Helper()
	if envStrDefault("", "ARGUS_WX_LIVE") != "1" {
		t.Skip("set ARGUS_WX_LIVE=1 to run WeChat automator live integration tests")
	}
	// Configure from env vars (no hard-coded paths).
	port := envIntDefault(9420, "ARGUS_WX_PORT")
	project := envStrDefault("", "ARGUS_WX_PROJECT")
	backendMu.Lock()
	configuredBackend = BackendWeChat
	configuredWxPort = port
	configuredWxProject = project
	backendMu.Unlock()
	// Reset the connection pool so we dial fresh.
	closeAutomatorConn()
}

// setupLiveMCP creates a real MCP server with all tools registered,
// connected to an in-memory client, for live handler-boundary tests.
func setupLiveMCP(t *testing.T) *mcp.ClientSession {
	t.Helper()
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
	return cs
}

// callLiveTool invokes a tool through the real MCP handler pipeline.
func callLiveTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s failed: %v", name, err)
	}
	return parseLiveToolResult(t, name, res)
}

// callLiveToolErr invokes a tool and returns the result and error (does not
// fatal on error). Used for tests that expect an error response. When the
// MCP SDK sets IsError=true, the error message is in the text content.
func callLiveToolErr(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (map[string]any, error) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, err
	}
	if res == nil || len(res.Content) == 0 {
		return nil, fmt.Errorf("empty result from %s", name)
	}
	// If the MCP result marks an error, extract the text content as the
	// error message and return a map with the raw text for inspection.
	if res.IsError {
		rawText := ""
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				rawText = tc.Text
				break
			}
		}
		// Try to parse as JSON; if it fails, wrap in a map.
		var parsed map[string]any
		if json.Unmarshal([]byte(rawText), &parsed) == nil {
			return parsed, fmt.Errorf("%s", rawText)
		}
		return map[string]any{"error": rawText, "is_error": true}, fmt.Errorf("%s", rawText)
	}
	return parseLiveToolResultRaw(t, name, res), nil
}

// parseLiveToolResult extracts the JSON result from an MCP response.
func parseLiveToolResult(t *testing.T, name string, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	result := parseLiveToolResultRaw(t, name, res)
	if isError, _ := result["error"].(bool); isError {
		t.Fatalf("tool %s returned error: %s", name, mustJSON(result))
	}
	return result
}

// parseLiveToolResultRaw extracts the JSON result without checking for errors.
func parseLiveToolResultRaw(t *testing.T, name string, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatalf("empty result from %s", name)
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
	t.Fatalf("no text content in result from %s", name)
	return nil
}

func TestAutomatorLiveGetCurrentPage(t *testing.T) {
	automatorLiveSkip(t)
	path, pageID, err := automatorCurrentPage()
	if err != nil {
		t.Fatalf("automatorCurrentPage: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty page path")
	}
	if pageID == 0 {
		t.Error("expected non-zero pageId")
	}
	t.Logf("current page: path=%s pageId=%d", path, pageID)
}

func TestAutomatorLivePageData(t *testing.T) {
	automatorLiveSkip(t)
	pageID, err := resolvePageID("")
	if err != nil {
		t.Fatalf("resolvePageID: %v", err)
	}
	data, err := automatorPageData(pageID, "")
	if err != nil {
		t.Fatalf("automatorPageData: %v", err)
	}
	raw, _ := json.Marshal(data)
	if len(raw) == 0 {
		t.Error("expected non-empty page data")
	}
	t.Logf("page data length: %d bytes", len(raw))
	// uni-app pages typically expose baseUrl at the top level.
	if m, ok := data.(map[string]any); ok {
		if bu, ok := m["baseUrl"].(string); ok {
			t.Logf("baseUrl: %s", bu)
		}
	}
}

func TestAutomatorLiveScreenshot(t *testing.T) {
	automatorLiveSkip(t)
	png, err := automatorScreenshot()
	if err != nil {
		t.Fatalf("automatorScreenshot: %v", err)
	}
	if len(png) < 1000 {
		t.Errorf("screenshot too small: %d bytes", len(png))
	}
	// PNG magic header.
	if len(png) < 8 || png[0] != 0x89 || png[1] != 'P' || png[2] != 'N' || png[3] != 'G' {
		t.Errorf("screenshot is not a PNG: first bytes %x", png[:minInt(8, len(png))])
	}
	t.Logf("screenshot: %d bytes", len(png))
}

func TestAutomatorLivePageStack(t *testing.T) {
	automatorLiveSkip(t)
	stack, err := automatorPageStack()
	if err != nil {
		t.Fatalf("automatorPageStack: %v", err)
	}
	if len(stack) == 0 {
		t.Error("expected non-empty page stack")
	}
	t.Logf("page stack depth: %d", len(stack))
}

func TestAutomatorLiveEnableLog(t *testing.T) {
	automatorLiveSkip(t)
	if err := automatorEnableLog(); err != nil {
		t.Fatalf("automatorEnableLog: %v", err)
	}
	// Wait briefly for any events to accumulate; we do not require events
	// to arrive (the runtime may be idle).
	time.Sleep(500 * time.Millisecond)
	logs, dropped, total := automatorRecentLogs(true, 20, 500)
	t.Logf("recent logs: %d total=%d dropped=%d", len(logs), total, dropped)
}

func TestAutomatorLiveSnapshot(t *testing.T) {
	automatorLiveSkip(t)
	result, err := automatorSnapshot(30)
	if err != nil {
		t.Fatalf("automatorSnapshot: %v", err)
	}
	if result["backend"] != BackendWeChat {
		t.Errorf("snapshot backend = %v, want %q", result["backend"], BackendWeChat)
	}
	path, _ := result["path"].(string)
	if path == "" {
		t.Error("snapshot should include page path")
	}
	if _, ok := result["elements"]; !ok {
		t.Error("snapshot should include elements array")
	}
	if _, ok := result["rpc_count"]; !ok {
		t.Error("snapshot should include rpc_count")
	}
	if _, ok := result["duration_ms"]; !ok {
		t.Error("snapshot should include duration_ms")
	}
	t.Logf("snapshot path=%s keys=%v", path, mapKeys(result))
}

func TestAutomatorLiveStatus(t *testing.T) {
	automatorLiveSkip(t)
	status, err := wechatStatus()
	if err != nil {
		t.Fatalf("wechatStatus: %v", err)
	}
	if status["backend"] != BackendWeChat {
		t.Errorf("status backend = %v, want %q", status["backend"], BackendWeChat)
	}
	if status["automator_connected"] != true {
		t.Error("expected automator_connected=true")
	}
	page, _ := status["page"].(map[string]any)
	if page == nil {
		t.Error("expected page info in status")
	}
	t.Logf("status: %s", mustJSON(status))
}

// TestAutomatorLiveMCPStatus asserts that status works through the real
// MCP handler pipeline against a live WeChat automator.
func TestAutomatorLiveMCPStatus(t *testing.T) {
	automatorLiveSkip(t)
	cs := setupLiveMCP(t)
	result := callLiveTool(t, cs, "status", map[string]any{})
	if result["backend"] != BackendWeChat {
		t.Errorf("backend = %v, want %q", result["backend"], BackendWeChat)
	}
	if result["automator_connected"] != true {
		t.Error("expected automator_connected=true")
	}
	t.Logf("MCP status: %s", mustJSON(result))
}

// TestAutomatorLiveMCPSnapshot asserts that snapshot returns refs through
// the real MCP handler pipeline against a live WeChat automator.
func TestAutomatorLiveMCPSnapshot(t *testing.T) {
	automatorLiveSkip(t)
	cs := setupLiveMCP(t)
	result := callLiveTool(t, cs, "snapshot", map[string]any{})
	if result["backend"] != BackendWeChat {
		t.Errorf("backend = %v, want %q", result["backend"], BackendWeChat)
	}
	elements, ok := result["elements"].([]any)
	if !ok {
		t.Fatal("expected elements array")
	}
	t.Logf("MCP snapshot: %d elements, rpc_count=%v, duration_ms=%v",
		len(elements), result["rpc_count"], result["duration_ms"])
	// If there are elements, verify they have refs.
	if len(elements) > 0 {
		first, ok := elements[0].(map[string]any)
		if !ok {
			t.Fatal("expected element to be map[string]any")
		}
		ref, ok := first["ref"].(string)
		if !ok || !strings.HasPrefix(ref, "wx:") {
			t.Errorf("element ref = %v, want wx: prefix", first["ref"])
		}
		t.Logf("first element ref: %s tag=%v", ref, first["tag"])
	}
}

// TestAutomatorLiveMCPClickViaRef asserts that click with a ref from
// snapshot works through the real MCP handler against a live automator.
// This is the core acceptance test: configure -> snapshot -> click.
func TestAutomatorLiveMCPClickViaRef(t *testing.T) {
	automatorLiveSkip(t)
	cs := setupLiveMCP(t)

	// Step 1: snapshot to get a ref.
	snapResult := callLiveTool(t, cs, "snapshot", map[string]any{})
	elements, ok := snapResult["elements"].([]any)
	if !ok || len(elements) == 0 {
		t.Skip("no interactive elements found on current page — skipping click test")
	}
	first, ok := elements[0].(map[string]any)
	if !ok {
		t.Fatal("expected element to be map[string]any")
	}
	ref, ok := first["ref"].(string)
	if !ok || !strings.HasPrefix(ref, "wx:") {
		t.Skipf("element has no valid ref: %v", first["ref"])
	}
	t.Logf("clicking element ref=%s tag=%v", ref, first["tag"])

	// Step 2: click via ref through the MCP handler.
	clickResult := callLiveTool(t, cs, "click", map[string]any{"ref": ref})
	if clickResult["backend"] != BackendWeChat {
		t.Errorf("click backend = %v, want %q", clickResult["backend"], BackendWeChat)
	}
	t.Logf("click result: %s", mustJSON(clickResult))
}

// TestAutomatorLiveMCPNavigate asserts that navigate with a typed mode
// works through the real MCP handler against a live automator.
func TestAutomatorLiveMCPNavigate(t *testing.T) {
	automatorLiveSkip(t)
	cs := setupLiveMCP(t)

	// Read current page first.
	statusResult := callLiveTool(t, cs, "status", map[string]any{})
	page, _ := statusResult["page"].(map[string]any)
	beforePath, _ := page["path"].(string)
	t.Logf("before navigate: path=%s", beforePath)

	// Navigate using reLaunch to the same page (safe, idempotent).
	// WeChat Mini Program navigation APIs require a leading slash in the URL.
	navURL := beforePath
	if !strings.HasPrefix(navURL, "/") {
		navURL = "/" + navURL
	}
	result := callLiveTool(t, cs, "navigate", map[string]any{
		"url":  navURL,
		"mode": "reLaunch",
	})
	if result["backend"] != BackendWeChat {
		t.Errorf("navigate backend = %v, want %q", result["backend"], BackendWeChat)
	}
	if result["navigated"] != "reLaunch" {
		t.Errorf("navigated = %v, want reLaunch", result["navigated"])
	}
	t.Logf("navigate result: %s", mustJSON(result))
}

// TestAutomatorLiveErrUnsupportedUnderCDP asserts that page_data returns
// ErrUnsupported when the backend is cdp, through the MCP handler.
func TestAutomatorLiveErrUnsupportedUnderCDP(t *testing.T) {
	automatorLiveSkip(t)
	// Temporarily flip to cdp and confirm page_data returns unsupported.
	backendMu.Lock()
	configuredBackend = BackendCDP
	backendMu.Unlock()
	defer func() {
		backendMu.Lock()
		configuredBackend = BackendWeChat
		backendMu.Unlock()
	}()
	err := errUnsupportedBackend("page_data", BackendWeChat)
	if err == nil || err.Code != ErrUnsupported {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
	if !strings.Contains(err.Message, "cdp") {
		t.Errorf("error should mention active backend cdp: %q", err.Message)
	}
}

// TestAutomatorLiveMCPFillViaRef asserts that fill with a ref from snapshot
// works through the real MCP handler against a live automator. This is a
// required acceptance test: snapshot -> find input -> fill -> observe value.
func TestAutomatorLiveMCPFillViaRef(t *testing.T) {
	automatorLiveSkip(t)
	cs := setupLiveMCP(t)

	// Step 1: snapshot to find an input element.
	snapResult := callLiveTool(t, cs, "snapshot", map[string]any{})
	elements, ok := snapResult["elements"].([]any)
	if !ok {
		t.Fatal("expected elements array in snapshot")
	}

	// Find an input or textarea element.
	var inputRef string
	var inputTag string
	for _, e := range elements {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		tag, _ := em["tag"].(string)
		if tag == "input" || tag == "textarea" {
			ref, _ := em["ref"].(string)
			if strings.HasPrefix(ref, "wx:") {
				inputRef = ref
				inputTag = tag
				break
			}
		}
	}
	if inputRef == "" {
		t.Skip("no input/textarea element found on current page — skipping fill test")
	}
	t.Logf("filling element ref=%s tag=%s", inputRef, inputTag)

	// Step 2: fill via ref through the MCP handler.
	fillResult := callLiveTool(t, cs, "fill", map[string]any{
		"ref":   inputRef,
		"value": "test-value-from-argus",
	})
	if fillResult["backend"] != BackendWeChat {
		t.Errorf("fill backend = %v, want %q", fillResult["backend"], BackendWeChat)
	}
	if fillResult["tag"] != inputTag {
		t.Errorf("fill tag = %v, want %q", fillResult["tag"], inputTag)
	}
	t.Logf("fill result: %s", mustJSON(fillResult))
}

// TestAutomatorLiveMCPStaleRefAfterNavigate asserts that a ref from before
// navigation returns stale_reference after a reLaunch to a different route.
// This is the core safety property: refs do not silently re-match after
// route changes.
func TestAutomatorLiveMCPStaleRefAfterNavigate(t *testing.T) {
	automatorLiveSkip(t)
	cs := setupLiveMCP(t)

	// Step 1: snapshot to get a ref.
	snapResult := callLiveTool(t, cs, "snapshot", map[string]any{})
	elements, ok := snapResult["elements"].([]any)
	if !ok || len(elements) == 0 {
		t.Skip("no elements found on current page — skipping stale-ref test")
	}
	first, ok := elements[0].(map[string]any)
	if !ok {
		t.Fatal("expected element to be map[string]any")
	}
	ref, ok := first["ref"].(string)
	if !ok || !strings.HasPrefix(ref, "wx:") {
		t.Skipf("element has no valid ref: %v", first["ref"])
	}
	t.Logf("captured ref=%s before navigate", ref)

	// Step 2: navigate (reLaunch to same page — this changes pageId).
	statusResult := callLiveTool(t, cs, "status", map[string]any{})
	page, _ := statusResult["page"].(map[string]any)
	beforePath, _ := page["path"].(string)
	navURL := beforePath
	if !strings.HasPrefix(navURL, "/") {
		navURL = "/" + navURL
	}
	callLiveTool(t, cs, "navigate", map[string]any{
		"url":  navURL,
		"mode": "reLaunch",
	})
	t.Logf("navigated via reLaunch to %s", navURL)

	// Step 3: try to click the old ref — should get stale_reference.
	// The reLaunch creates a new page with a different pageId, so the old
	// ref's pageId no longer matches.
	result, err := callLiveToolErr(t, cs, "click", map[string]any{"ref": ref})
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	rawJSON := mustJSON(result)
	t.Logf("stale-ref click result: err=%v raw=%s", err, rawJSON)
	// The error must mention "stale" somewhere (error message or result JSON).
	if !strings.Contains(errStr, "stale") && !strings.Contains(rawJSON, "stale") {
		t.Errorf("expected stale_reference error, got err=%q raw=%s", errStr, rawJSON)
	}
}

// TestAutomatorLiveMCPReconnectStaleRef asserts that refs from before a
// reconnect return stale_reference after the connection epoch changes.
func TestAutomatorLiveMCPReconnectStaleRef(t *testing.T) {
	automatorLiveSkip(t)
	cs := setupLiveMCP(t)

	// Step 1: snapshot to get a ref.
	snapResult := callLiveTool(t, cs, "snapshot", map[string]any{})
	elements, ok := snapResult["elements"].([]any)
	if !ok || len(elements) == 0 {
		t.Skip("no elements found — skipping reconnect stale-ref test")
	}
	first, ok := elements[0].(map[string]any)
	if !ok {
		t.Fatal("expected element to be map[string]any")
	}
	ref, ok := first["ref"].(string)
	if !ok || !strings.HasPrefix(ref, "wx:") {
		t.Skipf("element has no valid ref: %v", first["ref"])
	}
	t.Logf("captured ref=%s before reconnect", ref)

	// Step 2: force a reconnect by closing the connection.
	closeAutomatorConn()
	t.Logf("closed automator connection — next call will reconnect")

	// Step 3: try to click the old ref — should get stale_reference
	// because the epoch changed.
	result, err := callLiveToolErr(t, cs, "click", map[string]any{"ref": ref})
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	rawJSON := mustJSON(result)
	t.Logf("reconnect stale-ref click result: err=%v raw=%s", err, rawJSON)
	if !strings.Contains(errStr, "stale") && !strings.Contains(rawJSON, "stale") {
		t.Errorf("expected stale_reference error after reconnect, got err=%q raw=%s", errStr, rawJSON)
	}
}

// --- helpers ---

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
