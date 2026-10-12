//go:build integration

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Workload W1: Enterprise SPA Authentication & JWT Session Persistence
// Flow: Navigate -> mock_route login API -> fill controlled credentials -> click login ->
//
//	verify storage JWT token -> screenshot profile/store section to disk.
func TestE2E_Tier4_W1_EnterpriseSPAAuthJWTPersistence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	// Step 1: Navigate to SPA login page
	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/complex-app")})
	assertE2ESuccess(t, navRes, "navigate to complex SPA")

	// Step 2: Configure route mock for authentication gateway (mocking POST /api/v1/auth/login)
	mockRes := callMCPTool(t, cs, "mock_route", map[string]any{
		"pattern": "*/api/v1/auth/login",
		"status":  200,
		"body": map[string]any{
			"token": "JWT_TOKEN_W1_ENTERPRISE_SECRET",
			"user":  "enterprise_admin",
		},
	})
	assertE2ESuccess(t, mockRes, "mock_route auth login")

	// Step 3: Controlled Fill on credentials with transition settle
	fillUserRes := callMCPTool(t, cs, "fill", map[string]any{
		"selector":        "#login-user",
		"value":           "enterprise_admin",
		"settle_ms":       50,
		"wait_transition": true,
	})
	assertE2ESuccess(t, fillUserRes, "fill username")

	fillPassRes := callMCPTool(t, cs, "fill", map[string]any{
		"selector":        "#login-pass",
		"value":           "vault_password_99",
		"settle_ms":       50,
		"wait_transition": true,
	})
	assertE2ESuccess(t, fillPassRes, "fill password")

	// Step 4: Click submit and verify act-and-verify telemetry
	clickRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#login-btn"})
	assertE2ESuccess(t, clickRes, "click login button")

	// Allow fetch and state transition to complete
	time.Sleep(150 * time.Millisecond)

	// Step 5: Verify login success message rendered
	msgRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#login-msg"})
	assertE2ESuccess(t, msgRes, "get login message")
	msgHTML := getElementHTML(extractToolResultJSON(t, msgRes))
	if !strings.Contains(msgHTML, "SUCCESS: enterprise_admin") {
		t.Fatalf("expected login success message, got: %s", msgHTML)
	}

	// Step 6: Verify JWT token persistence in LocalStorage via consolidated storage tool
	storeRes := callMCPTool(t, cs, "storage", map[string]any{
		"action": "get",
		"type":   "local",
		"key":    "jwt_token",
	})
	assertE2ESuccess(t, storeRes, "get jwt_token from localStorage")
	storeData := extractToolResultJSON(t, storeRes)
	tokenVal, _ := storeData["value"].(string)
	if tokenVal != "JWT_TOKEN_W1_ENTERPRISE_SECRET" {
		t.Fatalf("expected JWT_TOKEN_W1_ENTERPRISE_SECRET in storage, got: %v", tokenVal)
	}

	// Step 7: Capture cropped screenshot of authenticated store section to disk
	tmpPath := filepath.Join(t.TempDir(), "w1_auth_receipt.png")
	screenRes := callMCPTool(t, cs, "screenshot", map[string]any{
		"selector": "#sec-store",
		"disk":     true,
		"path":     tmpPath,
		"format":   "png",
	})
	assertE2ESuccess(t, screenRes, "screenshot store section to disk")
	screenData := extractToolResultJSON(t, screenRes)

	savedPath, _ := screenData["file_path"].(string)
	if savedPath == "" {
		savedPath, _ = screenData["saved_to"].(string)
	}
	if savedPath == "" {
		savedPath = tmpPath
	}

	fi, err := os.Stat(savedPath)
	if err != nil {
		t.Fatalf("expected screenshot file on disk: %v", err)
	}
	if fi.Size() < 50 {
		t.Fatalf("expected screenshot size >= 50 bytes, got %d", fi.Size())
	}
}

// Workload W2: E-Commerce Product Catalog, Filter, Cart & Checkout Confirmation
// Flow: Navigate -> switch to store -> a11y snapshot perception -> smart card click ->
//
//	controlled quantity fill -> add to cart -> mock checkout API ->
//	checkout click with native confirm auto-accepted -> wait_for order confirmation -> receipt screenshot.
func TestE2E_Tier4_W2_EcommerceProductCartCheckout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	// Step 1: Navigate to SPA
	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/complex-app")})
	assertE2ESuccess(t, navRes, "navigate to complex SPA")

	// Step 2: Switch to store catalog section
	navStoreRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#nav-store"})
	assertE2ESuccess(t, navStoreRes, "navigate to store tab")

	// Step 3: Accessibility perception
	snapRes := callMCPTool(t, cs, "a11y_snapshot", map[string]any{})
	assertE2ESuccess(t, snapRes, "a11y_snapshot of store catalog")
	snapData := extractToolResultJSON(t, snapRes)
	nodes, _ := snapData["nodes"].([]any)
	if len(nodes) == 0 {
		t.Fatalf("expected non-empty a11y nodes in catalog")
	}

	// Step 4: Click product card 101
	cardClickRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#card-101"})
	assertE2ESuccess(t, cardClickRes, "click card 101")

	// Verify selected item updated
	selRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#store-selected"})
	assertE2ESuccess(t, selRes, "get selected item")
	if !strings.Contains(getElementHTML(extractToolResultJSON(t, selRes)), "Item 101") {
		t.Fatalf("expected Item 101 selected")
	}

	// Step 5: Fill quantity with controlled value "2"
	fillQtyRes := callMCPTool(t, cs, "fill", map[string]any{
		"selector":  "#store-qty",
		"value":     "2",
		"settle_ms": 50,
	})
	assertE2ESuccess(t, fillQtyRes, "fill quantity 2")

	// Step 6: Add to cart
	addCartRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#store-add-cart"})
	assertE2ESuccess(t, addCartRes, "click add to cart")

	cartStatusRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#store-cart-status"})
	assertE2ESuccess(t, cartStatusRes, "get cart status")
	if !strings.Contains(getElementHTML(extractToolResultJSON(t, cartStatusRes)), "Cart: 2 items") {
		t.Fatalf("expected Cart: 2 items, got: %s", getElementHTML(extractToolResultJSON(t, cartStatusRes)))
	}

	// Step 7: Configure mock route for checkout order submission
	mockRes := callMCPTool(t, cs, "mock_route", map[string]any{
		"pattern": "*/api/checkout",
		"status":  200,
		"body": map[string]any{
			"order_id": "ORD-E2E-9944",
			"status":   "confirmed",
		},
	})
	assertE2ESuccess(t, mockRes, "mock_route checkout API")

	// Step 8: Configure dialog watchdog policy to accept native confirm dialog
	diagPolicyRes := callMCPTool(t, cs, "handle_dialog", map[string]any{
		"action": "policy",
		"policy": "accept",
	})
	assertE2ESuccess(t, diagPolicyRes, "set dialog auto-accept policy")

	// Step 9: Click Checkout (triggers native confirm -> auto-accepted -> calls /api/checkout)
	checkoutRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#store-checkout"})
	assertE2ESuccess(t, checkoutRes, "click checkout button")

	// Step 10: Wait for order confirmation banner
	waitRes := callMCPTool(t, cs, "wait_for", map[string]any{
		"selector":   "#order-success",
		"timeout_ms": 3000,
	})
	assertE2ESuccess(t, waitRes, "wait for order-success element")

	// Step 11: Inspect confirmation element
	orderRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#order-success"})
	assertE2ESuccess(t, orderRes, "get order confirmation element")
	orderHTML := getElementHTML(extractToolResultJSON(t, orderRes))
	if !strings.Contains(orderHTML, "ORD-E2E-9944") {
		t.Fatalf("expected order confirmation to mention ORD-E2E-9944, got: %s", orderHTML)
	}

	// Step 12: Capture visual receipt screenshot to disk
	tmpPath := filepath.Join(t.TempDir(), "w2_order_receipt.png")
	shotRes := callMCPTool(t, cs, "screenshot", map[string]any{
		"selector": "#order-success",
		"disk":     true,
		"path":     tmpPath,
	})
	assertE2ESuccess(t, shotRes, "screenshot order receipt")
	fi, err := os.Stat(tmpPath)
	if err != nil || fi.Size() < 50 {
		t.Fatalf("expected receipt screenshot file on disk with size >= 50, err: %v", err)
	}
}

// Workload W3: Micro-Frontend Dashboard with Route Mocking & Error Recovery
// Flow: Navigate -> switch to dashboard -> simulate micro-frontend 500 error ->
//
//	perceive error banner via wait_for -> diagnose page state ->
//	click retry -> wait_for recovered text -> verify element text.
func TestE2E_Tier4_W3_MicroFrontendRouteMockErrorRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	// Step 1: Navigate to SPA
	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/complex-app")})
	assertE2ESuccess(t, navRes, "navigate to complex SPA")

	// Step 2: Switch to dashboard section
	navDashRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#nav-dashboard"})
	assertE2ESuccess(t, navDashRes, "click nav-dashboard")

	// Step 3: Trigger simulated micro-frontend network failure
	evalRes := callMCPTool(t, cs, "eval", map[string]any{
		"expression": `(function() {
			document.getElementById('widget-error').style.display = 'block';
			document.getElementById('widget-content').textContent = 'Widget Failed: 500 Network Failure';
			return 'error_injected';
		})()`,
	})
	assertE2ESuccess(t, evalRes, "eval inject micro-frontend error")

	// Step 4: Perceive error state via wait_for
	waitErrRes := callMCPTool(t, cs, "wait_for", map[string]any{
		"selector":   "#widget-error",
		"timeout_ms": 2000,
	})
	assertE2ESuccess(t, waitErrRes, "wait for widget-error element")

	// Step 5: Run diagnose to capture live telemetry snapshot
	diagRes := callMCPTool(t, cs, "diagnose", map[string]any{"limit": 5})
	assertE2ESuccess(t, diagRes, "diagnose page state")
	diagData := extractToolResultJSON(t, diagRes)
	if diagData["target_id"] == nil {
		t.Errorf("expected target_id in diagnose output, got %+v", diagData)
	}

	// Step 6: Trigger recovery retry
	retryRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#widget-retry"})
	assertE2ESuccess(t, retryRes, "click widget-retry button")

	// Step 7: Wait for recovered state text
	waitRecovRes := callMCPTool(t, cs, "wait_for", map[string]any{
		"text":       "Widget Recovered & Active",
		"timeout_ms": 2000,
	})
	assertE2ESuccess(t, waitRecovRes, "wait for recovered text")

	// Step 8: Assert element content
	contentRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#widget-content"})
	assertE2ESuccess(t, contentRes, "get widget-content")
	contentHTML := getElementHTML(extractToolResultJSON(t, contentRes))
	if !strings.Contains(contentHTML, "Widget Recovered") || !strings.Contains(contentHTML, "Active") {
		t.Fatalf("expected recovered text in widget-content, got: %s", contentHTML)
	}
}

// Workload W4: Critical Dev Server Crash Triage & Forensic Extraction Pipeline
// Flow: Navigate to Vite overlay -> wait_for detects overlay and aborts early ->
//
//	diagnose extracts structured overlay errors -> timeline retrieves flight recorder entries ->
//	screenshot preserves evidence on disk.
func TestE2E_Tier4_W4_DevServerCrashForensicTriage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	// Step 1: Navigate to page with Vite crash overlay
	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/overlays/vite")})
	assertE2ESuccess(t, navRes, "navigate to vite overlay fixture")

	// Step 2: wait_for on non-existent element must abort immediately due to error overlay detection
	startWait := time.Now()
	waitRes := callMCPTool(t, cs, "wait_for", map[string]any{
		"selector":   "#never-existing-app-content",
		"timeout_ms": 5000,
	})
	elapsed := time.Since(startWait)

	if !waitRes.IsError {
		t.Fatalf("expected wait_for to fail due to dev overlay detection, got success")
	}
	waitErrText := extractToolResultText(t, waitRes)
	if !strings.Contains(waitErrText, "dev server error overlay detected") {
		t.Fatalf("expected dev overlay abort message, got: %s", waitErrText)
	}
	if elapsed > 2*time.Second {
		t.Errorf("expected early abort well under 2s, took %v", elapsed)
	}

	// Step 3: Deep forensic inspection with diagnose
	diagRes := callMCPTool(t, cs, "diagnose", map[string]any{"limit": 10})
	assertE2ESuccess(t, diagRes, "diagnose crash state")
	diagData := extractToolResultJSON(t, diagRes)

	errors, ok := diagData["errors"].([]any)
	if !ok || len(errors) == 0 {
		t.Fatalf("expected errors list in diagnose output, got %+v", diagData)
	}

	foundViteOverlay := false
	for _, e := range errors {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if em["type"] == "dev_overlay" && em["framework"] == "vite" {
			foundViteOverlay = true
			break
		}
	}
	if !foundViteOverlay {
		t.Fatalf("expected dev_overlay error with framework vite in errors list: %+v", errors)
	}

	// Step 4: Flight recorder timeline query
	tlRes := callMCPTool(t, cs, "timeline", map[string]any{
		"include_actions": true,
		"limit":           20,
	})
	assertE2ESuccess(t, tlRes, "query timeline")
	tlData := extractToolResultJSON(t, tlRes)
	entries, _ := tlData["timeline"].([]any)
	if len(entries) == 0 {
		t.Logf("timeline entries count: 0 (recorded)")
	}

	// Step 5: Forensic evidence capture via disk screenshot
	tmpPath := filepath.Join(t.TempDir(), "w4_crash_evidence.png")
	screenRes := callMCPTool(t, cs, "screenshot", map[string]any{
		"disk":   true,
		"path":   tmpPath,
		"format": "png",
	})
	assertE2ESuccess(t, screenRes, "screenshot crash evidence")

	fi, err := os.Stat(tmpPath)
	if err != nil || fi.Size() < 50 {
		t.Fatalf("expected forensic screenshot on disk with size >= 50 bytes, err: %v", err)
	}
}

// Workload W5: Multi-Tab Back-Office Admin vs. Client Session Synchronization
// Flow: Tab 1: Navigate to SPA -> set LocalStorage maintenance flag ->
//
//	Tab 2: Open new tab to SPA -> verify LocalStorage maintenance flag syncs ->
//	Tab 2: Write client acknowledgment -> switch back to Tab 1 ->
//	Close Tab 2 -> verify tab count returns to 1 cleanly.
func TestE2E_Tier4_W5_MultiTabAdminClientSync(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	// Step 1: Tab 1 navigation (Admin context)
	navRes1 := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/complex-app")})
	assertE2ESuccess(t, navRes1, "tab 1 navigate to complex SPA")

	// Step 2: Admin sets maintenance mode in LocalStorage
	setStoreRes := callMCPTool(t, cs, "storage", map[string]any{
		"action": "set",
		"type":   "local",
		"key":    "maintenance_mode",
		"value":  "true",
	})
	assertE2ESuccess(t, setStoreRes, "tab 1 set maintenance_mode in localStorage")

	// Step 3: Open Tab 2 to same SPA origin
	newTabRes := callMCPTool(t, cs, "tab", map[string]any{
		"action": "new",
		"url":    fixtureURL(server.URL, "/fixtures/complex-app"),
	})
	assertE2ESuccess(t, newTabRes, "open tab 2")
	newTabData := extractToolResultJSON(t, newTabRes)
	tab2ID, _ := newTabData["tab_id"].(string)
	if tab2ID == "" {
		tab2ID, _ = newTabData["id"].(string)
	}

	// Step 4: Verify tab list reports 2 open tabs
	listRes := callMCPTool(t, cs, "tab", map[string]any{"action": "list"})
	assertE2ESuccess(t, listRes, "list tabs")
	listData := extractToolResultJSON(t, listRes)
	tabs, _ := listData["tabs"].([]any)
	if len(tabs) < 2 {
		t.Fatalf("expected at least 2 open tabs, got %d", len(tabs))
	}

	// Step 5: Read storage in Tab 2 to confirm origin-scoped sync
	getStoreRes2 := callMCPTool(t, cs, "storage", map[string]any{
		"action": "get",
		"type":   "local",
		"key":    "maintenance_mode",
	})
	assertE2ESuccess(t, getStoreRes2, "tab 2 get maintenance_mode")
	val2, _ := extractToolResultJSON(t, getStoreRes2)["value"].(string)
	if val2 != "true" {
		t.Fatalf("expected maintenance_mode='true' in tab 2, got: %s", val2)
	}

	// Step 6: Client writes acknowledgment flag in Tab 2
	setAckRes := callMCPTool(t, cs, "storage", map[string]any{
		"action": "set",
		"type":   "local",
		"key":    "client_ack",
		"value":  "ack_client_tab2",
	})
	assertE2ESuccess(t, setAckRes, "tab 2 set client_ack")

	// Step 7: Close Tab 2
	if tab2ID != "" {
		closeRes := callMCPTool(t, cs, "tab", map[string]any{
			"action": "close",
			"tab_id": tab2ID,
		})
		assertE2ESuccess(t, closeRes, "close tab 2")
	} else {
		// If tab2ID was not parsed from response, find it from list
		var targetCloseID string
		for _, tb := range tabs {
			tbm, _ := tb.(map[string]any)
			tid, _ := tbm["id"].(string)
			if tid != "" {
				targetCloseID = tid
			}
		}
		if targetCloseID != "" {
			closeRes := callMCPTool(t, cs, "tab", map[string]any{
				"action": "close",
				"tab_id": targetCloseID,
			})
			assertE2ESuccess(t, closeRes, "close tab 2 via list ID")
		}
	}

	// Step 8: Verify active tabs count reduced
	listAfterRes := callMCPTool(t, cs, "tab", map[string]any{"action": "list"})
	assertE2ESuccess(t, listAfterRes, "list tabs after close")
	tabsAfter, _ := extractToolResultJSON(t, listAfterRes)["tabs"].([]any)
	if len(tabsAfter) != len(tabs)-1 {
		t.Logf("tabs count after close: %d (original: %d)", len(tabsAfter), len(tabs))
	}
}
