package main

import (
	"context"
	"encoding/json"
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
	TimeMs     int    `json:"time_ms,omitempty"`     // sleep/wait duration alias
	SleepMs    int    `json:"sleep_ms,omitempty"`    // sleep/wait duration alias
	DurationMs int    `json:"duration_ms,omitempty"` // sleep/wait duration alias
	PollMs     int    `json:"poll_ms,omitempty"`
	Ref        string `json:"ref,omitempty"`
	Target     string `json:"target,omitempty"`
	CSS        string `json:"css,omitempty"`
	XPath      string `json:"xpath,omitempty"`
	Query      string `json:"query,omitempty"`
	URL        string `json:"url,omitempty"`
	Timeout    int    `json:"timeout,omitempty"`
	Delay      int    `json:"delay,omitempty"`
	DelayMs    int    `json:"delay_ms,omitempty"`
}

type tryUntilInput struct {
	Condition   string `json:"condition,omitempty"`
	Script      string `json:"script,omitempty"`
	Expression  string `json:"expression,omitempty"`
	Code        string `json:"code,omitempty"`
	TimeoutMs   int    `json:"timeout_ms,omitempty"`
	Timeout     int    `json:"timeout,omitempty"`
	PollMs      int    `json:"poll_ms,omitempty"`
	MaxAttempts int    `json:"max_attempts,omitempty"`
}

func registerWaitTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "wait_for",
		Description: "Wait for a page condition (text, selector, url_matches), page load (wait_until: load/networkidle), or sleep (time_ms/delay).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input waitForInput) (*mcp.CallToolResult, any, error) {
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}

		input.TimeoutMs = resolveTimeout(input.TimeoutMs, input.Timeout)
		input.Selector = resolveSelector(input.Ref, input.Selector, input.CSS, input.Target, input.XPath, "")
		if input.Selector == "" && input.Text == "" {
			input.Text = strings.TrimSpace(input.Query)
		}
		if input.URLMatches == "" {
			input.URLMatches = strings.TrimSpace(input.URL)
		}

		// --- Fixed duration sleep / delay fallback ---
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
		// If agent requested a simple pause, or specified timeout_ms with no conditions:
		if sleepMs > 0 || (input.TimeoutMs > 0 && input.Text == "" && input.TextAbsent == "" && input.Selector == "" && input.URLMatches == "" && input.WaitUntil == "") {
			if sleepMs <= 0 {
				sleepMs = input.TimeoutMs
			}
			select {
			case <-ctx.Done():
				return nil, map[string]any{"satisfied": false, "cancelled": true}, ctx.Err()
			case <-time.After(time.Duration(sleepMs) * time.Millisecond):
				return nil, map[string]any{"satisfied": true, "slept_ms": sleepMs}, nil
			}
		}

		// --- Page load waiting (wait_until) ---
		if waitUntil := strings.TrimSpace(input.WaitUntil); waitUntil != "" {
			timeout := time.Duration(input.TimeoutMs) * time.Millisecond
			if timeout <= 0 {
				timeout = 15 * time.Second
			}
			res, err := waitForPageLoad(ctx, ws, waitUntil, timeout)
			return nil, res, err
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
		Description: "Poll a JS condition on the page until it returns truthy or timeout. Use this to wait for a condition before calling click/type/fill separately. The condition is a JS expression evaluated on the page.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input tryUntilInput) (*mcp.CallToolResult, any, error) {
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		input.TimeoutMs = resolveTimeout(input.TimeoutMs, input.Timeout)
		timeout := time.Duration(input.TimeoutMs) * time.Millisecond
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
		poll := time.Duration(input.PollMs) * time.Millisecond
		if poll <= 0 {
			poll = 500 * time.Millisecond
		}
		condition := resolveFirstNonEmpty(input.Condition, input.Script, input.Expression, input.Code)
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
