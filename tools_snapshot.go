package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type snapshotInput struct {
	MaxText     int `json:"max_text,omitempty"`
	MaxElements int `json:"max_elements,omitempty"`
}

type a11ySnapshotInput struct {
	MaxDepth int `json:"max_depth,omitempty"`
}

type findInput struct {
	Text  string `json:"text,omitempty"`
	Regex string `json:"regex,omitempty"`
}

func registerSnapshotTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "snapshot",
		Description: "Return page text and visible interactive DOM elements with CSS refs that can be used by click/type tools. Coordinates are viewport CSS pixels. Output is bounded (max_text default 4000, max_elements default 80) with truncation metadata.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input snapshotInput) (*mcp.CallToolResult, any, error) {
		if activeBackend() == BackendWeChat {
			result, err := automatorSnapshot(input.MaxElements)
			if err != nil {
				return nil, nil, formatWxError("snapshot", err)
			}
			return nil, result, nil
		}
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		maxText := input.MaxText
		if maxText <= 0 {
			maxText = 4000
		}
		if maxText > 20000 {
			maxText = 20000
		}
		maxElements := input.MaxElements
		if maxElements <= 0 {
			maxElements = 80
		}
		if maxElements > 300 {
			maxElements = 300
		}
		value, err := cdpEvaluate(ws, snapshotExpression(maxText, maxElements), true)
		if err != nil {
			return nil, nil, err
		}
		// Attach truncation metadata so agents know the text/elements were
		// bounded. The JS expression reports truncated flags when the limits
		// were hit.
		if info, ok := value.(map[string]any); ok && info != nil {
			textTrunc, _ := info["text_truncated"].(bool)
			elemsTrunc, _ := info["elements_truncated"].(bool)
			reason := ""
			if textTrunc || elemsTrunc {
				if textTrunc {
					reason = "max_text"
				}
				if elemsTrunc {
					if reason != "" {
						reason += ","
					}
					reason += "max_elements"
				}
			}
			attachTruncation(info, TruncationMeta{
				Truncated: textTrunc || elemsTrunc,
				Reason:    reason,
			})
		}
		return nil, value, err
	})

	addTool(server, &mcp.Tool{
		Name:        "a11y_snapshot",
		Description: "Capture the accessibility tree of the current page using CDP Accessibility.getFullAXTree. Returns semantic tree with role, name, value, and ref for each node. More reliable than DOM snapshot for agent-driven interaction.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input a11ySnapshotInput) (*mcp.CallToolResult, any, error) {
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		// Enable Accessibility domain first
		_ = cdpRequest(ws, "Accessibility.enable", map[string]any{}, nil)
		var result struct {
			Nodes []map[string]any `json:"nodes"`
		}
		if err := cdpRequest(ws, "Accessibility.getFullAXTree", map[string]any{}, &result); err != nil {
			return nil, nil, err
		}
		nodes := result.Nodes
		if input.MaxDepth > 0 {
			nodes = filterA11yByDepth(nodes, input.MaxDepth)
		}
		return nil, map[string]any{"nodes": nodes, "count": len(nodes)}, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "inspect_point",
		Description: "Inspect what is under a viewport coordinate or screenshot image coordinate. Returns elementFromPoint details and nearby text/ARIA info.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input inspectPointInput) (*mcp.CallToolResult, any, error) {
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		x, y, err := resolveInspectPoint(ws, input)
		if err != nil {
			return nil, nil, err
		}
		value, err := cdpEvaluate(ws, inspectPointExpression(x, y), true)
		return nil, value, err
	})

	addTool(server, &mcp.Tool{
		Name:        "find",
		Description: "Search the page for text or a regular expression. Returns matching elements with their CSS refs, text snippets, and bounding boxes. Cheaper than a full snapshot when you only need to locate an element. Output is bounded to 50 matches with truncation metadata.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input findInput) (*mcp.CallToolResult, any, error) {
		if activeBackend() == BackendWeChat {
			result, err := automatorFind(input.Text, input.Regex)
			return nil, result, err
		}
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		text := strings.TrimSpace(input.Text)
		regex := strings.TrimSpace(input.Regex)
		if text == "" && regex == "" {
			return nil, nil, fmt.Errorf("find requires text or regex")
		}
		value, err := cdpEvaluate(ws, findExpression(text, regex), true)
		if err != nil {
			return nil, nil, err
		}
		// Attach typed TruncationMeta derived from the JS result so agents
		// can detect bounded output without parsing nested fields.
		if info, ok := value.(map[string]any); ok && info != nil {
			total, _ := numberValue(info["total_count"])
			shown, _ := numberValue(info["count"])
			if total > 0 || shown > 0 {
				attachTruncation(info, newTruncationMeta(int(total), int(shown), "max_matches"))
			}
			// Attach typed AmbiguityInfo so consumers can branch on the
			// structured ambiguity fields without re-parsing the JS result.
			if amb, ok := ambiguityFromMap(info); ok {
				info["ambiguity"] = amb
			}
		}
		return nil, value, err
	})
}

type inspectPointInput struct {
	X              *float64 `json:"x,omitempty"`
	Y              *float64 `json:"y,omitempty"`
	ImageX         *float64 `json:"image_x,omitempty"`
	ImageY         *float64 `json:"image_y,omitempty"`
	ImageWidth     *float64 `json:"image_width,omitempty"`
	ImageHeight    *float64 `json:"image_height,omitempty"`
	ScreenshotPath string   `json:"screenshot_path,omitempty"`
}

func resolveInspectPoint(ws string, input inspectPointInput) (float64, float64, error) {
	if x, y, ok, err := pointFromImageCoordinates(ws, input.ImageX, input.ImageY, input.ImageWidth, input.ImageHeight, input.ScreenshotPath); ok || err != nil {
		return x, y, err
	}
	if input.X != nil && input.Y != nil {
		return *input.X, *input.Y, nil
	}
	return 0, 0, fmt.Errorf("inspect_point requires x/y or image_x/image_y")
}

func filterA11yByDepth(nodes []map[string]any, maxDepth int) []map[string]any {
	// Build child count by depth using parentId chains
	// For simplicity, just return all nodes — CDP a11y tree is already
	// structured. Full depth filtering would require tree traversal.
	return nodes
}

func snapshotExpression(maxText, maxElements int) string {
	return fmt.Sprintf(`(() => {
const maxText = %d;
const maxElements = %d;
const visible = (el) => {
  const style = getComputedStyle(el);
  const rect = el.getBoundingClientRect();
  return style.visibility !== 'hidden' && style.display !== 'none' && rect.width > 0 && rect.height > 0 && rect.bottom >= 0 && rect.right >= 0 && rect.top <= innerHeight && rect.left <= innerWidth;
};
const cssEscape = (value) => window.CSS && CSS.escape ? CSS.escape(value) : String(value).replace(/[^a-zA-Z0-9_-]/g, '\\$&');
const selectorFor = (el) => {
  if (el.id) return '#' + cssEscape(el.id);
  const testId = el.getAttribute('data-testid') || el.getAttribute('data-test-id');
  if (testId) return '[' + (el.hasAttribute('data-testid') ? 'data-testid' : 'data-test-id') + '=' + JSON.stringify(testId) + ']';
  const parts = [];
  let node = el;
  while (node && node.nodeType === 1 && node !== document.documentElement) {
    let part = node.localName;
    const parent = node.parentElement;
    if (!parent) break;
    const same = Array.from(parent.children).filter((child) => child.localName === node.localName);
    if (same.length > 1) part += ':nth-of-type(' + (same.indexOf(node) + 1) + ')';
    parts.unshift(part);
    const selector = parts.join(' > ');
    if (document.querySelectorAll(selector).length === 1) return selector;
    node = parent;
  }
  return parts.join(' > ');
};
const candidates = Array.from(document.querySelectorAll('a,button,input,textarea,select,summary,canvas,[role],[tabindex],[aria-label],[data-testid],[data-test-id]'));
const interactive = [];
let elementsTruncated = false;
for (const el of candidates) {
  if (!visible(el)) continue;
  if (interactive.length >= maxElements) { elementsTruncated = true; break; }
  const rect = el.getBoundingClientRect();
  interactive.push({
    ref: selectorFor(el),
    tag: el.localName,
    role: el.getAttribute('role') || '',
    text: (el.innerText || '').trim().slice(0, 200),
    ariaLabel: el.getAttribute('aria-label') || '',
    placeholder: el.getAttribute('placeholder') || '',
    value: 'value' in el ? String(el.value).slice(0, 200) : '',
    rect: {x: rect.x, y: rect.y, width: rect.width, height: rect.height}
  });
}
const fullText = (document.body && document.body.innerText ? document.body.innerText : '');
const textTruncated = fullText.length > maxText;
return {
  url: location.href,
  title: document.title,
  readyState: document.readyState,
  text: fullText.slice(0, maxText),
  text_truncated: textTruncated,
  elements_truncated: elementsTruncated,
  interactive
};
})()`, maxText, maxElements)
}

func inspectPointExpression(x, y float64) string {
	return fmt.Sprintf(`(() => {
const x = %s;
const y = %s;
const cssEscape = (value) => window.CSS && CSS.escape ? CSS.escape(value) : String(value).replace(/[^a-zA-Z0-9_-]/g, '\\$&');
const selectorFor = (el) => {
  if (!el) return '';
  if (el.id) return '#' + cssEscape(el.id);
  const testId = el.getAttribute('data-testid') || el.getAttribute('data-test-id');
  if (testId) return '[' + (el.hasAttribute('data-testid') ? 'data-testid' : 'data-test-id') + '=' + JSON.stringify(testId) + ']';
  const parts = [];
  let node = el;
  while (node && node.nodeType === 1 && node !== document.documentElement) {
    let part = node.localName;
    const parent = node.parentElement;
    if (!parent) break;
    const same = Array.from(parent.children).filter((child) => child.localName === node.localName);
    if (same.length > 1) part += ':nth-of-type(' + (same.indexOf(node) + 1) + ')';
    parts.unshift(part);
    const selector = parts.join(' > ');
    if (document.querySelectorAll(selector).length === 1) return selector;
    node = parent;
  }
  return parts.join(' > ');
};
const describe = (el) => {
  if (!el) return null;
  const rect = el.getBoundingClientRect();
  return {
    ref: selectorFor(el),
    tag: el.localName,
    id: el.id || '',
    className: typeof el.className === 'string' ? el.className : '',
    role: el.getAttribute('role') || '',
    ariaLabel: el.getAttribute('aria-label') || '',
    placeholder: el.getAttribute('placeholder') || '',
    text: (el.innerText || '').trim().slice(0, 200),
    value: 'value' in el ? String(el.value).slice(0, 500) : '',
    rect: {x: rect.x, y: rect.y, width: rect.width, height: rect.height}
  };
};
const el = document.elementFromPoint(x, y);
const chain = [];
let node = el;
while (node && node.nodeType === 1 && chain.length < 6) {
  chain.push(describe(node));
  node = node.parentElement;
}
return {point: {x, y}, viewport: {width: innerWidth, height: innerHeight, devicePixelRatio: devicePixelRatio || 1}, element: describe(el), ancestors: chain};
})()`, jsNumber(x), jsNumber(y))
}

func findExpression(text, regex string) string {
	return fmt.Sprintf(`(() => {
const text = %s;
const regex = %s;
const maxMatches = 50;
const cssEscape = (value) => window.CSS && CSS.escape ? CSS.escape(value) : String(value).replace(/[^a-zA-Z0-9_-]/g, '\\$&');
const selectorFor = (el) => {
  if (el.id) return '#' + cssEscape(el.id);
  const testId = el.getAttribute('data-testid') || el.getAttribute('data-test-id');
  if (testId) return '[' + (el.hasAttribute('data-testid') ? 'data-testid' : 'data-test-id') + '=' + JSON.stringify(testId) + ']';
  const parts = [];
  let node = el;
  while (node && node.nodeType === 1 && node !== document.documentElement) {
    let part = node.localName;
    const parent = node.parentElement;
    if (!parent) break;
    const same = Array.from(parent.children).filter((child) => child.localName === node.localName);
    if (same.length > 1) part += ':nth-of-type(' + (same.indexOf(node) + 1) + ')';
    parts.unshift(part);
    const selector = parts.join(' > ');
    if (document.querySelectorAll(selector).length === 1) return selector;
    node = parent;
  }
  return parts.join(' > ');
};
const xPathFor = (el) => {
  if (el.id) return '//*[@id=' + JSON.stringify(el.id) + ']';
  const parts = [];
  let node = el;
  while (node && node.nodeType === 1 && node !== document.documentElement) {
    const parent = node.parentElement;
    let part = node.localName;
    if (parent) {
      const same = Array.from(parent.children).filter((child) => child.localName === node.localName);
      if (same.length > 1) part += '[' + (same.indexOf(node) + 1) + ']';
    }
    parts.unshift(part);
    node = parent;
  }
  return '/' + parts.join('/');
};
const visible = (el) => {
  const style = getComputedStyle(el);
  const rect = el.getBoundingClientRect();
  return style.visibility !== 'hidden' && style.display !== 'none' && rect.width > 0 && rect.height > 0;
};
const all = Array.from(document.querySelectorAll('*'));
const matches = [];
let totalCount = 0;
let pattern = null;
if (regex) {
  try { pattern = new RegExp(regex, 'i'); } catch(e) { return {error: e.message}; }
}
for (const el of all) {
  if (!visible(el)) continue;
  const elText = (el.innerText || '').trim();
  if (!elText) continue;
  const ownText = elText.slice(0, 300);
  let hit = false;
  if (pattern) hit = pattern.test(ownText);
  else if (text) hit = ownText.toLowerCase().includes(text.toLowerCase());
  if (!hit) continue;
  totalCount++;
  if (matches.length >= maxMatches) continue;
  const rect = el.getBoundingClientRect();
  matches.push({
    ref: selectorFor(el),
    tag: el.localName,
    id: el.id || '',
    className: typeof el.className === 'string' ? el.className.slice(0, 120) : '',
    text: ownText.slice(0, 200),
    xPath: xPathFor(el),
    rect: {x: rect.x, y: rect.y, width: rect.width, height: rect.height}
  });
}
const ambiguous = totalCount > 1;
const result = {query: text || regex, matches, count: matches.length, total_count: totalCount, ambiguous: ambiguous, match_count: totalCount};
if (totalCount === 0) {
  result.hint = 'No elements matched. Try a different text/regex, or use snapshot to inspect the page.';
} else if (ambiguous) {
  result.hint = 'Selector matched ' + totalCount + ' elements. Refine with id, text, or nth index.';
}
return result;
})()`, jsString(text), jsString(regex))
}
