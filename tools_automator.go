package main

// tools_automator.go — WeChat Mini Program automator tools.
//
// These tools are only meaningful under the wechat backend. They expose
// Mini Program runtime capabilities that have no direct CDP equivalent:
//
//   - page_data:        read the current page's reactive data (Page.getData)
//   - call_wx_method:   invoke a wx.* API in the Mini Program runtime
//   - mock_wx_method:   mock a wx.* API to return a fixed value
//   - wxml:             dump the WXML structure of a page or element
//
// The shared tools (screenshot, click, fill, snapshot, console_messages,
// eval, navigate, exists) are routed in their existing files via
// activeBackend() checks; the protocol-correct helpers they call live in
// automator_protocol.go.
//
// page_stack was merged into status (which already reports page_stack_depth).
// raw WXML, wx call, and wx mock are escape hatches (CapDiagnose), not core.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerAutomatorTools(server *mcp.Server) {
	registerAutomatorPageData(server)
	registerAutomatorCallWxMethod(server)
	registerAutomatorMockWxMethod(server)
	registerAutomatorWXML(server)
}

// --- page_data ---

type pageDataInput struct {
	PageID   int    `json:"page_id,omitempty"`   // optional: page ID (defaults to current page)
	DataPath string `json:"data_path,omitempty"` // optional: nested data path within the page data
	MaxChars int    `json:"max_chars,omitempty"` // bound output (default 8000)
}

func registerAutomatorPageData(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name: "page_data",
		Description: "Read the current Mini Program page's reactive data (Page.getData). " +
			"Returns the page's data object, which includes list contents, flags, and " +
			"the active baseUrl (useful for verifying test vs production environment). " +
			"Set page_id to target a specific page in the stack; defaults to the current page. " +
			"Set data_path to read a nested path within the page data. " +
			"Output is bounded (max_chars default 8000). WeChat backend only.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input pageDataInput) (*mcp.CallToolResult, any, error) {
		if activeBackend() != BackendWeChat {
			return nil, nil, errUnsupportedBackend("page_data", BackendWeChat)
		}
		maxChars := input.MaxChars
		if maxChars <= 0 {
			maxChars = 8000
		}
		if maxChars > 40000 {
			maxChars = 40000
		}
		pageID := input.PageID
		if pageID <= 0 {
			var err error
			pageID, err = resolveCurrentPageID()
			if err != nil {
				return nil, nil, err
			}
		}
		data, err := automatorPageData(pageID, input.DataPath)
		if err != nil {
			return nil, nil, err
		}
		// Serialize and bound it. A truncated object is returned as a
		// string plus truncation metadata, never invalid json.RawMessage.
		raw, _ := json.Marshal(data)
		bounded := string(raw)
		truncated := false
		if len(bounded) > maxChars {
			bounded = truncateRunes(bounded, maxChars)
			truncated = true
		}
		result := map[string]any{
			"backend":   BackendWeChat,
			"page_id":   pageID,
			"data":      bounded,
			"truncated": truncated,
		}
		// Surface baseUrl if present at the top level (common in uni-app).
		if m, ok := data.(map[string]any); ok {
			if bu, ok := m["baseUrl"].(string); ok && bu != "" {
				result["base_url"] = bu
			}
		}
		if truncated {
			attachTruncation(result, TruncationMeta{Truncated: true, Reason: "max_chars"})
		}
		return nil, result, nil
	})
}

// --- call_wx_method ---

type callWxMethodInput struct {
	Method string `json:"method"` // wx method name, e.g. "getStorageSync"
	Args   []any  `json:"args,omitempty"`
}

func registerAutomatorCallWxMethod(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name: "call_wx_method",
		Description: "Invoke a wx.* API in the Mini Program runtime (App.callWxMethod). " +
			"Example: {\"method\": \"getStorageSync\", \"args\": [\"token\"]}. " +
			"Useful for reading storage, system info, or other wx APIs that affect page " +
			"behavior. This is an escape hatch, not a core interaction tool. " +
			"WeChat backend only.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input callWxMethodInput) (*mcp.CallToolResult, any, error) {
		if activeBackend() != BackendWeChat {
			return nil, nil, errUnsupportedBackend("call_wx_method", BackendWeChat)
		}
		method := strings.TrimSpace(input.Method)
		if method == "" {
			return nil, nil, &ToolError{
				Code:    ErrInvalidInput,
				Op:      "call_wx_method",
				Message: "call_wx_method requires method",
			}
		}
		args := input.Args
		if args == nil {
			args = []any{}
		}
		result, err := automatorCallWxMethod(method, args)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{
			"backend": BackendWeChat,
			"method":  method,
			"result":  result,
		}, nil
	})
}

// --- mock_wx_method ---

type mockWxMethodInput struct {
	Method string `json:"method"` // wx method name to mock
	Result any    `json:"result"` // value the mock should return
}

func registerAutomatorMockWxMethod(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name: "mock_wx_method",
		Description: "Mock a wx.* API to return a fixed value (App.mockWxMethod). " +
			"Useful for simulating payment success, authorization, or location without " +
			"real device APIs. The mock persists for the rest of the session. " +
			"This is an escape hatch, not a core interaction tool. " +
			"WeChat backend only.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input mockWxMethodInput) (*mcp.CallToolResult, any, error) {
		if activeBackend() != BackendWeChat {
			return nil, nil, errUnsupportedBackend("mock_wx_method", BackendWeChat)
		}
		method := strings.TrimSpace(input.Method)
		if method == "" {
			return nil, nil, &ToolError{
				Code:    ErrInvalidInput,
				Op:      "mock_wx_method",
				Message: "mock_wx_method requires method",
			}
		}
		if err := automatorCall("App.mockWxMethod", map[string]any{
			"method": method,
			"result": input.Result,
		}, nil); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{
			"backend": BackendWeChat,
			"mocked":  method,
			"result":  input.Result,
		}, nil
	})
}

// --- wxml ---

type wxmlInput struct {
	Selector string `json:"selector,omitempty"` // element selector; empty = whole page
	MaxChars int    `json:"max_chars,omitempty"`
}

func registerAutomatorWXML(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name: "wxml",
		Description: "Dump the WXML structure of the current Mini Program page or a " +
			"specific element (Page.getElement + Element.getWXML). Useful for inspecting " +
			"component structure when snapshot text is insufficient. Output is bounded " +
			"(max_chars default 8000). This is an on-demand inspection tool, not core. " +
			"WeChat backend only.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input wxmlInput) (*mcp.CallToolResult, any, error) {
		if activeBackend() != BackendWeChat {
			return nil, nil, errUnsupportedBackend("wxml", BackendWeChat)
		}
		maxChars := input.MaxChars
		if maxChars <= 0 {
			maxChars = 8000
		}
		if maxChars > 40000 {
			maxChars = 40000
		}
		pageID, err := resolveCurrentPageID()
		if err != nil {
			return nil, nil, err
		}
		selector := strings.TrimSpace(input.Selector)
		if selector == "" {
			selector = "page"
		}
		elem, err := automatorGetElement(pageID, selector)
		if err != nil {
			return nil, nil, fmt.Errorf("Page.getElement(%s): %w", selector, err)
		}
		wxml, err := automatorGetWXML(pageID, elem, "inner")
		if err != nil {
			return nil, nil, err
		}
		out := wxml
		truncated := false
		if len(out) > maxChars {
			out = truncateRunes(out, maxChars)
			truncated = true
		}
		result := map[string]any{
			"backend":   BackendWeChat,
			"selector":  selector,
			"wxml":      out,
			"truncated": truncated,
		}
		if truncated {
			attachTruncation(result, TruncationMeta{Truncated: true, Reason: "max_chars"})
		}
		return nil, result, nil
	})
}

// --- shared helpers used by routed tools ---

// resolveCurrentPageID returns the pageId of the current page.
func resolveCurrentPageID() (int, error) {
	_, pageID, err := automatorCurrentPage()
	if err != nil {
		return 0, fmt.Errorf("App.getCurrentPage: %w", err)
	}
	return pageID, nil
}

// resolvePageID returns the pageId for the given path, or the current page
// when path is empty. Kept for backward compatibility with existing callers.
func resolvePageID(path string) (int, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return resolveCurrentPageID()
	}
	// Find the page in the stack matching the path.
	stack, err := automatorPageStack()
	if err != nil {
		return 0, fmt.Errorf("App.getPageStack: %w", err)
	}
	for _, p := range stack {
		if p.Path == path {
			return p.PageID, nil
		}
	}
	return 0, fmt.Errorf("page not in stack: %s", path)
}

// automatorRecentLogs returns recent log/exception events from the ring
// buffer, optionally filtered to warnings/errors. Mirrors the CDP
// console_messages from_recorder mode.
func automatorRecentLogs(all bool, limit int, maxChars int) ([]map[string]any, int64, int) {
	conn, err := getAutomatorConn()
	if err != nil {
		return nil, 0, 0
	}
	if limit <= 0 {
		limit = 50
	}
	events, dropped := conn.recentEvents(limit)
	out := make([]map[string]any, 0, len(events))
	for _, e := range events {
		level := automatorEventLevel(e.Method, e.Params)
		if !all && level != "warn" && level != "error" {
			continue
		}
		entry := map[string]any{
			"seq":       e.Seq,
			"timestamp": e.Timestamp.Format(time.RFC3339Nano),
			"method":    e.Method,
			"level":     level,
		}
		if e.Params != nil {
			text := automatorEventText(e.Params, maxChars)
			entry["text"] = text
		}
		out = append(out, entry)
	}
	return out, dropped, len(events)
}

// automatorEventLevel classifies an event method (and optionally params)
// into a log level. If the params contain a "level" field, it takes
// precedence over the method-based classification.
func automatorEventLevel(method string, params any) string {
	// Check params for an explicit level field first.
	if params != nil {
		if m, ok := params.(map[string]any); ok {
			if level, ok := m["level"].(string); ok {
				switch level {
				case "error", "warn", "info", "log", "debug":
					return level
				}
			}
		}
	}
	// Fall back to method-based classification.
	switch {
	case strings.Contains(method, "error"), strings.Contains(method, "Error"):
		return "error"
	case strings.Contains(method, "warn"), strings.Contains(method, "Warn"):
		return "warn"
	case strings.Contains(method, "exception"), strings.Contains(method, "Exception"):
		return "error"
	default:
		return "log"
	}
}

// automatorEventText extracts a readable text representation from an event
// params payload, bounded by maxChars.
func automatorEventText(params any, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 1000
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return ""
	}
	s := string(raw)
	if len(s) > maxChars {
		s = truncateRunes(s, maxChars)
	}
	return s
}

// automatorScreenshotResult captures a Mini Program screenshot, compresses
// it to JPEG, and returns an MCP result parallel to screenshotResult.
// Temp files are removed after reading — the agent never receives a path
// to a deleted file.
func automatorScreenshotResult(maxSizeKB int) (*mcp.CallToolResult, any, error) {
	pngData, err := automatorScreenshot()
	if err != nil {
		return nil, nil, err
	}
	path, err := automatorScreenshotPath()
	if err != nil {
		return nil, nil, err
	}
	if err := writeCompressedScreenshot(pngData, path); err != nil {
		_ = os.Remove(path)
		return nil, nil, err
	}
	jpegData, err := os.ReadFile(path)
	// Always clean up the temp file after reading.
	_ = os.Remove(path)
	if err != nil {
		return nil, nil, err
	}
	if maxSizeKB <= 0 {
		maxSizeKB = 500
	}
	if maxSizeKB > 2000 {
		maxSizeKB = 2000
	}
	maxBytes := maxSizeKB * 1024
	imageWidth, imageHeight, _ := imageDimensions(jpegData)
	pagePath, pageID := "", 0
	if p, pid, err := automatorCurrentPage(); err == nil {
		pagePath, pageID = p, pid
	}
	meta := map[string]any{
		"backend":   BackendWeChat,
		"page":      pagePath,
		"page_id":   pageID,
		"bytes":     len(jpegData),
		"mime_type": "image/jpeg",
		"image": map[string]any{
			"width":  imageWidth,
			"height": imageHeight,
		},
	}
	if len(jpegData) > maxBytes {
		attachTruncation(meta, TruncationMeta{
			Truncated:  true,
			TotalCount: len(jpegData),
			ShownCount: 0,
			Reason:     fmt.Sprintf("screenshot %d bytes exceeds max_size_kb=%d (%d bytes); image omitted from response", len(jpegData), maxSizeKB, maxBytes),
		})
		meta["max_size_kb"] = maxSizeKB
		metaText, _ := json.MarshalIndent(meta, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: string(metaText)},
		}}, nil, nil
	}
	metaText, _ := json.MarshalIndent(meta, "", "  ")
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: string(metaText)},
		&mcp.ImageContent{Data: jpegData, MIMEType: "image/jpeg"},
	}}, nil, nil
}

// automatorScreenshotPath returns a temp path for a wechat screenshot.
func automatorScreenshotPath() (string, error) {
	return filepath.Join(os.TempDir(), "argus-wx-screenshot-"+time.Now().Format("20060102-150405.000000000")+".png"), nil
}
