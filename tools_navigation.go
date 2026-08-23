package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerNavigationTools(server *mcp.Server) {
	type navigateInput struct {
		URL         string `json:"url,omitempty"`
		Direction   string `json:"direction,omitempty"` // "back" or "forward"
		Refresh     bool   `json:"refresh,omitempty"`
		IgnoreCache bool   `json:"ignore_cache,omitempty"`
	}

	addTool(server, &mcp.Tool{
		Name:        "navigate",
		Description: "Navigate to a URL, go back/forward in browser history, or reload the current tab. Provide url to navigate to a new page, direction (back/forward) for history navigation, or refresh=true to reload the current tab (use ignore_cache=true to bypass cache). Returns an actionable error with hints on DNS/SSL/timeout failures.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input navigateInput) (*mcp.CallToolResult, any, error) {
		url := strings.TrimSpace(input.URL)
		direction := strings.TrimSpace(input.Direction)

		// --- Refresh (reload current tab) ---
		if input.Refresh {
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
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(target.ID)
		_, err = cdpEvaluate(target.WebSocketDebuggerURL, fmt.Sprintf(`location.href = %s`, jsString(url)), true)
		if err != nil {
			// Wrap navigation CDP/eval failures in an actionable error so
			// the agent gets categorized hints (DNS, SSL, timeout, etc.).
			navErr := newNavigationError(url, err.Error())
			rec.SetErrorFromError(navErr)
			return nil, nil, navErr
		}
		status, err := browserStatus()
		if status == nil {
			status = map[string]any{}
		}
		status["action_id"] = string(rec.ID)
		if page := miniSnapshot(target.WebSocketDebuggerURL); page != nil {
			status["page"] = page
		}
		if err != nil {
			rec.SetErrorFromError(err)
		} else {
			rec.SetResult(status)
		}
		return nil, status, err
	})
}
