package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// autoWaitOptions controls auto-wait behavior for interaction tools.
type autoWaitOptions struct {
	TimeoutMs  int    // max time to wait for element; 0 = use default
	PollMs     int    // poll interval; 0 = use default
	NoWait     bool   // skip auto-wait entirely
	ExpectMark string // element must carry this data-argus-m mark (from a stored snapshot); mismatch = stale ref
}

const (
	defaultAutoWaitTimeout = 3000
	defaultAutoWaitPoll    = 100
)

// autoWaitForElement polls the page until the target element is present, visible, and enabled.
// Returns the element's center coordinates and bounding box.
// If noWait is true, returns immediately without waiting (still resolves the element once).
// Responds to ctx cancellation for clean shutdown.
func autoWaitForElement(ctx context.Context, ws, selector, text string, opts autoWaitOptions) (float64, float64, map[string]any, error) {
	timeout := opts.TimeoutMs
	if timeout <= 0 {
		timeout = defaultAutoWaitTimeout
	}
	poll := opts.PollMs
	if poll <= 0 {
		poll = defaultAutoWaitPoll
	}
	if selector != "" {
		if a11yNode, isA11y, err := resolveA11yNode(ws, selector); err != nil {
			return 0, 0, nil, err
		} else if isA11y {
			return autoWaitForA11yElement(ctx, ws, a11yNode, opts)
		}
	}
	if opts.NoWait {
		return resolveElementPoint(ws, selector, text, opts.ExpectMark)
	}
	deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
	pollDuration := time.Duration(poll) * time.Millisecond
	for {
		select {
		case <-ctx.Done():
			return 0, 0, nil, fmt.Errorf("auto-wait cancelled: %w", ctx.Err())
		default:
		}
		x, y, info, err := resolveElementPointWithState(ws, selector, text, opts.ExpectMark)
		if errors.Is(err, errStaleRef) {
			return 0, 0, info, err // stale is terminal — the mark can't come back
		}
		if err == nil && info != nil {
			if visible, _ := info["visible"].(bool); visible {
				if enabled, ok := info["enabled"].(bool); !ok || enabled {
					return x, y, info, nil
				}
			}
		}
		if time.Now().After(deadline) {
			// Feature F11: Check if failure is due to frontend dev server error overlay
			if overlay, _ := checkDevOverlay(ws); overlay != nil && overlay.Detected {
				return 0, 0, nil, fmt.Errorf("element not found: dev server error overlay detected (%s): %s (file: %s:%d:%d)",
					overlay.Framework, overlay.Message, overlay.File, overlay.Line, overlay.Column)
			}
			if err != nil {
				return 0, 0, nil, fmt.Errorf("element not found within %dms: %w", timeout, err)
			}
			if info != nil {
				visible, _ := info["visible"].(bool)
				enabled, _ := info["enabled"].(bool)
				return 0, 0, info, fmt.Errorf("element found but not ready within %dms (visible=%v enabled=%v)", timeout, visible, enabled)
			}
			return 0, 0, nil, fmt.Errorf("element not ready within %dms", timeout)
		}
		select {
		case <-ctx.Done():
			return 0, 0, nil, fmt.Errorf("auto-wait cancelled: %w", ctx.Err())
		case <-time.After(pollDuration):
		}
	}
}

// autoWaitForA11yElement polls until the a11y element is rendered and has a valid box model.
func autoWaitForA11yElement(ctx context.Context, ws string, node A11yNodeRef, opts autoWaitOptions) (float64, float64, map[string]any, error) {
	if node.BackendDOMNodeID <= 0 {
		return 0, 0, nil, fmt.Errorf("a11y node %q has invalid backendDOMNodeId: %d", node.NodeID, node.BackendDOMNodeID)
	}
	timeout := opts.TimeoutMs
	if timeout <= 0 {
		timeout = defaultAutoWaitTimeout
	}
	poll := opts.PollMs
	if poll <= 0 {
		poll = defaultAutoWaitPoll
	}
	deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
	pollDuration := time.Duration(poll) * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			return 0, 0, nil, fmt.Errorf("auto-wait cancelled: %w", ctx.Err())
		default:
		}

		_ = cdpScrollIntoViewIfNeeded(ws, node.BackendDOMNodeID)
		model, err := cdpGetBoxModel(ws, node.BackendDOMNodeID)
		if err == nil && model != nil && model.Width > 0 && model.Height > 0 {
			if cx, cy, errCenter := model.Center(); errCenter == nil {
				info := map[string]any{
					"found":               true,
					"visible":             true,
					"enabled":             true,
					"x":                   cx,
					"y":                   cy,
					"rect":                model.BoundingBox(),
					"backend_dom_node_id": node.BackendDOMNodeID,
					"ax_node_id":          node.NodeID,
					"role":                node.Role,
					"name":                node.Name,
				}
				return cx, cy, info, nil
			}
		}

		if opts.NoWait || time.Now().After(deadline) {
			if err != nil {
				return 0, 0, nil, fmt.Errorf("a11y element (nodeId=%s, backendDOMNodeId=%d) not ready within %dms: %w", node.NodeID, node.BackendDOMNodeID, timeout, err)
			}
			return 0, 0, nil, fmt.Errorf("a11y element (nodeId=%s, backendDOMNodeId=%d) not ready within %dms", node.NodeID, node.BackendDOMNodeID, timeout)
		}

		select {
		case <-ctx.Done():
			return 0, 0, nil, fmt.Errorf("auto-wait cancelled: %w", ctx.Err())
		case <-time.After(pollDuration):
		}
	}
}

// resolveElementPoint resolves an element to viewport coordinates without waiting.
func resolveElementPoint(ws, selector, text, expectMark string) (float64, float64, map[string]any, error) {
	x, y, info, err := resolveElementPointWithState(ws, selector, text, expectMark)
	if err != nil {
		return 0, 0, info, err
	}
	if info == nil {
		return 0, 0, nil, fmt.Errorf("element not found")
	}
	return x, y, info, nil
}

// resolveElementPointWithState resolves an element and returns its state (visible, enabled, rect).
// When expectMark is non-empty, a resolved element whose data-argus-m
// mark differs is reported stale — it is a different DOM node than the
// one the snapshot's ref pointed at.
func resolveElementPointWithState(ws, selector, text, expectMark string) (float64, float64, map[string]any, error) {
	selector = strings.TrimSpace(selector)
	text = strings.TrimSpace(text)
	if selector == "" && text == "" {
		return 0, 0, nil, fmt.Errorf("no selector or text provided")
	}
	value, err := cdpEvaluate(ws, elementStateExpression(selector, text, expectMark), true)
	if err != nil {
		return 0, 0, nil, err
	}
	info, ok := value.(map[string]any)
	if !ok || info == nil {
		return 0, 0, nil, fmt.Errorf("element not found")
	}
	if stale, _ := info["stale"].(bool); stale {
		return 0, 0, info, fmt.Errorf("%w: element was re-created since the snapshot; call snapshot or look again for a fresh ref", errStaleRef)
	}
	if found, _ := info["found"].(bool); !found {
		return 0, 0, info, fmt.Errorf("element not found")
	}
	x, okX := numberValue(info["x"])
	y, okY := numberValue(info["y"])
	if !okX || !okY {
		return 0, 0, info, fmt.Errorf("element has no valid coordinates")
	}
	return x, y, info, nil
}

func elementStateExpression(selector, text, expectMark string) string {
	return fmt.Sprintf(`(() => {
const selector = %s;
const text = %s;
const expectMark = %s;
`+argusQueryJS+`
const visible = (el) => {
  if (!el) return false;
  const style = getComputedStyle(el);
  const rect = el.getBoundingClientRect();
  return style.visibility !== 'hidden' && style.display !== 'none' && style.opacity !== '0' && rect.width > 0 && rect.height > 0 && rect.bottom >= 0 && rect.right >= 0 && rect.top <= (innerHeight + 100) && rect.left <= (innerWidth + 100);
};
const enabled = (el) => {
  if (!el) return false;
  if (el.disabled) return false;
  if (el.getAttribute('aria-disabled') === 'true') return false;
  return true;
};
let el = null;
if (selector) {
  el = argusResolve(selector);
}
`+rankedTextMatchJS+`
if (!el) return {found: false, visible: false, enabled: false};

// Smart hidden / transparent input delegation (Issue #5)
if (el.tagName === 'INPUT') {
  const style = getComputedStyle(el);
  const isHiddenInput = style.opacity === '0' ||
                        style.visibility === 'hidden' ||
                        style.display === 'none' ||
                        el.type === 'hidden' ||
                        (parseFloat(style.opacity) < 0.1);
  if (isHiddenInput) {
    const wrapper = el.closest('label, .ant-checkbox-wrapper, .ant-radio-wrapper, .el-checkbox, .el-radio, .el-switch, .MuiFormControlLabel-root, .MuiCheckbox-root, .MuiRadio-root, [role="checkbox"], [role="radio"], [role="switch"]') ||
                    (el.labels && el.labels.length > 0 ? el.labels[0] : null) ||
                    (el.id ? document.querySelector('label[for=' + JSON.stringify(el.id) + ']') : null);
    if (wrapper && visible(wrapper)) {
      el = wrapper;
    }
  }
}

if (expectMark && el.getAttribute('data-argus-m') !== expectMark) {
  return {found: false, stale: true, tag: el.localName};
}
el.scrollIntoView({block: 'center', inline: 'center', behavior: 'instant'});
window.__argusHitEl = el;
const rect = el.getBoundingClientRect();
return {
  found: true,
  visible: visible(el),
  enabled: enabled(el),
  tag: el.localName,
  text: (el.innerText || '').trim().slice(0, 100),
  x: Math.max(0, Math.min(innerWidth - 1, rect.left + rect.width / 2)),
  y: Math.max(0, Math.min(innerHeight - 1, rect.top + rect.height / 2)),
  rect: {x: rect.x, y: rect.y, width: rect.width, height: rect.height}
};
})()`, jsString(selector), jsString(text), jsString(expectMark))
}

// autoWaitForSelector polls until a CSS selector matches at least one visible element.
func autoWaitForSelector(ws, selector string, opts autoWaitOptions) (map[string]any, error) {
	timeout := opts.TimeoutMs
	if timeout <= 0 {
		timeout = defaultAutoWaitTimeout
	}
	poll := opts.PollMs
	if poll <= 0 {
		poll = defaultAutoWaitPoll
	}
	deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
	pollDuration := time.Duration(poll) * time.Millisecond
	for {
		value, err := cdpEvaluate(ws, fmt.Sprintf(`(() => {
`+argusQueryJS+`
const el = argusFind(%s);
if (!el) return {found: false};
let targetEl = el;
if (targetEl.tagName === 'INPUT') {
  const style = getComputedStyle(targetEl);
  if (style.opacity === '0' || style.visibility === 'hidden' || style.display === 'none' || targetEl.type === 'hidden' || parseFloat(style.opacity) < 0.1) {
    const wrapper = targetEl.closest('label, .ant-checkbox-wrapper, .ant-radio-wrapper, .el-checkbox, .el-radio, .el-switch, .MuiFormControlLabel-root, .MuiCheckbox-root, .MuiRadio-root, [role="checkbox"], [role="radio"], [role="switch"]') ||
                    (targetEl.labels && targetEl.labels.length > 0 ? targetEl.labels[0] : null) ||
                    (targetEl.id ? document.querySelector('label[for=' + JSON.stringify(targetEl.id) + ']') : null);
    if (wrapper) targetEl = wrapper;
  }
}
const style = getComputedStyle(targetEl);
const rect = targetEl.getBoundingClientRect();
const vis = style.visibility !== 'hidden' && style.display !== 'none' && rect.width > 0 && rect.height > 0;
return {found: true, visible: vis, tag: targetEl.localName, text: (targetEl.innerText || '').trim().slice(0, 100)};
})()`, jsString(selector)), true)
		if err == nil {
			if info, ok := value.(map[string]any); ok && info != nil {
				if found, _ := info["found"].(bool); found {
					if opts.NoWait {
						return info, nil
					}
					if visible, _ := info["visible"].(bool); visible {
						return info, nil
					}
				}
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("selector %s not found/visible within %dms", selector, timeout)
		}
		time.Sleep(pollDuration)
	}
}

// postInputTransitionSettleScript evaluates requestAnimationFrame, animation settling,
// and microtask drainage to allow reactive frameworks (React Concurrent, Vue 3,
// Element Plus, Ant Design) to commit DOM transitions and form validations.
func postInputTransitionSettleScript(settleMs int) string {
	if settleMs <= 0 {
		settleMs = 50
	}
	return fmt.Sprintf(`(() => {
  return new Promise((resolve) => {
    const settleMs = %d;
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        try {
          const animations = (typeof document.getAnimations === 'function')
            ? document.getAnimations().filter(a => a.playState === 'running')
            : [];
          if (animations.length > 0 && typeof Promise.allSettled === 'function') {
            const animPromises = animations.map(a => a.finished ? a.finished.catch(() => {}) : Promise.resolve());
            const timer = new Promise(r => setTimeout(r, settleMs));
            Promise.race([Promise.allSettled(animPromises), timer]).then(() => {
              Promise.resolve().then(resolve);
            });
            return;
          }
        } catch (e) {}
        setTimeout(() => {
          Promise.resolve().then(resolve);
        }, settleMs);
      });
    });
  });
})()`, settleMs)
}

// waitTransitionSettle probes and settles DOM transitions after input operations.
// It is non-fatal: errors (e.g. context cancellation, navigation) are safely ignored.
func waitTransitionSettle(ws string, settleMs int) {
	if settleMs <= 0 {
		settleMs = 50
	}
	timeout := time.Duration(settleMs+300) * time.Millisecond
	_, _ = cdpEvaluateTimeout(ws, postInputTransitionSettleScript(settleMs), true, timeout)
}
