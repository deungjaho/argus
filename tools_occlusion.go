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

// occlusionInfoJS 给遮挡者/元素生成带语义的描述：类别（dialog/drawer/
// overlay/fixed/absolute/in-flow）、透明度（含 backdrop-filter 磨砂玻璃）、
// rect、堆叠属性。弹窗抽屉这类浮层是层级问题的重灾区，kind 字段让 agent
// 直接看出"盖住我的是个什么"。
const occlusionInfoJS = `
const occInfo = (s) => {
  const cs = getComputedStyle(s);
  const r = s.getBoundingClientRect();
  const cls = ((typeof s.className === 'string' ? s.className : '') || '').toLowerCase();
  const role = (s.getAttribute('role') || '').toLowerCase();
  let kind = 'in-flow';
  if (s.localName === 'dialog' || role === 'dialog' || s.getAttribute('aria-modal') === 'true' || /modal|dialog/.test(cls)) kind = 'dialog';
  else if (/drawer/.test(cls)) kind = 'drawer';
  else if (/popup|popper|popover|tooltip|dropdown|menu|overlay|mask|backdrop|hud|toast|notification|select-dropdown/.test(cls)) kind = 'overlay';
  else if (cs.position === 'fixed' || cs.position === 'sticky') kind = 'fixed';
  else if (cs.position === 'absolute') kind = 'absolute';
  const m = cs.backgroundColor.match(/rgba?\([^)]*,\s*([\d.]+)\s*\)/);
  const bgAlpha = m ? parseFloat(m[1]) : 1;
  const translucent = parseFloat(cs.opacity) < 1 || bgAlpha < 1 || cs.backdropFilter !== 'none';
  return {
    selector: selectorFor(s) || s.localName,
    kind, position: cs.position, z_index: cs.zIndex,
    pointer_events: cs.pointerEvents, translucent,
    rect: {x: Math.round(r.x), y: Math.round(r.y), width: Math.round(r.width), height: Math.round(r.height)}
  };
};`

func occlusionExpression(input occlusionInput, sample int) string {
	if input.Scan {
		within := strings.TrimSpace(input.Within)
		return fmt.Sprintf(`(() => {
%s
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
    if (seen.has(el)) break;
    seen.add(el);
    const info = occInfo(s);
    out.push({element: selectorFor(el) || el.localName, text: (el.innerText || '').trim().slice(0, 40), by: info, at: {x: Math.round(x), y: Math.round(y)}});
    break;
  }
  if (out.length >= 50) break;
}
return {scanned: cand.length, occluded: out};
})()`, argusQueryJS, selectorForJS, occlusionInfoJS, jsString(within))
	}

	sel := resolveSelector(input.Ref, input.Selector, input.CSS, input.Target, input.XPath, "")
	return fmt.Sprintf(`(() => {
%s
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
const occluders = new Map();
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
  const info = occInfo(blocked);
  const k = info.selector + '|' + info.pointer_events;
  const prev = occluders.get(k) || {info, points: 0};
  prev.points++;
  occluders.set(k, prev);
}
const list = [...occluders.values()].map(o => Object.assign({points: o.points}, o.info));
const ecs = getComputedStyle(el);
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
  target_style: {position: ecs.position, z_index: ecs.zIndex, opacity: ecs.opacity},
  occluders: list
};
})()`, argusQueryJS, selectorForJS, occlusionInfoJS, jsString(sel), input.NoScroll, sample)
}
