package main

import (
	"testing"
)

func TestToolMetaRegistryCount(t *testing.T) {
	if len(toolMetaRegistry) != 27 {
		t.Errorf("toolMetaRegistry has %d entries, expected 27", len(toolMetaRegistry))
	}
	foundMockRoute := false
	for _, m := range toolMetaRegistry {
		if m.Name == "mock_route" {
			foundMockRoute = true
			if m.Capability != CapDiagnose {
				t.Errorf("mock_route capability = %s, want %s", m.Capability, CapDiagnose)
			}
			if m.Sensitivity != "high" {
				t.Errorf("mock_route sensitivity = %s, want high", m.Sensitivity)
			}
			break
		}
	}
	if !foundMockRoute {
		t.Errorf("mock_route missing from toolMetaRegistry")
	}
}

func TestToolMetaDefaultLoopIsCompact(t *testing.T) {
	// The default toolset should be compact: ~13 shared semantic tools.
	// Count must be <= 14.
	defaults := toolsForCapability(CapDefault)
	if len(defaults) == 0 {
		t.Fatal("default capability should have tools")
	}
	if len(defaults) > 14 {
		t.Errorf("default toolset has %d tools, want <= 14", len(defaults))
	}
	t.Logf("default toolset: %d tools: %v", len(defaults), defaults)
	// Verify the core interaction loop is present.
	required := []string{"configure", "status", "snapshot", "look", "click", "fill", "navigate", "eval", "screenshot", "get_element", "wait_for"}
	for _, name := range required {
		found := false
		for _, d := range defaults {
			if d == name {
				found = true
			}
		}
		if !found {
			t.Errorf("required default tool %s not in default toolset", name)
		}
	}
}

func TestToolMetaNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range toolMetaRegistry {
		if seen[m.Name] {
			t.Errorf("duplicate tool name in registry: %s", m.Name)
		}
		seen[m.Name] = true
	}
}

func TestToolMetaAllHaveCapability(t *testing.T) {
	for _, m := range toolMetaRegistry {
		if m.Capability == "" {
			t.Errorf("tool %s has empty capability", m.Name)
		}
		switch m.Capability {
		case CapDefault, CapCore, CapDiagnose, CapDebug, CapFull:
		default:
			t.Errorf("tool %s has unknown capability %q", m.Name, m.Capability)
		}
	}
}

func TestToolMetaAllHaveSensitivity(t *testing.T) {
	for _, m := range toolMetaRegistry {
		if m.Sensitivity == "" {
			t.Errorf("tool %s has empty sensitivity", m.Name)
		}
		switch m.Sensitivity {
		case "low", "medium", "high":
		default:
			t.Errorf("tool %s has unknown sensitivity %q", m.Name, m.Sensitivity)
		}
	}
}

func TestFindToolMeta(t *testing.T) {
	m := findToolMeta("click")
	if m.Name != "click" {
		t.Errorf("findToolMeta(click) returned %s", m.Name)
	}
	if m.Capability != CapDefault {
		t.Errorf("click capability = %s, want default", m.Capability)
	}

	m = findToolMeta("nonexistent")
	if m.Name != "nonexistent" {
		t.Errorf("findToolMeta(nonexistent) returned %s", m.Name)
	}
	if m.Capability != CapFull {
		t.Errorf("nonexistent capability = %s, want full (default)", m.Capability)
	}
	if m.Sensitivity != "high" {
		t.Errorf("nonexistent sensitivity = %s, want high (default)", m.Sensitivity)
	}
}

func TestToolsForCapability(t *testing.T) {
	core := toolsForCapability(CapCore)
	if len(core) == 0 {
		t.Error("core capability should have tools")
	}
	// Verify click is in default (not core anymore)
	defaults := toolsForCapability(CapDefault)
	found := false
	for _, name := range defaults {
		if name == "click" {
			found = true
		}
	}
	if !found {
		t.Error("click should be in default capability")
	}

	diagnose := toolsForCapability(CapDiagnose)
	found = false
	foundMockRoute := false
	for _, name := range diagnose {
		if name == "network_requests" {
			found = true
		}
		if name == "mock_route" {
			foundMockRoute = true
		}
	}
	if !found {
		t.Error("network_requests should be in diagnose capability")
	}
	if !foundMockRoute {
		t.Error("mock_route should be in diagnose capability")
	}

	full := toolsForCapability(CapFull)
	if len(full) != len(toolMetaRegistry) {
		t.Errorf("full capability should return all %d tools, got %d", len(toolMetaRegistry), len(full))
	}
}

func TestParseToolsets(t *testing.T) {
	// "full" or empty returns all
	all := parseToolsets("full")
	if len(all) != len(toolMetaRegistry) {
		t.Errorf("parseToolsets(full) returned %d, expected %d", len(all), len(toolMetaRegistry))
	}

	all = parseToolsets("")
	if len(all) != len(toolMetaRegistry) {
		t.Errorf("parseToolsets(empty) returned %d, expected %d", len(all), len(toolMetaRegistry))
	}

	// "core" returns only core tools
	core := parseToolsets("core")
	if len(core) == 0 || len(core) >= len(toolMetaRegistry) {
		t.Errorf("parseToolsets(core) returned %d tools", len(core))
	}

	// "core,diagnose" returns union
	union := parseToolsets("core,diagnose")
	if len(union) <= len(core) {
		t.Errorf("parseToolsets(core,diagnose) = %d, should be > core alone %d", len(union), len(core))
	}

	// Verify no duplicates in union
	seen := map[string]bool{}
	for _, name := range union {
		if seen[name] {
			t.Errorf("duplicate in parseToolsets result: %s", name)
		}
		seen[name] = true
	}
}

func TestSplitComma(t *testing.T) {
	parts := splitComma("a,b,c")
	if len(parts) != 3 {
		t.Fatalf("splitComma returned %d parts, want 3", len(parts))
	}
	if parts[0] != "a" || parts[1] != "b" || parts[2] != "c" {
		t.Errorf("splitComma(a,b,c) = %v, want [a b c]", parts)
	}

	parts = splitComma("")
	if len(parts) != 0 {
		t.Errorf("splitComma(empty) returned %d parts, want 0", len(parts))
	}

	parts = splitComma("single")
	if len(parts) != 1 || parts[0] != "single" {
		t.Errorf("splitComma(single) = %v, want [single]", parts)
	}

	parts = splitComma(" core , diagnose , full ")
	if len(parts) != 3 || parts[0] != "core" || parts[1] != "diagnose" || parts[2] != "full" {
		t.Errorf("splitComma with spaces = %v, want [core diagnose full]", parts)
	}
}
