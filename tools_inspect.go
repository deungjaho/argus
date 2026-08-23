package main

import (
	"context"
	"fmt"
	"strings"
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
