//go:build integration

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Scenario P1: Batch Actions + Controlled Form Components (F01 + F02)
func TestE2E_Tier3_P1_BatchControlledForm(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/controlled-react")})
	assertE2ESuccess(t, navRes, "navigate to controlled-react")

	batchRes := callMCPTool(t, cs, "batch_actions", map[string]any{
		"actions": []map[string]any{
			{"action": "fill", "selector": "#react-username", "value": "pairwise_tester"},
			{"action": "fill", "selector": "#react-password", "value": "pairwise_secret"},
			{"action": "click", "selector": "#react-submit-btn"},
			{"action": "extract", "selector": "#submission-status", "name": "auth_result", "attr": "text"},
		},
	})
	assertE2ESuccess(t, batchRes, "execute batch form sequence")
	data := extractToolResultJSON(t, batchRes)

	vars, ok := data["vars"].(map[string]any)
	if !ok {
		t.Fatalf("expected vars in batch result, got %+v", data)
	}
	authRes, _ := vars["auth_result"].(string)
	if !strings.Contains(authRes, "USER_SUBMITTED: pairwise_tester") {
		t.Errorf("expected USER_SUBMITTED in auth_result, got: %s", authRes)
	}

	getRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#state-username"})
	assertE2ESuccess(t, getRes, "get state-username")
	outerHTML := getElementHTML(extractToolResultJSON(t, getRes))
	if !strings.Contains(outerHTML, "pairwise_tester") {
		t.Errorf("expected state-username to match batch fill value, got: %s", outerHTML)
	}
}

// Scenario P2: Batch Actions + Act-and-Verify Inline Attributions (F01 + F04)
func TestE2E_Tier3_P2_BatchActAndVerify(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/act-and-verify")})
	assertE2ESuccess(t, navRes, "navigate to act-and-verify")

	batchRes := callMCPTool(t, cs, "batch_actions", map[string]any{
		"actions": []map[string]any{
			{"action": "click", "selector": "#action-trigger"},
			{"action": "extract", "selector": "#injected-container", "name": "badges", "attr": "text"},
		},
	})
	assertE2ESuccess(t, batchRes, "batch click with inline mutations")
	data := extractToolResultJSON(t, batchRes)

	vars, ok := data["vars"].(map[string]any)
	if !ok {
		t.Fatalf("expected vars in batch output, got %+v", data)
	}
	badges, _ := vars["badges"].(string)
	if !strings.Contains(badges, "Badge 1: Payment Secured") {
		t.Errorf("expected badges text, got: %s", badges)
	}
}

// Scenario P3: a11y Reference + Act-and-Verify Inline Mutations (F03 + F04)
func TestE2E_Tier3_P3_A11yRefActAndVerify(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/a11y-tree")})
	assertE2ESuccess(t, navRes, "navigate to a11y-tree")

	snapRes := callMCPTool(t, cs, "a11y_snapshot", map[string]any{})
	assertE2ESuccess(t, snapRes, "a11y_snapshot")
	snapData := extractToolResultJSON(t, snapRes)
	nodes, ok := snapData["nodes"].([]any)
	if !ok || len(nodes) == 0 {
		t.Fatalf("expected non-empty a11y nodes, got %+v", snapData)
	}

	var confirmRef string
	for _, n := range nodes {
		m, ok := n.(map[string]any)
		if !ok {
			continue
		}
		if bID := extractBackendNodeID(m["backendDOMNodeId"]); bID <= 0 {
			continue
		}
		name := extractAXValue(m["name"])
		role := extractAXValue(m["role"])
		if strings.Contains(name, "Confirm") || role == "button" {
			confirmRef, _ = m["ref"].(string)
			break
		}
	}
	if confirmRef == "" {
		t.Fatalf("failed to locate button node in a11y snapshot")
	}

	clickRes := callMCPTool(t, cs, "click", map[string]any{"ref": confirmRef})
	assertE2ESuccess(t, clickRes, "click via a11y ref")
	clickData := extractToolResultJSON(t, clickRes)

	verif, ok := clickData["verification"].(map[string]any)
	if !ok || verif == nil {
		t.Fatalf("expected verification payload in click result, got %+v", clickData)
	}
	if verified, _ := verif["verified"].(bool); !verified {
		t.Errorf("expected verified=true")
	}

	getRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#a11y-action-log"})
	assertE2ESuccess(t, getRes, "get action log")
	outerHTML := getElementHTML(extractToolResultJSON(t, getRes))
	if !strings.Contains(outerHTML, "CONFIRM_BUTTON_CLICKED") {
		t.Errorf("expected CONFIRM_BUTTON_CLICKED in log, got: %s", outerHTML)
	}
}

// Scenario P4: a11y Reference + Element Screenshot Bounding Box (F03 + F08)
func TestE2E_Tier3_P4_A11yRefElementScreenshot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/a11y-tree")})
	assertE2ESuccess(t, navRes, "navigate to a11y-tree")

	snapRes := callMCPTool(t, cs, "a11y_snapshot", map[string]any{})
	assertE2ESuccess(t, snapRes, "a11y_snapshot")
	snapData := extractToolResultJSON(t, snapRes)
	nodes, _ := snapData["nodes"].([]any)

	var confirmRef string
	for _, n := range nodes {
		m, ok := n.(map[string]any)
		if !ok {
			continue
		}
		if bID := extractBackendNodeID(m["backendDOMNodeId"]); bID <= 0 {
			continue
		}
		name := extractAXValue(m["name"])
		role := extractAXValue(m["role"])
		if strings.Contains(name, "Confirm") || role == "button" {
			confirmRef, _ = m["ref"].(string)
			break
		}
	}
	if confirmRef == "" {
		t.Fatalf("failed to locate a11y ref")
	}

	tmpPath := filepath.Join(t.TempDir(), "a11y_elem_snap.png")
	shotRes := callMCPTool(t, cs, "screenshot", map[string]any{
		"ref":    confirmRef,
		"disk":   true,
		"path":   tmpPath,
		"format": "png",
	})
	assertE2ESuccess(t, shotRes, "screenshot via a11y ref")
	data := extractToolResultJSON(t, shotRes)

	savedPath, _ := data["file_path"].(string)
	if savedPath == "" {
		savedPath, _ = data["saved_to"].(string)
	}
	if savedPath == "" {
		t.Fatalf("expected non-empty saved file path, got %+v", data)
	}

	fi, err := os.Stat(savedPath)
	if err != nil {
		t.Fatalf("failed to stat screenshot artifact: %v", err)
	}
	if fi.Size() < 50 {
		t.Errorf("expected screenshot file size >= 50 bytes, got %d", fi.Size())
	}
}

// Scenario P5: Dialog Anti-Deadlock + Act-and-Verify Feedback (F06 + F04)
func TestE2E_Tier3_P5_DialogAntiDeadlockActAndVerify(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/dialogs")})
	assertE2ESuccess(t, navRes, "navigate to dialogs")

	clickRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#trigger-alert"})
	assertE2ESuccess(t, clickRes, "trigger alert dialog")
	clickData := extractToolResultJSON(t, clickRes)

	verif, ok := clickData["verification"].(map[string]any)
	if !ok || verif == nil {
		t.Fatalf("expected verification in click output, got %+v", clickData)
	}
	dialogInfo, hasDialog := verif["dialog_handled"].(map[string]any)
	if !hasDialog || dialogInfo == nil {
		t.Fatalf("expected dialog_handled recorded in verification payload, got %+v", verif)
	}
	t.Logf("P5 dialogInfo: %+v", dialogInfo)
	msg, _ := dialogInfo["message"].(string)
	if msg == "" {
		msg, _ = dialogInfo["text"].(string)
	}
	if !strings.Contains(msg, "CRITICAL_SYSTEM_ALERT") {
		t.Errorf("expected message to mention CRITICAL_SYSTEM_ALERT, got: %s (data: %+v)", msg, dialogInfo)
	}
}

// Scenario P6: Dev Error Overlay + wait_for Early Abort (F07 + F13)
func TestE2E_Tier3_P6_DevOverlayWaitForAbort(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/overlays/vite")})
	assertE2ESuccess(t, navRes, "navigate to vite overlay")

	start := time.Now()
	waitRes := callMCPTool(t, cs, "wait_for", map[string]any{
		"selector":   "#never-existing-target-elem-p6",
		"timeout_ms": 5000,
	})
	elapsed := time.Since(start)

	if !waitRes.IsError {
		t.Fatalf("expected wait_for to abort with error when overlay is present, got success")
	}
	errText := extractToolResultText(t, waitRes)
	if !strings.Contains(errText, "wait_for aborted: dev server error overlay detected") {
		t.Errorf("expected dev overlay abort message, got: %s", errText)
	}
	if !strings.Contains(errText, "vite") {
		t.Errorf("expected overlay framework vite, got: %s", errText)
	}
	if elapsed > 2*time.Second {
		t.Errorf("expected early abort well under 2s, took %v", elapsed)
	}
}

// Scenario P7: Route Interception Mocking + Act-and-Verify (F09 + F04)
func TestE2E_Tier3_P7_MockRouteActAndVerify(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/api-mock")})
	assertE2ESuccess(t, navRes, "navigate to api-mock")

	mockRes := callMCPTool(t, cs, "mock_route", map[string]any{
		"pattern": "*/api/v1/user/*",
		"body":    `{"id":"usr_p7_mock","name":"Ada Lovelace (Mocked)"}`,
		"status":  200,
	})
	assertE2ESuccess(t, mockRes, "mock_route user API")

	clickRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#btn-fetch-user"})
	assertE2ESuccess(t, clickRes, "click fetch user")

	time.Sleep(100 * time.Millisecond)

	getRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#mock-body"})
	assertE2ESuccess(t, getRes, "get mock-body")
	outerHTML := getElementHTML(extractToolResultJSON(t, getRes))
	if !strings.Contains(outerHTML, "Ada Lovelace (Mocked)") {
		t.Errorf("expected mocked user data rendered, got: %s", outerHTML)
	}
}

// Scenario P8: Multi-Tab Context Switching + Storage Isolation (F10 + F11)
func TestE2E_Tier3_P8_MultiTabStorageIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	// Tab 1: navigate to controlled-react and write localStorage
	navRes1 := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/controlled-react")})
	assertE2ESuccess(t, navRes1, "tab 1 navigate")

	setRes := callMCPTool(t, cs, "storage", map[string]any{
		"action": "set",
		"type":   "local",
		"key":    "tab1_session_token",
		"value":  "token_abc_123",
	})
	assertE2ESuccess(t, setRes, "set localStorage in tab 1")

	// Open Tab 2 to a different origin / path
	newTabRes := callMCPTool(t, cs, "tab", map[string]any{
		"action": "new",
		"url":    fixtureURL(server.URL, "/fixtures/dialogs"),
	})
	assertE2ESuccess(t, newTabRes, "new tab dialogs")

	// Read storage from Tab 1 origin to verify persistence
	tab1SelectRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/controlled-react")})
	assertE2ESuccess(t, tab1SelectRes, "navigate back to tab 1 url")

	getStoreRes := callMCPTool(t, cs, "storage", map[string]any{
		"action": "get",
		"type":   "local",
		"key":    "tab1_session_token",
	})
	assertE2ESuccess(t, getStoreRes, "get localStorage")
	storeData := extractToolResultJSON(t, getStoreRes)
	val, _ := storeData["value"].(string)
	if val != "token_abc_123" {
		t.Errorf("expected token_abc_123 in storage, got: %v", val)
	}
}

// Scenario P9: Mock Route Registered Before Initial Navigation (Anti-Deadlock + F09)
func TestE2E_Tier3_P9_MockRouteBeforeNavigateAntiDeadlock(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	// Explicitly invoke mock_route BEFORE any navigate call on the unpooled target connection.
	// Prior to the re-entrancy fix, this caused an unrecoverable deadlock in executeMockRouteAction.
	mockRes := callMCPTool(t, cs, "mock_route", map[string]any{
		"pattern": "*/api/v1/user/*",
		"body":    `{"id":"usr_p9_premock","name":"Grace Hopper (Pre-Nav Mocked)"}`,
		"status":  200,
	})
	assertE2ESuccess(t, mockRes, "mock_route registered before navigate")

	// Now navigate to fixture page
	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/api-mock")})
	assertE2ESuccess(t, navRes, "navigate to api-mock after route pre-configuration")

	// Trigger request and verify that the pre-registered mock route is active and intercepting
	clickRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#btn-fetch-user"})
	assertE2ESuccess(t, clickRes, "click fetch user")

	time.Sleep(100 * time.Millisecond)

	getRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#mock-body"})
	assertE2ESuccess(t, getRes, "get mock-body")
	outerHTML := getElementHTML(extractToolResultJSON(t, getRes))
	if !strings.Contains(outerHTML, "Grace Hopper (Pre-Nav Mocked)") {
		t.Errorf("expected pre-nav mocked user data rendered, got: %s", outerHTML)
	}
}
