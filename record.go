package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// recorderScript captures real user input as replayable action steps into
// window.__argusTrace. Injected alongside the picker; recording state lives
// in sessionStorage so it survives navigations within the tab.
const recorderScript = `(() => {
if (window.__argusRecorder) return true;
window.__argusRecorder = true;
window.__argusTrace = window.__argusTrace || [];
const esc = (v) => window.CSS && CSS.escape ? CSS.escape(v) : String(v).replace(/[^a-zA-Z0-9_-]/g, '\\$&');
const stableClass = (c) => c && c.length < 40 && !/^(css-|sc-|emotion-|jss|makeStyles|styles_|_css|e[a-z0-9]{4,}$)/.test(c);
const segmentFor = (el) => {
  if (!el || el.nodeType !== 1) return '';
  if (el.id) return '#' + esc(el.id);
  const cls = String(el.className && el.className.baseVal !== undefined ? el.className.baseVal : el.className || '')
    .split(/\s+/).filter(stableClass).slice(0, 2);
  let base = el.localName + (cls.length ? '.' + cls.map(esc).join('.') : '');
  const parent = el.parentElement;
  if (parent) {
    let sibs = [];
    try { sibs = Array.from(parent.children).filter(c => c.matches(base)); } catch (e) {}
    if (sibs.length > 1) base += ':nth-child(' + (Array.from(parent.children).indexOf(el) + 1) + ')';
  }
  return base;
};
const pathFor = (el) => {
  const chain = [];
  let node = el, guard = 0;
  while (node && node.nodeType === 1 && guard++ < 8) {
    const root = node.getRootNode();
    const parts = [segmentFor(node)];
    let parent = node.parentElement;
    let g2 = 0;
    const count = (s) => { try { return root.querySelectorAll(s).length; } catch (e) { return 0; } };
    while (count(parts.join(' > ')) !== 1 && parent && g2++ < 6) {
      parts.unshift(segmentFor(parent));
      parent = parent.parentElement;
    }
    chain.unshift(parts.join(' > '));
    node = root && root.host ? root.host : null;
  }
  return chain.join(' >>> ');
};
const active = () => sessionStorage.getItem('__argusRecording') === '1';
const push = (step) => {
  if (!active()) return;
  step.t = Date.now();
  window.__argusTrace.push(step);
};
if (active() && !window.__argusNavRecorded) {
  window.__argusNavRecorded = true;
  push({action: 'navigate', url: location.href});
}
const deepTarget = (e) => (e.composedPath && e.composedPath()[0]) || e.target;
document.addEventListener('click', (e) => {
  if (window.__argusPicker && window.__argusPicker.armed) return;
  const el = deepTarget(e);
  if (!el || el.nodeType !== 1) return;
  push({action: 'click', selector: pathFor(el), text: (el.innerText || '').trim().slice(0, 40)});
}, true);
document.addEventListener('change', (e) => {
  const el = deepTarget(e);
  if (!el || el.nodeType !== 1) return;
  if (el.tagName === 'SELECT') {
    push({action: 'select', selector: pathFor(el), values: Array.from(el.selectedOptions).map(o => o.value)});
    return;
  }
  if ('value' in el) {
    push({action: 'fill', selector: pathFor(el), value: el.type === 'password' ? '***' : String(el.value)});
  }
}, true);
let scrollTimer = null;
document.addEventListener('scroll', (e) => {
  if (scrollTimer) clearTimeout(scrollTimer);
  scrollTimer = setTimeout(() => {
    const el = e.target === document ? null : deepTarget(e);
    push({action: 'scroll', selector: el ? pathFor(el) : '', dy: window.scrollY});
  }, 300);
}, true);
window.__argusRecordStart = () => {
  sessionStorage.setItem('__argusRecording', '1');
  window.__argusTrace = [{action: 'navigate', url: location.href, t: Date.now()}];
  return true;
};
window.__argusRecordStop = () => {
  sessionStorage.removeItem('__argusRecording');
  const out = window.__argusTrace || [];
  window.__argusTrace = [];
  return out;
};
return true;
})()`

type recordInput struct {
	Action   string `json:"action" jsonschema:"start|stop|status"`
	Target   string `json:"target,omitempty"`
	TargetID string `json:"target_id,omitempty"`
	TabID    string `json:"tab_id,omitempty"`
}

func registerRecordTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "record",
		Description: "Record real user input (clicks, fills, scrolls, navigations) as a trace. stop returns steps replayable via batch_actions; passwords masked.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input recordInput) (*mcp.CallToolResult, any, error) {
		if targetID := resolveFirstNonEmpty(input.TargetID, input.TabID, input.Target); targetID != "" {
			setCurrentTargetID(targetID)
		}
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		if _, err := cdpEvaluate(ws, recorderScript, true); err != nil {
			return nil, nil, err
		}
		switch input.Action {
		case "start":
			if _, err := cdpEvaluate(ws, `window.__argusRecordStart()`, true); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"recording": true}, nil
		case "status":
			val, err := cdpEvaluate(ws, `(() => { return {recording: sessionStorage.getItem('__argusRecording')==='1', steps: (window.__argusTrace||[]).length}; })()`, true)
			return nil, val, err
		case "stop":
			val, err := cdpEvaluate(ws, `window.__argusRecordStop()`, true)
			if err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"recording": false, "trace": val, "replay": "feed steps to batch_actions"}, nil
		default:
			return nil, nil, fmt.Errorf("action must be start, stop, or status")
		}
	})
}
