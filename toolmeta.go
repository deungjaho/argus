package main

import (
	"strings"
)

// toolMetaRegistry is the single declaration source for all MCP tools.
// It drives:
//   - --toolsets capability filtering
//   - documentation generation
//   - security audit
//   - tool discovery
//
// Every registered MCP tool should have a corresponding entry here.
// If a tool is not in this registry, it defaults to CapFull with
// unknown sensitivity (treated as high for safety).

var toolMetaRegistry = []ToolMeta{
	// --- Target & Status (Default Loop) ---
	{Name: "configure", Capability: CapDefault, SideEffects: []string{"launches_chrome", "sets_global_config"}, Sensitivity: "medium", Idempotent: true, Description: "Set target URL/port, attach mode, and profile."},
	{Name: "status", Capability: CapDefault, Sensitivity: "low", Idempotent: true, Description: "Return current browser status and page state."},
	{Name: "navigate", Capability: CapDefault, SideEffects: []string{"navigates", "navigates_history", "reloads_page"}, Sensitivity: "low", Idempotent: true, Description: "Navigate to a URL, go back/forward in history, or reload the current tab."},

	// --- Observe & Perceive (Default Loop & Core) ---
	{Name: "look", Capability: CapDefault, Sensitivity: "low", Idempotent: true, Description: "Compressed screenshot plus interactive element list in one call."},
	{Name: "snapshot", Capability: CapDefault, Sensitivity: "low", Idempotent: true, Description: "Return page text and visible interactive elements with refs."},
	{Name: "a11y_snapshot", Capability: CapCore, Sensitivity: "low", Idempotent: true, Description: "Capture the accessibility tree."},
	{Name: "screenshot", Capability: CapDefault, SideEffects: []string{"writes_temp_file"}, Sensitivity: "low", Idempotent: true, Description: "Capture viewport, element, or full-page screenshot."},
	{Name: "get_element", Capability: CapDefault, Sensitivity: "low", Idempotent: true, Description: "Get detailed info about an element by selector, coordinates, or query."},
	{Name: "query", Capability: CapDefault, Sensitivity: "low", Idempotent: true, Description: "Structured DOM query: per-element text/box/classes/attrs with reusable refs; '>>>' pierces shadow DOM."},
	{Name: "get_picked", Capability: CapCore, Sensitivity: "low", Idempotent: true, Description: "Return latest user-picked element locator (picker capture) with fallbacks."},
	{Name: "record", Capability: CapCore, SideEffects: []string{"records_user_input"}, Sensitivity: "medium", Idempotent: false, Description: "Capture real user input as replayable action trace (start/stop/status)."},
	{Name: "session", Capability: CapCore, SideEffects: []string{"creates_window"}, Sensitivity: "medium", Idempotent: false, Description: "Manage browsing sessions (start/stop/list/reveal/hide), incl. login-state fork."},

	// --- Act & Interact (Default Loop & Core) ---
	{Name: "click", Capability: CapDefault, SideEffects: []string{"clicks_element"}, Sensitivity: "low", Idempotent: false, Description: "Click an element by ref/selector/coordinates."},
	{Name: "type", Capability: CapCore, SideEffects: []string{"types_text", "sends_key"}, Sensitivity: "low", Idempotent: false, Description: "Type text into a focused element or dispatch a keyboard key."},
	{Name: "fill", Capability: CapDefault, SideEffects: []string{"fills_form_field", "fills_form_fields", "selects_dropdown"}, Sensitivity: "low", Idempotent: false, Description: "Fill form fields with controlled component setter and settle wait."},
	{Name: "scroll", Capability: CapCore, SideEffects: []string{"scrolls_viewport"}, Sensitivity: "low", Idempotent: true, Description: "Scroll the viewport or overflow container."},
	{Name: "hover", Capability: CapCore, SideEffects: []string{"hovers_element"}, Sensitivity: "low", Idempotent: true, Description: "Hover over an element to trigger pseudo-classes."},
	{Name: "drag", Capability: CapCore, SideEffects: []string{"drags_element"}, Sensitivity: "low", Idempotent: false, Description: "Drag an element to a target or coordinate."},
	{Name: "batch_actions", Capability: CapCore, SideEffects: []string{"executes_batch_actions"}, Sensitivity: "low", Idempotent: false, Description: "Execute a sequence of UI interactions in a single turn."},
	{Name: "upload_file", Capability: CapCore, SideEffects: []string{"uploads_files", "reads_local_file"}, Sensitivity: "medium", Idempotent: false, Description: "Upload local files to a file input."},
	{Name: "handle_dialog", Capability: CapCore, SideEffects: []string{"handles_dialog"}, Sensitivity: "low", Idempotent: false, Description: "Accept or dismiss a JS dialog with anti-deadlock protection."},

	// --- Synchronization & State CRUD (Default Loop & Core) ---
	{Name: "wait_for", Capability: CapDefault, Sensitivity: "low", Idempotent: true, Description: "Wait for a selector, text, load, or condition (timeout_ms=0 for instant check)."},
	{Name: "tab", Capability: CapCore, SideEffects: []string{"creates_tab", "activates_tab", "closes_tab"}, Sensitivity: "low", Idempotent: false, Description: "Browser tab management (list, new, select, close)."},
	{Name: "storage", Capability: CapCore, SideEffects: []string{"writes_storage", "deletes_storage"}, Sensitivity: "high", Idempotent: false, Description: "Browser storage CRUD for cookies, localStorage, and sessionStorage."},

	// --- Emulate & Execute ---
	{Name: "emulate", Capability: CapCore, SideEffects: []string{"changes_viewport", "changes_user_agent"}, Sensitivity: "low", Idempotent: true, Description: "Emulate a device viewport and user agent."},
	{Name: "eval", Capability: CapDefault, SideEffects: []string{"executes_js"}, Sensitivity: "high", Idempotent: false, Description: "Evaluate JavaScript in the page context."},

	// --- Diagnostics (Diagnose Profile) ---
	{Name: "network_requests", Capability: CapDiagnose, Sensitivity: "medium", Idempotent: true, Description: "Query recorded network requests."},
	{Name: "console_messages", Capability: CapDiagnose, Sensitivity: "medium", Idempotent: true, Description: "Query recorded console messages."},
	{Name: "diagnose", Capability: CapDiagnose, Sensitivity: "medium", Idempotent: true, Description: "Diagnostic summary with dev error overlay detection."},
	{Name: "timeline", Capability: CapDiagnose, Sensitivity: "medium", Idempotent: true, Description: "Chronological event timeline correlating actions and events."},
	{Name: "mock_route", Capability: CapDiagnose, SideEffects: []string{"intercepts_network", "mocks_routes"}, Sensitivity: "high", Idempotent: false, Description: "Intercept and mock network requests with custom responses."},
}

// findToolMeta returns the metadata for a tool by name.
// If not found, returns a default with CapFull and high sensitivity.
func findToolMeta(name string) ToolMeta {
	for _, m := range toolMetaRegistry {
		if m.Name == name {
			return m
		}
	}
	return ToolMeta{
		Name:        name,
		Capability:  CapFull,
		Sensitivity: "high",
		Idempotent:  false,
	}
}

// toolsForCapability returns the names of tools that belong to the
// given capability set. CapFull returns all tools.
func toolsForCapability(cap CapabilitySet) []string {
	if cap == CapFull {
		all := make([]string, 0, len(toolMetaRegistry))
		for _, m := range toolMetaRegistry {
			all = append(all, m.Name)
		}
		return all
	}
	var result []string
	for _, m := range toolMetaRegistry {
		if m.Capability == cap {
			result = append(result, m.Name)
		}
	}
	return result
}

// parseToolsets parses a comma-separated list of capability set names
// and returns the union of tool names. "full" returns all tools.
// The "default" set (the compact agent loop) is always included: it
// contains the discovery and interaction primitives (eval, navigate,
// click, screenshot, ...) without which an agent cannot operate,
// so requesting e.g. "core,diagnose" still yields a working tool surface.
// Example: "core,diagnose" returns default + core + diagnose tools.
func parseToolsets(s string) []string {
	if s == "" || s == "full" {
		return toolsForCapability(CapFull)
	}
	seen := map[string]struct{}{}
	for _, toolName := range toolsForCapability(CapDefault) {
		seen[toolName] = struct{}{}
	}
	for _, name := range splitComma(s) {
		cap := CapabilitySet(name)
		for _, toolName := range toolsForCapability(cap) {
			seen[toolName] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for _, m := range toolMetaRegistry {
		if _, ok := seen[m.Name]; ok {
			result = append(result, m.Name)
		}
	}
	return result
}

func splitComma(s string) []string {
	var parts []string
	current := ""
	for _, c := range s {
		if c == ',' {
			trimmed := strings.TrimSpace(current)
			if trimmed != "" {
				parts = append(parts, trimmed)
			}
			current = ""
		} else {
			current += string(c)
		}
	}
	trimmed := strings.TrimSpace(current)
	if trimmed != "" {
		parts = append(parts, trimmed)
	}
	return parts
}
