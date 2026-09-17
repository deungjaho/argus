package main

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
	// --- Target ---
	// configure and status are in the default interaction loop.
	{Name: "configure", Capability: CapDefault, SideEffects: []string{"launches_chrome", "sets_global_config"}, Sensitivity: "medium", Idempotent: true, Description: "Set target URL/port, attach mode, and profile."},
	{Name: "new_tab", Capability: CapCore, SideEffects: []string{"creates_tab"}, Sensitivity: "low", Idempotent: false, Description: "Open a new browser tab."},
	{Name: "list_tabs", Capability: CapCore, Sensitivity: "low", Idempotent: true, Description: "List open browser tabs."},
	{Name: "select_tab", Capability: CapCore, SideEffects: []string{"activates_tab"}, Sensitivity: "low", Idempotent: true, Description: "Activate a tab by id or url."},
	{Name: "close_tab", Capability: CapCore, SideEffects: []string{"closes_tab"}, Sensitivity: "low", Idempotent: true, Description: "Close a browser tab."},
	{Name: "fork", Capability: CapCore, SideEffects: []string{"creates_tab"}, Sensitivity: "low", Idempotent: false, Description: "Duplicate the current tab into a new tab."},

	// --- Observe ---
	// look, snapshot, screenshot, find, get_element are in the default loop.
	{Name: "look", Capability: CapDefault, Sensitivity: "low", Idempotent: true, Description: "Compressed screenshot plus interactive element list in one call."},
	{Name: "snapshot", Capability: CapDefault, Sensitivity: "low", Idempotent: true, Description: "Return page text and visible interactive elements with refs."},
	{Name: "a11y_snapshot", Capability: CapCore, Sensitivity: "low", Idempotent: true, Description: "Capture the accessibility tree."},
	{Name: "screenshot", Capability: CapDefault, SideEffects: []string{"writes_temp_file"}, Sensitivity: "low", Idempotent: true, Description: "Capture a viewport screenshot (optional grid overlay)."},
	{Name: "screenshot_full_page", Capability: CapCore, SideEffects: []string{"writes_temp_file"}, Sensitivity: "low", Idempotent: true, Description: "Capture a full-page screenshot."},
	{Name: "inspect_point", Capability: CapCore, Sensitivity: "low", Idempotent: true, Description: "Inspect the element under a viewport or image coordinate."},
	{Name: "find", Capability: CapDefault, Sensitivity: "low", Idempotent: true, Description: "Search the page for text or regex; returns matching elements."},
	{Name: "get_element", Capability: CapDefault, Sensitivity: "low", Idempotent: true, Description: "Get detailed info about one element (html, attrs, style, box)."},

	// --- Act ---
	// click and fill are in the default loop.
	{Name: "click", Capability: CapDefault, SideEffects: []string{"clicks_element"}, Sensitivity: "low", Idempotent: false, Description: "Click an element by ref/selector/coordinates."},
	{Name: "type", Capability: CapCore, SideEffects: []string{"types_text", "sends_key"}, Sensitivity: "low", Idempotent: false, Description: "Type text into a focused or selected element, or send a keyboard key."},
	{Name: "fill", Capability: CapDefault, SideEffects: []string{"fills_form_field", "fills_form_fields", "selects_dropdown"}, Sensitivity: "low", Idempotent: false, Description: "Fill a form field (clears first), fill multiple fields, or select dropdown options."},
	{Name: "scroll", Capability: CapCore, SideEffects: []string{"scrolls_viewport"}, Sensitivity: "low", Idempotent: true, Description: "Scroll the viewport by amount or to an element."},
	{Name: "hover", Capability: CapCore, SideEffects: []string{"hovers_element"}, Sensitivity: "low", Idempotent: true, Description: "Hover over an element."},
	{Name: "drag", Capability: CapCore, SideEffects: []string{"drags_element"}, Sensitivity: "low", Idempotent: false, Description: "Drag an element to a target or coordinate."},
	{Name: "upload_file", Capability: CapCore, SideEffects: []string{"uploads_files", "reads_local_file"}, Sensitivity: "medium", Idempotent: false, Description: "Upload local files to a file input."},
	{Name: "handle_dialog", Capability: CapCore, SideEffects: []string{"handles_dialog"}, Sensitivity: "low", Idempotent: false, Description: "Accept or dismiss a JS dialog (alert/confirm/prompt)."},

	// --- Wait ---
	// wait_for and exists are in the default loop.
	{Name: "wait_for", Capability: CapDefault, Sensitivity: "low", Idempotent: true, Description: "Wait for a selector/text/condition or page load with timeout."},
	{Name: "exists", Capability: CapDefault, Sensitivity: "low", Idempotent: true, Description: "Check if an element exists (never throws)."},
	{Name: "try_until", Capability: CapCore, Sensitivity: "low", Idempotent: true, Description: "Retry an action until it succeeds or times out."},

	// --- Navigate ---
	// navigate is in the default loop.
	{Name: "navigate", Capability: CapDefault, SideEffects: []string{"navigates", "navigates_history", "reloads_page"}, Sensitivity: "low", Idempotent: true, Description: "Navigate to a URL, go back/forward in history, or reload the current tab."},

	// --- Emulate ---
	{Name: "emulate", Capability: CapCore, SideEffects: []string{"changes_viewport", "changes_user_agent"}, Sensitivity: "low", Idempotent: true, Description: "Emulate a device viewport and user agent, or set a custom viewport size."},
	// eval is in the default loop (escape hatch for runtime inspection).
	{Name: "eval", Capability: CapDefault, SideEffects: []string{"executes_js"}, Sensitivity: "high", Idempotent: false, Description: "Evaluate JavaScript in the page (arbitrary code risk)."},

	// --- Network (diagnose) ---
	{Name: "network_requests", Capability: CapDiagnose, Sensitivity: "medium", Idempotent: true, Description: "Query recorded network requests."},
	{Name: "network_request", Capability: CapDiagnose, SideEffects: []string{"makes_http_request"}, Sensitivity: "high", Idempotent: true, Description: "Issue an HTTP request from the browser."},
	{Name: "console_messages", Capability: CapDiagnose, Sensitivity: "medium", Idempotent: true, Description: "Query recorded console messages."},
	{Name: "intercept", Capability: CapDiagnose, SideEffects: []string{"intercepts_network"}, Sensitivity: "medium", Idempotent: false, Description: "Add a network interception rule."},
	{Name: "list_intercepts", Capability: CapDiagnose, Sensitivity: "low", Idempotent: true, Description: "List active interception rules."},
	{Name: "clear_intercepts", Capability: CapDiagnose, SideEffects: []string{"clears_intercept_rules"}, Sensitivity: "low", Idempotent: true, Description: "Clear all interception rules."},

	// --- Storage ---
	{Name: "get_storage", Capability: CapCore, Sensitivity: "high", Idempotent: true, Description: "Read cookies/local/session storage (values redacted in debug)."},
	{Name: "set_storage", Capability: CapCore, SideEffects: []string{"writes_storage"}, Sensitivity: "high", Idempotent: false, Description: "Write cookies/local/session storage."},
	{Name: "clear_storage", Capability: CapCore, SideEffects: []string{"deletes_storage"}, Sensitivity: "medium", Idempotent: true, Description: "Clear cookies/local/session storage."},

	// --- Misc ---
	{Name: "pdf", Capability: CapDiagnose, SideEffects: []string{"writes_pdf_file"}, Sensitivity: "low", Idempotent: true, Description: "Export the current page as a PDF."},
	{Name: "list_downloads", Capability: CapDiagnose, Sensitivity: "low", Idempotent: true, Description: "List files downloaded by the browser."},

	// --- Config ---
	// status is in the default loop.
	{Name: "status", Capability: CapDefault, Sensitivity: "low", Idempotent: true, Description: "Return current browser status and page state."},

	// --- Trace (diagnose) ---
	{Name: "timeline", Capability: CapDiagnose, Sensitivity: "medium", Idempotent: true, Description: "Return the unified action/event timeline."},
	{Name: "diagnose", Capability: CapDiagnose, Sensitivity: "medium", Idempotent: true, Description: "Return a target-scoped diagnostic summary."},
	{Name: "export_trace", Capability: CapDiagnose, SideEffects: []string{"writes_trace_file"}, Sensitivity: "medium", Idempotent: true, Description: "Export a bounded trace bundle to a controlled file."},

	// --- Discovery ---
	// tool_summary is in the default loop.
	{Name: "tool_summary", Capability: CapDefault, Sensitivity: "low", Idempotent: true, Description: "Return a compact list of all tools and their capabilities."},
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
// contains the discovery and interaction primitives (tool_summary, eval,
// navigate, click, screenshot, ...) without which an agent cannot operate,
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
			if current != "" {
				parts = append(parts, current)
			}
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}
