package main

// argusQueryJS is the shared selector-resolution helper injected into page
// expressions. It adds shadow-DOM support on top of querySelector semantics:
//   - "host >>> inner" segmented selectors pierce shadowRoot boundaries
//   - a plain selector that misses in light DOM falls back to a bounded
//     deep walk through all open shadow roots
const argusQueryJS = `
const argusFindAll = (root, sel) => {
  const segs = sel.split(/\s*>>>\s*/).filter(s => s.length);
  if (!segs.length) return [];
  let scopes = [root];
  let matched = [];
  for (let i = 0; i < segs.length; i++) {
    matched = [];
    const nextScopes = [];
    for (const sc of scopes) {
      let found;
      try { found = sc.querySelectorAll(segs[i]); } catch (e) { return []; }
      for (const el of found) {
        matched.push(el);
        if (i < segs.length - 1 && el.shadowRoot) nextScopes.push(el.shadowRoot);
      }
    }
    if (!matched.length) return [];
    if (i < segs.length - 1) {
      scopes = nextScopes;
      if (!scopes.length) return [];
    }
  }
  return matched;
};
const argusDeepAll = (sel, cap) => {
  let out;
  try { out = Array.from(document.querySelectorAll(sel)); } catch (e) { return []; }
  if (out.length) return out;
  out = [];
  let budget = 20000;
  const walk = (root) => {
    if (out.length >= cap || budget <= 0) return;
    let all;
    try { all = root.querySelectorAll('*'); } catch (e) { return; }
    for (const el of all) {
      if (out.length >= cap || budget <= 0) return;
      budget--;
      if (el.shadowRoot) {
        try { out.push(...Array.from(el.shadowRoot.querySelectorAll(sel))); } catch (e) {}
        walk(el.shadowRoot);
      }
    }
  };
  walk(document);
  return out.slice(0, cap);
};
const argusFind = (sel) => {
  const all = sel.indexOf('>>>') >= 0 ? argusFindAll(document, sel) : argusDeepAll(sel, 8);
  return all.length ? all[0] : null;
};
const argusResolve = (selector) => {
  if (!selector) return null;
  const el = argusFind(selector);
  if (el) return el;
  if (selector.indexOf('>>>') >= 0) return null;
  let valid = true;
  try { document.querySelector(selector); } catch (e) { valid = false; }
  if (valid) return null;
  try {
    return document.getElementById(selector) ||
           document.querySelector('[data-testid=' + JSON.stringify(selector) + ']') ||
           document.querySelector('[data-test-id=' + JSON.stringify(selector) + ']') ||
           document.querySelector('[aria-label=' + JSON.stringify(selector) + ']');
  } catch (e) { return null; }
};
const argusShadowPath = (el) => {
  if (typeof selectorFor !== 'function') return '';
  const segs = [];
  let node = el;
  let guard = 0;
  while (node && guard++ < 10) {
    const root = node.getRootNode ? node.getRootNode() : document;
    if (root && root.host) {
      segs.unshift(selectorFor(node));
      node = root.host;
    } else {
      segs.unshift(selectorFor(node));
      break;
    }
  }
  return segs.join(' >>> ');
};
`
