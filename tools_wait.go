package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// waitForInput represents parameters for the unified canonical wait tool.
type waitForInput struct {
	Selector         string `json:"selector,omitempty"`    // CSS selector to wait for
	Text             string `json:"text,omitempty"`        // Text content to wait for
	TextAbsent       string `json:"text_absent,omitempty"` // Text content to wait to disappear
	Condition        string `json:"condition,omitempty"`   // JS expression returning truthy (absorbs try_until)
	Script           string `json:"script,omitempty"`      // alias for condition
	Expression       string `json:"expression,omitempty"`  // alias for condition
	Code             string `json:"code,omitempty"`        // alias for condition
	URLMatches       string `json:"url_matches,omitempty"` // URL pattern to wait for
	WaitUntil        string `json:"wait_until,omitempty"`  // "load" | "domcontentloaded" | "networkidle"
	TimeoutMs        int    `json:"timeout_ms,omitempty"`  // Timeout in ms (0 = instant exists check!)
	Timeout          int    `json:"timeout,omitempty"`     // alias for timeout_ms
	TimeMs           int    `json:"time_ms,omitempty"`     // sleep duration
	SleepMs          int    `json:"sleep_ms,omitempty"`    // sleep duration
	DurationMs       int    `json:"duration_ms,omitempty"` // sleep duration
	PollMs           int    `json:"poll_ms,omitempty"`     // Polling interval in ms (default: 200ms)
	Ref              string `json:"ref,omitempty"`         // CSS ref alias
	Target           string `json:"target,omitempty"`
	CSS              string `json:"css,omitempty"`
	XPath            string `json:"xpath,omitempty"`
	Query            string `json:"query,omitempty"`
	URL              string `json:"url,omitempty"`
	Delay            int    `json:"delay,omitempty"`
	DelayMs          int    `json:"delay_ms,omitempty"`
	MaxAttempts      int    `json:"max_attempts,omitempty"`
	TimeoutSpecified bool   `json:"-"`
}

func (w *waitForInput) UnmarshalJSON(data []byte) error {
	type alias waitForInput
	var raw struct {
		alias
		RawTimeoutMs *int `json:"timeout_ms"`
		RawTimeout   *int `json:"timeout"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*w = waitForInput(raw.alias)
	if raw.RawTimeoutMs != nil {
		w.TimeoutMs = *raw.RawTimeoutMs
		w.Timeout = *raw.RawTimeoutMs
		w.TimeoutSpecified = true
	} else if raw.RawTimeout != nil {
		w.Timeout = *raw.RawTimeout
		w.TimeoutMs = *raw.RawTimeout
		w.TimeoutSpecified = true
	}
	return nil
}

// Backward-compatibility struct aliases
type tryUntilInput = waitForInput
type existsInput = waitForInput

func registerWaitTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "wait_for",
		Description: "Wait for element, text, url, page lifecycle (load/networkidle), or custom JS condition. Set timeout_ms=0 for instant existence check without waiting.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input waitForInput) (*mcp.CallToolResult, any, error) {
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}

		input.Selector = resolveSelector(input.Ref, input.Selector, input.CSS, input.Target, input.XPath, "")
		if input.Selector == "" && input.Text == "" {
			input.Text = strings.TrimSpace(input.Query)
		}
		if input.URLMatches == "" {
			input.URLMatches = strings.TrimSpace(input.URL)
		}

		// 1. Instant Exists Check (timeout_ms == 0 explicitly requested)
		isInstantExists := input.TimeoutSpecified && input.TimeoutMs == 0 && input.Timeout == 0
		if isInstantExists && (input.Selector != "" || input.Text != "") {
			result, err := checkElementExists(ws, input.Selector, input.Text)
			if err != nil {
				return nil, nil, err
			}
			exists, _ := result["exists"].(bool)
			result["satisfied"] = exists
			return nil, result, nil
		}

		// 2. Fixed Duration Sleep / Delay Fallback
		sleepMs := input.TimeMs
		if sleepMs <= 0 {
			sleepMs = input.SleepMs
		}
		if sleepMs <= 0 {
			sleepMs = input.DurationMs
		}
		if sleepMs <= 0 {
			sleepMs = resolveTimeout(input.DelayMs, input.Delay)
		}
		hasNoConditions := input.Text == "" && input.TextAbsent == "" && input.Selector == "" &&
			input.URLMatches == "" && input.WaitUntil == "" && input.Condition == "" &&
			input.Script == "" && input.Expression == "" && input.Code == ""
		if sleepMs > 0 || (hasNoConditions && input.TimeoutMs > 0) {
			if sleepMs <= 0 && input.TimeoutMs > 0 {
				sleepMs = input.TimeoutMs
			}
			select {
			case <-ctx.Done():
				return nil, map[string]any{"satisfied": false, "cancelled": true}, ctx.Err()
			case <-time.After(time.Duration(sleepMs) * time.Millisecond):
				return nil, map[string]any{"satisfied": true, "slept_ms": sleepMs}, nil
			}
		}

		// 3. Page Load Lifecycle Waiting (wait_until)
		if waitUntil := strings.TrimSpace(input.WaitUntil); waitUntil != "" {
			timeoutDuration := 15 * time.Second
			if input.TimeoutMs > 0 {
				timeoutDuration = time.Duration(input.TimeoutMs) * time.Millisecond
			}
			res, err := waitForPageLoad(ctx, ws, waitUntil, timeoutDuration)
			return nil, res, err
		}

		// 4. Polling Condition Evaluation
		timeoutMsVal := 10000
		if input.TimeoutMs > 0 {
			timeoutMsVal = input.TimeoutMs
		} else if input.Timeout > 0 {
			timeoutMsVal = input.Timeout
		}
		timeout := time.Duration(timeoutMsVal) * time.Millisecond

		poll := time.Duration(input.PollMs) * time.Millisecond
		if poll <= 0 {
			poll = 200 * time.Millisecond
		}
		deadline := time.Now().Add(timeout)

		// Check if condition is a JS expression (absorbs try_until)
		conditionExpr := resolveFirstNonEmpty(input.Condition, input.Script, input.Expression, input.Code)
		if conditionExpr != "" {
			for time.Now().Before(deadline) {
				select {
				case <-ctx.Done():
					return nil, map[string]any{"satisfied": false, "cancelled": true}, ctx.Err()
				default:
				}

				val, err := cdpEvaluate(ws, fmt.Sprintf(`(() => { try { return Boolean(%s); } catch (e) { return false; } })()`, conditionExpr), true)
				if err == nil {
					if truthy, ok := val.(bool); ok && truthy {
						return nil, map[string]any{"satisfied": true, "condition": conditionExpr}, nil
					}
				}

				if overlay, _ := checkDevOverlay(ws); overlay != nil && overlay.Detected {
					return nil, map[string]any{
						"satisfied":   false,
						"aborted":     true,
						"reason":      "dev_overlay_detected",
						"dev_overlay": overlay,
					}, fmt.Errorf("wait_for aborted: dev server error overlay detected (%s): %s", overlay.Framework, overlay.Message)
				}

				select {
				case <-ctx.Done():
					return nil, map[string]any{"satisfied": false, "cancelled": true}, ctx.Err()
				case <-time.After(poll):
				}
			}
			return nil, map[string]any{"satisfied": false, "timed_out": true, "timeout_ms": timeoutMsVal}, nil
		}

		// Standard Selector / Text / URL Condition
		evalCond := waitForExpression(input)
		if evalCond == "" {
			return nil, nil, fmt.Errorf("wait_for requires selector, text, text_absent, url_matches, condition, or wait_until")
		}

		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return nil, map[string]any{"satisfied": false, "cancelled": true}, ctx.Err()
			default:
			}

			value, err := cdpEvaluate(ws, evalCond, true)
			if err == nil {
				if result, ok := value.(map[string]any); ok && result != nil {
					if satisfied, ok := result["satisfied"].(bool); ok && satisfied {
						return nil, result, nil
					}
				}
			}

			if overlay, _ := checkDevOverlay(ws); overlay != nil && overlay.Detected {
				return nil, map[string]any{
					"satisfied":   false,
					"aborted":     true,
					"reason":      "dev_overlay_detected",
					"dev_overlay": overlay,
				}, fmt.Errorf("wait_for aborted: dev server error overlay detected (%s): %s (file: %s:%d)",
					overlay.Framework, overlay.Message, overlay.File, overlay.Line)
			}

			select {
			case <-ctx.Done():
				return nil, map[string]any{"satisfied": false, "cancelled": true}, ctx.Err()
			case <-time.After(poll):
			}
		}
		return nil, map[string]any{"satisfied": false, "timed_out": true, "timeout_ms": timeoutMsVal}, nil
	})
}

func waitForPageLoad(ctx context.Context, ws string, waitUntil string, timeout time.Duration) (map[string]any, error) {
	if waitUntil != "load" && waitUntil != "domcontentloaded" && waitUntil != "networkidle" {
		return nil, fmt.Errorf("wait_until must be 'load', 'domcontentloaded', or 'networkidle'")
	}
	deadline := time.Now().Add(timeout)

	var netSub *eventSubscription
	inFlight := map[string]struct{}{}
	lastActivity := time.Now()
	if waitUntil == "networkidle" {
		if conn, err := getConn(ws); err == nil {
			netSub = conn.subscribeEvents("networkidle", 256, false)
			_ = cdpRequest(ws, "Network.enable", map[string]any{}, nil)
			defer conn.hub.unsubscribe(netSub)
		}
	}
	drainNet := func() {
		if netSub == nil {
			return
		}
		for {
			select {
			case ev, ok := <-netSub.ch:
				if !ok || ev == nil {
					return
				}
				var p struct {
					RequestID string `json:"requestId"`
				}
				_ = json.Unmarshal(ev.Params, &p)
				switch ev.Method {
				case "Network.requestWillBeSent":
					if p.RequestID != "" {
						inFlight[p.RequestID] = struct{}{}
					}
					lastActivity = time.Now()
				case "Network.loadingFinished", "Network.loadingFailed":
					delete(inFlight, p.RequestID)
					lastActivity = time.Now()
				}
			default:
				return
			}
		}
	}

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return map[string]any{"cancelled": true, "wait_until": waitUntil}, ctx.Err()
		default:
		}
		drainNet()
		value, err := cdpEvaluate(ws, `(() => ({
readyState: document.readyState,
url: location.href,
title: document.title
}))()`, true)
		if err == nil {
			if info, ok := value.(map[string]any); ok {
				readyState, _ := info["readyState"].(string)
				switch waitUntil {
				case "domcontentloaded":
					if readyState == "interactive" || readyState == "complete" {
						return info, nil
					}
				case "load":
					if readyState == "complete" {
						return info, nil
					}
				case "networkidle":
					if readyState == "complete" && len(inFlight) == 0 && time.Since(lastActivity) >= 500*time.Millisecond {
						return info, nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return map[string]any{"cancelled": true, "wait_until": waitUntil}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return map[string]any{"timed_out": true, "wait_until": waitUntil, "timeout_ms": timeout.Milliseconds()}, nil
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
