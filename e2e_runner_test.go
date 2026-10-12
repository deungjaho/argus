//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// setupMCPServer creates an in-memory MCP server with all tools registered and an attached client session.
// It registers cleanup via t.Cleanup().
func setupMCPServer(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	enabledTools = parseToolsets("full")
	server := mcp.NewServer(&mcp.Implementation{Name: "argus-e2e", Version: "e2e"}, nil)
	registerAllTools(server)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("setupMCPServer: server connect failed: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "argus-e2e-client", Version: "e2e"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("setupMCPServer: client connect failed: %v", err)
	}

	t.Cleanup(func() {
		_ = cs.Close()
	})
	return cs
}

// callMCPTool invokes an MCP tool and fails the test if CallTool itself returns an unexpected transport error.
func callMCPTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("callMCPTool [%s] failed with transport/RPC error: %v", name, err)
	}
	return res
}

// callMCPToolRaw invokes an MCP tool with explicit context and returns the CallToolResult and error.
func callMCPToolRaw(ctx context.Context, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, error) {
	return cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
}

// extractToolResultJSON extracts and parses the JSON response payload from the CallToolResult's TextContent.
func extractToolResultJSON(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	if res == nil {
		t.Fatal("extractToolResultJSON: CallToolResult is nil")
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			var parsed map[string]any
			if err := json.Unmarshal([]byte(tc.Text), &parsed); err != nil {
				t.Fatalf("extractToolResultJSON: failed to unmarshal JSON: %v\nraw text: %s", err, tc.Text)
			}
			return parsed
		}
	}
	t.Fatalf("extractToolResultJSON: no TextContent found in result content (count: %d)", len(res.Content))
	return nil
}

// extractToolResultText extracts the raw string content from the CallToolResult's TextContent.
func extractToolResultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil {
		t.Fatal("extractToolResultText: CallToolResult is nil")
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	t.Fatalf("extractToolResultText: no TextContent found in result content (count: %d)", len(res.Content))
	return ""
}

// assertE2ESuccess asserts that the CallToolResult did not return an error (res.IsError == false).
func assertE2ESuccess(t *testing.T, res *mcp.CallToolResult, actionDesc string) {
	t.Helper()
	if res == nil {
		t.Fatalf("[%s] expected success, got nil result", actionDesc)
	}
	if res.IsError {
		text := extractToolResultText(t, res)
		t.Fatalf("[%s] expected success, got IsError=true with output: %s", actionDesc, text)
	}
}

// assertE2EError asserts that the CallToolResult returned an error (res.IsError == true).
func assertE2EError(t *testing.T, res *mcp.CallToolResult, actionDesc string) {
	t.Helper()
	if res == nil {
		t.Fatalf("[%s] expected error, got nil result", actionDesc)
	}
	if !res.IsError {
		text := extractToolResultText(t, res)
		t.Fatalf("[%s] expected error, got IsError=false with output: %s", actionDesc, text)
	}
}

// getElementHTML helper extracts HTML string from get_element result (handling html vs outer_html keys).
func getElementHTML(data map[string]any) string {
	if s, ok := data["html"].(string); ok {
		return s
	}
	if s, ok := data["outer_html"].(string); ok {
		return s
	}
	return ""
}

// formatURL helper returns full URL combining baseURL and relative path
func fixtureURL(serverURL, path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return fmt.Sprintf("%s%s", serverURL, path)
}
