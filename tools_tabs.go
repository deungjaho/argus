package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type newTabInput struct {
	URL        string `json:"url"`
	Background bool   `json:"background,omitempty"`
}

type selectTabInput struct {
	TabID string `json:"tab_id"`
}

type closeTabInput struct {
	TabID string `json:"tab_id,omitempty"`
}

type forkInput struct {
	URL string `json:"url,omitempty"`
}

func registerTabTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "new_tab",
		Description: "Open a new browser tab and select it as the current target. Returns the new tab's ID.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input newTabInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("new_tab")
		defer rec.End()
		rec.SetParams(input)

		url := strings.TrimSpace(input.URL)
		if url == "" {
			rec.SetErrorFromError(fmt.Errorf("url is required"))
			return nil, nil, fmt.Errorf("url is required")
		}
		version, err := cdpVersion()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		tabID, err := cdpCreateTarget(version.WebSocketDebuggerURL, url, input.Background)
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		setConfig(url, 0, nil)
		if !input.Background {
			setCurrentTargetID(tabID)
			_ = cdpActivateTarget(tabID)
		}
		rec.SetTarget(tabID)
		rec.SetResult(map[string]any{"tab_id": tabID, "url": url, "background": input.Background, "action_id": string(rec.ID)})
		return nil, map[string]any{"tab_id": tabID, "url": url, "background": input.Background, "action_id": string(rec.ID)}, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "list_tabs",
		Description: "List all open browser tabs (page-type targets) with their IDs, URLs, and titles.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
		targets, err := cdpTargets()
		if err != nil {
			return nil, nil, err
		}
		pages := []map[string]any{}
		for _, t := range targets {
			if t.Type != "page" || strings.TrimSpace(t.ParentID) != "" {
				continue
			}
			pages = append(pages, map[string]any{
				"id":    t.ID,
				"url":   t.URL,
				"title": t.Title,
			})
		}
		return nil, map[string]any{"tabs": pages, "count": len(pages)}, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "select_tab",
		Description: "Activate a browser tab by its ID (from list_tabs). Sets the tab as the current target for subsequent operations.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input selectTabInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("select_tab")
		defer rec.End()
		rec.SetParams(input)

		tabID := strings.TrimSpace(input.TabID)
		if tabID == "" {
			rec.SetErrorFromError(fmt.Errorf("tab_id is required"))
			return nil, nil, fmt.Errorf("tab_id is required")
		}
		// Verify the tab exists
		targets, err := cdpTargets()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		target := findTargetByID(targets, tabID)
		if target == nil {
			rec.SetErrorFromError(fmt.Errorf("tab %s not found", tabID))
			return nil, nil, fmt.Errorf("tab %s not found", tabID)
		}
		if err := cdpActivateTarget(tabID); err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		setCurrentTargetID(tabID)
		setConfig(target.URL, 0, nil)
		rec.SetTarget(getCurrentTargetID())
		rec.SetResult(map[string]any{"selected": tabID, "url": target.URL, "title": target.Title, "action_id": string(rec.ID)})
		return nil, map[string]any{"selected": tabID, "url": target.URL, "title": target.Title, "action_id": string(rec.ID)}, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "close_tab",
		Description: "Close a browser tab by ID. If no tab_id is given, closes the current target tab.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input closeTabInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("close_tab")
		defer rec.End()
		rec.SetParams(input)

		tabID := strings.TrimSpace(input.TabID)
		if tabID == "" {
			tabID = getCurrentTargetID()
			if tabID == "" {
				target, err := requireTarget()
				if err != nil {
					rec.SetErrorFromError(err)
					return nil, nil, err
				}
				tabID = target.ID
			}
		}
		rec.SetTarget(tabID)
		if err := cdpCloseTarget(tabID); err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		// If we closed the current target, clear it
		if getCurrentTargetID() == tabID {
			clearCurrentTargetID()
		}
		rec.SetResult(map[string]any{"closed": tabID, "action_id": string(rec.ID)})
		return nil, map[string]any{"closed": tabID, "action_id": string(rec.ID)}, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "fork",
		Description: "Fork the current tab: create a new tab with the same URL as the current target. The new tab becomes the current target.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input forkInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("fork")
		defer rec.End()
		rec.SetParams(input)

		url := strings.TrimSpace(input.URL)
		if url == "" {
			target, err := requireTarget()
			if err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			url = target.URL
		}
		version, err := cdpVersion()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		tabID, err := cdpCreateTarget(version.WebSocketDebuggerURL, url, false)
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		setCurrentTargetID(tabID)
		rec.SetTarget(getCurrentTargetID())
		rec.SetResult(map[string]any{"forked": true, "tab_id": tabID, "url": url, "action_id": string(rec.ID)})
		return nil, map[string]any{"forked": true, "tab_id": tabID, "url": url, "action_id": string(rec.ID)}, nil
	})
}
