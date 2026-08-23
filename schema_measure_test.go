package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// schemaMeasureTest.go — Runtime 8.0 surface measurement.
//
// These tests report the exact registered tool count and total JSON schema
// size for each capability profile, so surface changes are visible and gated.
// The MCP client sees the same JSON an agent client sees, so the byte count
// is the real on-the-wire schema cost (before any transport framing).

// setupMCPWithToolsets builds a real MCP server with the given toolset filter
// and connects an in-memory client, returning the client session.
func setupMCPWithToolsets(t *testing.T, toolsets string) *mcp.ClientSession {
	t.Helper()
	orig := enabledTools
	defer func() { enabledTools = orig }()
	enabledTools = parseToolsets(toolsets)

	server := mcp.NewServer(&mcp.Implementation{Name: "argus-measure", Version: "test"}, nil)
	registerAllTools(server)
	ct, st := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatalf("server connect failed: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "measure-client", Version: "test"}, nil)
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}
	return cs
}

// measureToolset lists tools through the real MCP pipeline and returns the
// tool count and the total JSON schema bytes (the serialized ListToolsResult
// Tools array, each entry including name, description, and inputSchema).
func measureToolset(t *testing.T, toolsets string) (toolCount int, schemaBytes int) {
	t.Helper()
	orig := enabledTools
	defer func() { enabledTools = orig }()
	enabledTools = parseToolsets(toolsets)

	server := mcp.NewServer(&mcp.Implementation{Name: "argus-measure", Version: "test"}, nil)
	registerAllTools(server)
	ct, st := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatalf("server connect failed: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "measure-client", Version: "test"}, nil)
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}

	// Paginate through all tools.
	var allTools []*mcp.Tool
	cursor := ""
	for {
		res, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			t.Fatalf("ListTools failed: %v", err)
		}
		allTools = append(allTools, res.Tools...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}

	// Serialize each tool individually and sum, so we measure the real
	// per-tool schema cost an agent receives.
	total := 0
	for _, tool := range allTools {
		b, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("marshal tool %s: %v", tool.Name, err)
		}
		total += len(b)
	}
	return len(allTools), total
}

// TestRuntime8DefaultToolCount verifies the default toolset has <= 14 tools.
func TestRuntime8DefaultToolCount(t *testing.T) {
	count, bytes := measureToolset(t, "default")
	t.Logf("default toolset: %d tools, %d schema bytes (~%d estimated tokens)",
		count, bytes, bytes/4)
	if count > 14 {
		t.Errorf("default toolset has %d tools, Runtime 8.0 gate is <= 14", count)
	}
}

// TestRuntime8DefaultSchemaSize verifies the default total schema is within
// the 8.0 budget. Target is 1,500–2,000 estimated tokens; hard gate is
// ~2,500 estimated tokens (10,000 bytes at 4 bytes/token).
func TestRuntime8DefaultSchemaSize(t *testing.T) {
	count, bytes := measureToolset(t, "default")
	estTokens := bytes / 4
	t.Logf("default toolset: %d tools, %d bytes, ~%d tokens", count, bytes, estTokens)
	if estTokens > 2500 {
		t.Errorf("default schema ~%d tokens, Runtime 8.0 hard gate is <= 2500 tokens", estTokens)
	}
}

// TestRuntime8SurfaceReport prints the full surface report for all profiles
// so any change is visible in test output. This is informational; the gates
// above enforce the limits.
func TestRuntime8SurfaceReport(t *testing.T) {
	profiles := []string{"default", "default,diagnose", "core", "full"}
	for _, p := range profiles {
		count, bytes := measureToolset(t, p)
		t.Logf("profile=%-20s tools=%-3d schema_bytes=%-6d est_tokens=%d",
			p, count, bytes, bytes/4)
	}
}
