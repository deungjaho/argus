package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// autoWaitOptions controls auto-wait behavior for interaction tools.
type autoWaitOptions struct {
	TimeoutMs int  // max time to wait for element; 0 = use default
	PollMs    int  // poll interval; 0 = use default
	NoWait    bool // skip auto-wait entirely
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
	if opts.NoWait {
		return resolveElementPoint(ws, selector, text)
	}
	deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
	pollDuration := time.Duration(poll) * time.Millisecond
	for {
		select {
		case <-ctx.Done():
			return 0, 0, nil, fmt.Errorf("auto-wait cancelled: %w", ctx.Err())
		default:
		}
		x, y, info, err := resolveElementPointWithState(ws, selector, text)
		if err == nil && info != nil {
			if visible, _ := info["visible"].(bool); visible {
				if enabled, ok := info["enabled"].(bool); !ok || enabled {
					return x, y, info, nil
				}
			}
		}
		if time.Now().After(deadline) {
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

// resolveElementPoint resolves an element to viewport coordinates without waiting.
func resolveElementPoint(ws, selector, text string) (float64, float64, map[string]any, error) {
	x, y, info, err := resolveElementPointWithState(ws, selector, text)
	if err != nil {
		return 0, 0, nil, err
	}
	if info == nil {
		return 0, 0, nil, fmt.Errorf("element not found")
	}
	return x, y, info, nil
}

// resolveElementPointWithState resolves an element and returns its state (visible, enabled, rect).
func resolveElementPointWithState(ws, selector, text string) (float64, float64, map[string]any, error) {
	selector = strings.TrimSpace(selector)
	text = strings.TrimSpace(text)
	if selector == "" && text == "" {
		return 0, 0, nil, fmt.Errorf("no selector or text provided")
	}
	value, err := cdpEvaluate(ws, elementStateExpression(selector, text), true)
	if err != nil {
		return 0, 0, nil, err
	}
	info, ok := value.(map[string]any)
	if !ok || info == nil {
		return 0, 0, nil, fmt.Errorf("element not found")
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

func elementStateExpression(selector, text string) string {
	return fmt.Sprintf(`(() => {
const selector = %s;
const text = %s;
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
if (selector) el = document.querySelector(selector);
if (!el && text) {
  const needle = text.toLowerCase();
  const candidates = Array.from(document.querySelectorAll('a,button,input,textarea,select,summary,[role],[tabindex],[aria-label],[data-testid],[data-test-id],label,span,div'));
  el = candidates.find(node => {
    const nodeText = ((node.innerText || '') + ' ' + (node.getAttribute('aria-label') || '') + ' ' + (node.getAttribute('placeholder') || '') + ' ' + (node.getAttribute('title') || '')).toLowerCase();
    return nodeText.includes(needle);
  });
}
if (!el) return {found: false, visible: false, enabled: false};
el.scrollIntoView({block: 'center', inline: 'center', behavior: 'instant'});
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
})()`, jsString(selector), jsString(text))
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
const el = document.querySelector(%s);
if (!el) return {found: false};
const style = getComputedStyle(el);
const rect = el.getBoundingClientRect();
const vis = style.visibility !== 'hidden' && style.display !== 'none' && rect.width > 0 && rect.height > 0;
return {found: true, visible: vis, tag: el.localName, text: (el.innerText || '').trim().slice(0, 100)};
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
