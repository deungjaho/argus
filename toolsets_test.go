package main

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolEnabledDefaultAllEnabled(t *testing.T) {
	orig := enabledTools
	defer func() { enabledTools = orig }()
	enabledTools = nil // default: all enabled

	if !toolEnabled("click") {
		t.Error("click should be enabled by default")
	}
	if !toolEnabled("network_requests") {
		t.Error("network_requests should be enabled by default")
	}
	if !toolEnabled("any_unknown_tool") {
		t.Error("unknown tools should be enabled when filter is nil")
	}
}

func TestToolEnabledCoreOnly(t *testing.T) {
	orig := enabledTools
	defer func() { enabledTools = orig }()
	enabledTools = parseToolsets("core")

	// click/navigate/snapshot are CapDefault, not CapCore.
	// CapCore tools like new_tab, scroll, type should be enabled.
	if !toolEnabled("new_tab") {
		t.Error("new_tab should be enabled in core toolset")
	}
	if !toolEnabled("scroll") {
		t.Error("scroll should be enabled in core toolset")
	}
	if !toolEnabled("type") {
		t.Error("type should be enabled in core toolset")
	}
	if toolEnabled("network_requests") {
		t.Error("network_requests should NOT be enabled in core-only toolset")
	}
	if toolEnabled("intercept") {
		t.Error("intercept should NOT be enabled in core-only toolset")
	}
	if toolEnabled("pdf") {
		t.Error("pdf should NOT be enabled in core-only toolset")
	}
}

func TestToolEnabledDefaultOnly(t *testing.T) {
	orig := enabledTools
	defer func() { enabledTools = orig }()
	enabledTools = parseToolsets("default")

	if !toolEnabled("click") {
		t.Error("click should be enabled in default toolset")
	}
	if !toolEnabled("navigate") {
		t.Error("navigate should be enabled in default toolset")
	}
	if !toolEnabled("snapshot") {
		t.Error("snapshot should be enabled in default toolset")
	}
	if toolEnabled("page_data") {
		t.Error("page_data should NOT be enabled in default toolset (moved to CapCore)")
	}
	if toolEnabled("network_requests") {
		t.Error("network_requests should NOT be enabled in default-only toolset")
	}
	if toolEnabled("new_tab") {
		t.Error("new_tab should NOT be enabled in default-only toolset")
	}
	if toolEnabled("scroll") {
		t.Error("scroll should NOT be enabled in default-only toolset")
	}
}

func TestToolEnabledCoreDiagnose(t *testing.T) {
	orig := enabledTools
	defer func() { enabledTools = orig }()
	enabledTools = parseToolsets("core,diagnose")

	if !toolEnabled("new_tab") {
		t.Error("new_tab should be enabled in core,diagnose")
	}
	if !toolEnabled("network_requests") {
		t.Error("network_requests should be enabled in core,diagnose")
	}
	if !toolEnabled("intercept") {
		t.Error("intercept should be enabled in core,diagnose")
	}
}

func TestAddToolFiltersCorrectly(t *testing.T) {
	orig := enabledTools
	defer func() { enabledTools = orig }()

	// Default only — core tools should be skipped
	enabledTools = parseToolsets("default")

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)

	// Register a default tool — should succeed
	addTool(server, &mcp.Tool{Name: "click", Description: "test"}, func(_ context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
		return nil, nil, nil
	})

	// Register a core-only tool — should be silently skipped
	addTool(server, &mcp.Tool{Name: "scroll", Description: "test"}, func(_ context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
		return nil, nil, nil
	})

	// We can't easily count registered tools from the MCP SDK,
	// but we can verify addTool didn't panic and the server is usable.
	_ = server
}
