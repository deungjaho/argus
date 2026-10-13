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
		Focus   *bool  `json:"focus,omitempty"`
		Target  string `json:"target,omitempty"`
		Href    string `json:"href,omitempty"`
	}

	type statusInput struct {
		Target string `json:"target,omitempty"`
		TabID  string `json:"tab_id,omitempty"`
	}

	addTool(server, &mcp.Tool{
		Name:        "configure",
		Description: "Set target URL/port, attach mode, profile (isolated/user), and window focus.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input configureInput) (*mcp.CallToolResult, any, error) {
		resolvedURL := resolveURL(input.URL, input.Href, input.Target)
		if resolvedURL != "" {
			resolvedURL = normalizeURL(resolvedURL)
		}
		port := input.Port
		if port == 0 && input.Target != "" && !looksLikeURL(input.Target) {
			if p, ok := isNumericPort(input.Target); ok {
				port = p
			}
		}
		setConfig(resolvedURL, port, input.Attach)
		if input.Profile == "user" {
			setProfile("user")
		} else if input.Profile == "isolated" {
			setProfile("isolated")
		}
		if input.Focus != nil {
			setFocus(*input.Focus)
		}
		status, err := browserStatus()
		return nil, status, err
	})

	addTool(server, &mcp.Tool{
		Name:        "status",
		Description: "Return the current runtime status: configured URL/port, Chrome running state and tab/page info.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input statusInput) (*mcp.CallToolResult, any, error) {
		if tabID := resolveFirstNonEmpty(input.TabID, input.Target); tabID != "" && !looksLikeURL(tabID) {
			setCurrentTargetID(tabID)
		}
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
		"focus_mode":      wantFocus(),
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
	pageTabs := 0
	for i := range targets {
		if targets[i].Type == "page" && strings.TrimSpace(targets[i].ParentID) == "" {
			pageTabs++
		}
	}
	status["tabs"] = pageTabs
	if target == nil {
		return status, nil
	}
	if d := dialogMgr.GetPendingDialog(target.ID); d != nil {
		status["pending_dialog"] = map[string]any{"type": d.Type, "message": d.Message}
	}
	status["tab"] = map[string]any{"id": target.ID, "url": target.URL, "title": target.Title}
	if ext := externalEvents(cdpLedgerRead(target.WebSocketDebuggerURL), 60000); len(ext) > 0 {
		status["recent_external_input"] = ext
	}
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
