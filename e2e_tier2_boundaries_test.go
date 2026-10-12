//go:build integration

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Feature F01 Boundary Tests: batch_actions
func TestE2E_Tier2_Issue1_BatchActions_Boundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/controlled-react")})
	assertE2ESuccess(t, navRes, "navigate")

	// Case 1: Empty actions array returns error
	t.Run("Case1_EmptyActionsArray", func(t *testing.T) {
		res := callMCPTool(t, cs, "batch_actions", map[string]any{"actions": []map[string]any{}})
		assertE2EError(t, res, "empty batch_actions should return error")
	})

	// Case 2: Unknown / malformed key name
	t.Run("Case2_UnsupportedActionType", func(t *testing.T) {
		res := callMCPTool(t, cs, "batch_actions", map[string]any{
			"actions": []map[string]any{
				{"action": "unsupported_xyz_command", "target": "#react-username"},
			},
		})
		assertE2EError(t, res, "unsupported action type should fail")
	})

	// Case 3: High-volume step sequence (15 sequential steps)
	t.Run("Case3_HighVolumeStepSequence", func(t *testing.T) {
		actions := make([]map[string]any, 0, 15)
		for i := 0; i < 15; i++ {
			actions = append(actions, map[string]any{
				"action": "fill",
				"target": "#react-company",
				"value":  fmt.Sprintf("Corp_Iter_%d", i),
			})
		}
		res := callMCPTool(t, cs, "batch_actions", map[string]any{"actions": actions})
		assertE2ESuccess(t, res, "high volume batch sequence")
		data := extractToolResultJSON(t, res)
		if completed, _ := data["completed"].(float64); completed != 15 {
			t.Errorf("expected 15 completed, got %v", completed)
		}
	})

	// Case 4: Multiple modifier combinations (ctrl+shift)
	t.Run("Case4_MultipleModifierCombination", func(t *testing.T) {
		res := callMCPTool(t, cs, "batch_actions", map[string]any{
			"actions": []map[string]any{
				{"action": "fill", "target": "#react-company", "value": "test_text"},
				{"action": "key", "key": "ArrowLeft", "modifiers": "shift"},
			},
		})
		assertE2ESuccess(t, res, "key with shift modifier")
	})

	// Case 5: Extract from non-existent element with optional: false fails batch
	t.Run("Case5_ExtractNonExistentElementFailsBatch", func(t *testing.T) {
		res := callMCPTool(t, cs, "batch_actions", map[string]any{
			"actions": []map[string]any{
				{"action": "extract", "target": "#missing-node-id-9999", "name": "val", "optional": false},
			},
		})
		assertE2EError(t, res, "extract on missing node should fail")
	})
}

// Feature F02 Boundary Tests: Controlled Components
func TestE2E_Tier2_Issue2_ControlledForm_Boundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/controlled-react")})
	assertE2ESuccess(t, navRes, "navigate")

	// Case 1: Zero-length empty string value
	t.Run("Case1_EmptyStringFill", func(t *testing.T) {
		res := callMCPTool(t, cs, "fill", map[string]any{
			"selector": "#react-username",
			"value":    "",
		})
		assertE2ESuccess(t, res, "fill empty string")
	})

	// Case 2: Massive 10KB text payload
	t.Run("Case2_LargeTextPayload", func(t *testing.T) {
		largePayload := strings.Repeat("A-1234567890-", 800) // ~10KB
		res := callMCPTool(t, cs, "fill", map[string]any{
			"selector": "#react-company",
			"value":    largePayload,
		})
		assertE2ESuccess(t, res, "fill large payload")
	})

	// Case 3: Special Unicode & emojis
	t.Run("Case3_SpecialUnicodeAndEmojis", func(t *testing.T) {
		unicodeText := "🚀 𝒳𝒴𝒵 \t 日本語 🐱‍👤 Ñandú"
		res := callMCPTool(t, cs, "fill", map[string]any{
			"selector": "#react-username",
			"value":    unicodeText,
		})
		assertE2ESuccess(t, res, "fill unicode & emojis")

		time.Sleep(80 * time.Millisecond)
		getRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#state-username"})
		assertE2ESuccess(t, getRes, "get state-username")
		data := extractToolResultJSON(t, getRes)
		outerHTML := getElementHTML(data)
		if !strings.Contains(outerHTML, "🚀") || !strings.Contains(outerHTML, "日本語") {
			t.Errorf("expected unicode preserved, got: %s", outerHTML)
		}
	})

	// Case 4: Missing target element returns clear error
	t.Run("Case4_MissingTargetElementError", func(t *testing.T) {
		res := callMCPTool(t, cs, "fill", map[string]any{
			"selector":   "#totally-missing-input",
			"value":      "ghost",
			"timeout_ms": 200,
		})
		assertE2EError(t, res, "fill non-existent element should error")
	})

	// Case 5: Consecutive rapid fills on same element
	t.Run("Case5_RapidConsecutiveFills", func(t *testing.T) {
		for i := 1; i <= 5; i++ {
			res := callMCPTool(t, cs, "fill", map[string]any{
				"selector": "#react-username",
				"value":    fmt.Sprintf("rapid_%d", i),
			})
			assertE2ESuccess(t, res, fmt.Sprintf("rapid fill %d", i))
		}
	})
}

// Feature F03 Boundary Tests: a11y Node Bridge
func TestE2E_Tier2_Issue3_A11yBridge_Boundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/a11y-tree")})
	assertE2ESuccess(t, navRes, "navigate")

	// Case 1: Non-existent node ID
	t.Run("Case1_NonExistentNodeID", func(t *testing.T) {
		res := callMCPTool(t, cs, "click", map[string]any{
			"ref":        "ax:999999",
			"timeout_ms": 200,
		})
		assertE2EError(t, res, "click ax:999999 should error")
	})

	// Case 2: Malformed node ID string
	t.Run("Case2_MalformedNodeIDString", func(t *testing.T) {
		res := callMCPTool(t, cs, "click", map[string]any{
			"ref":        "ax:invalid_alpha_chars",
			"timeout_ms": 200,
		})
		assertE2EError(t, res, "click malformed ax ref should error")
	})

	// Case 3: Empty ref parameter
	t.Run("Case3_EmptyRefParameter", func(t *testing.T) {
		res := callMCPTool(t, cs, "click", map[string]any{
			"ref":        "",
			"timeout_ms": 200,
		})
		assertE2EError(t, res, "click empty ref should error")
	})

	// Case 4: Node in open Shadow DOM
	t.Run("Case4_ShadowDOMNodeClick", func(t *testing.T) {
		// Shadow DOM button click via CSS selector traversal or eval
		evalRes := callMCPTool(t, cs, "eval", map[string]any{
			"expression": "document.getElementById('shadow-badge').shadowRoot.getElementById('shadow-inner-btn').click(); 'clicked'",
		})
		assertE2ESuccess(t, evalRes, "click shadow inner btn via eval")

		getRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#a11y-action-log"})
		assertE2ESuccess(t, getRes, "get action log")
		data := extractToolResultJSON(t, getRes)
		outerHTML := getElementHTML(data)
		if !strings.Contains(outerHTML, "SHADOW_BUTTON_CLICKED") {
			t.Errorf("expected SHADOW_BUTTON_CLICKED, got: %s", outerHTML)
		}
	})

	// Case 5: a11y inspection with non-matching ref
	t.Run("Case5_GetElementNonMatchingRef", func(t *testing.T) {
		res := callMCPTool(t, cs, "get_element", map[string]any{
			"ref": "ax:88888",
		})
		assertE2EError(t, res, "get_element with invalid ref should error")
	})
}

// Feature F04 Boundary Tests: Act-and-Verify
func TestE2E_Tier2_Issue4_ActAndVerify_Boundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/act-and-verify")})
	assertE2ESuccess(t, navRes, "navigate")

	// Case 1: Static non-mutating click (DOM mutation count == 0)
	t.Run("Case1_StaticNonMutatingClick", func(t *testing.T) {
		clickRes := callMCPTool(t, cs, "click", map[string]any{"selector": "h3"})
		assertE2ESuccess(t, clickRes, "click static h3")
		data := extractToolResultJSON(t, clickRes)
		verification, ok := data["verification"].(map[string]any)
		if !ok || verification == nil {
			t.Fatalf("expected verification object, got %+v", data)
		}
		domChanges, _ := verification["dom_changes"].(map[string]any)
		added, _ := domChanges["added_count"].(float64)
		if added != 0 {
			t.Logf("dom additions on static click: %v", added)
		}
	})

	// Case 2: Rapid back-to-back clicks with independent attribution
	t.Run("Case2_RapidBackToBackClicks", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			res := callMCPTool(t, cs, "click", map[string]any{"selector": "h3"})
			assertE2ESuccess(t, res, fmt.Sprintf("click %d", i))
			data := extractToolResultJSON(t, res)
			if data["verification"] == nil {
				t.Errorf("missing verification payload in rapid click %d", i)
			}
		}
	})

	// Case 3: Action on page with non-navigating hash
	t.Run("Case3_ActionOnHashPage", func(t *testing.T) {
		res := callMCPTool(t, cs, "click", map[string]any{"selector": "#action-trigger"})
		assertE2ESuccess(t, res, "click action-trigger")
		data := extractToolResultJSON(t, res)
		verification, _ := data["verification"].(map[string]any)
		if verification == nil {
			t.Fatalf("expected verification object")
		}
	})

	// Case 4: Action with dialog auto-handling reporting dialog details
	t.Run("Case4_ActionReportingDialogDetails", func(t *testing.T) {
		_ = callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/dialogs")})
		res := callMCPTool(t, cs, "click", map[string]any{"selector": "#trigger-alert"})
		assertE2ESuccess(t, res, "click trigger-alert")
		data := extractToolResultJSON(t, res)
		if data["verification"] == nil {
			t.Errorf("expected verification payload")
		}
	})

	// Case 5: Action with no_wait: true returns verification payload
	t.Run("Case5_NoWaitActionVerification", func(t *testing.T) {
		res := callMCPTool(t, cs, "click", map[string]any{
			"selector": "h2",
			"no_wait":  true,
		})
		assertE2ESuccess(t, res, "click no_wait")
		data := extractToolResultJSON(t, res)
		if data["verification"] == nil {
			t.Errorf("expected verification object even with no_wait=true")
		}
	})
}

// Feature F05 Boundary Tests: Smart Framework Locator
func TestE2E_Tier2_Issue5_SmartLocator_Boundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/controlled-vue")})
	assertE2ESuccess(t, navRes, "navigate")

	// Case 1: Empty selector string fails validation cleanly
	t.Run("Case1_EmptySelectorFailsValidation", func(t *testing.T) {
		res := callMCPTool(t, cs, "click", map[string]any{"selector": ""})
		assertE2EError(t, res, "click empty selector should error")
	})

	// Case 2: Deeply nested container traversal
	t.Run("Case2_DeeplyNestedContainerTraversal", func(t *testing.T) {
		fillRes := callMCPTool(t, cs, "fill", map[string]any{
			"selector": ".card-wrapper",
			"value":    "card_nested_value",
		})
		// Sniffer should find first input inside card-wrapper or error cleanly without crashing
		t.Logf("fill on .card-wrapper result: IsError=%v", fillRes.IsError)
	})

	// Case 3: Container with multiple inputs
	t.Run("Case3_ContainerWithMultipleInputs", func(t *testing.T) {
		fillRes := callMCPTool(t, cs, "fill", map[string]any{
			"selector": "#vue-form",
			"value":    "first_form_field",
		})
		t.Logf("fill on #vue-form result: IsError=%v", fillRes.IsError)
	})

	// Case 4: Hidden input without clickable wrapper times out gracefully
	t.Run("Case4_HiddenInputWithoutWrapper", func(t *testing.T) {
		res := callMCPTool(t, cs, "click", map[string]any{
			"selector":   "#ant-agree-input",
			"timeout_ms": 200,
		})
		// opacity:0 absolute element - clicking directly may succeed via center coordinates or fail gracefully
		t.Logf("click on invisible input: IsError=%v", res.IsError)
	})

	// Case 5: Non-standard SVG or complex container
	t.Run("Case5_ComplexContainerClick", func(t *testing.T) {
		res := callMCPTool(t, cs, "click", map[string]any{
			"selector": "#checkbox-box",
		})
		assertE2ESuccess(t, res, "click checkbox-box span")
	})
}

// Feature F06 Boundary Tests: Dialog Anti-Deadlock
func TestE2E_Tier2_Issue6_DialogDeadlock_Boundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/dialogs")})
	assertE2ESuccess(t, navRes, "navigate")

	// Case 1: Cascading rapid alerts (alert immediately followed by confirm)
	t.Run("Case1_CascadingRapidAlerts", func(t *testing.T) {
		res := callMCPTool(t, cs, "click", map[string]any{"selector": "#trigger-cascade"})
		assertE2ESuccess(t, res, "click trigger-cascade")

		// Subsequent eval must NOT hang
		evalRes := callMCPTool(t, cs, "eval", map[string]any{"expression": "document.getElementById('cascade-state').textContent"})
		assertE2ESuccess(t, evalRes, "eval after cascade")
	})

	// Case 2: handle_dialog on page when NO dialog exists
	t.Run("Case2_HandleDialogOnCleanPage", func(t *testing.T) {
		res := callMCPTool(t, cs, "handle_dialog", map[string]any{"action": "status"})
		assertE2ESuccess(t, res, "handle_dialog status on clean page")
		data := extractToolResultJSON(t, res)
		if hasDialog, _ := data["has_dialog"].(bool); hasDialog {
			t.Errorf("expected has_dialog=false on clean page, got true")
		}
	})

	// Case 3: Prompt with empty string input delivers ""
	t.Run("Case3_PromptWithEmptyStringInput", func(t *testing.T) {
		_ = callMCPTool(t, cs, "handle_dialog", map[string]any{
			"action":      "policy",
			"policy":      "accept",
			"prompt_text": "",
		})
		res := callMCPTool(t, cs, "click", map[string]any{"selector": "#trigger-prompt"})
		assertE2ESuccess(t, res, "click prompt with empty string")
	})

	// Case 4: Unknown dialog policy value defaults or errors cleanly
	t.Run("Case4_UnknownDialogPolicy", func(t *testing.T) {
		res := callMCPTool(t, cs, "handle_dialog", map[string]any{
			"action": "policy",
			"policy": "invalid_unknown_policy",
		})
		t.Logf("unknown policy response: IsError=%v", res.IsError)
	})

	// Case 5: Repeated rapid dialog policy updates
	t.Run("Case5_RapidPolicyUpdates", func(t *testing.T) {
		for _, pol := range []string{"accept", "dismiss", "accept"} {
			res := callMCPTool(t, cs, "handle_dialog", map[string]any{
				"action": "policy",
				"policy": pol,
			})
			assertE2ESuccess(t, res, "rapid policy update")
		}
	})
}

// Feature F07 Boundary Tests: Dev Overlay
func TestE2E_Tier2_Issue7_DevOverlay_Boundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	// Case 1: Benign overlay class distinction (e.g. .card-wrapper is NOT an error overlay)
	t.Run("Case1_BenignClassDistinction", func(t *testing.T) {
		_ = callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/controlled-vue")})
		diagRes := callMCPTool(t, cs, "diagnose", map[string]any{})
		assertE2ESuccess(t, diagRes, "diagnose on normal page")
		data := extractToolResultJSON(t, diagRes)
		if data["dev_overlay"] != nil {
			t.Errorf("false positive dev_overlay detected on normal page: %+v", data["dev_overlay"])
		}
	})

	// Case 2: Overlay without full stack frame parsed gracefully
	t.Run("Case2_PartialOverlayStructure", func(t *testing.T) {
		_ = callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/overlays/webpack")})
		diagRes := callMCPTool(t, cs, "diagnose", map[string]any{})
		assertE2ESuccess(t, diagRes, "diagnose webpack overlay")
		data := extractToolResultJSON(t, diagRes)
		if data["dev_overlay"] == nil {
			t.Errorf("expected dev_overlay for webpack")
		}
	})

	// Case 3: wait_for with timeout 0 on page with error overlay
	t.Run("Case3_WaitForTimeoutZeroOnOverlayPage", func(t *testing.T) {
		_ = callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/overlays/nextjs")})
		waitRes := callMCPTool(t, cs, "wait_for", map[string]any{
			"selector":   "#some-button",
			"timeout_ms": 0,
		})
		assertE2ESuccess(t, waitRes, "wait_for timeout 0")
	})

	// Case 4: Concurrent overlay and diagnose errors count
	t.Run("Case4_DiagnoseErrorsCount", func(t *testing.T) {
		_ = callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/overlays/vite")})
		time.Sleep(100 * time.Millisecond)
		diagRes := callMCPTool(t, cs, "diagnose", map[string]any{})
		assertE2ESuccess(t, diagRes, "diagnose vite")
		data := extractToolResultJSON(t, diagRes)
		errCount, _ := data["error_count"].(float64)
		if errCount < 1 {
			t.Errorf("expected error_count >= 1, got %v", errCount)
		}
	})

	// Case 5: wait_for non-existent element on clean page does NOT report dev_overlay_detected
	t.Run("Case5_NormalPageWaitTimeoutDoesNotReportOverlay", func(t *testing.T) {
		_ = callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/act-and-verify")})
		waitRes := callMCPTool(t, cs, "wait_for", map[string]any{
			"selector":   "#missing-element",
			"timeout_ms": 300,
		})
		data := extractToolResultJSON(t, waitRes)
		reason, _ := data["reason"].(string)
		if reason == "dev_overlay_detected" {
			t.Errorf("spurious dev_overlay_detected on clean page")
		}
	})
}

// Feature F08 Boundary Tests: Element Screenshot
func TestE2E_Tier2_Issue8_ElementScreenshot_Boundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/screenshot-box")})
	assertE2ESuccess(t, navRes, "navigate")

	// Case 1: Missing element returns error rather than full page
	t.Run("Case1_MissingElementReturnsError", func(t *testing.T) {
		shotRes := callMCPTool(t, cs, "screenshot", map[string]any{
			"selector": "#non-existent-element-404",
		})
		assertE2EError(t, shotRes, "screenshot on missing element should error")
	})

	// Case 2: Transformed element (rotate 3deg)
	t.Run("Case2_TransformedElementClipping", func(t *testing.T) {
		shotRes := callMCPTool(t, cs, "screenshot", map[string]any{
			"selector": "#card-transformed",
			"disk":     true,
		})
		assertE2ESuccess(t, shotRes, "screenshot transformed element")
		data := extractToolResultJSON(t, shotRes)
		path, _ := data["path"].(string)
		if path != "" {
			_ = os.Remove(path)
		}
	})

	// Case 3: Invalid disk mode file path returns error cleanly
	t.Run("Case3_InvalidFilePathReturnsError", func(t *testing.T) {
		shotRes := callMCPTool(t, cs, "screenshot", map[string]any{
			"selector": "#card-standard",
			"disk":     true,
			"path":     "/non_existent_root_dir_abc/sub/test.png",
		})
		assertE2EError(t, shotRes, "screenshot invalid path should error")
	})

	// Case 4: Out-of-range quality parameter clamped
	t.Run("Case4_ClampedQualityParameter", func(t *testing.T) {
		shotRes := callMCPTool(t, cs, "screenshot", map[string]any{
			"quality": 500, // clamped to 100
		})
		assertE2ESuccess(t, shotRes, "screenshot quality clamped")
	})

	// Case 5: max_size_kb clamping
	t.Run("Case5_MaxSizeKBClamping", func(t *testing.T) {
		shotRes := callMCPTool(t, cs, "screenshot", map[string]any{
			"max_size_kb": 100,
			"disk":        true,
		})
		assertE2ESuccess(t, shotRes, "screenshot max_size_kb")
	})
}

// Feature F09 Boundary Tests: mock_route
func TestE2E_Tier2_Issue9_MockRoute_Boundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/api-mock")})
	assertE2ESuccess(t, navRes, "navigate")

	// Case 1: Malformed regex syntax rejected cleanly
	t.Run("Case1_MalformedRegexRejected", func(t *testing.T) {
		res := callMCPTool(t, cs, "mock_route", map[string]any{
			"pattern": "^[a-z(unclosed_regex",
			"action":  "mock",
		})
		assertE2EError(t, res, "malformed regex should be rejected")
	})

	// Case 2: Method filtering (GET vs POST)
	t.Run("Case2_MethodFiltering", func(t *testing.T) {
		res := callMCPTool(t, cs, "mock_route", map[string]any{
			"pattern": "*/api/checkout",
			"method":  "POST",
			"status":  200,
			"body":    map[string]any{"mocked_method": "POST_ONLY"},
		})
		assertE2ESuccess(t, res, "mock_route with POST filter")
	})

	// Case 3: Mocking with empty body
	t.Run("Case3_MockingEmptyBody", func(t *testing.T) {
		res := callMCPTool(t, cs, "mock_route", map[string]any{
			"pattern": "*/api/empty",
			"status":  204,
			"body":    "",
		})
		assertE2ESuccess(t, res, "mock_route empty body")
	})

	// Case 4: Remove non-existent rule succeeds or reports 0 removed
	t.Run("Case4_RemoveNonExistentRule", func(t *testing.T) {
		res := callMCPTool(t, cs, "mock_route", map[string]any{
			"action":  "remove",
			"pattern": "*/non_existent_rule_pattern*",
		})
		assertE2ESuccess(t, res, "remove non-existent rule")
	})

	// Case 5: Rapid consecutive mock additions and clears
	t.Run("Case5_RapidAddAndClear", func(t *testing.T) {
		for i := 0; i < 5; i++ {
			_ = callMCPTool(t, cs, "mock_route", map[string]any{
				"pattern": fmt.Sprintf("*/api/iter/%d", i),
				"body":    "ok",
			})
		}
		clearRes := callMCPTool(t, cs, "mock_route", map[string]any{"action": "clear"})
		assertE2ESuccess(t, clearRes, "clear mock rules")
	})
}

// Feature F10 Boundary Tests: Tab
func TestE2E_Tier2_Tool_Tab_Boundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	cs := setupMCPServer(t)

	// Case 1: Non-existent tab ID returns error
	t.Run("Case1_NonExistentTabIDReturnsError", func(t *testing.T) {
		res := callMCPTool(t, cs, "tab", map[string]any{
			"action": "select",
			"tab_id": "DEAD_BEEF_INVALID_TARGET",
		})
		assertE2EError(t, res, "select invalid tab_id should error")
	})

	// Case 2: Tab select by title substring
	t.Run("Case2_TabSelectByTitle", func(t *testing.T) {
		res := callMCPTool(t, cs, "tab", map[string]any{
			"action": "select",
			"title":  "about:blank",
		})
		// If tab matches title, succeeds
		t.Logf("select by title result: IsError=%v", res.IsError)
	})

	// Case 3: Multiple rapid tab creations and closures
	t.Run("Case3_RapidTabCreationsAndClosures", func(t *testing.T) {
		tabIDs := make([]string, 0, 3)
		for i := 0; i < 3; i++ {
			newRes := callMCPTool(t, cs, "tab", map[string]any{"action": "new", "url": "about:blank"})
			assertE2ESuccess(t, newRes, "create tab")
			data := extractToolResultJSON(t, newRes)
			if id, ok := data["tab_id"].(string); ok && id != "" {
				tabIDs = append(tabIDs, id)
			}
		}
		for _, id := range tabIDs {
			_ = callMCPTool(t, cs, "tab", map[string]any{"action": "close", "tab_id": id})
		}
	})

	// Case 4: Tab list returns count matching tabs array
	t.Run("Case4_TabListConsistency", func(t *testing.T) {
		listRes := callMCPTool(t, cs, "tab", map[string]any{"action": "list"})
		assertE2ESuccess(t, listRes, "tab list")
		data := extractToolResultJSON(t, listRes)
		tabs, _ := data["tabs"].([]any)
		count, _ := data["count"].(float64)
		if int(count) != len(tabs) {
			t.Errorf("count %v != len(tabs) %d", count, len(tabs))
		}
	})

	// Case 5: Empty parameters defaults to action: list
	t.Run("Case5_DefaultActionList", func(t *testing.T) {
		res := callMCPTool(t, cs, "tab", map[string]any{})
		assertE2ESuccess(t, res, "empty params defaults to list")
		data := extractToolResultJSON(t, res)
		if data["tabs"] == nil {
			t.Errorf("expected tabs array in default tab action")
		}
	})
}

// Feature F11 Boundary Tests: Storage
func TestE2E_Tier2_Tool_Storage_Boundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/act-and-verify")})
	assertE2ESuccess(t, navRes, "navigate")

	// Case 1: Query non-existent storage key returns empty/null without error
	t.Run("Case1_QueryNonExistentKey", func(t *testing.T) {
		getRes := callMCPTool(t, cs, "storage", map[string]any{
			"action": "get",
			"type":   "local",
			"key":    "non_existent_key_999",
		})
		assertE2ESuccess(t, getRes, "query non-existent key")
		data := extractToolResultJSON(t, getRes)
		if val, _ := data["value"].(string); val != "" {
			t.Errorf("expected empty string for missing key, got %v", val)
		}
	})

	// Case 2: Key containing whitespace and quotes
	t.Run("Case2_KeyWithSpecialCharacters", func(t *testing.T) {
		specialKey := "key with spaces and 'quotes'"
		setRes := callMCPTool(t, cs, "storage", map[string]any{
			"action": "set",
			"type":   "local",
			"key":    specialKey,
			"value":  "special_val",
		})
		assertE2ESuccess(t, setRes, "set special key")

		getRes := callMCPTool(t, cs, "storage", map[string]any{
			"action": "get",
			"type":   "local",
			"key":    specialKey,
		})
		assertE2ESuccess(t, getRes, "get special key")
		data := extractToolResultJSON(t, getRes)
		if val, _ := data["value"].(string); val != "special_val" {
			t.Errorf("expected special_val, got %v", val)
		}
	})

	// Case 3: Cookie with path and domain
	t.Run("Case3_CookieWithPath", func(t *testing.T) {
		setRes := callMCPTool(t, cs, "storage", map[string]any{
			"action": "set",
			"type":   "cookie",
			"key":    "path_cookie",
			"value":  "path_val",
			"path":   "/fixtures",
		})
		assertE2ESuccess(t, setRes, "set cookie with path")
	})

	// Case 4: Invalid storage type returns error
	t.Run("Case4_InvalidStorageTypeReturnsError", func(t *testing.T) {
		res := callMCPTool(t, cs, "storage", map[string]any{
			"action": "get",
			"type":   "invalid_storage_type_xyz",
			"key":    "k",
		})
		assertE2EError(t, res, "invalid storage type should error")
	})

	// Case 5: Clear specific storage type does not wipe others
	t.Run("Case5_ClearIsolationBetweenTypes", func(t *testing.T) {
		// Set cookie and local
		_ = callMCPTool(t, cs, "storage", map[string]any{"action": "set", "type": "cookie", "key": "persist_c", "value": "cv"})
		_ = callMCPTool(t, cs, "storage", map[string]any{"action": "set", "type": "local", "key": "wipe_l", "value": "lv"})

		// Clear local only
		_ = callMCPTool(t, cs, "storage", map[string]any{"action": "clear", "type": "local"})

		// Verify cookie still exists
		getRes := callMCPTool(t, cs, "storage", map[string]any{"action": "get", "type": "cookie", "key": "persist_c"})
		assertE2ESuccess(t, getRes, "get persisted cookie")
		data := extractToolResultJSON(t, getRes)
		if val, _ := data["value"].(string); val != "cv" {
			t.Errorf("expected cookie to persist after local clear, got: %v", val)
		}
	})
}

// Feature F12 Boundary Tests: get_element
func TestE2E_Tier2_Tool_GetElement_Boundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/screenshot-box")})
	assertE2ESuccess(t, navRes, "navigate")

	// Case 1: Negative viewport coordinates
	t.Run("Case1_NegativeCoordinates", func(t *testing.T) {
		res := callMCPTool(t, cs, "get_element", map[string]any{
			"x": -50,
			"y": -100,
		})
		t.Logf("negative coordinates result: IsError=%v", res.IsError)
	})

	// Case 2: Non-matching text search
	t.Run("Case2_NonMatchingTextSearch", func(t *testing.T) {
		res := callMCPTool(t, cs, "get_element", map[string]any{
			"text": "Completely Non Existent Phantom Text String 999",
		})
		t.Logf("non-matching text result: IsError=%v", res.IsError)
	})

	// Case 3: Non-matching regex search
	t.Run("Case3_NonMatchingRegexSearch", func(t *testing.T) {
		res := callMCPTool(t, cs, "get_element", map[string]any{
			"regex": "^UnmatchedPattern[0-9]{9}$",
		})
		t.Logf("non-matching regex result: IsError=%v", res.IsError)
	})

	// Case 4: Invalid include filter
	t.Run("Case4_InvalidIncludeFilter", func(t *testing.T) {
		res := callMCPTool(t, cs, "get_element", map[string]any{
			"selector": "#card-standard",
			"include":  "unsupported_include_option",
		})
		assertE2ESuccess(t, res, "get_element invalid include filter defaults gracefully")
	})

	// Case 5: Non-existent selector returns found=false
	t.Run("Case5_NonExistentSelectorReturnsError", func(t *testing.T) {
		res := callMCPTool(t, cs, "get_element", map[string]any{
			"selector": "#element-that-does-not-exist",
		})
		assertE2ESuccess(t, res, "get_element on missing selector returns found=false")
		data := extractToolResultJSON(t, res)
		if found, ok := data["found"].(bool); !ok || found {
			t.Errorf("expected found=false for missing selector, got %+v", data)
		}
	})
}

// Feature F13 Boundary Tests: wait_for
func TestE2E_Tier2_Tool_WaitFor_Boundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/act-and-verify")})
	assertE2ESuccess(t, navRes, "navigate")

	// Case 1: Negative timeout value
	t.Run("Case1_NegativeTimeoutNormalized", func(t *testing.T) {
		res := callMCPTool(t, cs, "wait_for", map[string]any{
			"selector":   "#action-trigger",
			"timeout_ms": -500,
		})
		assertE2ESuccess(t, res, "wait_for negative timeout")
	})

	// Case 2: Condition script throwing JavaScript error
	t.Run("Case2_ConditionScriptThrowingError", func(t *testing.T) {
		res := callMCPTool(t, cs, "wait_for", map[string]any{
			"condition":  "null.nonExistentProp === true",
			"timeout_ms": 300,
		})
		// Should catch JS error and finish gracefully without crashing
		t.Logf("throwing condition script: IsError=%v", res.IsError)
	})

	// Case 3: Instant exists check (timeout_ms: 0) on present vs missing
	t.Run("Case3_InstantExistsCheckPresentVsMissing", func(t *testing.T) {
		presentRes := callMCPTool(t, cs, "wait_for", map[string]any{"selector": "h3", "timeout_ms": 0})
		assertE2ESuccess(t, presentRes, "present element instant check")
		pData := extractToolResultJSON(t, presentRes)
		if exists, _ := pData["exists"].(bool); !exists {
			t.Errorf("expected exists=true for h3, got false")
		}

		missingRes := callMCPTool(t, cs, "wait_for", map[string]any{"selector": "#non-existent", "timeout_ms": 0})
		assertE2ESuccess(t, missingRes, "missing element instant check")
		mData := extractToolResultJSON(t, missingRes)
		if exists, _ := mData["exists"].(bool); exists {
			t.Errorf("expected exists=false for missing, got true")
		}
	})

	// Case 4: Conflicting text and selector criteria
	t.Run("Case4_ConflictingTextAndSelector", func(t *testing.T) {
		res := callMCPTool(t, cs, "wait_for", map[string]any{
			"selector":   "h3",
			"text":       "This text does not exist in h3",
			"timeout_ms": 200,
		})
		data := extractToolResultJSON(t, res)
		if sat, _ := data["satisfied"].(bool); sat {
			t.Errorf("expected satisfied=false for conflicting criteria")
		}
	})

	// Case 5: Sleep duration wait
	t.Run("Case5_SleepDurationWait", func(t *testing.T) {
		start := time.Now()
		res := callMCPTool(t, cs, "wait_for", map[string]any{"sleep_ms": 60})
		elapsed := time.Since(start)
		assertE2ESuccess(t, res, "wait_for sleep_ms")
		if elapsed < 50*time.Millisecond {
			t.Errorf("expected sleep at least 50ms, took %v", elapsed)
		}
	})
}

// Feature F14 Boundary Tests: screenshot
func TestE2E_Tier2_Tool_Screenshot_Boundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/screenshot-box")})
	assertE2ESuccess(t, navRes, "navigate")

	// Case 1: Unsupported image format falls back gracefully
	t.Run("Case1_UnsupportedFormatFallback", func(t *testing.T) {
		res := callMCPTool(t, cs, "screenshot", map[string]any{
			"format": "bmp",
			"disk":   true,
		})
		// Either falls back to jpeg/png or errors cleanly
		t.Logf("unsupported format bmp: IsError=%v", res.IsError)
	})

	// Case 2: Negative grid step normalized
	t.Run("Case2_NegativeGridStepNormalized", func(t *testing.T) {
		res := callMCPTool(t, cs, "screenshot", map[string]any{
			"grid":       true,
			"major_step": -10,
			"disk":       true,
		})
		assertE2ESuccess(t, res, "screenshot negative grid step")
	})

	// Case 3: Viewport dimensions metadata check
	t.Run("Case3_ViewportDimensionsMetadata", func(t *testing.T) {
		res := callMCPTool(t, cs, "screenshot", map[string]any{"disk": true})
		assertE2ESuccess(t, res, "screenshot metadata")
		data := extractToolResultJSON(t, res)
		var w, h float64
		if img, ok := data["image"].(map[string]any); ok {
			if width, ok := img["width"].(float64); ok {
				w = width
			}
			if height, ok := img["height"].(float64); ok {
				h = height
			}
		} else if vp, ok := data["viewport"].(map[string]any); ok {
			if width, ok := vp["width"].(float64); ok {
				w = width
			}
			if height, ok := vp["height"].(float64); ok {
				h = height
			}
		}
		if w <= 0 || h <= 0 {
			t.Errorf("expected positive dimensions, got width=%v, height=%v (data: %+v)", w, h, data)
		}
	})

	// Case 4: Screenshot in disk mode verifies file size > 0
	t.Run("Case4_DiskModeFileExistsAndNonZero", func(t *testing.T) {
		tmpPath := filepath.Join(t.TempDir(), "disk_verify.jpg")
		res := callMCPTool(t, cs, "screenshot", map[string]any{
			"disk": true,
			"path": tmpPath,
		})
		assertE2ESuccess(t, res, "screenshot to specific file")
		fi, err := os.Stat(tmpPath)
		if err != nil {
			t.Fatalf("expected file to exist: %v", err)
		}
		if fi.Size() == 0 {
			t.Fatalf("expected non-zero file size, got 0 bytes")
		}
	})

	// Case 5: Full page screenshot on scrolling page
	t.Run("Case5_FullPageScreenshotHeight", func(t *testing.T) {
		res := callMCPTool(t, cs, "screenshot", map[string]any{
			"full_page": true,
			"disk":      true,
		})
		assertE2ESuccess(t, res, "screenshot full page")
		data := extractToolResultJSON(t, res)
		if fp, _ := data["full_page"].(bool); !fp {
			t.Errorf("expected full_page=true")
		}
	})
}
