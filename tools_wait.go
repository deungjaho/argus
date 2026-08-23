package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type waitForInput struct {
	Text       string `json:"text,omitempty"`
	TextAbsent string `json:"text_absent,omitempty"`
	Selector   string `json:"selector,omitempty"`
	URLMatches string `json:"url_matches,omitempty"`
	WaitUntil  string `json:"wait_until,omitempty"` // load | domcontentloaded | networkidle
	TimeoutMs  int    `json:"timeout_ms,omitempty"`
	PollMs     int    `json:"poll_ms,omitempty"`
}

type tryUntilInput struct {
	Tool        string         `json:"tool"`
	Params      map[string]any `json:"params"`
	Condition   string         `json:"condition,omitempty"`
	TimeoutMs   int            `json:"timeout_ms,omitempty"`
	PollMs      int            `json:"poll_ms,omitempty"`
	MaxAttempts int            `json:"max_attempts,omitempty"`
}

func registerWaitTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "wait_for",
		Description: "Wait for a page condition or page load. For element conditions, provide text, text_absent, selector, or url_matches (polls until met or timeout, default 10s). For page load, provide wait_until: 'load' (full page load, default), 'domcontentloaded' (DOM ready, faster), or 'networkidle' (no network requests for 500ms after readyState=complete). Default timeout for page load is 15s. Responds to context cancellation.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input waitForInput) (*mcp.CallToolResult, any, error) {
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}

		// --- Page load waiting (wait_until) ---
		if waitUntil := strings.TrimSpace(input.WaitUntil); waitUntil != "" {
			if waitUntil != "load" && waitUntil != "domcontentloaded" && waitUntil != "networkidle" {
				return nil, nil, fmt.Errorf("wait_until must be 'load', 'domcontentloaded', or 'networkidle'")
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
		}

		// --- Element condition waiting ---
		timeout := time.Duration(input.TimeoutMs) * time.Millisecond
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		poll := time.Duration(input.PollMs) * time.Millisecond
		if poll <= 0 {
			poll = 200 * time.Millisecond
		}
		deadline := time.Now().Add(timeout)
		condition := waitForExpression(input)
		if condition == "" {
			return nil, nil, fmt.Errorf("wait_for requires text, text_absent, selector, url_matches, or wait_until")
		}
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return nil, map[string]any{"satisfied": false, "cancelled": true}, ctx.Err()
			default:
			}
			value, err := cdpEvaluate(ws, condition, true)
			if err == nil {
				if result, ok := value.(map[string]any); ok && result != nil {
					if satisfied, ok := result["satisfied"].(bool); ok && satisfied {
						return nil, result, nil
					}
				}
			}
			select {
			case <-ctx.Done():
				return nil, map[string]any{"satisfied": false, "cancelled": true}, ctx.Err()
			case <-time.After(poll):
			}
		}
		return nil, map[string]any{"satisfied": false, "timed_out": true, "timeout_ms": timeout.Milliseconds()}, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "try_until",
		Description: "Poll a JS condition on the page until it returns truthy or timeout. NOTE: this tool does NOT call other tools — the tool/params fields are reserved for future use. Use this to wait for a condition before calling click/type/fill separately. The condition is a JS expression evaluated on the page.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input tryUntilInput) (*mcp.CallToolResult, any, error) {
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		timeout := time.Duration(input.TimeoutMs) * time.Millisecond
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
		poll := time.Duration(input.PollMs) * time.Millisecond
		if poll <= 0 {
			poll = 500 * time.Millisecond
		}
		condition := strings.TrimSpace(input.Condition)
		if condition == "" {
			return nil, nil, fmt.Errorf("try_until requires a condition (JS expression that returns truthy when satisfied)")
		}
		deadline := time.Now().Add(timeout)
		attempts := 0
		maxAttempts := input.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = 30
		}
		var lastResult any
		for time.Now().Before(deadline) && attempts < maxAttempts {
			select {
			case <-ctx.Done():
				return nil, map[string]any{"satisfied": false, "cancelled": true, "attempts": attempts}, ctx.Err()
			default:
			}
			attempts++
			value, err := cdpEvaluate(ws, fmt.Sprintf(`(() => { try { return !!(%s); } catch(e) { return false; } })()`, condition), true)
			if err == nil {
				if satisfied, ok := value.(bool); ok && satisfied {
					return nil, map[string]any{"satisfied": true, "attempts": attempts, "last_result": lastResult}, nil
				}
				lastResult = value
			}
			select {
			case <-ctx.Done():
				return nil, map[string]any{"satisfied": false, "cancelled": true, "attempts": attempts}, ctx.Err()
			case <-time.After(poll):
			}
		}
		return nil, map[string]any{"satisfied": false, "attempts": attempts, "timed_out": time.Now().After(deadline), "last_result": lastResult}, nil
	})
}

func waitForExpression(input waitForInput) string {
	parts := []string{}
	if text := strings.TrimSpace(input.Text); text != "" {
		parts = append(parts, fmt.Sprintf(`text_present: (document.body && document.body.innerText || '').includes(%s)`, jsString(text)))
	}
	if text := strings.TrimSpace(input.TextAbsent); text != "" {
		parts = append(parts, fmt.Sprintf(`text_absent: !(document.body && document.body.innerText || '').includes(%s)`, jsString(text)))
	}
	if selector := strings.TrimSpace(input.Selector); selector != "" {
		parts = append(parts, fmt.Sprintf(`selector_present: !!document.querySelector(%s)`, jsString(selector)))
	}
	if urlPattern := strings.TrimSpace(input.URLMatches); urlPattern != "" {
		parts = append(parts, fmt.Sprintf(`url_matches: location.href.includes(%s)`, jsString(urlPattern)))
	}
	if len(parts) == 0 {
		return ""
	}
	expr := "(() => { const checks = { " + strings.Join(parts, ", ") + " };"
	expr += " const satisfied = Object.values(checks).every(v => v === true);"
	expr += " return Object.assign({satisfied}, checks); })()"
	return expr
}
