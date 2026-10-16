package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// occlusion: 人眼意义上的"看得见/点得着"检测。
// snapshot/a11y 树只能告诉 agent 元素存在，z-index 遮挡、半透明遮罩、
// 悬浮工具栏压住点击目标这类问题必须用 elementsFromPoint 实测。

type occlusionInput struct {
	Target   string `json:"target,omitempty"`
	Ref      string `json:"ref,omitempty"`
	Selector string `json:"selector,omitempty"`
	CSS      string `json:"css,omitempty"`
	XPath    string `json:"xpath,omitempty"`
	Scan     bool   `json:"scan,omitempty"`
	Within   string `json:"within,omitempty"`
	NoScroll bool   `json:"no_scroll,omitempty"`
	Sample   int    `json:"sample,omitempty"`
	TargetID string `json:"target_id,omitempty"`
	TabID    string `json:"tab_id,omitempty"`
}

func registerOcclusionTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name: "occlusion",
		Description: "Check whether an element is actually visible/clickable — samples its rect with elementFromPoint and reports what covers it (z-index overlays, translucent layers, floating toolbars). " +
			"Use scan=true to audit all interactive elements on the page for occlusion. Use this for layout/stacking bugs instead of guessing from screenshots.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input occlusionInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("occlusion")
		defer rec.End()
		rec.SetParams(input)

		targetID := resolveFirstNonEmpty(input.TargetID, input.TabID)
		ws, err := requireTargetWS(targetID)
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())

		sample := input.Sample
		if sample <= 0 {
			sample = 3
		}
		if sample > 7 {
			sample = 7
		}
		expr := occlusionExpression(input, sample)
		v, err := cdpEvaluate(ws, expr, true)
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		result, _ := v.(map[string]any)
		if result == nil {
			var decoded map[string]any
			if s, ok := v.(string); ok && json.Unmarshal([]byte(s), &decoded) == nil {
				result = decoded
			}
		}
		if result == nil {
			err := fmt.Errorf("occlusion probe returned unexpected result: %v", v)
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		result["action_id"] = string(rec.ID)
		rec.SetResult(result)
		return nil, result, nil
	})
}

func occlusionExpression(input occlusionInput, sample int) string {
	if input.Scan {
		within := strings.TrimSpace(input.Within)
		return fmt.Sprintf(`(() => {
%s
%s
const within = %s;
const scope = within ? argusResolve(within) : document;
if (!scope) return {error: 'within selector matched nothing', within};
const cand = scope.querySelectorAll('a,button,input,select,textarea,[role="button"],[role="link"],[onclick],[data-ref]');
const out = [];
const seen = new Set();
for (const el of cand) {
  const r = el.getBoundingClientRect();
  if (r.width < 2 || r.height < 2) continue;
  const x = r.left + r.width / 2, y = r.top + r.height / 2;
  if (x < 0 || y < 0 || x >= innerWidth || y >= innerHeight) continue;
  const stack = document.elementsFromPoint(x, y);
  for (const s of stack) {
    if (s === el || el.contains(s) || s.contains(el)) break;
    const cs = getComputedStyle(s);
    if (cs.pointerEvents === 'none') continue;
    const d = selectorFor(s) || s.localName;
    if (seen.has(el)) break;
    seen.add(el);
    out.push({element: selectorFor(el) || el.localName, text: (el.innerText || '').trim().slice(0, 40), by: d, at: {x: Math.round(x), y: Math.round(y)}});
    break;
  }
  if (out.length >= 50) break;
}
return {scanned: cand.length, occluded: out};
})()`, argusQueryJS, selectorForJS, jsString(within))
	}

	sel := resolveSelector(input.Ref, input.Selector, input.CSS, input.Target, input.XPath, "")
	return fmt.Sprintf(`(() => {
%s
%s
const sel = %s;
const el = argusResolve(sel);
if (!el) return {found: false, selector: sel};
if (!%v) el.scrollIntoView({block: 'center', inline: 'center', behavior: 'instant'});
const r = el.getBoundingClientRect();
const N = %d;
const pts = [];
for (let i = 0; i < N; i++) for (let j = 0; j < N; j++) {
  const x = r.left + r.width * (i + 0.5) / N, y = r.top + r.height * (j + 0.5) / N;
  if (x >= 0 && y >= 0 && x < innerWidth && y < innerHeight) pts.push([x, y]);
}
const occluders = {};
let visible = 0, clickBlocked = 0;
for (const [x, y] of pts) {
  const stack = document.elementsFromPoint(x, y);
  let blocked = null, peBlocked = false;
  for (const s of stack) {
    if (s === el || el.contains(s) || s.contains(el)) break;
    if (!blocked) blocked = s;
    const cs = getComputedStyle(s);
    if (cs.pointerEvents === 'none') continue;
    peBlocked = true;
    break;
  }
  if (!blocked) { visible++; continue; }
  if (peBlocked) clickBlocked++;
  const d = selectorFor(blocked) || blocked.localName;
  const cs = getComputedStyle(blocked);
  const k = d + '|' + cs.pointerEvents + '|' + cs.zIndex;
  occluders[k] = (occluders[k] || 0) + 1;
}
const list = Object.entries(occluders).map(([k, n]) => {
  const [selector, pointer_events, z_index] = k.split('|');
  return {selector, pointer_events, z_index, points: n};
});
const total = pts.length;
let verdict = 'visible';
if (total === 0) verdict = 'off_screen';
else if (visible === 0) verdict = 'fully_occluded';
else if (clickBlocked > 0) verdict = 'partially_occluded';
return {
  found: true, verdict,
  coverage: total ? visible / total : 0,
  points_total: total, points_visible: visible, points_click_blocked: clickBlocked,
  rect: {x: Math.round(r.x), y: Math.round(r.y), width: Math.round(r.width), height: Math.round(r.height)},
  occluders: list
};
})()`, argusQueryJS, selectorForJS, jsString(sel), input.NoScroll, sample)
}
