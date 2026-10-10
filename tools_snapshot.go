package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type snapshotInput struct {
	MaxText              int    `json:"max_text,omitempty"`
	MaxElements          int    `json:"max_elements,omitempty"`
	InteractiveOnly      bool   `json:"interactive_only,omitempty"`
	InteractiveOnlyCamel bool   `json:"interactiveOnly,omitempty"`
	Target               string `json:"target,omitempty"`
	TargetID             string `json:"target_id,omitempty"`
	TabID                string `json:"tab_id,omitempty"`
	MaxDepth             int    `json:"max_depth,omitempty"`
	Depth                int    `json:"depth,omitempty"`
	Compact              bool   `json:"compact,omitempty"`
}

type a11ySnapshotInput struct {
	MaxDepth int    `json:"max_depth,omitempty"`
	Depth    int    `json:"depth,omitempty"`
	Target   string `json:"target,omitempty"`
	TargetID string `json:"target_id,omitempty"`
	TabID    string `json:"tab_id,omitempty"`
}

type findInput struct {
	Text     string `json:"text,omitempty"`
	Regex    string `json:"regex,omitempty"`
	Query    string `json:"query,omitempty"`
	Pattern  string `json:"pattern,omitempty"`
	Selector string `json:"selector,omitempty"`
	Ref      string `json:"ref,omitempty"`
	CSS      string `json:"css,omitempty"`
	XPath    string `json:"xpath,omitempty"`
	Target   string `json:"target,omitempty"`
}

type lookInput struct {
	MaxElements int    `json:"max_elements,omitempty"`
	MaxSizeKB   int    `json:"max_size_kb,omitempty"`
	Target      string `json:"target,omitempty"`
	TargetID    string `json:"target_id,omitempty"`
	TabID       string `json:"tab_id,omitempty"`
	Compact     bool   `json:"compact,omitempty"`
	MaxDepth    int    `json:"max_depth,omitempty"`
	Quality     int    `json:"quality,omitempty"`
	MaxWidth    int    `json:"max_width,omitempty"`
}

func registerSnapshotTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "look",
		Description: "See the page: returns a compressed screenshot plus a compact list of interactive elements with CSS refs and viewport coordinates. One call gives you both the visual layout and the actionable elements. Use this instead of calling snapshot + screenshot separately.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input lookInput) (*mcp.CallToolResult, any, error) {
		if targetID := resolveFirstNonEmpty(input.TargetID, input.TabID, input.Target); targetID != "" {
			setCurrentTargetID(targetID)
		}
		target, err := requireTarget()
		if err != nil {
			return nil, nil, err
		}
		ws := target.WebSocketDebuggerURL

		maxElements := input.MaxElements
		if maxElements <= 0 {
			if input.Compact {
				maxElements = 25
			} else {
				maxElements = 40
			}
		}
		if maxElements > 200 {
			maxElements = 200
		}
		maxSizeKB := input.MaxSizeKB
		if maxSizeKB <= 0 {
			maxSizeKB = 200
		}
		if maxSizeKB > 2000 {
			maxSizeKB = 2000
		}

		// Get compact snapshot (no full text, just summary + elements)
		value, err := cdpEvaluate(ws, lookExpression(maxElements), true)
		if err != nil {
			return nil, nil, err
		}

		// Capture compressed screenshot (encoded in memory — no temp file).
		pngData, err := cdpCaptureScreenshot(ws, false)
		if err != nil {
			return nil, nil, err
		}
		jpegData, err := encodeScreenshotJPEG(pngData)
		if err != nil {
			return nil, nil, err
		}

		imageWidth, imageHeight, _ := imageDimensions(jpegData)
		viewport, _ := viewportMetrics(ws)

		// Build text metadata: summary + elements (no full page text)
		meta := map[string]any{
			"url":      target.URL,
			"title":    target.Title,
			"summary":  "",
			"viewport": viewport,
			"image":    map[string]any{"width": imageWidth, "height": imageHeight},
			"bytes":    len(jpegData),
			"elements": []any{},
		}

		if info, ok := value.(map[string]any); ok {
			if s, ok := info["summary"].(string); ok {
				meta["summary"] = s
			}
			if els, ok := info["interactive"].([]any); ok {
				if snap := storeSnapshot(ws, target.URL, els); snap != nil {
					meta["snapshot_id"] = snap.ID
				}
				meta["elements"] = els
				if trunc, _ := info["elements_truncated"].(bool); trunc {
					attachTruncation(meta, TruncationMeta{Truncated: true, Reason: "max_elements"})
				}
			}
		}

		maxBytes := maxSizeKB * 1024
		if len(jpegData) > maxBytes {
			attachTruncation(meta, TruncationMeta{
				Truncated:  true,
				TotalCount: len(jpegData),
				ShownCount: 0,
				Reason:     fmt.Sprintf("screenshot %d bytes exceeds max_size_kb=%d; image omitted", len(jpegData), maxSizeKB),
			})
			meta["max_size_kb"] = maxSizeKB
			metaText, _ := json.MarshalIndent(meta, "", "  ")
			return &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.TextContent{Text: string(metaText)},
			}}, nil, nil
		}

		metaText, _ := json.MarshalIndent(meta, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: string(metaText)},
			&mcp.ImageContent{Data: jpegData, MIMEType: "image/jpeg"},
		}}, nil, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "snapshot",
		Description: "Return page text and visible interactive DOM elements with CSS refs that can be used by click/type tools. Set interactive_only=true to omit page text and return only interactive elements. Coordinates are viewport CSS pixels. Output is bounded (max_text default 4000, max_elements default 80) with truncation metadata.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input snapshotInput) (*mcp.CallToolResult, any, error) {
		if targetID := resolveFirstNonEmpty(input.TargetID, input.TabID, input.Target); targetID != "" {
			setCurrentTargetID(targetID)
		}
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		interactiveOnly := input.InteractiveOnly || input.InteractiveOnlyCamel
		maxText := input.MaxText
		if interactiveOnly {
			maxText = 0
		} else {
			if maxText <= 0 {
				if input.Compact {
					maxText = 1500
				} else {
					maxText = 4000
				}
			}
			if maxText > 20000 {
				maxText = 20000
			}
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
		if interactiveOnly {
			if info, ok := value.(map[string]any); ok && info != nil {
				info["text"] = ""
				delete(info, "text_truncated")
			}
		}
		// Attach truncation metadata so agents know the text/elements were
		// bounded. The JS expression reports truncated flags when the limits
		// were hit.
		if info, ok := value.(map[string]any); ok && info != nil {
			if els, ok := info["interactive"].([]any); ok {
				url, _ := info["url"].(string)
				if snap := storeSnapshot(ws, url, els); snap != nil {
					info["snapshot_id"] = snap.ID
				}
			}
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
		if targetID := resolveFirstNonEmpty(input.TargetID, input.TabID, input.Target); targetID != "" {
			setCurrentTargetID(targetID)
		}
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
		maxDepth := input.MaxDepth
		if maxDepth <= 0 {
			maxDepth = input.Depth
		}
		if maxDepth > 0 {
			nodes = filterA11yByDepth(nodes, maxDepth)
		}
		return nil, map[string]any{"nodes": nodes, "count": len(nodes)}, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "inspect_point",
		Description: "Inspect what is under a viewport coordinate or screenshot image coordinate. Returns elementFromPoint details and nearby text/ARIA info.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input inspectPointInput) (*mcp.CallToolResult, any, error) {
		if targetID := resolveFirstNonEmpty(input.TargetID, input.TabID, input.Target); targetID != "" {
			setCurrentTargetID(targetID)
		}
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
		Description: "Search the page for text or a regular expression. Returns matching elements with their CSS refs, text snippets, and bounding boxes. Cheaper than a full snapshot when you only need to locate an element. Output is bounded to 5 matches with truncation metadata.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input findInput) (*mcp.CallToolResult, any, error) {
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		text := resolveFirstNonEmpty(input.Text, input.Query, input.Pattern)
		regex := strings.TrimSpace(input.Regex)
		if text == "" && regex == "" {
			if sel := resolveSelector(input.Ref, input.Selector, input.CSS, input.Target, input.XPath, ""); sel != "" {
				text = sel
			}
		}
		if text == "" && regex == "" {
			return nil, nil, fmt.Errorf("find requires text, regex, or query")
		}
		value, err := cdpEvaluate(ws, findExpression(text, regex), true)
		if err != nil {
			return nil, nil, err
		}
		// Enforce capping and text truncation in Go as defense-in-depth
		if info, ok := value.(map[string]any); ok && info != nil {
			if rawMatches, ok := info["matches"].([]any); ok {
				for _, raw := range rawMatches {
					if mm, ok := raw.(map[string]any); ok {
						if t, ok := mm["text"].(string); ok {
							runes := []rune(t)
							if len(runes) > 100 {
								mm["text"] = string(runes[:100])
							}
						}
					}
				}
				if len(rawMatches) > 5 {
					info["matches"] = rawMatches[:5]
					info["count"] = 5
				}
			}
			// Attach typed TruncationMeta derived from the JS result so agents
			// can detect bounded output without parsing nested fields.
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

const domAutoSettleScript = `(() => {
  return new Promise((resolve) => {
    let timeoutId;
    let observer;
    let rafId;
    const minWait = 110;
    const maxWait = 250;
    const quietWindow = 40;
    const startTime = Date.now();
    let lastMutation = 0;

    const done = () => {
      if (observer) observer.disconnect();
      if (timeoutId) clearTimeout(timeoutId);
      if (rafId) cancelAnimationFrame(rafId);
      resolve(true);
    };

    observer = new MutationObserver(() => {
      lastMutation = Date.now();
    });
    const targetNode = document.body || document.documentElement;
    if (targetNode) {
      observer.observe(targetNode, { childList: true, subtree: true, attributes: true });
    }

    const check = () => {
      const now = Date.now();
      const elapsed = now - startTime;
      if (elapsed >= maxWait) {
        done();
        return;
      }
      if (elapsed >= minWait) {
        if (lastMutation === 0 || (now - lastMutation >= quietWindow)) {
          done();
          return;
        }
      }
      rafId = requestAnimationFrame(check);
    };

    timeoutId = setTimeout(done, maxWait + 20);
    rafId = requestAnimationFrame(check);
  });
})()`

// miniSnapshot captures a compact page snapshot (summary + interactive
// elements, max 20) and returns {"summary": "...", "elements": [...]}.
// It is non-fatal: any failure returns nil so the calling operation's
// result is never blocked.
func miniSnapshot(ws string) map[string]any {
	// Auto-settle DOM before snapshot so async portals, animations, and transitions are captured.
	_, _ = cdpEvaluateTimeout(ws, domAutoSettleScript, true, 500*time.Millisecond)
	value, err := cdpEvaluate(ws, lookExpression(20), true)
	if err != nil {
		return nil
	}
	info, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	summary, _ := info["summary"].(string)
	elements, _ := info["interactive"].([]any)
	if elements == nil {
		elements = []any{}
	}
	for _, raw := range elements {
		if m, ok := raw.(map[string]any); ok {
			delete(m, "m") // internal stale-check mark, not agent-facing
		}
	}
	return map[string]any{
		"summary":  summary,
		"elements": elements,
	}
}

type inspectPointInput struct {
	X              *float64 `json:"x,omitempty"`
	Y              *float64 `json:"y,omitempty"`
	ImageX         *float64 `json:"image_x,omitempty"`
	ImageY         *float64 `json:"image_y,omitempty"`
	ImageWidth     *float64 `json:"image_width,omitempty"`
	ImageHeight    *float64 `json:"image_height,omitempty"`
	ScreenshotPath string   `json:"screenshot_path,omitempty"`
	Target         string   `json:"target,omitempty"`
	TargetID       string   `json:"target_id,omitempty"`
	TabID          string   `json:"tab_id,omitempty"`
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

// filterA11yByDepth keeps nodes whose depth (distance from the root via
// parentId / childIds chains) is <= maxDepth. Root nodes have depth 0.
func filterA11yByDepth(nodes []map[string]any, maxDepth int) []map[string]any {
	if maxDepth <= 0 || len(nodes) == 0 {
		return nodes
	}
	nodeID := func(n map[string]any) string {
		v, _ := n["nodeId"].(string)
		return v
	}
	parentOf := map[string]string{}
	for _, n := range nodes {
		if p, ok := n["parentId"].(string); ok && p != "" {
			parentOf[nodeID(n)] = p
		}
	}
	if len(parentOf) == 0 {
		// Older protocol versions: invert childIds.
		for _, n := range nodes {
			kids, _ := n["childIds"].([]any)
			for _, k := range kids {
				if ks, ok := k.(string); ok {
					parentOf[ks] = nodeID(n)
				}
			}
		}
	}
	depthMemo := map[string]int{}
	var depth func(id string) int
	depth = func(id string) int {
		if d, ok := depthMemo[id]; ok {
			return d
		}
		depthMemo[id] = 0 // cycle guard: mark seen before recursing
		d := 0
		if p, ok := parentOf[id]; ok && p != "" && p != id {
			d = depth(p) + 1
		}
		depthMemo[id] = d
		return d
	}
	kept := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		if depth(nodeID(n)) <= maxDepth {
			kept = append(kept, n)
		}
	}
	return kept
}

const selectorForJS = `
const cssEscape = (value) => window.CSS && CSS.escape ? CSS.escape(value) : String(value).replace(/[^a-zA-Z0-9_-]/g, '\\$&');
const selectorFor = (el) => {
  if (!el || el.nodeType !== 1) return '';
  if (el.id) return '#' + cssEscape(el.id);
  const testId = el.getAttribute('data-testid') || el.getAttribute('data-test-id') || el.getAttribute('data-qa') || el.getAttribute('data-cy');
  if (testId) {
    const attr = el.hasAttribute('data-testid') ? 'data-testid' : (el.hasAttribute('data-test-id') ? 'data-test-id' : (el.hasAttribute('data-qa') ? 'data-qa' : 'data-cy'));
    return '[' + attr + '=' + JSON.stringify(testId) + ']';
  }
  const name = el.getAttribute('name');
  if (name) {
    const sel = el.localName + '[name=' + JSON.stringify(name) + ']';
    if (document.querySelectorAll(sel).length === 1) return sel;
  }
  const ariaLabel = el.getAttribute('aria-label');
  if (ariaLabel) {
    const sel = el.localName + '[aria-label=' + JSON.stringify(ariaLabel) + ']';
    if (document.querySelectorAll(sel).length === 1) return sel;
  }
  const placeholder = el.getAttribute('placeholder');
  if (placeholder) {
    const sel = el.localName + '[placeholder=' + JSON.stringify(placeholder) + ']';
    if (document.querySelectorAll(sel).length === 1) return sel;
  }
  const role = el.getAttribute('role');
  if (role) {
    const sel = el.localName + '[role=' + JSON.stringify(role) + ']';
    if (document.querySelectorAll(sel).length === 1) return sel;
  }
  const parts = [];
  let node = el;
  while (node && node.nodeType === 1 && node !== document.documentElement) {
    if (node !== el && node.id) {
      parts.unshift('#' + cssEscape(node.id));
      break;
    }
    let part = node.localName;
    const nodeAria = node.getAttribute('aria-label');
    const nodeName = node.getAttribute('name');
    if (nodeAria) {
      part += '[aria-label=' + JSON.stringify(nodeAria) + ']';
    } else if (nodeName) {
      part += '[name=' + JSON.stringify(nodeName) + ']';
    } else {
      const parent = node.parentElement;
      if (!parent) break;
      const same = Array.from(parent.children).filter((child) => child.localName === node.localName);
      if (same.length > 1) part += ':nth-of-type(' + (same.indexOf(node) + 1) + ')';
    }
    parts.unshift(part);
    const selector = parts.join(' > ');
    if (document.querySelectorAll(selector).length === 1) return selector;
    node = node.parentElement;
  }
  return parts.join(' > ');
};
`

// rankedTextMatchJS assigns `el` to the best-ranked visible element whose
// text/aria-label/placeholder/title contains `text` when the selector did
// not match. Requires `el`, `text`, and `visible` in scope. Ranking:
// exact match > interactive element > shortest text.
const rankedTextMatchJS = `if (!el && text) {
  const needle = text.toLowerCase().trim();
  const candidates = Array.from(document.querySelectorAll('a,button,input,textarea,select,summary,[role],[tabindex],[aria-label],[data-testid],[data-test-id],label,span,div,p'));
  const matches = candidates.filter(node => {
    if (!visible(node)) return false;
    const nodeText = ((node.innerText || '') + ' ' + (node.getAttribute('aria-label') || '') + ' ' + (node.getAttribute('placeholder') || '') + ' ' + (node.getAttribute('title') || '')).toLowerCase();
    return nodeText.includes(needle);
  });
  if (matches.length > 0) {
    matches.sort((a, b) => {
      const isInteractiveA = /^(BUTTON|A|INPUT|TEXTAREA|SELECT|SUMMARY)$/.test(a.tagName) || /^(button|link|menuitem|tab|option)$/i.test(a.getAttribute('role') || '');
      const isInteractiveB = /^(BUTTON|A|INPUT|TEXTAREA|SELECT|SUMMARY)$/.test(b.tagName) || /^(button|link|menuitem|tab|option)$/i.test(b.getAttribute('role') || '');
      const textA = (a.innerText || '').trim().toLowerCase();
      const textB = (b.innerText || '').trim().toLowerCase();
      const exactA = textA === needle;
      const exactB = textB === needle;
      if (exactA && !exactB) return -1;
      if (!exactA && exactB) return 1;
      if (isInteractiveA && !isInteractiveB) return -1;
      if (!isInteractiveA && isInteractiveB) return 1;
      const lenA = (a.innerText || '').length;
      const lenB = (b.innerText || '').length;
      return lenA - lenB;
    });
    el = matches[0];
  }
}`

const markElementsJS = `window.__argusMarkSeq = window.__argusMarkSeq || 0;
const argusMarkFor = (el) => {
  let m = el.getAttribute('data-argus-m');
  if (!m) { m = 'm' + (++window.__argusMarkSeq); el.setAttribute('data-argus-m', m); }
  return m;
};`

func lookExpression(maxElements int) string {
	return fmt.Sprintf(`(() => {
const maxElements = %d;
const visible = (el) => {
  const style = getComputedStyle(el);
  const rect = el.getBoundingClientRect();
  return style.visibility !== 'hidden' && style.display !== 'none' && rect.width > 0 && rect.height > 0 && rect.bottom >= 0 && rect.right >= 0 && rect.top <= innerHeight && rect.left <= innerWidth;
};
`+selectorForJS+`
`+markElementsJS+`
const candidates = Array.from(document.querySelectorAll('a,button,input,textarea,select,summary,canvas,[role],[tabindex],[aria-label],[data-testid],[data-test-id]'));
const interactive = [];
let elementsTruncated = false;
for (const el of candidates) {
  if (!visible(el)) continue;
  if (interactive.length >= maxElements) { elementsTruncated = true; break; }
  const rect = el.getBoundingClientRect();
  interactive.push({
    ref: selectorFor(el),
    m: argusMarkFor(el),
    tag: el.localName,
    role: el.getAttribute('role') || '',
    text: (el.innerText || '').trim().slice(0, 100),
    ariaLabel: el.getAttribute('aria-label') || '',
    placeholder: el.getAttribute('placeholder') || '',
    rect: {x: rect.x, y: rect.y, width: rect.width, height: rect.height}
  });
}
const counts = {};
for (const e of interactive) counts[e.tag] = (counts[e.tag] || 0) + 1;
const parts = [];
for (const tag of Object.keys(counts).sort()) parts.push(counts[tag] + ' ' + tag + (counts[tag] > 1 ? 's' : ''));
const hasForm = document.querySelector('form') !== null;
const summary = document.title + ' — ' + (hasForm ? 'form page' : 'page') + ' with ' + (parts.length ? parts.join(', ') : 'no interactive elements');
return {
  summary: summary,
  elements_truncated: elementsTruncated,
  interactive
};
})()`, maxElements)
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
`+selectorForJS+`
`+markElementsJS+`
const candidates = Array.from(document.querySelectorAll('a,button,input,textarea,select,summary,canvas,[role],[tabindex],[aria-label],[data-testid],[data-test-id]'));
const interactive = [];
let elementsTruncated = false;
for (const el of candidates) {
  if (!visible(el)) continue;
  if (interactive.length >= maxElements) { elementsTruncated = true; break; }
  const rect = el.getBoundingClientRect();
  interactive.push({
    ref: selectorFor(el),
    m: argusMarkFor(el),
    tag: el.localName,
    role: el.getAttribute('role') || '',
    text: (el.innerText || '').trim().slice(0, 200),
    ariaLabel: el.getAttribute('aria-label') || '',
    placeholder: el.getAttribute('placeholder') || '',
    value: 'value' in el ? String(el.value).slice(0, 200) : '',
    rect: {x: rect.x, y: rect.y, width: rect.width, height: rect.height}
  });
}
const fullText = (maxText > 0 && document.body && document.body.innerText ? document.body.innerText : '');
const textTruncated = maxText > 0 && fullText.length > maxText;
const counts = {};
for (const e of interactive) counts[e.tag] = (counts[e.tag] || 0) + 1;
const parts = [];
for (const tag of Object.keys(counts).sort()) parts.push(counts[tag] + ' ' + tag + (counts[tag] > 1 ? 's' : ''));
const hasForm = document.querySelector('form') !== null;
const summary = document.title + ' — ' + (hasForm ? 'form page' : 'page') + ' with ' + (parts.length ? parts.join(', ') : 'no interactive elements');
return {
  url: location.href,
  title: document.title,
  summary: summary,
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
`+selectorForJS+`
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
const maxReturned = 5;
`+selectorForJS+`
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
const interactiveTags = new Set(['button', 'a', 'input', 'select', 'textarea', 'summary']);
const interactiveRoles = new Set(['button', 'link', 'menuitem', 'menuitemcheckbox', 'menuitemradio', 'tab', 'checkbox', 'radio', 'combobox', 'option', 'switch']);
const containerTags = new Set(['html', 'body', 'div', 'section', 'article', 'main', 'aside', 'nav', 'header', 'footer', 'form', 'ul', 'ol', 'table', 'tbody', 'thead', 'tfoot', 'tr']);

const getPriority = (el) => {
  const tag = el.localName.toLowerCase();
  const role = (el.getAttribute('role') || '').toLowerCase();
  if (interactiveTags.has(tag) || interactiveRoles.has(role)) return 0;
  if (containerTags.has(tag)) return 2;
  return 1;
};

const all = Array.from(document.querySelectorAll('*'));
const candidatesP0 = [];
const candidatesP1 = [];
const candidatesP2 = [];
let totalCount = 0;
let pattern = null;
if (regex) {
  try { pattern = new RegExp(regex, 'i'); } catch(e) { return {error: e.message}; }
}
for (const el of all) {
  if (!visible(el)) continue;
  const inner = (el.innerText || '').trim();
  const val = ('value' in el && typeof el.value === 'string') ? el.value.trim() : '';
  const aria = (el.getAttribute('aria-label') || '').trim();
  const placeholder = (el.getAttribute('placeholder') || '').trim();
  const title = (el.getAttribute('title') || '').trim();
  const textParts = [inner, val, aria, placeholder, title].filter(Boolean);
  if (textParts.length === 0) continue;
  const combinedText = textParts.join(' ').trim();
  const ownText = combinedText.slice(0, 300);
  let hit = false;
  if (pattern) hit = pattern.test(ownText);
  else if (text) hit = ownText.toLowerCase().includes(text.toLowerCase());
  if (!hit) continue;
  totalCount++;
  const priority = getPriority(el);
  const rect = el.getBoundingClientRect();
  const item = {
    priority: priority,
    data: {
      ref: selectorFor(el),
      tag: el.localName,
      id: el.id || '',
      className: typeof el.className === 'string' ? el.className.slice(0, 120) : '',
      text: ownText.slice(0, 100),
      xPath: xPathFor(el),
      rect: {x: rect.x, y: rect.y, width: rect.width, height: rect.height}
    }
  };
  if (priority === 0 && candidatesP0.length < maxReturned) {
    candidatesP0.push(item);
  } else if (priority === 1 && candidatesP1.length < maxReturned) {
    candidatesP1.push(item);
  } else if (priority === 2 && candidatesP2.length < maxReturned) {
    candidatesP2.push(item);
  }
}
const candidates = candidatesP0.concat(candidatesP1).concat(candidatesP2);
candidates.sort((a, b) => a.priority - b.priority);
const matches = candidates.slice(0, maxReturned).map((c) => c.data);
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
