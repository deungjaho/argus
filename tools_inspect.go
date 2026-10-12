package main

import (
	"fmt"
)

// checkElementExists is a shared helper used by wait_for for instant existence checks (timeout_ms=0).
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
