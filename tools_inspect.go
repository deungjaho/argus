package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- wait_for_load ---

type waitForLoadInput struct {
	WaitUntil string `json:"wait_until,omitempty"` // load | domcontentloaded | networkidle
	TimeoutMs int    `json:"timeout_ms,omitempty"`
}

func registerInspectTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "wait_for_load",
		Description: "Wait for the page to finish loading. wait_until options: 'load' (full page load, default), 'domcontentloaded' (DOM ready, faster), 'networkidle' (no network requests for 500ms after readyState=complete). Default timeout 15s.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input waitForLoadInput) (*mcp.CallToolResult, any, error) {
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		waitUntil := strings.TrimSpace(input.WaitUntil)
		if waitUntil == "" {
			waitUntil = "load"
		}
		timeout := time.Duration(input.TimeoutMs) * time.Millisecond
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return nil, map[string]any{"cancelled": true, "wait_until": waitUntil}, ctx.Err()
			default:
			}
			value, err := cdpEvaluate(ws, `(() => ({
readyState: document.readyState,
url: location.href,
title: document.title,
lastResourceTime: performance.getEntriesByType('resource').reduce((max, e) => Math.max(max, e.responseEnd || e.startTime || 0), 0),
now: performance.now()
}))()`, true)
			if err == nil {
				if info, ok := value.(map[string]any); ok {
					readyState, _ := info["readyState"].(string)
					switch waitUntil {
					case "domcontentloaded":
						if readyState == "interactive" || readyState == "complete" {
							return nil, info, nil
						}
					case "load":
						if readyState == "complete" {
							return nil, info, nil
						}
					case "networkidle":
						if readyState == "complete" {
							// Check if no network activity in the last 500ms
							lastResource, _ := numberValue(info["lastResourceTime"])
							now, _ := numberValue(info["now"])
							if now-lastResource >= 500 {
								return nil, info, nil
							}
						}
					}
				}
			}
			select {
			case <-ctx.Done():
				return nil, map[string]any{"cancelled": true, "wait_until": waitUntil}, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		return nil, map[string]any{"timed_out": true, "wait_until": waitUntil, "timeout_ms": timeout.Milliseconds()}, nil
	})

	// --- exists ---

	type existsInput struct {
		Ref       string `json:"ref,omitempty"`
		Selector  string `json:"selector,omitempty"`
		Text      string `json:"text,omitempty"`
		TimeoutMs int    `json:"timeout_ms,omitempty"`
	}

	addTool(server, &mcp.Tool{
		Name:        "exists",
		Description: "Check if an element exists on the page. Returns {exists, visible, enabled, count} — never throws. With timeout_ms > 0, polls until found or timeout. Default timeout 0 (instant check).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input existsInput) (*mcp.CallToolResult, any, error) {
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		selector := firstNonEmpty(strings.TrimSpace(input.Ref), strings.TrimSpace(input.Selector))
		text := strings.TrimSpace(input.Text)
		if selector == "" && text == "" {
			return nil, nil, fmt.Errorf("exists requires ref, selector, or text")
		}
		timeout := time.Duration(input.TimeoutMs) * time.Millisecond
		if timeout > 0 {
			deadline := time.Now().Add(timeout)
			for time.Now().Before(deadline) {
				result, _ := checkElementExists(ws, selector, text)
				if result["exists"].(bool) {
					return nil, result, nil
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
		result, _ := checkElementExists(ws, selector, text)
		return nil, result, nil
	})

	// --- current_url ---

	addTool(server, &mcp.Tool{
		Name:        "current_url",
		Description: "Deprecated: use status instead. Get the current page URL and title.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
		logWarn("current_url is deprecated, use status instead")
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		value, err := cdpEvaluate(ws, `(() => ({url: location.href, title: document.title, readyState: document.readyState}))()`, true)
		return nil, value, err
	})
}

func checkElementExists(ws, selector, text string) (map[string]any, error) {
	value, err := cdpEvaluate(ws, fmt.Sprintf(`(() => {
const selector = %s;
const text = %s;
let el = null;
let count = 0;
if (selector) {
  const all = document.querySelectorAll(selector);
  count = all.length;
  el = all[0] || null;
}
if (!el && text) {
  const needle = text.toLowerCase();
  const candidates = Array.from(document.querySelectorAll('*'));
  el = candidates.find(node => ((node.innerText || '') + ' ' + (node.getAttribute('aria-label') || '')).toLowerCase().includes(needle));
  count = el ? 1 : 0;
}
if (!el) return {exists: false, visible: false, enabled: false, count: 0};
const style = getComputedStyle(el);
const rect = el.getBoundingClientRect();
const visible = style.visibility !== 'hidden' && style.display !== 'none' && style.opacity !== '0' && rect.width > 0 && rect.height > 0;
const enabled = !el.disabled && el.getAttribute('aria-disabled') !== 'true';
return {exists: true, visible, enabled, count, tag: el.localName, text: (el.innerText || '').trim().slice(0, 100)};
})()`, jsString(selector), jsString(text)), true)
	if err != nil {
		return map[string]any{"exists": false, "visible": false, "enabled": false, "count": 0, "error": err.Error()}, nil
	}
	if info, ok := value.(map[string]any); ok && info != nil {
		return info, nil
	}
	return map[string]any{"exists": false, "visible": false, "enabled": false, "count": 0}, nil
}
