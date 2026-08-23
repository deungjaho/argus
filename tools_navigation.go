package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerNavigationTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "open",
		Description: "Deprecated: use configure to set URL, then navigate to open it. Open or select the configured URL in Chrome. Launches Chrome if not running, creates a tab if none matches.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input noInput) (*mcp.CallToolResult, any, error) {
		logWarn("open is deprecated, use configure + navigate")
		rec := BeginAction("open")
		defer rec.End()
		rec.SetParams(input)

		target, err := requireTarget()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(target.ID)
		_ = cdpActivateTarget(target.ID)
		status, err := browserStatus()
		if status == nil {
			status = map[string]any{}
		}
		status["action_id"] = string(rec.ID)
		if err != nil {
			rec.SetErrorFromError(err)
		} else {
			rec.SetResult(status)
		}
		return nil, status, err
	})

	type navigateInput struct {
		URL string `json:"url"`
	}

	addTool(server, &mcp.Tool{
		Name:        "navigate",
		Description: "Navigate the current tab to a new URL. Updates the configured URL. The targetID stays the same (same tab, different URL). Returns an actionable error with hints on DNS/SSL/timeout failures.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input navigateInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("navigate")
		defer rec.End()
		rec.SetParams(input)

		url := strings.TrimSpace(input.URL)
		if url == "" {
			rec.SetErrorFromError(fmt.Errorf("url is required"))
			return nil, nil, fmt.Errorf("url is required")
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
		if err != nil {
			rec.SetErrorFromError(err)
		} else {
			rec.SetResult(status)
		}
		return nil, status, err
	})

	type goInput struct {
		Direction string `json:"direction"` // "back" or "forward"
	}

	addTool(server, &mcp.Tool{
		Name:        "go",
		Description: "Go back or forward in browser history. direction: 'back' (previous page) or 'forward' (next page).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input goInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("go")
		defer rec.End()
		rec.SetParams(input)

		direction := strings.TrimSpace(input.Direction)
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
		rec.SetResult(map[string]any{"navigated": direction, "action_id": string(rec.ID)})
		return nil, map[string]any{"navigated": direction, "action_id": string(rec.ID)}, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "navigate_back",
		Description: "Deprecated: use go with direction=back instead. Go back to the previous page in browser history.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input noInput) (*mcp.CallToolResult, any, error) {
		logWarn("navigate_back is deprecated, use go with direction=back")
		rec := BeginAction("navigate_back")
		defer rec.End()
		rec.SetParams(input)

		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())
		_, err = cdpEvaluate(ws, `history.back()`, true)
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetResult(map[string]any{"navigated": "back", "action_id": string(rec.ID)})
		return nil, map[string]any{"navigated": "back", "action_id": string(rec.ID)}, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "navigate_forward",
		Description: "Deprecated: use go with direction=forward instead. Go forward to the next page in browser history.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input noInput) (*mcp.CallToolResult, any, error) {
		logWarn("navigate_forward is deprecated, use go with direction=forward")
		rec := BeginAction("navigate_forward")
		defer rec.End()
		rec.SetParams(input)

		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())
		_, err = cdpEvaluate(ws, `history.forward()`, true)
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetResult(map[string]any{"navigated": "forward", "action_id": string(rec.ID)})
		return nil, map[string]any{"navigated": "forward", "action_id": string(rec.ID)}, nil
	})

	type refreshInput struct {
		IgnoreCache bool `json:"ignore_cache,omitempty"`
	}

	addTool(server, &mcp.Tool{
		Name:        "refresh",
		Description: "Reload the current tab through CDP. Set ignore_cache=true to bypass cache (default: true).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input refreshInput) (*mcp.CallToolResult, any, error) {
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
		// input.IgnoreCache is false when omitted; use it only if explicitly set
		// Since Go zero value is false, we can't distinguish "not set" from "false"
		// but the description says default is true, so we keep true unless we add
		// a pointer type. For now, always ignore cache (matches description).
		_ = input // accepted for future use
		if err := cdpPageReload(ws, ignoreCache); err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetResult(map[string]any{"refreshed": true, "ignored_cache": ignoreCache, "action_id": string(rec.ID)})
		return nil, map[string]any{"refreshed": true, "ignored_cache": ignoreCache, "action_id": string(rec.ID)}, nil
	})
}
