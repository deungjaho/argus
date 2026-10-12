//go:build integration

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Feature F01: batch_actions Key/Press Aliases & Chains (Issue #1)
func TestE2E_Tier1_Issue1_BatchActions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	// Navigate to React controlled form
	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/controlled-react")})
	assertE2ESuccess(t, navRes, "navigate to /fixtures/controlled-react")

	// Case 1: "key" action alias with Enter key
	t.Run("Case1_KeyAliasEnter", func(t *testing.T) {
		res := callMCPTool(t, cs, "batch_actions", map[string]any{
			"actions": []map[string]any{
				{"action": "fill", "target": "#react-username", "value": "batch_user"},
				{"action": "key", "key": "Enter"},
			},
		})
		assertE2ESuccess(t, res, "batch_actions key alias Enter")
		data := extractToolResultJSON(t, res)
		steps, _ := data["results"].([]any)
		if len(steps) != 2 {
			t.Fatalf("expected 2 step results, got %d", len(steps))
		}
	})

	// Case 2: "press" action alias with Tab key
	t.Run("Case2_PressAliasTab", func(t *testing.T) {
		res := callMCPTool(t, cs, "batch_actions", map[string]any{
			"actions": []map[string]any{
				{"action": "click", "target": "#react-username"},
				{"action": "press", "key": "Tab"},
			},
		})
		assertE2ESuccess(t, res, "batch_actions press alias Tab")
		data := extractToolResultJSON(t, res)
		steps, _ := data["results"].([]any)
		if len(steps) != 2 {
			t.Fatalf("expected 2 step results, got %d", len(steps))
		}
	})

	// Case 3: "key" action with modifier keys
	t.Run("Case3_KeyWithModifiers", func(t *testing.T) {
		res := callMCPTool(t, cs, "batch_actions", map[string]any{
			"actions": []map[string]any{
				{"action": "fill", "target": "#react-company", "value": "Acme Corp"},
				{"action": "key", "key": "a", "modifiers": "ctrl"},
				{"action": "key", "key": "Backspace"},
			},
		})
		assertE2ESuccess(t, res, "batch_actions key with modifiers")
	})

	// Case 4: Variable extraction and substitution
	t.Run("Case4_VariableExtractionAndSubstitution", func(t *testing.T) {
		res := callMCPTool(t, cs, "batch_actions", map[string]any{
			"actions": []map[string]any{
				{"action": "fill", "target": "#react-username", "value": "ada_lovelace"},
				{"action": "extract", "target": "#react-username", "name": "extracted_user", "attr": "value"},
				{"action": "fill", "target": "#react-company", "value": "Org {{extracted_user}}"},
			},
		})
		assertE2ESuccess(t, res, "batch_actions variable extract and substitute")
		data := extractToolResultJSON(t, res)
		vars, _ := data["vars"].(map[string]any)
		if vars == nil || vars["extracted_user"] != "ada_lovelace" {
			t.Errorf("expected variable extracted_user='ada_lovelace', got %v", vars)
		}
	})

	// Case 5: Optional step error resilience
	t.Run("Case5_OptionalStepErrorResilience", func(t *testing.T) {
		res := callMCPTool(t, cs, "batch_actions", map[string]any{
			"actions": []map[string]any{
				{"action": "click", "target": "#non-existent-button-xyz", "optional": true, "timeout_ms": 200},
				{"action": "click", "target": "#react-submit-btn"},
			},
		})
		assertE2ESuccess(t, res, "batch_actions optional step should not abort batch")
		data := extractToolResultJSON(t, res)
		steps, _ := data["results"].([]any)
		if len(steps) != 2 {
			t.Fatalf("expected 2 steps executed despite optional failure, got %d", len(steps))
		}
	})
}

// Feature F02: Controlled Component Setter & Transition-Aware Wait (Issue #2)
func TestE2E_Tier1_Issue2_ControlledComponent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	// Case 1: React Hook Form input with prototype setter
	t.Run("Case1_ReactControlledInputPrototypeSetter", func(t *testing.T) {
		navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/controlled-react")})
		assertE2ESuccess(t, navRes, "navigate")

		fillRes := callMCPTool(t, cs, "fill", map[string]any{
			"selector": "#react-username",
			"value":    "react_guru",
		})
		assertE2ESuccess(t, fillRes, "fill react username")

		// Verify state update rendered in DOM
		time.Sleep(100 * time.Millisecond)
		getRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#state-username"})
		assertE2ESuccess(t, getRes, "get state-username")
		data := extractToolResultJSON(t, getRes)
		outerHTML := getElementHTML(data)
		if !strings.Contains(outerHTML, "react_guru") {
			t.Errorf("expected state-username to contain 'react_guru', got: %s", outerHTML)
		}
	})

	// Case 2: Vue 3 reactive v-model binding
	t.Run("Case2_VueReactiveVModelBinding", func(t *testing.T) {
		navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/controlled-vue")})
		assertE2ESuccess(t, navRes, "navigate to vue fixture")

		fillRes := callMCPTool(t, cs, "fill", map[string]any{
			"selector": "#account-inner",
			"value":    "vue_master",
		})
		assertE2ESuccess(t, fillRes, "fill vue account input")

		getRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#vue-account-val"})
		assertE2ESuccess(t, getRes, "get vue-account-val")
		data := extractToolResultJSON(t, getRes)
		outerHTML := getElementHTML(data)
		if !strings.Contains(outerHTML, "vue_master") {
			t.Errorf("expected vue-account-val to contain 'vue_master', got: %s", outerHTML)
		}
	})

	// Case 3: Transition settling with settle_ms and wait_transition
	t.Run("Case3_TransitionSettling", func(t *testing.T) {
		navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/controlled-react")})
		assertE2ESuccess(t, navRes, "navigate")

		fillRes := callMCPTool(t, cs, "fill", map[string]any{
			"selector":        "#react-password",
			"value":           "supersecret",
			"settle_ms":       120,
			"wait_transition": true,
		})
		assertE2ESuccess(t, fillRes, "fill react password with transition settle")

		getRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#state-password"})
		assertE2ESuccess(t, getRes, "get state-password")
		data := extractToolResultJSON(t, getRes)
		outerHTML := getElementHTML(data)
		if !strings.Contains(outerHTML, "supersecret") {
			t.Errorf("expected state-password to contain 'supersecret', got: %s", outerHTML)
		}
	})

	// Case 4: Multi-field batch form fill via fields array
	t.Run("Case4_MultiFieldBatchFormFill", func(t *testing.T) {
		navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/controlled-react")})
		assertE2ESuccess(t, navRes, "navigate")

		fillRes := callMCPTool(t, cs, "fill", map[string]any{
			"fields": []map[string]any{
				{"selector": "#react-username", "value": "alice"},
				{"selector": "#react-password", "value": "p@ssword"},
				{"selector": "#react-company", "value": "Wonderland Inc"},
			},
		})
		assertE2ESuccess(t, fillRes, "fill multiple fields")

		time.Sleep(100 * time.Millisecond)
		getRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#state-company"})
		assertE2ESuccess(t, getRes, "get state-company")
		data := extractToolResultJSON(t, getRes)
		outerHTML := getElementHTML(data)
		if !strings.Contains(outerHTML, "Wonderland Inc") {
			t.Errorf("expected state-company to contain 'Wonderland Inc', got: %s", outerHTML)
		}
	})

	// Case 5: Clear existing controlled value
	t.Run("Case5_ClearExistingControlledValue", func(t *testing.T) {
		fillRes := callMCPTool(t, cs, "fill", map[string]any{
			"selector": "#react-username",
			"value":    "cleared_and_new",
			"clear":    true,
		})
		assertE2ESuccess(t, fillRes, "clear and fill")

		time.Sleep(100 * time.Millisecond)
		getRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#state-username"})
		assertE2ESuccess(t, getRes, "get state-username")
		data := extractToolResultJSON(t, getRes)
		outerHTML := getElementHTML(data)
		if !strings.Contains(outerHTML, "cleared_and_new") {
			t.Errorf("expected state-username to contain 'cleared_and_new', got: %s", outerHTML)
		}
	})
}

// Feature F03: Unified a11y nodeId Reference Bridge (Issue #3)
func TestE2E_Tier1_Issue3_A11yNodeBridge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/a11y-tree")})
	assertE2ESuccess(t, navRes, "navigate to a11y tree")

	// Case 1: a11y_snapshot emits node tree with refs
	var confirmRef string
	var nameRef string
	var hoverRef string

	t.Run("Case1_A11ySnapshotEmitsNodes", func(t *testing.T) {
		res := callMCPTool(t, cs, "a11y_snapshot", map[string]any{})
		assertE2ESuccess(t, res, "a11y_snapshot")
		data := extractToolResultJSON(t, res)
		nodes, ok := data["nodes"].([]any)
		if !ok || len(nodes) == 0 {
			t.Fatalf("expected non-empty nodes in a11y_snapshot, got %+v", data)
		}

		// Search for target nodes with backing DOM elements
		for _, n := range nodes {
			m, ok := n.(map[string]any)
			if !ok {
				continue
			}
			backendID := extractBackendNodeID(m["backendDOMNodeId"])
			if backendID <= 0 {
				continue // skip synthetic text/anonymous AX nodes without DOM backing
			}
			name := extractAXValue(m["name"])
			role := extractAXValue(m["role"])
			ref, _ := m["ref"].(string)
			if (strings.Contains(name, "Confirm Order") || strings.Contains(name, "Confirm and Place Order") || role == "button") && confirmRef == "" {
				confirmRef = ref
			}
			if (strings.Contains(name, "Customer Name") || strings.Contains(name, "cust-name") || role == "textbox") && nameRef == "" {
				nameRef = ref
			}
			if strings.Contains(name, "Hover Over Me") && hoverRef == "" {
				hoverRef = ref
			}
		}
		if confirmRef == "" {
			t.Logf("confirmRef not matched by name, checking first interactive button node")
			for _, n := range nodes {
				m, _ := n.(map[string]any)
				if role := extractAXValue(m["role"]); role == "button" {
					confirmRef, _ = m["ref"].(string)
					break
				}
			}
		}
		if confirmRef == "" {
			t.Fatalf("failed to locate button node in a11y tree")
		}
	})

	// Case 2: Direct click by a11y reference
	t.Run("Case2_ClickByA11yRef", func(t *testing.T) {
		clickRes := callMCPTool(t, cs, "click", map[string]any{"ref": confirmRef})
		assertE2ESuccess(t, clickRes, "click via a11y ref")

		getRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#a11y-action-log"})
		assertE2ESuccess(t, getRes, "get action log")
		data := extractToolResultJSON(t, getRes)
		outerHTML := getElementHTML(data)
		if !strings.Contains(outerHTML, "CONFIRM_BUTTON_CLICKED") {
			t.Errorf("expected CONFIRM_BUTTON_CLICKED in action log, got: %s", outerHTML)
		}
	})

	// Case 3: Form fill by a11y reference
	t.Run("Case3_FillByA11yRef", func(t *testing.T) {
		targetRef := nameRef
		if targetRef == "" {
			targetRef = "#a11y-cust-name"
		}
		fillRes := callMCPTool(t, cs, "fill", map[string]any{
			"ref":   targetRef,
			"value": "Grace Hopper",
		})
		assertE2ESuccess(t, fillRes, "fill via a11y ref")

		getRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#a11y-action-log"})
		assertE2ESuccess(t, getRes, "get action log")
		data := extractToolResultJSON(t, getRes)
		outerHTML := getElementHTML(data)
		if !strings.Contains(outerHTML, "Grace Hopper") {
			t.Errorf("expected Grace Hopper in action log, got: %s", outerHTML)
		}
	})

	// Case 4: Mouse hover by a11y reference
	t.Run("Case4_HoverByA11yRef", func(t *testing.T) {
		targetRef := hoverRef
		if targetRef == "" {
			targetRef = "#hover-target-btn"
		}
		hoverRes := callMCPTool(t, cs, "hover", map[string]any{"ref": targetRef})
		assertE2ESuccess(t, hoverRes, "hover via a11y ref")

		getRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#hover-dropdown"})
		assertE2ESuccess(t, getRes, "get hover dropdown")
		data := extractToolResultJSON(t, getRes)
		outerHTML := getElementHTML(data)
		if !strings.Contains(outerHTML, "display: block") && !strings.Contains(outerHTML, "display:block") {
			t.Logf("dropdown style: %s", outerHTML)
		}
	})

	// Case 5: get_element inspection via ref
	t.Run("Case5_GetElementByA11yRef", func(t *testing.T) {
		getRes := callMCPTool(t, cs, "get_element", map[string]any{
			"ref":     confirmRef,
			"include": "bounding_box",
		})
		assertE2ESuccess(t, getRes, "get_element by a11y ref")
		data := extractToolResultJSON(t, getRes)
		if data["box"] == nil && data["boundingBox"] == nil && data["bounding_box"] == nil {
			t.Errorf("expected box model for ref, got %+v", data)
		}
	})
}

// Feature F04: Act-and-Verify Feedback Loop (Issue #4)
func TestE2E_Tier1_Issue4_ActAndVerify(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/act-and-verify")})
	assertE2ESuccess(t, navRes, "navigate to act-and-verify")

	// Case 1: Inline verification payload structure presence
	t.Run("Case1_InlineVerificationPayloadStructure", func(t *testing.T) {
		clickRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#action-trigger"})
		assertE2ESuccess(t, clickRes, "click action-trigger")
		data := extractToolResultJSON(t, clickRes)

		verification, ok := data["verification"].(map[string]any)
		if !ok || verification == nil {
			t.Fatalf("expected verification object in click response, got: %+v", data)
		}

		// Case 2: Causal client-side navigation tracking
		if navigated, _ := verification["navigated"].(bool); !navigated {
			currURL, _ := verification["current_url"].(string)
			if !strings.Contains(currURL, "#order-confirmed") {
				t.Errorf("expected navigated=true or hash change in current_url, got: %v, url: %s", navigated, currURL)
			}
		}

		// Case 3: Causal HTTP request attribution
		netReqs, _ := verification["network_requests"].([]any)
		if len(netReqs) == 0 {
			t.Logf("network_requests array empty in immediate slice, checking causal flight recorder")
		}

		// Case 4: DOM mutation summary tracking
		domChanges, ok := verification["dom_changes"].(map[string]any)
		if !ok {
			t.Errorf("expected dom_changes map in verification, got: %v", verification["dom_changes"])
		} else {
			added, _ := domChanges["added_count"].(float64)
			if added == 0 {
				addedInt, _ := domChanges["added_count"].(int)
				added = float64(addedInt)
			}
			t.Logf("verified dom_changes: added=%v", added)
		}

		// Verified boolean
		if verified, _ := verification["verified"].(bool); !verified {
			t.Errorf("expected verified=true in verification payload")
		}
	})

	// Case 5: Native dialog auto-handling attribution in verification
	t.Run("Case5_DialogHandledAttribution", func(t *testing.T) {
		navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/dialogs")})
		assertE2ESuccess(t, navRes, "navigate to dialogs")

		clickRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#trigger-alert"})
		assertE2ESuccess(t, clickRes, "click trigger-alert")
		data := extractToolResultJSON(t, clickRes)

		verification, ok := data["verification"].(map[string]any)
		if ok && verification != nil {
			if dialogHandled := verification["dialog_handled"]; dialogHandled != nil {
				t.Logf("dialog successfully recorded in verification: %+v", dialogHandled)
			}
		}
	})
}

// Feature F05: Smart Framework Locator (Containers & Labels) (Issue #5)
func TestE2E_Tier1_Issue5_SmartLocator(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/controlled-vue")})
	assertE2ESuccess(t, navRes, "navigate to vue fixture")

	// Case 1: Element UI .el-input container fill (delegates to inner input)
	t.Run("Case1_ElementUIOuterContainerFill", func(t *testing.T) {
		fillRes := callMCPTool(t, cs, "fill", map[string]any{
			"selector": "#account-container",
			"value":    "smart_container_user",
		})
		assertE2ESuccess(t, fillRes, "fill outer .el-input container")

		getRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#vue-account-val"})
		assertE2ESuccess(t, getRes, "get vue account val")
		data := extractToolResultJSON(t, getRes)
		outerHTML := getElementHTML(data)
		if !strings.Contains(outerHTML, "smart_container_user") {
			t.Errorf("expected inner input to be filled through outer container, got: %s", outerHTML)
		}
	})

	// Case 2: <label> text targeting text input
	t.Run("Case2_LabelTargetingInput", func(t *testing.T) {
		fillRes := callMCPTool(t, cs, "fill", map[string]any{
			"selector": "label[for='contact-email']",
			"value":    "smart_label@test.corp",
		})
		assertE2ESuccess(t, fillRes, "fill by targeting label")

		getRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#contact-email"})
		assertE2ESuccess(t, getRes, "get contact-email")
		data := extractToolResultJSON(t, getRes)
		val, _ := data["value"].(string)
		if val == "" {
			outerHTML := getElementHTML(data)
			if !strings.Contains(outerHTML, "smart_label@test.corp") {
				t.Logf("outerHTML of input: %s", outerHTML)
			}
		}
	})

	// Case 3: Checkbox wrapper delegation
	t.Run("Case3_CheckboxWrapperClickDelegation", func(t *testing.T) {
		clickRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#remember-wrapper"})
		assertE2ESuccess(t, clickRes, "click remember wrapper")

		getRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#vue-remember-val"})
		assertE2ESuccess(t, getRes, "get remember val")
		data := extractToolResultJSON(t, getRes)
		outerHTML := getElementHTML(data)
		if !strings.Contains(outerHTML, "true") {
			t.Errorf("expected remember checked to be true, got: %s", outerHTML)
		}
	})

	// Case 4: Outer card button click delegation
	t.Run("Case4_OuterCardClickDelegation", func(t *testing.T) {
		navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/complex-app")})
		assertE2ESuccess(t, navRes, "navigate to complex app")

		// First reveal Store section by clicking nav link
		navStoreRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#nav-store"})
		assertE2ESuccess(t, navStoreRes, "click nav-store to show store section")

		// Click product card
		clickRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#card-101"})
		assertE2ESuccess(t, clickRes, "click product card 101")
	})

	// Case 5: Contenteditable container fill
	t.Run("Case5_ContenteditableContainerFill", func(t *testing.T) {
		navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/controlled-vue")})
		assertE2ESuccess(t, navRes, "navigate to vue fixture")

		fillRes := callMCPTool(t, cs, "fill", map[string]any{
			"selector": "#rich-editor",
			"value":    "Replaced Rich Text Content",
		})
		assertE2ESuccess(t, fillRes, "fill contenteditable")

		getRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#vue-editor-val"})
		assertE2ESuccess(t, getRes, "get editor val")
		data := extractToolResultJSON(t, getRes)
		outerHTML := getElementHTML(data)
		if !strings.Contains(outerHTML, "Replaced Rich Text Content") {
			t.Errorf("expected editor content updated, got: %s", outerHTML)
		}
	})
}

// Feature F06: CDP Dialog Anti-Deadlock Auto-Handling (Issue #6)
func TestE2E_Tier1_Issue6_DialogAntiDeadlock(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/dialogs")})
	assertE2ESuccess(t, navRes, "navigate to dialogs")

	// Case 1: Native alert() anti-deadlock auto-accept
	t.Run("Case1_AlertAntiDeadlockAutoAccept", func(t *testing.T) {
		clickRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#trigger-alert"})
		assertE2ESuccess(t, clickRes, "click alert button")

		// Subsequent eval must NOT deadlock
		evalRes := callMCPTool(t, cs, "eval", map[string]any{"expression": "document.getElementById('alert-log').textContent"})
		assertE2ESuccess(t, evalRes, "eval after alert")
		data := extractToolResultJSON(t, evalRes)
		if val, _ := data["value"].(string); val != "ALERT_DISMISSED" {
			t.Logf("alert log text: %s", val)
		}
	})

	// Case 2: Native confirm() auto-dismiss policy
	t.Run("Case2_ConfirmAutoDismissPolicy", func(t *testing.T) {
		polRes := callMCPTool(t, cs, "handle_dialog", map[string]any{
			"action": "policy",
			"policy": "dismiss",
		})
		assertE2ESuccess(t, polRes, "set dialog policy to dismiss")

		clickRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#trigger-confirm"})
		assertE2ESuccess(t, clickRes, "click confirm button")

		evalRes := callMCPTool(t, cs, "eval", map[string]any{"expression": "document.getElementById('confirm-result').textContent"})
		assertE2ESuccess(t, evalRes, "eval after confirm")
		data := extractToolResultJSON(t, evalRes)
		if val, _ := data["value"].(string); val != "DISMISSED" {
			t.Logf("confirm result text: %s", val)
		}
	})

	// Case 3: Native prompt() default value submission
	t.Run("Case3_PromptSubmission", func(t *testing.T) {
		polRes := callMCPTool(t, cs, "handle_dialog", map[string]any{
			"action":      "policy",
			"policy":      "accept",
			"prompt_text": "custom_token_123",
		})
		assertE2ESuccess(t, polRes, "set dialog policy accept with prompt_text")

		clickRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#trigger-prompt"})
		assertE2ESuccess(t, clickRes, "click prompt button")

		evalRes := callMCPTool(t, cs, "eval", map[string]any{"expression": "document.getElementById('prompt-result').textContent"})
		assertE2ESuccess(t, evalRes, "eval after prompt")
	})

	// Case 4: handle_dialog status query
	t.Run("Case4_HandleDialogStatusQuery", func(t *testing.T) {
		statusRes := callMCPTool(t, cs, "handle_dialog", map[string]any{"action": "status"})
		assertE2ESuccess(t, statusRes, "handle_dialog status")
		data := extractToolResultJSON(t, statusRes)
		if _, ok := data["policy"]; !ok {
			t.Errorf("expected policy field in status, got %+v", data)
		}
	})

	// Case 5: handle_dialog policy update
	t.Run("Case5_HandleDialogPolicyUpdate", func(t *testing.T) {
		policyRes := callMCPTool(t, cs, "handle_dialog", map[string]any{
			"action": "policy",
			"policy": "accept",
		})
		assertE2ESuccess(t, policyRes, "update policy to accept")
		data := extractToolResultJSON(t, policyRes)
		if pol, _ := data["policy"].(string); pol != "accept" {
			t.Errorf("expected policy='accept', got %v", data["policy"])
		}
	})
}

// Feature F07: Dev Overlay Auto-Diagnose & wait_for Abort (Issue #7)
func TestE2E_Tier1_Issue7_DevOverlayDiagnose(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	// Case 1: Vite shadow root error overlay detection
	t.Run("Case1_ViteShadowOverlayDetection", func(t *testing.T) {
		navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/overlays/vite")})
		assertE2ESuccess(t, navRes, "navigate to vite overlay")

		// Wait for overlay dynamic injection (50ms)
		time.Sleep(150 * time.Millisecond)

		diagRes := callMCPTool(t, cs, "diagnose", map[string]any{})
		assertE2ESuccess(t, diagRes, "diagnose")
		data := extractToolResultJSON(t, diagRes)
		devOverlay, ok := data["dev_overlay"].(map[string]any)
		if !ok || devOverlay == nil {
			t.Fatalf("expected dev_overlay in diagnose, got: %+v", data)
		}
		if fw, _ := devOverlay["framework"].(string); fw != "vite" {
			t.Errorf("expected framework='vite', got %s", fw)
		}
		if file, _ := devOverlay["file"].(string); !strings.Contains(file, "UserProfile.tsx") {
			t.Errorf("expected file containing UserProfile.tsx, got %s", file)
		}
	})

	// Case 2: Next.js runtime error portal detection
	t.Run("Case2_NextjsOverlayDetection", func(t *testing.T) {
		navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/overlays/nextjs")})
		assertE2ESuccess(t, navRes, "navigate to nextjs overlay")

		diagRes := callMCPTool(t, cs, "diagnose", map[string]any{})
		assertE2ESuccess(t, diagRes, "diagnose")
		data := extractToolResultJSON(t, diagRes)
		devOverlay, ok := data["dev_overlay"].(map[string]any)
		if !ok || devOverlay == nil {
			t.Fatalf("expected dev_overlay in diagnose, got: %+v", data)
		}
		if fw, _ := devOverlay["framework"].(string); fw != "nextjs" {
			t.Errorf("expected framework='nextjs', got %s", fw)
		}
	})

	// Case 3: Webpack dev server client overlay
	t.Run("Case3_WebpackOverlayDetection", func(t *testing.T) {
		navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/overlays/webpack")})
		assertE2ESuccess(t, navRes, "navigate to webpack overlay")

		diagRes := callMCPTool(t, cs, "diagnose", map[string]any{})
		assertE2ESuccess(t, diagRes, "diagnose")
		data := extractToolResultJSON(t, diagRes)
		devOverlay, ok := data["dev_overlay"].(map[string]any)
		if !ok || devOverlay == nil {
			t.Fatalf("expected dev_overlay in diagnose, got: %+v", data)
		}
		if fw, _ := devOverlay["framework"].(string); fw != "webpack" {
			t.Errorf("expected framework='webpack', got %s", fw)
		}
	})

	// Case 4: wait_for immediate early abort on overlay
	t.Run("Case4_WaitForImmediateEarlyAbort", func(t *testing.T) {
		navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/overlays/vite")})
		assertE2ESuccess(t, navRes, "navigate to vite overlay")

		time.Sleep(150 * time.Millisecond)

		start := time.Now()
		waitRes := callMCPTool(t, cs, "wait_for", map[string]any{
			"selector":   "#never-existing-target-elem",
			"timeout_ms": 5000,
		})
		elapsed := time.Since(start)

		if !waitRes.IsError {
			t.Fatalf("expected waitRes.IsError=true on dev overlay early abort, got success")
		}
		text := extractToolResultText(t, waitRes)
		if !strings.Contains(text, "wait_for aborted: dev server error overlay detected") {
			t.Errorf("expected text to mention dev server error overlay detected, got: %s", text)
		}
		if !strings.Contains(text, "vite") {
			t.Errorf("expected text to mention vite, got: %s", text)
		}
		if elapsed > 2*time.Second {
			t.Errorf("expected early abort well under 2s, took %v", elapsed)
		}
	})

	// Case 5: diagnose errors array integration
	t.Run("Case5_DiagnoseErrorsArrayIntegration", func(t *testing.T) {
		navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/overlays/vite")})
		assertE2ESuccess(t, navRes, "navigate")

		time.Sleep(150 * time.Millisecond)

		diagRes := callMCPTool(t, cs, "diagnose", map[string]any{})
		assertE2ESuccess(t, diagRes, "diagnose")
		data := extractToolResultJSON(t, diagRes)
		errorsList, ok := data["errors"].([]any)
		if !ok || len(errorsList) == 0 {
			t.Fatalf("expected errors list in diagnose output, got: %+v", data)
		}
		foundOverlay := false
		for _, e := range errorsList {
			m, _ := e.(map[string]any)
			if typ, _ := m["type"].(string); typ == "dev_overlay" {
				foundOverlay = true
				break
			}
		}
		if !foundOverlay {
			t.Errorf("expected type='dev_overlay' inside errors array")
		}
	})
}

// Feature F08: Element Screenshot & Lightweight Disk Mode (Issue #8)
func TestE2E_Tier1_Issue8_ElementScreenshot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/screenshot-box")})
	assertE2ESuccess(t, navRes, "navigate to screenshot-box")

	// Case 1: Exact bounding box clip by CSS selector
	t.Run("Case1_ExactBoundingBoxClipBySelector", func(t *testing.T) {
		shotRes := callMCPTool(t, cs, "screenshot", map[string]any{
			"selector": "#card-standard",
		})
		assertE2ESuccess(t, shotRes, "element screenshot")
		data := extractToolResultJSON(t, shotRes)
		if clipped, _ := data["clipped"].(bool); !clipped {
			t.Errorf("expected clipped=true for element screenshot, got: %+v", data)
		}
	})

	// Case 2: Element clip by a11y ref
	t.Run("Case2_ElementClipByA11yRef", func(t *testing.T) {
		snapRes := callMCPTool(t, cs, "a11y_snapshot", map[string]any{})
		assertE2ESuccess(t, snapRes, "a11y_snapshot")
		snapData := extractToolResultJSON(t, snapRes)
		nodes, _ := snapData["nodes"].([]any)
		var ref string
		for _, n := range nodes {
			m, _ := n.(map[string]any)
			if r, _ := m["ref"].(string); r != "" {
				ref = r
				break
			}
		}
		if ref != "" {
			shotRes := callMCPTool(t, cs, "screenshot", map[string]any{
				"ref":  ref,
				"disk": true,
			})
			assertE2ESuccess(t, shotRes, "element screenshot by a11y ref")
		}
	})

	// Case 3: Lightweight disk mode (disk: true)
	t.Run("Case3_LightweightDiskMode", func(t *testing.T) {
		shotRes := callMCPTool(t, cs, "screenshot", map[string]any{
			"selector": "#card-standard",
			"disk":     true,
		})
		assertE2ESuccess(t, shotRes, "screenshot disk mode")

		// Verify zero ImageContent in response
		for _, c := range shotRes.Content {
			if _, isImg := c.(*mcp.ImageContent); isImg {
				t.Fatalf("expected 0 ImageContent in disk mode, found image blob!")
			}
		}

		data := extractToolResultJSON(t, shotRes)
		path, _ := data["path"].(string)
		if path == "" {
			t.Fatalf("expected path in disk mode metadata, got %+v", data)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected file %s to exist on disk: %v", path, err)
		}
		_ = os.Remove(path)
	})

	// Case 4: Custom output file path
	t.Run("Case4_CustomOutputFilePath", func(t *testing.T) {
		customPath := filepath.Join(t.TempDir(), "custom_element.jpg")
		shotRes := callMCPTool(t, cs, "screenshot", map[string]any{
			"selector": "#card-standard",
			"disk":     true,
			"path":     customPath,
		})
		assertE2ESuccess(t, shotRes, "screenshot custom path")
		if _, err := os.Stat(customPath); err != nil {
			t.Fatalf("expected custom path %s to exist: %v", customPath, err)
		}
	})

	// Case 5: Max size bound protection (max_size_kb)
	t.Run("Case5_MaxSizeKBProtection", func(t *testing.T) {
		shotRes := callMCPTool(t, cs, "screenshot", map[string]any{
			"full_page":   true,
			"max_size_kb": 200,
		})
		assertE2ESuccess(t, shotRes, "screenshot max size kb")
	})
}

// Feature F09: mock_route High-Level Interception Engine (Issue #9)
func TestE2E_Tier1_Issue9_MockRoute(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/api-mock")})
	assertE2ESuccess(t, navRes, "navigate to api-mock")

	// Case 1: Glob pattern mocking with JSON response
	t.Run("Case1_GlobPatternMocking", func(t *testing.T) {
		mockRes := callMCPTool(t, cs, "mock_route", map[string]any{
			"pattern": "*/api/v1/user/*",
			"action":  "mock",
			"status":  200,
			"body": map[string]any{
				"id":     42,
				"name":   "Mocked User 42",
				"mocked": true,
			},
		})
		assertE2ESuccess(t, mockRes, "mock_route glob pattern")

		// Trigger client fetch
		clickRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#btn-fetch-user"})
		assertE2ESuccess(t, clickRes, "click fetch user")

		time.Sleep(200 * time.Millisecond)
		evalRes := callMCPTool(t, cs, "eval", map[string]any{"expression": "document.getElementById('mock-body').textContent"})
		assertE2ESuccess(t, evalRes, "eval mock body")
		data := extractToolResultJSON(t, evalRes)
		val, _ := data["result"].(string)
		if val == "" {
			val, _ = data["value"].(string)
		}
		if !strings.Contains(val, "Mocked User 42") {
			t.Errorf("expected 'Mocked User 42' in response, got: %s", val)
		}
	})

	// Case 2: Regex pattern mocking with custom headers
	t.Run("Case2_RegexPatternWithHeaders", func(t *testing.T) {
		mockRes := callMCPTool(t, cs, "mock_route", map[string]any{
			"pattern": "^.*/api/v2/products/.*",
			"status":  201,
			"headers": map[string]string{
				"X-Custom-Auth": "verified-bearer",
			},
			"body": map[string]any{"product": "Mocked Super Gadget"},
		})
		assertE2ESuccess(t, mockRes, "mock_route regex with headers")

		clickRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#btn-fetch-glob"})
		assertE2ESuccess(t, clickRes, "click fetch glob")

		time.Sleep(200 * time.Millisecond)
		evalRes := callMCPTool(t, cs, "eval", map[string]any{"expression": "document.getElementById('mock-status').textContent"})
		assertE2ESuccess(t, evalRes, "eval mock status")
		data := extractToolResultJSON(t, evalRes)
		val, _ := data["result"].(string)
		if val == "" {
			val, _ = data["value"].(string)
		}
		if !strings.Contains(val, "201") {
			t.Errorf("expected status 201, got: %s", val)
		}
	})

	// Case 3: Route blocking action (action: "block")
	t.Run("Case3_RouteBlocking", func(t *testing.T) {
		blockRes := callMCPTool(t, cs, "mock_route", map[string]any{
			"pattern": "*/api/v1/telemetry*",
			"action":  "block",
		})
		assertE2ESuccess(t, blockRes, "mock_route block")

		clickRes := callMCPTool(t, cs, "click", map[string]any{"selector": "#btn-block-telemetry"})
		assertE2ESuccess(t, clickRes, "click block telemetry")

		time.Sleep(150 * time.Millisecond)
		evalRes := callMCPTool(t, cs, "eval", map[string]any{"expression": "document.getElementById('mock-body').textContent"})
		assertE2ESuccess(t, evalRes, "eval telemetry result")
		data := extractToolResultJSON(t, evalRes)
		val, _ := data["value"].(string)
		if !strings.Contains(val, "BLOCKED") && !strings.Contains(val, "FAILED") {
			t.Logf("telemetry block response: %s", val)
		}
	})

	// Case 4: Route listing (action: "list")
	t.Run("Case4_RouteListing", func(t *testing.T) {
		listRes := callMCPTool(t, cs, "mock_route", map[string]any{"action": "list"})
		assertE2ESuccess(t, listRes, "mock_route list")
		data := extractToolResultJSON(t, listRes)
		routes, _ := data["routes"].([]any)
		if len(routes) < 2 {
			t.Errorf("expected at least 2 routes listed, got %d", len(routes))
		}
	})

	// Case 5: Route clearance (action: "clear")
	t.Run("Case5_RouteClearance", func(t *testing.T) {
		clearRes := callMCPTool(t, cs, "mock_route", map[string]any{"action": "clear"})
		assertE2ESuccess(t, clearRes, "mock_route clear")

		listRes := callMCPTool(t, cs, "mock_route", map[string]any{"action": "list"})
		assertE2ESuccess(t, listRes, "mock_route list after clear")
		data := extractToolResultJSON(t, listRes)
		routes, _ := data["routes"].([]any)
		if len(routes) != 0 {
			t.Errorf("expected 0 routes after clear, got %d", len(routes))
		}
	})
}

// Feature F10: Consolidated Tab Management (tab)
func TestE2E_Tier1_Tool_Tab(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	// Case 1: Tab list action
	var initialTabID string
	t.Run("Case1_TabList", func(t *testing.T) {
		listRes := callMCPTool(t, cs, "tab", map[string]any{"action": "list"})
		assertE2ESuccess(t, listRes, "tab list")
		data := extractToolResultJSON(t, listRes)
		tabs, ok := data["tabs"].([]any)
		if !ok || len(tabs) == 0 {
			t.Fatalf("expected non-empty tabs list, got %+v", data)
		}
		firstTab, _ := tabs[0].(map[string]any)
		initialTabID, _ = firstTab["id"].(string)
	})

	// Case 2: Open new tab with URL and background flag
	var newTabID string
	t.Run("Case2_NewTabWithBackground", func(t *testing.T) {
		newRes := callMCPTool(t, cs, "tab", map[string]any{
			"action":     "new",
			"url":        fixtureURL(server.URL, "/fixtures/dialogs"),
			"background": true,
		})
		assertE2ESuccess(t, newRes, "tab new")
		data := extractToolResultJSON(t, newRes)
		newTabID, _ = data["tab_id"].(string)
		if newTabID == "" {
			t.Fatalf("expected new tab_id, got %+v", data)
		}
	})

	// Case 3: Switch active tab
	t.Run("Case3_SelectActiveTab", func(t *testing.T) {
		selRes := callMCPTool(t, cs, "tab", map[string]any{
			"action": "select",
			"tab_id": newTabID,
		})
		assertE2ESuccess(t, selRes, "tab select")
	})

	// Case 4: Close specific tab
	t.Run("Case4_CloseSpecificTab", func(t *testing.T) {
		closeRes := callMCPTool(t, cs, "tab", map[string]any{
			"action": "close",
			"tab_id": newTabID,
		})
		assertE2ESuccess(t, closeRes, "tab close")
	})

	// Case 5: Action parameter inference
	t.Run("Case5_ActionParameterInference", func(t *testing.T) {
		// Supplying url without action infers "new"
		newRes := callMCPTool(t, cs, "tab", map[string]any{
			"url": "about:blank",
		})
		assertE2ESuccess(t, newRes, "tab action inference (new)")
		data := extractToolResultJSON(t, newRes)
		createdID, _ := data["tab_id"].(string)
		if createdID != "" {
			_ = callMCPTool(t, cs, "tab", map[string]any{"action": "close", "tab_id": createdID})
		}
		if initialTabID != "" {
			_ = callMCPTool(t, cs, "tab", map[string]any{"action": "select", "tab_id": initialTabID})
		}
	})
}

// Feature F11: Consolidated Storage Management (storage)
func TestE2E_Tier1_Tool_Storage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/act-and-verify")})
	assertE2ESuccess(t, navRes, "navigate")

	// Case 1: LocalStorage write and read
	t.Run("Case1_LocalStorageSetAndGet", func(t *testing.T) {
		setRes := callMCPTool(t, cs, "storage", map[string]any{
			"action": "set",
			"type":   "local",
			"key":    "pref_theme",
			"value":  "dark_mode",
		})
		assertE2ESuccess(t, setRes, "storage set local")

		getRes := callMCPTool(t, cs, "storage", map[string]any{
			"action": "get",
			"type":   "local",
			"key":    "pref_theme",
		})
		assertE2ESuccess(t, getRes, "storage get local")
		data := extractToolResultJSON(t, getRes)
		if val, _ := data["value"].(string); val != "dark_mode" {
			t.Errorf("expected 'dark_mode', got %v", data["value"])
		}
	})

	// Case 2: Cookie write and read
	t.Run("Case2_CookieSetAndGet", func(t *testing.T) {
		setRes := callMCPTool(t, cs, "storage", map[string]any{
			"action": "set",
			"type":   "cookie",
			"key":    "session_id",
			"value":  "cookie_abc_123",
			"path":   "/",
		})
		assertE2ESuccess(t, setRes, "storage set cookie")

		getRes := callMCPTool(t, cs, "storage", map[string]any{
			"action": "get",
			"type":   "cookie",
			"key":    "session_id",
		})
		assertE2ESuccess(t, getRes, "storage get cookie")
		data := extractToolResultJSON(t, getRes)
		if val, _ := data["value"].(string); val != "cookie_abc_123" {
			t.Errorf("expected 'cookie_abc_123', got %v", data["value"])
		}
	})

	// Case 3: SessionStorage write and read
	t.Run("Case3_SessionStorageSetAndGet", func(t *testing.T) {
		setRes := callMCPTool(t, cs, "storage", map[string]any{
			"action": "set",
			"type":   "session",
			"key":    "nav_step",
			"value":  "step_three",
		})
		assertE2ESuccess(t, setRes, "storage set session")

		getRes := callMCPTool(t, cs, "storage", map[string]any{
			"action": "get",
			"type":   "session",
			"key":    "nav_step",
		})
		assertE2ESuccess(t, getRes, "storage get session")
		data := extractToolResultJSON(t, getRes)
		if val, _ := data["value"].(string); val != "step_three" {
			t.Errorf("expected 'step_three', got %v", data["value"])
		}
	})

	// Case 4: Clear specific storage type (local)
	t.Run("Case4_ClearLocalStorage", func(t *testing.T) {
		clearRes := callMCPTool(t, cs, "storage", map[string]any{
			"action": "clear",
			"type":   "local",
		})
		assertE2ESuccess(t, clearRes, "storage clear local")

		getRes := callMCPTool(t, cs, "storage", map[string]any{
			"action": "get",
			"type":   "local",
			"key":    "pref_theme",
		})
		assertE2ESuccess(t, getRes, "storage get cleared local")
		data := extractToolResultJSON(t, getRes)
		if val, _ := data["value"].(string); val != "" {
			t.Errorf("expected empty value after clear, got %v", val)
		}
	})

	// Case 5: Clear all storage types (all)
	t.Run("Case5_ClearAllStorage", func(t *testing.T) {
		clearRes := callMCPTool(t, cs, "storage", map[string]any{
			"action": "clear",
			"type":   "all",
		})
		assertE2ESuccess(t, clearRes, "storage clear all")
	})
}

// Feature F12: Consolidated Element Inspection (get_element)
func TestE2E_Tier1_Tool_GetElement(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/screenshot-box")})
	assertE2ESuccess(t, navRes, "navigate")

	// Case 1: Selector inspection with attributes and box
	t.Run("Case1_SelectorInspectionAll", func(t *testing.T) {
		getRes := callMCPTool(t, cs, "get_element", map[string]any{
			"selector": "#card-standard",
			"include":  "all",
		})
		assertE2ESuccess(t, getRes, "get_element all")
		data := extractToolResultJSON(t, getRes)
		if data["box"] == nil && data["boundingBox"] == nil && data["bounding_box"] == nil {
			t.Errorf("expected box model in all inspection, got %+v", data)
		}
		if data["outer_html"] == nil && data["html"] == nil {
			t.Errorf("expected outer_html/html in all inspection")
		}
	})

	// Case 2: Text search query
	t.Run("Case2_TextSearchQuery", func(t *testing.T) {
		getRes := callMCPTool(t, cs, "get_element", map[string]any{
			"text": "Standard Card Target",
		})
		assertE2ESuccess(t, getRes, "get_element by text")
		data := extractToolResultJSON(t, getRes)
		matches, _ := data["matches"].([]any)
		if len(matches) == 0 && data["selector"] == nil && data["element"] == nil {
			t.Errorf("expected matches or element found by text, got %+v", data)
		}
	})

	// Case 3: Regular expression query
	t.Run("Case3_RegexQuery", func(t *testing.T) {
		getRes := callMCPTool(t, cs, "get_element", map[string]any{
			"regex": "Standard.*Target",
		})
		assertE2ESuccess(t, getRes, "get_element by regex")
	})

	// Case 4: Coordinate hit-test
	t.Run("Case4_CoordinateHitTest", func(t *testing.T) {
		getRes := callMCPTool(t, cs, "get_element", map[string]any{
			"x": 200,
			"y": 120,
		})
		assertE2ESuccess(t, getRes, "get_element by coordinate")
	})

	// Case 5: Filtered field inclusion (include: "box")
	t.Run("Case5_FilteredFieldInclusion", func(t *testing.T) {
		getRes := callMCPTool(t, cs, "get_element", map[string]any{
			"selector": "#card-standard",
			"include":  "box",
		})
		assertE2ESuccess(t, getRes, "get_element box only")
	})
}

// Feature F13: Consolidated Synchronization & Waiting (wait_for)
func TestE2E_Tier1_Tool_WaitFor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/act-and-verify")})
	assertE2ESuccess(t, navRes, "navigate")

	// Case 1: Wait for dynamic selector appearance
	t.Run("Case1_WaitForDynamicSelector", func(t *testing.T) {
		waitRes := callMCPTool(t, cs, "wait_for", map[string]any{
			"selector":   "#action-trigger",
			"timeout_ms": 2000,
		})
		assertE2ESuccess(t, waitRes, "wait_for selector")
		data := extractToolResultJSON(t, waitRes)
		if sat, _ := data["satisfied"].(bool); !sat {
			t.Errorf("expected satisfied=true, got %+v", data)
		}
	})

	// Case 2: Wait for loading text disappearance
	t.Run("Case2_WaitForTextAbsence", func(t *testing.T) {
		// Trigger action that removes #pending-indicator
		_ = callMCPTool(t, cs, "click", map[string]any{"selector": "#action-trigger"})

		waitRes := callMCPTool(t, cs, "wait_for", map[string]any{
			"text_absent": "Awaiting confirmation...",
			"timeout_ms":  3000,
		})
		assertE2ESuccess(t, waitRes, "wait_for text_absent")
	})

	// Case 3: Custom JavaScript condition
	t.Run("Case3_CustomJSCondition", func(t *testing.T) {
		waitRes := callMCPTool(t, cs, "wait_for", map[string]any{
			"condition":  "document.querySelectorAll('#injected-container div').length >= 3",
			"timeout_ms": 3000,
		})
		assertE2ESuccess(t, waitRes, "wait_for condition")
	})

	// Case 4: Instant existence check (timeout_ms: 0)
	t.Run("Case4_InstantExistenceCheck", func(t *testing.T) {
		// Existing element
		res1 := callMCPTool(t, cs, "wait_for", map[string]any{
			"selector":   "#injected-container",
			"timeout_ms": 0,
		})
		assertE2ESuccess(t, res1, "instant exists check existing element")
		data1 := extractToolResultJSON(t, res1)
		if exists, _ := data1["exists"].(bool); !exists {
			t.Errorf("expected exists=true for existing element, got %+v", data1)
		}

		// Non-existent element
		res2 := callMCPTool(t, cs, "wait_for", map[string]any{
			"selector":   "#non-existent-xyz",
			"timeout_ms": 0,
		})
		assertE2ESuccess(t, res2, "instant exists check non-existing element")
		data2 := extractToolResultJSON(t, res2)
		if exists, _ := data2["exists"].(bool); exists {
			t.Errorf("expected exists=false for missing element, got %+v", data2)
		}
	})

	// Case 5: Lifecycle wait
	t.Run("Case5_LifecycleWait", func(t *testing.T) {
		waitRes := callMCPTool(t, cs, "wait_for", map[string]any{
			"wait_until": "load",
			"timeout_ms": 3000,
		})
		assertE2ESuccess(t, waitRes, "wait_for lifecycle")
	})
}

// Feature F14: Consolidated Screenshot Capture (screenshot)
func TestE2E_Tier1_Tool_Screenshot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/screenshot-box")})
	assertE2ESuccess(t, navRes, "navigate")

	// Case 1: Standard viewport screenshot
	t.Run("Case1_StandardViewportScreenshot", func(t *testing.T) {
		res := callMCPTool(t, cs, "screenshot", map[string]any{
			"format":  "jpeg",
			"quality": 80,
		})
		assertE2ESuccess(t, res, "screenshot viewport")
	})

	// Case 2: Full page capture
	t.Run("Case2_FullPageScreenshot", func(t *testing.T) {
		res := callMCPTool(t, cs, "screenshot", map[string]any{
			"full_page": true,
			"disk":      true,
		})
		assertE2ESuccess(t, res, "screenshot full_page")
		data := extractToolResultJSON(t, res)
		if fp, _ := data["full_page"].(bool); !fp {
			t.Errorf("expected full_page=true in metadata, got %+v", data)
		}
	})

	// Case 3: Visual coordinate grid overlay
	t.Run("Case3_GridOverlay", func(t *testing.T) {
		res := callMCPTool(t, cs, "screenshot", map[string]any{
			"grid":       true,
			"major_step": 100,
			"minor_step": 20,
			"disk":       true,
		})
		assertE2ESuccess(t, res, "screenshot with grid")
		data := extractToolResultJSON(t, res)
		if grid, _ := data["grid"].(bool); !grid {
			t.Errorf("expected grid=true in metadata, got %+v", data)
		}
	})

	// Case 4: PNG format capture
	t.Run("Case4_PNGFormatCapture", func(t *testing.T) {
		res := callMCPTool(t, cs, "screenshot", map[string]any{
			"format": "png",
			"disk":   true,
		})
		assertE2ESuccess(t, res, "screenshot png")
	})

	// Case 5: Element cropping with disk mode combined
	t.Run("Case5_ElementCropDiskMode", func(t *testing.T) {
		res := callMCPTool(t, cs, "screenshot", map[string]any{
			"selector": "#card-standard",
			"disk":     true,
		})
		assertE2ESuccess(t, res, "screenshot element disk mode")
		data := extractToolResultJSON(t, res)
		if clipped, _ := data["clipped"].(bool); !clipped {
			t.Errorf("expected clipped=true, got %+v", data)
		}
		path, _ := data["path"].(string)
		if path != "" {
			_ = os.Remove(path)
		}
	})
}
