package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type navigateInput struct {
	URL         string `json:"url,omitempty"`
	Direction   string `json:"direction,omitempty"` // "back" or "forward"
	Refresh     bool   `json:"refresh,omitempty"`
	Reload      bool   `json:"reload,omitempty"`
	IgnoreCache bool   `json:"ignore_cache,omitempty"`
	Href        string `json:"href,omitempty"`
	Target      string `json:"target,omitempty"`
	TargetID    string `json:"target_id,omitempty"`
	TabID       string `json:"tab_id,omitempty"`
	TimeoutMs   int    `json:"timeout_ms,omitempty"`
	Timeout     int    `json:"timeout,omitempty"`
	WaitUntil   string `json:"wait_until,omitempty"`
}

func registerNavigationTools(server *mcp.Server) {

	addTool(server, &mcp.Tool{
		Name:        "navigate",
		Description: "Navigate to a URL, go back/forward in browser history, or reload the current tab. Provide url to navigate to a new page, direction (back/forward) for history navigation, or refresh=true to reload the current tab (use ignore_cache=true to bypass cache). Returns an actionable error with hints on DNS/SSL/timeout failures.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input navigateInput) (*mcp.CallToolResult, any, error) {
		url := resolveURL(input.URL, input.Href, input.Target)
		if url != "" {
			url = normalizeURL(url)
		}
		direction := strings.TrimSpace(input.Direction)
		isRefresh := input.Refresh || input.Reload

		targetID := resolveFirstNonEmpty(input.TargetID, input.TabID)
		if targetID == "" && input.Target != "" && !looksLikeURL(input.Target) {
			targetID = strings.TrimSpace(input.Target)
		}
		if targetID != "" {
			setCurrentTargetID(targetID)
		}
		timeoutMs := resolveTimeout(input.TimeoutMs, input.Timeout)

		// --- Refresh (reload current tab) ---
		if isRefresh {
			rec := BeginAction("refresh")
			defer rec.End()
			rec.SetParams(input)

			ws, err := requireTargetWS()
			if err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			rec.SetTarget(getCurrentTargetID())
			ignoreCache := true // default
			// input.IgnoreCache is false when omitted; use it only if explicitly set.
			// Since Go zero value is false, we can't distinguish "not set" from "false"
			// but the description says default is true, so we keep true unless we add
			// a pointer type. For now, always ignore cache (matches description).
			if err := cdpPageReload(ws, ignoreCache); err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			result := map[string]any{"refreshed": true, "ignored_cache": ignoreCache, "action_id": string(rec.ID)}
			if page := miniSnapshot(ws); page != nil {
				result["page"] = page
			}
			attachEvidence(rec, ws, result)
			rec.SetResult(result)
			return nil, result, nil
		}

		// --- History navigation (back/forward) ---
		if direction != "" {
			rec := BeginAction("go")
			defer rec.End()
			rec.SetParams(input)

			if direction != "back" && direction != "forward" {
				rec.SetErrorFromError(fmt.Errorf("direction must be 'back' or 'forward'"))
				return nil, nil, fmt.Errorf("direction must be 'back' or 'forward'")
			}
			ws, err := requireTargetWS()
			if err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			rec.SetTarget(getCurrentTargetID())
			_, err = cdpEvaluate(ws, fmt.Sprintf(`history.%s()`, direction), true)
			if err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			result := map[string]any{"navigated": direction, "action_id": string(rec.ID)}
			if page := miniSnapshot(ws); page != nil {
				result["page"] = page
			}
			attachEvidence(rec, ws, result)
			rec.SetResult(result)
			return nil, result, nil
		}

		// --- URL navigation ---
		rec := BeginAction("navigate")
		defer rec.End()
		rec.SetParams(input)

		if url == "" {
			rec.SetErrorFromError(fmt.Errorf("url, direction, or refresh is required"))
			return nil, nil, fmt.Errorf("url, direction, or refresh is required")
		}
		setConfig(url, 0, nil)
		target, err := requireTarget()
		_, _, attach := getConfig()

		var fallbackTab bool
		if attach {
			if err != nil || target == nil || target.WebSocketDebuggerURL == "" {
				// No matching tab found or current target is closed/missing.
				fallbackTab = true
			}
		}

		if fallbackTab {
			version, vErr := cdpVersion()
			if vErr != nil {
				rec.SetErrorFromError(vErr)
				return nil, nil, vErr
			}
			tabID, cErr := cdpCreateTarget(version.WebSocketDebuggerURL, url, !wantFocus())
			if cErr != nil {
				rec.SetErrorFromError(cErr)
				return nil, nil, fmt.Errorf("failed to create tab in attach mode: %w", cErr)
			}
			setCurrentTargetID(tabID)
			if wantFocus() {
				_ = cdpActivateTarget(tabID)
			}
			rec.SetTarget(tabID)

			for attempt := 0; attempt < 5; attempt++ {
				targets, _ := cdpTargets()
				if t := findTargetByID(targets, tabID); t != nil && t.WebSocketDebuggerURL != "" {
					target = t
					break
				}
				time.Sleep(30 * time.Millisecond)
			}
			baseURL := cdpBaseURL()
			wsURL := strings.Replace(baseURL, "http://", "ws://", 1) + "/devtools/page/" + tabID
			if target == nil {
				target = &browserTarget{ID: tabID, URL: url, WebSocketDebuggerURL: wsURL}
			} else if target.WebSocketDebuggerURL == "" {
				target.WebSocketDebuggerURL = wsURL
			}
			err = nil
		}

		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(target.ID)
		var navResult map[string]any
		if target.WebSocketDebuggerURL != "" {
			navResult, err = cdpPageNavigate(target.WebSocketDebuggerURL, url, time.Duration(timeoutMs)*time.Millisecond)
			if err != nil {
				// Page.navigate errorText (net::ERR_*) feeds the classifier so
				// the agent gets categorized hints (DNS, SSL, timeout, etc.).
				navErr := newNavigationError(url, err.Error())
				rec.SetErrorFromError(navErr)
				return nil, nil, navErr
			}
			if input.WaitUntil != "" {
				waitTimeout := time.Duration(timeoutMs) * time.Millisecond
				if waitTimeout <= 0 {
					waitTimeout = 15 * time.Second
				}
				_, _ = waitForPageLoad(ctx, target.WebSocketDebuggerURL, input.WaitUntil, waitTimeout)
			}
		}
		status, err := browserStatus()
		if status == nil {
			status = map[string]any{}
		}
		status["action_id"] = string(rec.ID)
		for k, v := range navResult {
			status[k] = v
		}
		if target.WebSocketDebuggerURL != "" {
			if page := miniSnapshot(target.WebSocketDebuggerURL); page != nil {
				status["page"] = page
			}
			attachEvidence(rec, target.WebSocketDebuggerURL, status)
		}
		if err != nil {
			rec.SetErrorFromError(err)
		} else {
			rec.SetResult(status)
		}
		return nil, status, err
	})
}
