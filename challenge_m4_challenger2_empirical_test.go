package main

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// challenge_m4_challenger2_empirical_test.go — Milestone 4 Challenger 2 Empirical Test Suite.
// Focus: Schema, Registry & Token Economy.

var expectedCanonicalRegistry = []string{
	// Target & Environment (2)
	"configure", "status",
	// Navigation (1)
	"navigate",
	// Perception & Query (6)
	"look", "snapshot", "a11y_snapshot", "get_element", "screenshot", "query",
	// Interaction & Action (8)
	"click", "type", "fill", "scroll", "hover", "drag", "batch_actions", "upload_file",
	// Synchronization & Dialog (2)
	"wait_for", "handle_dialog",
	// Session & State CRUD (2)
	"tab", "storage",
	// Execution & Emulation (2)
	"emulate", "eval",
	// Diagnostics, Telemetry & Mocking (5)
	"network_requests", "console_messages", "diagnose", "timeline", "mock_route",
}

var deletedToolsBlacklistC2 = []string{
	"pdf", "list_downloads", "export_trace", "start_recording", "stop_recording",
	"diff_snapshot", "tool_summary", "network_request", "fork",
	"screenshot_full_page", "inspect_point", "find", "exists", "try_until",
	"new_tab", "list_tabs", "select_tab", "close_tab",
	"get_storage", "set_storage", "clear_storage",
	"intercept", "list_intercepts", "clear_intercepts",
}

// 1. ToolMetaRegistry Count and Invariants
func TestEmpirical_C2_ToolMetaRegistry_26CanonicalTools(t *testing.T) {
	if len(toolMetaRegistry) != 28 {
		t.Fatalf("toolMetaRegistry length = %d, want exactly 28", len(toolMetaRegistry))
	}

	seen := make(map[string]int)
	for i, m := range toolMetaRegistry {
		if strings.TrimSpace(m.Name) == "" {
			t.Errorf("entry %d has empty Name", i)
		}
		if strings.TrimSpace(m.Description) == "" {
			t.Errorf("entry %d (%s) has empty Description", i, m.Name)
		}
		seen[m.Name]++

		// Sensitivity gate
		switch m.Sensitivity {
		case "low", "medium", "high":
		default:
			t.Errorf("entry %s has invalid sensitivity %q", m.Name, m.Sensitivity)
		}

		// Capability gate
		switch m.Capability {
		case CapDefault, CapCore, CapDiagnose:
		default:
			t.Errorf("entry %s has unexpected capability %q", m.Name, m.Capability)
		}
	}

	for name, count := range seen {
		if count > 1 {
			t.Errorf("duplicate tool %s found in registry (%d times)", name, count)
		}
	}

	expectedMap := make(map[string]bool)
	for _, name := range expectedCanonicalRegistry {
		expectedMap[name] = true
	}

	for _, m := range toolMetaRegistry {
		if !expectedMap[m.Name] {
			t.Errorf("unexpected tool in registry: %s", m.Name)
		}
	}

	for _, name := range expectedCanonicalRegistry {
		if seen[name] == 0 {
			t.Errorf("missing canonical tool from registry: %s", name)
		}
	}

	for _, deleted := range deletedToolsBlacklistC2 {
		if seen[deleted] > 0 {
			t.Errorf("deleted tool %s still found in toolMetaRegistry!", deleted)
		}
	}
}

// 2. CapDefault Minimal Interactive Loop
func TestEmpirical_C2_CapDefault_MinimalInteractiveLoop(t *testing.T) {
	defaults := toolsForCapability(CapDefault)
	t.Logf("CapDefault count: %d, tools: %v", len(defaults), defaults)

	if len(defaults) > 14 {
		t.Fatalf("CapDefault has %d tools, exceeds constraint <= 14", len(defaults))
	}
	if len(defaults) == 0 {
		t.Fatal("CapDefault is empty")
	}

	requiredLoop := []string{
		"configure", "status", "navigate",
		"look", "snapshot", "screenshot", "get_element",
		"click", "fill",
		"wait_for", "eval",
	}

	defMap := make(map[string]bool)
	for _, name := range defaults {
		defMap[name] = true
	}

	for _, req := range requiredLoop {
		if !defMap[req] {
			t.Errorf("required minimal interactive loop tool missing from CapDefault: %s", req)
		}
	}
}

// 3. parseToolsets Capability Filtering Matrix
func TestEmpirical_C2_ParseToolsets_Matrix(t *testing.T) {
	tests := []struct {
		input         string
		expectedCount int
		mustInclude   []string
		mustExclude   []string
	}{
		{
			input:         "",
			expectedCount: 28,
			mustInclude:   []string{"configure", "status", "click", "tab", "storage", "diagnose", "timeline", "mock_route"},
			mustExclude:   nil,
		},
		{
			input:         "full",
			expectedCount: 28,
			mustInclude:   []string{"configure", "status", "click", "tab", "storage", "diagnose", "timeline", "mock_route"},
			mustExclude:   nil,
		},
		{
			input:         "default",
			expectedCount: 12,
			mustInclude:   []string{"configure", "status", "navigate", "look", "snapshot", "screenshot", "get_element", "query", "click", "fill", "wait_for", "eval"},
			mustExclude:   []string{"tab", "storage", "type", "scroll", "hover", "drag", "batch_actions", "upload_file", "handle_dialog", "emulate", "network_requests", "console_messages", "diagnose", "timeline", "mock_route"},
		},
		{
			input:         "diagnose",
			expectedCount: 17,
			mustInclude:   []string{"configure", "click", "eval", "network_requests", "console_messages", "diagnose", "timeline", "mock_route"},
			mustExclude:   []string{"tab", "storage", "type", "scroll", "hover", "drag", "batch_actions", "upload_file", "handle_dialog", "emulate"},
		},
		{
			input:         "default,diagnose",
			expectedCount: 17,
			mustInclude:   []string{"configure", "click", "eval", "network_requests", "console_messages", "diagnose", "timeline", "mock_route"},
			mustExclude:   []string{"tab", "storage", "type", "scroll", "hover"},
		},
		{
			input:         "core",
			expectedCount: 23,
			mustInclude:   []string{"configure", "click", "tab", "storage", "type", "scroll", "hover", "drag", "batch_actions", "upload_file", "handle_dialog", "emulate"},
			mustExclude:   []string{"network_requests", "console_messages", "diagnose", "timeline", "mock_route"},
		},
		{
			input:         "core,diagnose",
			expectedCount: 28,
			mustInclude:   []string{"configure", "click", "tab", "diagnose", "mock_route"},
			mustExclude:   nil,
		},
		{
			input:         "  core  ,   diagnose  ",
			expectedCount: 28,
			mustInclude:   []string{"configure", "click", "tab", "diagnose", "mock_route"},
			mustExclude:   nil,
		},
		{
			input:         "unknown_set,bogus_cap",
			expectedCount: 12,
			mustInclude:   []string{"configure", "status", "click", "eval"},
			mustExclude:   []string{"tab", "storage", "diagnose", "mock_route"},
		},
	}

	for _, tc := range tests {
		actual := parseToolsets(tc.input)
		if len(actual) != tc.expectedCount {
			t.Errorf("parseToolsets(%q) returned %d tools, want %d", tc.input, len(actual), tc.expectedCount)
		}

		actualMap := make(map[string]bool)
		for _, name := range actual {
			actualMap[name] = true
		}

		for _, inc := range tc.mustInclude {
			if !actualMap[inc] {
				t.Errorf("parseToolsets(%q) missing tool %s", tc.input, inc)
			}
		}

		for _, exc := range tc.mustExclude {
			if actualMap[exc] {
				t.Errorf("parseToolsets(%q) contains excluded tool %s", tc.input, exc)
			}
		}
	}
}

// 4. Schema Token Economy and Measurements
func TestEmpirical_C2_SchemaTokenEconomy(t *testing.T) {
	const legacyFullBytes = 28627

	profiles := []struct {
		name         string
		maxTools     int
		maxEstTokens int
	}{
		{"default", 14, 2500},
		{"default,diagnose", 18, 3700},
		{"core", 23, 4500},
		{"full", 28, 6000},
	}

	for _, p := range profiles {
		count, bytes := measureToolset(t, p.name)
		estTokens := bytes / 4
		t.Logf("Profile %-18s: tools=%-2d bytes=%-5d estTokens=%-4d", p.name, count, bytes, estTokens)

		if count > p.maxTools {
			t.Errorf("profile %s tool count %d > max %d", p.name, count, p.maxTools)
		}
		if estTokens > p.maxEstTokens {
			t.Errorf("profile %s est tokens %d > max %d", p.name, estTokens, p.maxEstTokens)
		}
	}

	_, defaultBytes := measureToolset(t, "default")
	defaultSavings := float64(legacyFullBytes-defaultBytes) / float64(legacyFullBytes) * 100.0
	t.Logf("Default savings vs legacy full 48-tools: %.1f%%", defaultSavings)
	if defaultSavings < 60.0 {
		t.Errorf("default savings %.1f%% < 60%%", defaultSavings)
	}

	_, fullBytes := measureToolset(t, "full")
	fullSavings := float64(legacyFullBytes-fullBytes) / float64(legacyFullBytes) * 100.0
	t.Logf("Full canonical 27 savings vs legacy full 48-tools: %.1f%%", fullSavings)
	if fullSavings < 25.0 {
		t.Errorf("full savings %.1f%% < 25%%", fullSavings)
	}
}

// 5. Empirical Challenge: MockRoute Surface Ghost Tool
// In docs/tool-review-report.md line 198, it claims:
// "Route mocking is provided canonically by mock_route as defined in Issue #9;
// in the 26-tool baseline distribution, it is registered under capability sets or aliased alongside network diagnostics."
// BUT when parseToolsets("full") runs, mock_route is absent from toolMetaRegistry,
// so toolEnabled("mock_route") is FALSE, making mock_route inaccessible on live MCP!
func TestEmpirical_C2_MockRouteGhostToolDiscrepancy(t *testing.T) {
	orig := enabledTools
	defer func() { enabledTools = orig }()

	for _, profile := range []string{"full", "diagnose"} {
		t.Run("profile_"+profile, func(t *testing.T) {
			enabledTools = parseToolsets(profile)

			isMockRouteEnabled := toolEnabled("mock_route")
			t.Logf("toolEnabled(\"mock_route\") under parseToolsets(%q) = %v", profile, isMockRouteEnabled)

			cs := setupMCPWithToolsets(t, profile)
			res, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
			if err != nil {
				t.Fatalf("ListTools failed: %v", err)
			}

			foundMockRoute := false
			for _, tool := range res.Tools {
				if tool.Name == "mock_route" {
					foundMockRoute = true
				}
			}

			t.Logf("mock_route exposed on live MCP under %s toolsets: %v", profile, foundMockRoute)

			if !isMockRouteEnabled {
				t.Errorf("toolEnabled(\"mock_route\") must be true under parseToolsets(%q), got false", profile)
			}
			if !foundMockRoute {
				t.Errorf("mock_route must be exposed on live MCP under %s toolsets, but was not found", profile)
			}
		})
	}
}

// 6. Zero Deleted Tools on Wire
func TestEmpirical_C2_ZeroDeletedToolsOnWire(t *testing.T) {
	cs := setupMCPWithToolsets(t, "full")
	res, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	registeredNames := make(map[string]bool)
	for _, tool := range res.Tools {
		registeredNames[tool.Name] = true
	}

	for _, deleted := range deletedToolsBlacklistC2 {
		if registeredNames[deleted] {
			t.Errorf("blacklisted deleted tool exposed: %s", deleted)
		}
	}

	var sortedNames []string
	for name := range registeredNames {
		sortedNames = append(sortedNames, name)
	}
	sort.Strings(sortedNames)
	b, _ := json.Marshal(sortedNames)
	t.Logf("Live wire registered tools (count %d): %s", len(sortedNames), string(b))
}
