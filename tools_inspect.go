package main

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerInspectTools(server *mcp.Server) {
	// --- exists ---

	type existsInput struct {
		Ref       string `json:"ref,omitempty"`
		Selector  string `json:"selector,omitempty"`
		Text      string `json:"text,omitempty"`
		TimeoutMs int    `json:"timeout_ms,omitempty"`
		Timeout   int    `json:"timeout,omitempty"`
		Target    string `json:"target,omitempty"`
		CSS       string `json:"css,omitempty"`
		XPath     string `json:"xpath,omitempty"`
		Query     string `json:"query,omitempty"`
	}

	addTool(server, &mcp.Tool{
		Name:        "exists",
		Description: "Check if an element exists on the page. Returns {exists, visible, enabled, count} — never throws. With timeout_ms > 0, polls until found or timeout. Default timeout 0 (instant check).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input existsInput) (*mcp.CallToolResult, any, error) {
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		selector := resolveSelector(input.Ref, input.Selector, input.CSS, input.Target, input.XPath, "")
		text := resolveFirstNonEmpty(input.Text, input.Query)
		if selector == "" && text == "" {
			return nil, nil, fmt.Errorf("exists requires ref, selector, or text")
		}
		timeoutMs := resolveTimeout(input.TimeoutMs, input.Timeout)
		timeout := time.Duration(timeoutMs) * time.Millisecond
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
}

func checkElementExists(ws, selector, text string) (map[string]any, error) {
	value, err := cdpEvaluate(ws, fmt.Sprintf(`(() => {
const selector = %s;
const text = %s;
const visible = (el) => {
  const style = getComputedStyle(el);
  const rect = el.getBoundingClientRect();
  return style.visibility !== 'hidden' && style.display !== 'none' && style.opacity !== '0' && rect.width > 0 && rect.height > 0;
};
let el = null;
let count = 0;
if (selector) {
  try {
    const all = document.querySelectorAll(selector);
    count = all.length;
    el = all[0] || null;
  } catch (e) {
    el = document.getElementById(selector) ||
         document.querySelector('[data-testid=' + JSON.stringify(selector) + ']') ||
         document.querySelector('[data-test-id=' + JSON.stringify(selector) + ']') ||
         document.querySelector('[aria-label=' + JSON.stringify(selector) + ']');
  }
}
`+rankedTextMatchJS+`
if (!el) return {exists: false, visible: false, enabled: false, count: 0};
if (count === 0) count = 1;
const vis = visible(el);
const enabled = !el.disabled && el.getAttribute('aria-disabled') !== 'true';
return {exists: true, visible: vis, enabled, count, tag: el.localName, text: (el.innerText || '').trim().slice(0, 100)};
})()`, jsString(selector), jsString(text)), true)
	if err != nil {
		return map[string]any{"exists": false, "visible": false, "enabled": false, "count": 0, "error": err.Error()}, nil
	}
	if info, ok := value.(map[string]any); ok && info != nil {
		return info, nil
	}
	return map[string]any{"exists": false, "visible": false, "enabled": false, "count": 0}, nil
}
