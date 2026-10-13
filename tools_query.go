package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type queryInput struct {
	Selector string   `json:"selector" jsonschema:"CSS selector. Use 'host >>> inner' segments to explicitly pierce shadow DOM; plain selectors auto-fall back to a shadow-piercing search when light DOM misses."`
	Fields   []string `json:"fields,omitempty" jsonschema:"Fields per match: text, box, classes, attrs, html, value. Default: [text, box]."`
	Limit    int      `json:"limit,omitempty" jsonschema:"Max results returned (default 20, max 200)."`
	Scope    string   `json:"scope,omitempty" jsonschema:"all (default) or visible — restrict to elements visible in viewport."`
	Target   string   `json:"target,omitempty"`
	TargetID string   `json:"target_id,omitempty"`
	TabID    string   `json:"tab_id,omitempty"`
}

func registerQueryTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "query",
		Description: "Structured DOM query: return per-element fields (text, box, classes, attrs, html, value) for selector matches with a reusable ref. Use 'host >>> inner' to pierce shadow DOM; plain selectors auto-pierce shadow roots on light-DOM miss. Prefer this over eval for reading page structure.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input queryInput) (*mcp.CallToolResult, any, error) {
		if targetID := resolveFirstNonEmpty(input.TargetID, input.TabID, input.Target); targetID != "" {
			setCurrentTargetID(targetID)
		}
		selector := strings.TrimSpace(input.Selector)
		if selector == "" {
			return nil, nil, fmt.Errorf("selector is required")
		}
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		limit := input.Limit
		if limit <= 0 {
			limit = 20
		}
		if limit > 200 {
			limit = 200
		}
		fields := input.Fields
		if len(fields) == 0 {
			fields = []string{"text", "box"}
		}
		visibleOnly := strings.EqualFold(input.Scope, "visible")
		val, err := cdpEvaluate(ws, queryExpression(selector, fields, limit, visibleOnly), true)
		return nil, val, err
	})
}

func queryExpression(selector string, fields []string, limit int, visibleOnly bool) string {
	fieldsJSON := "["
	for i, f := range fields {
		if i > 0 {
			fieldsJSON += ","
		}
		fieldsJSON += jsString(strings.ToLower(strings.TrimSpace(f)))
	}
	fieldsJSON += "]"
	return fmt.Sprintf(`(() => {
const sel = %s;
const fields = %s;
const limit = %d;
const visibleOnly = %t;
`+selectorForJS+`
`+markElementsJS+`
`+argusQueryJS+`
const visible = (el) => {
  const style = getComputedStyle(el);
  const rect = el.getBoundingClientRect();
  return style.visibility !== 'hidden' && style.display !== 'none' && rect.width > 0 && rect.height > 0;
};
let els;
if (sel.indexOf('>>>') >= 0) {
  els = argusFindAll(document, sel);
} else {
  els = argusDeepAll(sel, 400);
}
const out = [];
for (const el of els) {
  if (out.length >= limit) break;
  const rect = el.getBoundingClientRect();
  const vis = visible(el);
  if (visibleOnly && !vis) continue;
  const o = {
    index: out.length,
    ref: argusShadowPath(el) || selectorFor(el),
    m: argusMarkFor(el),
    tag: el.localName,
    visible: vis
  };
  const root = el.getRootNode && el.getRootNode();
  if (root && root.host) {
    o.in_shadow = true;
    o.host = root.host.localName + (root.host.id ? '#' + root.host.id : '');
  }
  if (fields.indexOf('text') >= 0) o.text = (el.innerText || '').trim().slice(0, 300);
  if (fields.indexOf('box') >= 0) o.box = {x: rect.x, y: rect.y, width: rect.width, height: rect.height};
  if (fields.indexOf('classes') >= 0) {
    const cn = el.className && el.className.baseVal !== undefined ? el.className.baseVal : (el.className || '');
    o.classes = String(cn).split(/\s+/).filter(Boolean);
  }
  if (fields.indexOf('attrs') >= 0) {
    const a = {};
    for (const at of el.attributes) a[at.name] = at.value;
    o.attrs = a;
  }
  if (fields.indexOf('value') >= 0) o.value = 'value' in el ? String(el.value).slice(0, 500) : '';
  if (fields.indexOf('html') >= 0) o.html = el.outerHTML.slice(0, 2000);
  out.push(o);
}
return {selector: sel, total_matched: els.length, returned: out.length, results: out};
})()`, jsString(selector), fieldsJSON, limit, visibleOnly)
}
