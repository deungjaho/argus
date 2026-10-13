package main

import (
	"os"
	"strings"
	"sync"
)

// pickerScript is injected into every argus-managed tab. Ctrl+Shift+X arms
// the picker; hover highlights; click captures a uniqueness-verified,
// shadow-aware locator into window.__argusLastPick (+ clipboard copy).
const pickerScript = `(() => {
if (window.__argusPicker) return true;
const state = {armed: false};
window.__argusPicker = state;
const esc = (v) => window.CSS && CSS.escape ? CSS.escape(v) : String(v).replace(/[^a-zA-Z0-9_-]/g, '\\$&');
const stableClass = (c) => c && c.length < 40 && !/^(css-|sc-|emotion-|jss|makeStyles|styles_|_css|e[a-z0-9]{4,}$)/.test(c);
const segmentFor = (el) => {
  if (!el || el.nodeType !== 1) return '';
  if (el.id) return '#' + esc(el.id);
  const testId = el.getAttribute('data-testid') || el.getAttribute('data-test-id');
  if (testId) return el.localName + '[data-testid=' + JSON.stringify(testId) + ']';
  const cls = String(el.className && el.className.baseVal !== undefined ? el.className.baseVal : el.className || '')
    .split(/\s+/).filter(stableClass).slice(0, 2);
  const aria = el.getAttribute('aria-label');
  let base = el.localName + (cls.length ? '.' + cls.map(esc).join('.') : '');
  if (aria && !cls.length) base += '[aria-label=' + JSON.stringify(aria) + ']';
  const parent = el.parentElement;
  if (parent) {
    let sibs = [];
    try { sibs = Array.from(parent.children).filter(c => c.matches(base)); } catch (e) {}
    if (sibs.length > 1) base += ':nth-child(' + (Array.from(parent.children).indexOf(el) + 1) + ')';
  }
  return base;
};
const scopeUnique = (el) => {
  const root = el.getRootNode();
  const count = (s) => { try { return root.querySelectorAll(s).length; } catch (e) { return 0; } };
  const parts = [segmentFor(el)];
  let parent = el.parentElement;
  let guard = 0;
  while (count(parts.join(' > ')) !== 1 && parent && guard++ < 8) {
    parts.unshift(segmentFor(parent));
    parent = parent.parentElement;
  }
  return {sel: parts.join(' > '), unique: count(parts.join(' > ')) === 1};
};
const pickSelector = (el) => {
  const chain = [];
  let node = el, unique = true, guard = 0;
  while (node && guard++ < 8) {
    const r = scopeUnique(node);
    chain.unshift(r.sel);
    if (!r.unique) unique = false;
    const root = node.getRootNode();
    node = root && root.host ? root.host : null;
  }
  return {selector: chain.join(' >>> '), unique};
};
const xpathFor = (el) => {
  const parts = [];
  let node = el, guard = 0;
  while (node && node.nodeType === 1 && node !== document.documentElement && guard++ < 10) {
    let idx = 1, sib = node.previousElementSibling;
    while (sib) { if (sib.localName === node.localName) idx++; sib = sib.previousElementSibling; }
    parts.unshift(node.localName + '[' + idx + ']');
    node = node.parentElement;
  }
  return '//' + parts.join('/');
};
let hl = null;
const ensureHl = () => {
  if (hl) return hl;
  hl = document.createElement('div');
  hl.id = '__argus-pick-hl';
  hl.style.cssText = 'position:fixed;pointer-events:none;z-index:2147483647;border:2px solid #6f42c1;background:rgba(111,66,193,.12);transition:all .05s;';
  document.documentElement.appendChild(hl);
  return hl;
};
const clearHl = () => { if (hl && hl.parentNode) hl.parentNode.removeChild(hl); hl = null; };
const deepTarget = (e) => (e.composedPath && e.composedPath()[0]) || e.target;
const capture = (el) => {
  const r = pickSelector(el);
  const rect = el.getBoundingClientRect();
  const text = (el.innerText || '').trim().slice(0, 60);
  const pick = {
    selector: r.selector,
    uniqueness_verified: r.unique,
    fallbacks: {
      xpath: xpathFor(el),
      text: text,
      scope: el.parentElement ? segmentFor(el.parentElement) : ''
    },
    ax_hint: {role: el.getAttribute('role') || '', name: el.getAttribute('aria-label') || text},
    outerHTML: el.outerHTML.slice(0, 500),
    box: [rect.x, rect.y, rect.width, rect.height],
    page: location.href,
    time: Date.now()
  };
  window.__argusLastPick = pick;
  try { navigator.clipboard.writeText(JSON.stringify(pick, null, 1)); } catch (e) {}
  return pick;
};
document.addEventListener('keydown', (e) => {
  if (e.key.toLowerCase() === 'x' && (e.ctrlKey || e.metaKey) && e.shiftKey) {
    state.armed = !state.armed;
    if (!state.armed) clearHl();
    e.preventDefault();
    e.stopPropagation();
  }
}, true);
document.addEventListener('mousemove', (e) => {
  if (!state.armed) return;
  const el = deepTarget(e);
  if (!el || el.nodeType !== 1) return;
  const r = el.getBoundingClientRect();
  const d = ensureHl();
  d.style.left = r.x + 'px'; d.style.top = r.y + 'px';
  d.style.width = r.width + 'px'; d.style.height = r.height + 'px';
}, true);
document.addEventListener('click', (e) => {
  if (!state.armed) return;
  e.preventDefault(); e.stopPropagation(); e.stopImmediatePropagation();
  capture(deepTarget(e));
  state.armed = false;
  clearHl();
}, true);
return true;
})()`

var (
	pickerMu   sync.Mutex
	pickerDone = map[string]bool{}
)

// ensurePickerInjected installs the picker into a target once per process:
// addScriptToEvaluateOnNewDocument for future loads plus an immediate eval
// for the current document. Disabled via ARGUS_PICKER=0.
func ensurePickerInjected(ws, targetID string) {
	if os.Getenv("ARGUS_PICKER") == "0" {
		return
	}
	pickerMu.Lock()
	if pickerDone[targetID] {
		pickerMu.Unlock()
		return
	}
	pickerDone[targetID] = true
	pickerMu.Unlock()
	bundle := pickerScript + ";\n" + recorderScript + ";\n" + ledgerScript
	_ = cdpRequest(ws, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": bundle}, nil)
	_, _ = cdpEvaluate(ws, bundle, true)
}

// pickedRef reports whether a selector string refers to the latest pick.
func pickedRef(selector string) bool {
	s := strings.TrimSpace(selector)
	return s == "picked" || strings.HasPrefix(s, "picked:")
}
