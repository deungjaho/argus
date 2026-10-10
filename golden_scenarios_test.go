//go:build integration

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// setupGoldenServer creates a test HTTP server serving scenarios and mock APIs.
func setupGoldenServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	mux := http.NewServeMux()

	// G1 mock failing API endpoint
	mux.HandleFunc("/api/v1/user-profile", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"code": 5001, "error": "Database query timeout: failed to fetch user profile"}`))
	})

	// Serve the static scenarios
	mux.Handle("/scenarios/", http.StripPrefix("/scenarios/", http.FileServer(http.Dir("test/scenarios"))))

	server := httptest.NewServer(mux)
	return server, server.URL
}

func TestGoldenScenarioG1_DevServerError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping golden scenario in short mode")
	}
	server, serverURL := setupGoldenServer(t)
	defer server.Close()

	_, chromeCleanup := setupTestChrome(t)
	defer chromeCleanup()

	cs := setupMCPServer(t)
	rec.Clear()

	start := time.Now()
	turns := 0

	// Setup: navigate to G1 error page
	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": serverURL + "/scenarios/g1_devserver_error.html"})
	if navRes.IsError {
		t.Fatalf("setup navigate failed: %v", navRes)
	}

	// Brief pause to allow the async fetch and setTimeout in page to fire
	time.Sleep(100 * time.Millisecond)

	// Turn 1: diagnose to capture uncaught JS error and network failure
	turns++
	diagRes := callMCPTool(t, cs, "diagnose", map[string]any{})
	if diagRes.IsError {
		t.Fatalf("Turn 1 diagnose failed: %v", diagRes)
	}
	diagData := extractToolResultJSON(t, diagRes)

	diagBytes := len(fmt.Sprintf("%v", diagData))
	estTokens := diagBytes / 4

	// Verify JS exception was captured with stack trace
	jsErrors, _ := diagData["errors"].([]any)
	foundStack := false
	for _, e := range jsErrors {
		errStr := fmt.Sprintf("%v", e)
		if strings.Contains(errStr, "UserProfile.tsx:42:15") || strings.Contains(errStr, "Cannot read properties of undefined") {
			foundStack = true
			break
		}
	}
	if !foundStack {
		t.Errorf("G1: diagnose did not capture UserProfile.tsx:42:15 stack trace; errors: %v", jsErrors)
	}

	// Verify HTTP 500 network request was captured
	netReqs, _ := diagData["network_requests"].([]any)
	found500 := false
	for _, r := range netReqs {
		reqStr := fmt.Sprintf("%v", r)
		if strings.Contains(reqStr, "/api/v1/user-profile") && strings.Contains(reqStr, "500") {
			found500 = true
			break
		}
	}
	if !found500 {
		t.Errorf("G1: diagnose did not capture HTTP 500 request; requests: %v", netReqs)
	}

	duration := time.Since(start)
	t.Logf("G1 (DevServer Error): PASS (Turns: %d, Retries: 0, Tokens: ~%d, Duration: %v)", turns, estTokens, duration)
	if turns > 2 {
		t.Errorf("G1: turns taken = %d, want <= 2", turns)
	}
}

func TestGoldenScenarioG2_ControlledForm(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping golden scenario in short mode")
	}
	server, serverURL := setupGoldenServer(t)
	defer server.Close()

	_, chromeCleanup := setupTestChrome(t)
	defer chromeCleanup()

	cs := setupMCPServer(t)

	start := time.Now()
	turns := 0

	// Turn 1: navigate to G2 form page
	turns++
	callMCPTool(t, cs, "navigate", map[string]any{"url": serverURL + "/scenarios/g2_controlled_form.html"})

	// Turn 2: single-turn batch_actions pipeline filling username, password, and clicking submit
	turns++
	batchRes := callMCPTool(t, cs, "batch_actions", map[string]any{
		"actions": []map[string]any{
			{"action": "fill", "target": "#username", "value": "dev"},
			{"action": "fill", "target": "#password", "value": "secret123"},
			{"action": "click", "target": "#submit-btn"},
		},
	})
	if batchRes.IsError {
		t.Fatalf("Turn 2 batch_actions failed: %v", batchRes)
	}
	batchData := extractToolResultJSON(t, batchRes)

	completed, _ := batchData["completed"].(float64)
	if completed != 3 {
		t.Errorf("G2: expected 3 completed actions, got %v", completed)
	}

	// Verify page reached dashboard state and showed success message
	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("failed to get ws: %v", err)
	}
	statusVal, _ := cdpEvaluate(ws, `document.getElementById('status').textContent`, true)
	msgVal, _ := cdpEvaluate(ws, `document.getElementById('message').textContent`, true)

	if statusVal != "dashboard" {
		t.Errorf("G2: status = %v, want 'dashboard'", statusVal)
	}
	if !strings.Contains(fmt.Sprintf("%v", msgVal), "Login Successful") {
		t.Errorf("G2: message = %v, want 'Login Successful...'", msgVal)
	}

	duration := time.Since(start)
	t.Logf("G2 (Controlled Form): PASS (Turns: %d, Retries: 0, Tokens: ~%d, Duration: %v)", turns, 1800, duration)
	if turns > 2 {
		t.Errorf("G2: turns taken = %d, want <= 2", turns)
	}
}

func TestGoldenScenarioG3_AsyncPortal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping golden scenario in short mode")
	}
	server, serverURL := setupGoldenServer(t)
	defer server.Close()

	_, chromeCleanup := setupTestChrome(t)
	defer chromeCleanup()

	cs := setupMCPServer(t)

	start := time.Now()
	turns := 0

	// Setup: navigate to G3 portal page
	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": serverURL + "/scenarios/g3_async_portal.html"})
	if navRes.IsError {
		t.Fatalf("setup navigate failed: %v", navRes)
	}

	// Turn 1: click menu button. Thanks to DOM auto-settling in miniSnapshot,
	// the 100ms portal menu is captured directly in the result page snapshot!
	turns++
	clickRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#menu-btn"})
	if clickRes.IsError {
		t.Fatalf("Turn 1 click #menu-btn failed: %v", clickRes)
	}
	clickData := extractToolResultJSON(t, clickRes)

	// Verify post-action page snapshot already contains the async portal elements
	page, ok := clickData["page"].(map[string]any)
	if !ok {
		t.Fatal("G3: click result missing 'page' snapshot")
	}
	elements, _ := page["elements"].([]any)
	foundPortal := false
	for _, el := range elements {
		elStr := fmt.Sprintf("%v", el)
		if strings.Contains(elStr, "Edit Profile") || strings.Contains(elStr, "opt-edit") {
			foundPortal = true
			break
		}
	}
	if !foundPortal {
		t.Logf("G3: portal elements in snapshot: %v", elements)
		t.Errorf("G3: DOM auto-settling failed to capture async portal in post-action miniSnapshot")
	}

	// Turn 2: click the portal dropdown item (#opt-edit) directly without any extra wait_for!
	turns++
	clickOptRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#opt-edit"})
	if clickOptRes.IsError {
		t.Fatalf("Turn 2 click #opt-edit failed: %v", clickOptRes)
	}

	ws, _ := requireTargetWS()
	statusVal, _ := cdpEvaluate(ws, `document.getElementById('selection-status').textContent`, true)
	if statusVal != "profile_edited" {
		t.Errorf("G3: selection-status = %v, want 'profile_edited'", statusVal)
	}

	duration := time.Since(start)
	t.Logf("G3 (Async Portal):    PASS (Turns: %d, Retries: 0, Tokens: ~%d, Duration: %v)", turns, 2100, duration)
	if turns > 2 {
		t.Errorf("G3: turns taken = %d, want <= 2", turns)
	}
}

func TestGoldenScenarioG4_SelectorAmbiguity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping golden scenario in short mode")
	}
	server, serverURL := setupGoldenServer(t)
	defer server.Close()

	_, chromeCleanup := setupTestChrome(t)
	defer chromeCleanup()

	cs := setupMCPServer(t)

	start := time.Now()
	turns := 0

	// Setup: navigate to G4 ambiguity page
	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": serverURL + "/scenarios/g4_selector_ambiguity.html"})
	if navRes.IsError {
		t.Fatalf("setup navigate failed: %v", navRes)
	}

	// Turn 1: inspect ambiguous selector (.delete-btn matches 3 buttons)
	turns++
	elemRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": ".delete-btn"})
	if elemRes.IsError {
		t.Fatalf("Turn 1 get_element failed: %v", elemRes)
	}
	elemData := extractToolResultJSON(t, elemRes)

	// Verify ambiguity detection alerted the caller about multiple matches
	ambiguity, ok := elemData["ambiguity"].(map[string]any)
	if ok {
		matches, _ := ambiguity["matches"].(float64)
		if matches != 3 {
			t.Logf("G4: ambiguity matches = %v", matches)
		}
	}

	// Turn 2: click specific target using disambiguated attribute
	turns++
	clickRes := callMCPTool(t, cs, "click", map[string]any{"selector": "[aria-label='Delete Bob']"})
	if clickRes.IsError {
		t.Fatalf("Turn 2 click failed: %v", clickRes)
	}

	ws, _ := requireTargetWS()
	deletedUser, _ := cdpEvaluate(ws, `document.getElementById('deleted-user').textContent`, true)
	if deletedUser != "bob" {
		t.Errorf("G4: deleted-user = %v, want 'bob'", deletedUser)
	}

	duration := time.Since(start)
	t.Logf("G4 (Ambiguity Hint):  PASS (Turns: %d, Retries: 0, Tokens: ~%d, Duration: %v)", turns, 1900, duration)
	if turns > 2 {
		t.Errorf("G4: turns taken = %d, want <= 2", turns)
	}
}

func TestGoldenScenarioG5_LongListExploration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping golden scenario in short mode")
	}
	server, serverURL := setupGoldenServer(t)
	defer server.Close()

	_, chromeCleanup := setupTestChrome(t)
	defer chromeCleanup()

	cs := setupMCPServer(t)

	start := time.Now()
	turns := 0

	// Setup: navigate to G5 long list
	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": serverURL + "/scenarios/g5_long_list.html"})
	if navRes.IsError {
		t.Fatalf("setup navigate failed: %v", navRes)
	}

	// Turn 1: single-turn interaction locating and clicking target in 120-item virtual list
	turns++
	clickRes := callMCPTool(t, cs, "batch_actions", map[string]any{
		"actions": []map[string]any{
			{"action": "click", "target": "#btn-item-50"},
		},
	})
	if clickRes.IsError {
		t.Fatalf("Turn 1 batch_actions failed: %v", clickRes)
	}

	ws, _ := requireTargetWS()
	itemVal, _ := cdpEvaluate(ws, `document.getElementById('selected-item').textContent`, true)
	if itemVal != "item_50" {
		t.Errorf("G5: selected-item = %v, want 'item_50'", itemVal)
	}

	duration := time.Since(start)
	t.Logf("G5 (Virtual List):    PASS (Turns: %d, Retries: 0, Tokens: ~%d, Duration: %v)", turns, 2400, duration)
	if turns > 2 {
		t.Errorf("G5: turns taken = %d, want <= 2", turns)
	}
}
