package main

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerConfigTools(server *mcp.Server) {
	type configureInput struct {
		URL     string `json:"url,omitempty"`
		Port    int    `json:"port,omitempty"`
		Attach  *bool  `json:"attach,omitempty"`
		Profile string `json:"profile,omitempty"`
	}

	addTool(server, &mcp.Tool{
		Name: "configure",
		Description: "Configure the runtime target. Chrome via CDP.\n" +
			"\n" +
			"Options:\n" +
			"- url: target URL (e.g. \"http://localhost:3003\")\n" +
			"- port: dev server port (derives url as http://localhost:PORT)\n" +
			"- attach: if true, don't launch Chrome — connect to an existing CDP endpoint at the configured port.\n" +
			"- profile: \"isolated\" (default) or \"user\" (real Chrome profile with cookies/tokens)\n" +
			"\n" +
			"Examples:\n" +
			"  {\"url\": \"http://localhost:3003\"} — launch Chrome to localhost:3003\n" +
			"  {\"port\": 9222, \"attach\": true, \"url\": \"http://localhost:3003\"} — attach to existing Chrome",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input configureInput) (*mcp.CallToolResult, any, error) {
		setConfig(input.URL, input.Port, input.Attach)
		if input.Profile == "user" {
			setProfile("user")
		} else if input.Profile == "isolated" {
			setProfile("isolated")
		}
		status, err := browserStatus()
		return nil, status, err
	})

	addTool(server, &mcp.Tool{
		Name:        "status",
		Description: "Return the current runtime status: configured URL/port, Chrome running state and tab/page info.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
		status, err := browserStatus()
		return nil, status, err
	})
}

func browserStatus() (map[string]any, error) {
	url, port, attach := getConfig()
	profileMode := "isolated"
	if useUserProfile() {
		profileMode = "user"
	}
	status := map[string]any{
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
			status["hint"] = "attach mode is on — make sure the external CDP target is running and exposing CDP at the configured port"
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
