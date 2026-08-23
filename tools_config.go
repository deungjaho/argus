package main

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerConfigTools(server *mcp.Server) {
	type configureInput struct {
		URL       string `json:"url,omitempty"`
		Port      int    `json:"port,omitempty"`
		Attach    *bool  `json:"attach,omitempty"`
		Profile   string `json:"profile,omitempty"`
		Backend   string `json:"backend,omitempty"`    // "cdp" (default) or "wechat"
		WxProject string `json:"wx_project,omitempty"` // compiled mp-weixin output dir
		WxPort    int    `json:"wx_port,omitempty"`    // WeChat automator WebSocket port
	}

	addTool(server, &mcp.Tool{
		Name: "configure",
		Description: "Configure the runtime target. Argus supports two backends:\n" +
			"- backend=cdp (default): Chrome via CDP. Use url/port/attach/profile.\n" +
			"- backend=wechat: WeChat DevTools Mini Program via the automator WebSocket.\n" +
			"\n" +
			"CDP options:\n" +
			"- url: target URL (e.g. \"http://localhost:3003\")\n" +
			"- port: dev server port (derives url as http://localhost:PORT)\n" +
			"- attach: if true, don't launch Chrome — connect to an existing CDP endpoint at the configured port.\n" +
			"- profile: \"isolated\" (default) or \"user\" (real Chrome profile with cookies/tokens)\n" +
			"\n" +
			"WeChat options (set backend=wechat):\n" +
			"- wx_project: path to the compiled mp-weixin output (e.g. .../unpackage/dist/dev/mp-weixin)\n" +
			"- wx_port: WeChat automator WebSocket port (default 9420). WeChat DevTools must be running with `cli auto --auto-port <port>`.\n" +
			"\n" +
			"Examples:\n" +
			"  {\"url\": \"http://localhost:3003\"} — CDP: launch Chrome to localhost:3003\n" +
			"  {\"backend\": \"wechat\", \"wx_project\": \"/path/to/mp-weixin\", \"wx_port\": 9420} — WeChat Mini Program\n" +
			"  {\"port\": 9222, \"attach\": true, \"url\": \"http://localhost:3003\"} — CDP attach to existing Chrome",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input configureInput) (*mcp.CallToolResult, any, error) {
		// Backend switch: if switching away from wechat, tear down the
		// automator connection so the next call dials the new backend.
		newBackend := strings.ToLower(strings.TrimSpace(input.Backend))
		if newBackend != "" {
			if err := setBackendValidated(newBackend); err != nil {
				return nil, nil, &ToolError{
					Code:    ErrInvalidInput,
					Op:      "configure",
					Message: err.Error(),
				}
			}
			if activeBackend() != BackendWeChat {
				closeAutomatorConn()
			}
		}
		if strings.TrimSpace(input.WxProject) != "" {
			setWxProject(input.WxProject)
		}
		if input.WxPort > 0 {
			setWxPort(input.WxPort)
		}
		// CDP fields only apply under the cdp backend.
		if activeBackend() == BackendCDP {
			setConfig(input.URL, input.Port, input.Attach)
			if input.Profile == "user" {
				setProfile("user")
			} else if input.Profile == "isolated" {
				setProfile("isolated")
			}
		}
		status, err := runtimeStatus()
		return nil, status, err
	})

	addTool(server, &mcp.Tool{
		Name:        "status",
		Description: "Return the current runtime status: active backend (cdp or wechat), configured URL/port (cdp), Chrome running state and tab/page info (cdp), or WeChat automator connection and current page (wechat).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
		status, err := runtimeStatus()
		return nil, status, err
	})
}

// runtimeStatus returns status for the active backend. Under cdp it is the
// historical browserStatus; under wechat it probes the automator connection
// and current page.
func runtimeStatus() (map[string]any, error) {
	if activeBackend() == BackendWeChat {
		return wechatStatus()
	}
	return browserStatus()
}

// wechatStatus reports the WeChat automator connection and current page.
func wechatStatus() (map[string]any, error) {
	status := backendInfo()
	conn, err := getAutomatorConn()
	if err != nil {
		status["automator_connected"] = false
		if te, ok := err.(*ToolError); ok {
			status["automator_error"] = te.Message
		} else {
			status["automator_error"] = err.Error()
		}
		return status, nil
	}
	status["automator_connected"] = true
	// Event buffer diagnostics (visible drops/truncation).
	dropped, truncated, bytesIn := conn.eventStats()
	if dropped > 0 {
		status["events_dropped"] = dropped
	}
	if truncated > 0 {
		status["events_truncated"] = truncated
	}
	if bytesIn > 0 {
		status["event_bytes_in"] = bytesIn
	}
	// Current page
	path, pageID, err := automatorCurrentPage()
	if err != nil {
		status["page_error"] = err.Error()
		return status, nil
	}
	status["page"] = map[string]any{
		"page_id": pageID,
		"path":    path,
	}
	// Page stack depth (cheap, useful for navigation diagnostics)
	stack, err := automatorPageStack()
	if err == nil {
		status["page_stack_depth"] = len(stack)
	}
	return status, nil
}

func browserStatus() (map[string]any, error) {
	url, port, attach := getConfig()
	profileMode := "isolated"
	if useUserProfile() {
		profileMode = "user"
	}
	status := map[string]any{
		"backend":         BackendCDP,
		"configured_url":  url,
		"configured_port": port,
		"cdp_port":        cdpPort,
		"attach_mode":     attach,
		"profile_mode":    profileMode,
		"browser_enabled": strings.TrimSpace(url) != "",
	}
	if !status["browser_enabled"].(bool) {
		return status, nil
	}
	if _, err := cdpVersion(); err != nil {
		status["browser_running"] = false
		status["browser_error"] = err.Error()
		if attach {
			status["hint"] = "attach mode is on — make sure the external CDP target (e.g. WeChat DevTools) is running and exposing CDP at the configured port"
		}
		return status, nil
	}
	status["browser_running"] = true
	// Prefer targetID, fall back to URL
	targets, err := cdpTargets()
	if err != nil {
		return nil, err
	}
	var target *browserTarget
	if id := getCurrentTargetID(); id != "" {
		target = findTargetByID(targets, id)
	}
	if target == nil {
		target = findTargetByURL(targets, url)
	}
	status["tab_found"] = target != nil
	if target == nil {
		return status, nil
	}
	status["tab"] = map[string]any{"id": target.ID, "url": target.URL, "title": target.Title}
	page, err := cdpEvaluate(target.WebSocketDebuggerURL, `(() => ({
url: location.href,
title: document.title,
readyState: document.readyState,
visibilityState: document.visibilityState,
viewport: {width: innerWidth, height: innerHeight, devicePixelRatio: devicePixelRatio, scrollX: scrollX, scrollY: scrollY}
}))()`, true)
	if err != nil {
		status["page_error"] = err.Error()
		return status, nil
	}
	status["page"] = page
	return status, nil
}
