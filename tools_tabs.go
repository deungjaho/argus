package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// tabInput represents parameters for the unified canonical tab management tool.
type tabInput struct {
	Action     string `json:"action,omitempty"`     // "list" | "new" | "select" | "close" (default: "list")
	TabID      string `json:"tab_id,omitempty"`     // select/close target tab ID
	TargetID   string `json:"target_id,omitempty"`  // alias for tab_id
	ID         string `json:"id,omitempty"`         // alias for tab_id
	URL        string `json:"url,omitempty"`        // new/select/close target URL
	Href       string `json:"href,omitempty"`       // alias for url
	Target     string `json:"target,omitempty"`     // alias for url or tab_id
	Title      string `json:"title,omitempty"`      // select target title substring
	Background bool   `json:"background,omitempty"` // new tab background flag
}

// Backward-compatibility aliases for param tests and legacy call signatures
type newTabInput = tabInput
type selectTabInput = tabInput
type closeTabInput = tabInput
type forkInput = tabInput

func registerTabTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "tab",
		Description: "Manage browser tabs: action can be 'list', 'new', 'select', or 'close'. For 'new': specify url (default 'about:blank') and background. For 'select'/'close': specify tab_id (or url/title).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input tabInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("tab")
		defer rec.End()
		rec.SetParams(input)

		action := resolveTabAction(input)

		switch action {
		case "list":
			targets, err := cdpTargets()
			if err != nil {
				rec.SetErrorFromError(err)
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
			res := map[string]any{"tabs": pages, "count": len(pages), "action_id": string(rec.ID)}
			rec.SetResult(res)
			return nil, res, nil

		case "new":
			url := resolveURL(input.URL, input.Href, input.Target)
			if url == "" {
				url = "about:blank"
			} else {
				url = normalizeURL(url)
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
				if wantFocus() {
					_ = cdpActivateTarget(tabID)
				}
			}
			rec.SetTarget(tabID)
			res := map[string]any{"tab_id": tabID, "url": url, "background": input.Background, "action_id": string(rec.ID)}
			rec.SetResult(res)
			return nil, res, nil

		case "select":
			tabID := resolveFirstNonEmpty(input.TabID, input.TargetID, input.ID)
			urlQuery := resolveURL(input.URL, input.Href, "")
			if tabID == "" && input.Target != "" {
				if looksLikeURL(input.Target) {
					urlQuery = input.Target
				} else {
					tabID = strings.TrimSpace(input.Target)
				}
			}
			titleQuery := strings.TrimSpace(input.Title)
			if tabID == "" && urlQuery == "" && titleQuery == "" {
				err := fmt.Errorf("tab select requires tab_id, url, or title")
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			targets, err := cdpTargets()
			if err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			var target *browserTarget
			if tabID != "" {
				target = findTargetByID(targets, tabID)
			}
			if target == nil && urlQuery != "" {
				target = findTargetByURL(targets, urlQuery)
			}
			if target == nil && titleQuery != "" {
				for i := range targets {
					if targets[i].Type == "page" && strings.Contains(strings.ToLower(targets[i].Title), strings.ToLower(titleQuery)) {
						target = &targets[i]
						break
					}
				}
			}
			if target == nil {
				err := fmt.Errorf("tab not found matching id=%q url=%q title=%q", tabID, urlQuery, titleQuery)
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			tabID = target.ID
			if wantFocus() {
				if err := cdpActivateTarget(tabID); err != nil {
					rec.SetErrorFromError(err)
					return nil, nil, err
				}
			}
			setCurrentTargetID(tabID)
			setConfig(target.URL, 0, nil)
			rec.SetTarget(tabID)
			res := map[string]any{"selected": tabID, "url": target.URL, "title": target.Title, "action_id": string(rec.ID)}
			rec.SetResult(res)
			return nil, res, nil

		case "close":
			tabID := resolveFirstNonEmpty(input.TabID, input.TargetID, input.ID)
			if tabID == "" && input.Target != "" && !looksLikeURL(input.Target) {
				tabID = strings.TrimSpace(input.Target)
			}
			urlQuery := resolveURL(input.URL, input.Href, input.Target)
			if tabID == "" && urlQuery != "" {
				targets, _ := cdpTargets()
				if t := findTargetByURL(targets, urlQuery); t != nil {
					tabID = t.ID
				}
			}
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
			removeInterceptStateForTarget(tabID)
			dialogMgr.Remove(tabID)
			if getCurrentTargetID() == tabID {
				clearCurrentTargetID()
			}
			res := map[string]any{"closed": tabID, "action_id": string(rec.ID)}
			rec.SetResult(res)
			return nil, res, nil

		default:
			err := fmt.Errorf("unknown tab action %q: must be 'list', 'new', 'select', or 'close'", input.Action)
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
	})
}

// resolveTabAction normalizes the tab action or infers it based on input parameters.
func resolveTabAction(input tabInput) string {
	action := strings.ToLower(strings.TrimSpace(input.Action))
	if action == "" {
		// Infer default action: if tab_id or title is specified -> select; if url -> new; else list
		if input.TabID != "" || input.TargetID != "" || input.ID != "" || input.Title != "" {
			return "select"
		} else if input.URL != "" || input.Href != "" {
			return "new"
		} else {
			return "list"
		}
	}
	return action
}
