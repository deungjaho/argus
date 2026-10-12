package main

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// A11yNodeRef represents a cached accessibility tree node reference.
type A11yNodeRef struct {
	NodeID           string `json:"node_id"`
	BackendDOMNodeID int    `json:"backend_dom_node_id"`
	Role             string `json:"role,omitempty"`
	Name             string `json:"name,omitempty"`
}

var (
	a11yMu    sync.RWMutex
	a11yCache = map[string]map[string]A11yNodeRef{} // targetID -> refKey -> A11yNodeRef
)

// storeA11yTree indexes the nodes from an a11y_snapshot call for a target.
func storeA11yTree(targetID string, nodes []map[string]any) {
	if targetID == "" || len(nodes) == 0 {
		return
	}
	a11yMu.Lock()
	defer a11yMu.Unlock()

	targetMap := make(map[string]A11yNodeRef, len(nodes)*3)
	for _, n := range nodes {
		nodeID := extractAXNodeID(n["nodeId"])
		if nodeID == "" {
			continue
		}
		backendID := extractBackendNodeID(n["backendDOMNodeId"])
		role := extractAXValue(n["role"])
		name := extractAXValue(n["name"])
		ref := A11yNodeRef{
			NodeID:           nodeID,
			BackendDOMNodeID: backendID,
			Role:             role,
			Name:             name,
		}

		// Store under ax:nodeId and bare nodeId
		targetMap["ax:"+nodeID] = ref
		targetMap[nodeID] = ref

		// If backendNodeID exists, also index by backend:ID and ax:backend:ID
		if backendID > 0 {
			targetMap[fmt.Sprintf("backend:%d", backendID)] = ref
			targetMap[fmt.Sprintf("ax:backend:%d", backendID)] = ref
		}
	}
	a11yCache[targetID] = targetMap
}

// getA11yNode retrieves a cached a11y node by ref or ID.
func getA11yNode(targetID, ref string) (A11yNodeRef, bool) {
	a11yMu.RLock()
	defer a11yMu.RUnlock()
	targetMap, ok := a11yCache[targetID]
	if !ok {
		return A11yNodeRef{}, false
	}
	node, found := targetMap[ref]
	return node, found
}

// clearA11yCache clears the a11y cache for a target (or all targets if empty).
func clearA11yCache(targetID string) {
	a11yMu.Lock()
	defer a11yMu.Unlock()
	if targetID == "" {
		a11yCache = map[string]map[string]A11yNodeRef{}
	} else {
		delete(a11yCache, targetID)
	}
}

// isLikelyA11yRef checks whether a ref string is intended as an a11y reference.
func isLikelyA11yRef(ref string) bool {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "ax:") || strings.HasPrefix(trimmed, "ax_") {
		return true
	}
	if strings.HasPrefix(trimmed, "backend:") || strings.HasPrefix(trimmed, "ax:backend:") {
		return true
	}
	// Pure integer strings (e.g. "12", "104") are CDP AX tree node IDs
	// and are invalid CSS type/class selectors.
	if _, err := strconv.Atoi(trimmed); err == nil {
		return true
	}
	return false
}

// resolveA11yNode resolves a ref string to an A11yNodeRef.
// Returns (node, isA11y, err).
// If ref is not an a11y reference, returns (A11yNodeRef{}, false, nil).
func resolveA11yNode(ws, ref string) (A11yNodeRef, bool, error) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return A11yNodeRef{}, false, nil
	}

	targetID := targetIDFromWSURL(ws)

	// Direct backend ID reference: backend:123 or ax:backend:123
	if strings.HasPrefix(trimmed, "backend:") || strings.HasPrefix(trimmed, "ax:backend:") {
		idStr := strings.TrimPrefix(trimmed, "ax:backend:")
		idStr = strings.TrimPrefix(idStr, "backend:")
		if bID, err := strconv.Atoi(strings.TrimSpace(idStr)); err == nil && bID > 0 {
			if cached, ok := getA11yNode(targetID, trimmed); ok {
				return cached, true, nil
			}
			return A11yNodeRef{
				NodeID:           idStr,
				BackendDOMNodeID: bID,
			}, true, nil
		}
	}

	// Check cache first
	if cached, ok := getA11yNode(targetID, trimmed); ok {
		if cached.BackendDOMNodeID <= 0 {
			return cached, true, fmt.Errorf("a11y node %q has no associated DOM node (backendDOMNodeId is 0)", trimmed)
		}
		return cached, true, nil
	}

	// If ref has explicit ax: prefix, or stripped prefix
	hasExplicitPrefix := strings.HasPrefix(trimmed, "ax:") || strings.HasPrefix(trimmed, "ax_")
	rawID := trimmed
	if hasExplicitPrefix {
		if strings.HasPrefix(trimmed, "ax:") {
			rawID = strings.TrimPrefix(trimmed, "ax:")
		} else {
			rawID = strings.TrimPrefix(trimmed, "ax_")
		}
		if cached, ok := getA11yNode(targetID, rawID); ok {
			if cached.BackendDOMNodeID <= 0 {
				return cached, true, fmt.Errorf("a11y node %q has no associated DOM node (backendDOMNodeId is 0)", trimmed)
			}
			return cached, true, nil
		}
	}

	// If not in cache, and it has explicit prefix or looks like an a11y node ID,
	// query the AX tree from Chrome to refresh the cache.
	if hasExplicitPrefix || isLikelyA11yRef(trimmed) {
		_ = cdpRequest(ws, "Accessibility.enable", map[string]any{}, nil)
		var result struct {
			Nodes []map[string]any `json:"nodes"`
		}
		if err := cdpRequest(ws, "Accessibility.getFullAXTree", map[string]any{}, &result); err == nil && len(result.Nodes) > 0 {
			storeA11yTree(targetID, result.Nodes)
			if cached, ok := getA11yNode(targetID, trimmed); ok {
				if cached.BackendDOMNodeID <= 0 {
					return cached, true, fmt.Errorf("a11y node %q has no associated DOM node (backendDOMNodeId is 0)", trimmed)
				}
				return cached, true, nil
			}
			if cached, ok := getA11yNode(targetID, rawID); ok {
				if cached.BackendDOMNodeID <= 0 {
					return cached, true, fmt.Errorf("a11y node %q has no associated DOM node (backendDOMNodeId is 0)", trimmed)
				}
				return cached, true, nil
			}
		}
	}

	if hasExplicitPrefix {
		return A11yNodeRef{}, true, fmt.Errorf("a11y node %q not found in accessibility tree", trimmed)
	}

	return A11yNodeRef{}, false, nil
}

// cdpBoxModel represents the result of CDP DOM.getBoxModel.
type cdpBoxModel struct {
	Content []float64 `json:"content"`
	Padding []float64 `json:"padding"`
	Border  []float64 `json:"border"`
	Margin  []float64 `json:"margin"`
	Width   int       `json:"width"`
	Height  int       `json:"height"`
}

// Center computes the viewport center (x, y) from the quad content vertices.
func (m *cdpBoxModel) Center() (float64, float64, error) {
	if len(m.Content) < 8 {
		return 0, 0, fmt.Errorf("invalid box model content quad: %v", m.Content)
	}
	cx := (m.Content[0] + m.Content[2] + m.Content[4] + m.Content[6]) / 4.0
	cy := (m.Content[1] + m.Content[3] + m.Content[5] + m.Content[7]) / 4.0
	return cx, cy, nil
}

// BoundingBox returns the axis-aligned bounding box from quad vertices.
func (m *cdpBoxModel) BoundingBox() map[string]any {
	if len(m.Content) < 8 {
		return nil
	}
	minX := min4(m.Content[0], m.Content[2], m.Content[4], m.Content[6])
	maxX := max4(m.Content[0], m.Content[2], m.Content[4], m.Content[6])
	minY := min4(m.Content[1], m.Content[3], m.Content[5], m.Content[7])
	maxY := max4(m.Content[1], m.Content[3], m.Content[5], m.Content[7])
	return map[string]any{
		"x":      minX,
		"y":      minY,
		"width":  maxX - minX,
		"height": maxY - minY,
		"top":    minY,
		"right":  maxX,
		"bottom": maxY,
		"left":   minX,
	}
}

func min4(a, b, c, d float64) float64 {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	if d < m {
		m = d
	}
	return m
}

func max4(a, b, c, d float64) float64 {
	m := a
	if b > m {
		m = b
	}
	if c > m {
		m = c
	}
	if d > m {
		m = d
	}
	return m
}

// cdpGetBoxModel queries CDP DOM.getBoxModel by backendNodeId.
func cdpGetBoxModel(ws string, backendNodeID int) (*cdpBoxModel, error) {
	var res struct {
		Model cdpBoxModel `json:"model"`
	}
	params := map[string]any{"backendNodeId": backendNodeID}
	if err := cdpRequest(ws, "DOM.getBoxModel", params, &res); err != nil {
		return nil, err
	}
	return &res.Model, nil
}

// cdpScrollIntoViewIfNeeded scrolls an element into view by backendNodeId.
func cdpScrollIntoViewIfNeeded(ws string, backendNodeID int) error {
	return cdpRequest(ws, "DOM.scrollIntoViewIfNeeded", map[string]any{
		"backendNodeId": backendNodeID,
	}, nil)
}

// cdpResolveNode resolves a DOM backendNodeId into a CDP RemoteObject ObjectId.
func cdpResolveNode(ws string, backendNodeID int) (string, error) {
	var result struct {
		Object struct {
			ObjectID string `json:"objectId"`
		} `json:"object"`
	}
	if err := cdpRequest(ws, "DOM.resolveNode", map[string]any{
		"backendNodeId": backendNodeID,
	}, &result); err != nil {
		return "", err
	}
	if result.Object.ObjectID == "" {
		return "", fmt.Errorf("DOM.resolveNode returned empty objectId for backendNodeId %d", backendNodeID)
	}
	return result.Object.ObjectID, nil
}

// cdpCallFunctionOn calls a JavaScript function declaration on a CDP RemoteObject.
func cdpCallFunctionOn(ws, objectID, functionDeclaration string, args []any, awaitPromise bool) (any, error) {
	callArgs := make([]map[string]any, len(args))
	for i, arg := range args {
		callArgs[i] = map[string]any{"value": arg}
	}
	params := map[string]any{
		"objectId":            objectID,
		"functionDeclaration": functionDeclaration,
		"arguments":           callArgs,
		"returnByValue":       true,
		"awaitPromise":        awaitPromise,
	}
	var result browserRuntimeEvaluateResult
	if err := cdpRequest(ws, "Runtime.callFunctionOn", params, &result); err != nil {
		return nil, err
	}
	if result.ExceptionDetails != nil {
		return nil, fmt.Errorf("browser evaluation failed: %s", exceptionText(result.ExceptionDetails))
	}
	if result.Result.Value != nil {
		return result.Result.Value, nil
	}
	return map[string]any{
		"type":        result.Result.Type,
		"subtype":     result.Result.Subtype,
		"description": result.Result.Description,
	}, nil
}

func extractAXNodeID(val any) string {
	if s, ok := val.(string); ok {
		return s
	}
	if f, ok := val.(float64); ok {
		return strconv.Itoa(int(f))
	}
	if i, ok := val.(int); ok {
		return strconv.Itoa(i)
	}
	return ""
}

func extractBackendNodeID(val any) int {
	if f, ok := val.(float64); ok {
		return int(f)
	}
	if i, ok := val.(int); ok {
		return i
	}
	if s, ok := val.(string); ok {
		if id, err := strconv.Atoi(s); err == nil {
			return id
		}
	}
	return 0
}

func extractAXValue(val any) string {
	if s, ok := val.(string); ok {
		return s
	}
	if m, ok := val.(map[string]any); ok {
		if v, ok := m["value"].(string); ok {
			return v
		}
	}
	return ""
}

const a11yFillFunction = `function(text) {
  let el = this;
  if (!el) throw new Error('element not found');

  // Smart container / label sniffing (Issue #5)
  if (!('value' in el) && !el.isContentEditable && el.tagName !== 'SELECT') {
    let target = null;
    if (el.tagName === 'LABEL') {
      target = el.control || (el.htmlFor ? document.getElementById(el.htmlFor) : null) ||
               el.querySelector('input:not([type="hidden"]), textarea, select, [contenteditable="true"]');
    } else {
      target = el.querySelector('input:not([type="hidden"]), textarea, select, [contenteditable="true"]');
    }
    if (target) el = target;
  }

  if (typeof el.scrollIntoView === 'function') {
    el.scrollIntoView({block: 'center', inline: 'center', behavior: 'instant'});
  }
  if (typeof el.focus === 'function') el.focus();

  if (el.tagName === 'SELECT') {
    const opts = Array.from(el.options);
    for (const opt of opts) opt.selected = false;
    const opt = opts.find(o => o.value === text || o.text.trim() === text);
    if (opt) opt.selected = true;
    el.dispatchEvent(new Event('input', {bubbles: true, composed: true}));
    el.dispatchEvent(new Event('change', {bubbles: true, composed: true}));
    return {filled: true, value: text};
  }

  if ('value' in el) {
    const previousValue = el.value !== undefined ? el.value : '';
    if (el._valueTracker) {
      el._valueTracker.setValue(previousValue === text ? (text === '' ? '__argus_cleared__' : '') : previousValue);
    }

    if (typeof InputEvent === 'function') {
      el.dispatchEvent(new InputEvent('beforeinput', {
        bubbles: true,
        cancelable: true,
        composed: true,
        data: text,
        inputType: 'insertText'
      }));
    }

    const proto = el instanceof HTMLTextAreaElement ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype;
    const desc = Object.getOwnPropertyDescriptor(proto, 'value') || Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
    if (desc && desc.set) {
      desc.set.call(el, text);
    } else {
      el.value = text;
    }

    if (typeof InputEvent === 'function') {
      el.dispatchEvent(new InputEvent('input', {
        bubbles: true,
        composed: true,
        data: text,
        inputType: 'insertText'
      }));
    } else {
      el.dispatchEvent(new Event('input', {bubbles: true, composed: true}));
    }
    el.dispatchEvent(new Event('change', {bubbles: true, composed: true}));
  } else if (el.isContentEditable) {
    if (typeof InputEvent === 'function') {
      el.dispatchEvent(new InputEvent('beforeinput', {
        bubbles: true,
        cancelable: true,
        composed: true,
        data: text,
        inputType: 'insertText'
      }));
    }
    el.textContent = text;
    if (typeof InputEvent === 'function') {
      el.dispatchEvent(new InputEvent('input', {
        bubbles: true,
        composed: true,
        data: text,
        inputType: 'insertText'
      }));
    } else {
      el.dispatchEvent(new Event('input', {bubbles: true, composed: true}));
    }
    el.dispatchEvent(new Event('change', {bubbles: true, composed: true}));
  }
  return {filled: true, value: text};
}`

const a11ySelectOptionsFunction = `function(values) {
  let el = this;
  if (!el) throw new Error('element not found');
  if (el.tagName !== 'SELECT') throw new Error('element is not a select');
  const vals = new Set(values || []);
  const selected = [];
  for (const opt of el.options) {
    if (vals.has(opt.value) || vals.has(opt.text.trim())) {
      opt.selected = true;
      selected.push(opt.value);
    } else if (!el.multiple) {
      opt.selected = false;
    }
  }
  el.dispatchEvent(new Event('input', {bubbles: true, composed: true}));
  el.dispatchEvent(new Event('change', {bubbles: true, composed: true}));
  return selected;
}`

const a11yGetElementFunction = `function(include) {
  const el = this;
  if (!el) return {found: false};
  const includeHTML = include === 'html' || include === 'all';
  const includeAttrs = include === 'attributes' || include === 'all';
  const includeStyle = include === 'style' || include === 'all';
  const includeBox = include === 'bounding_box' || include === 'all';
  const res = {found: true, tag: el.localName};
  if (includeHTML) res.html = (el.outerHTML || '').slice(0, 5000);
  if (includeAttrs) {
    const attrs = {};
    for (const attr of el.attributes) attrs[attr.name] = attr.value;
    res.attributes = attrs;
  }
  if (includeStyle) {
    const s = getComputedStyle(el);
    const picked = {};
    const props = ['display','visibility','position','width','height','margin','padding','border','color','background-color','font-size','font-family','z-index','overflow','cursor','opacity','flex-direction','justify-content','align-items','gap'];
    for (const p of props) picked[p] = s.getPropertyValue(p);
    res.computedStyle = picked;
  }
  if (includeBox) {
    const rect = el.getBoundingClientRect();
    res.boundingBox = {x: rect.x, y: rect.y, width: rect.width, height: rect.height, top: rect.top, right: rect.right, bottom: rect.bottom, left: rect.left};
  }
  return res;
}`

const a11yFocusFunction = `function(clear) {
  const el = this;
  if (!el) throw new Error('element not found');
  if (typeof el.scrollIntoView === 'function') {
    el.scrollIntoView({block: 'center', inline: 'center', behavior: 'instant'});
  }
  if (typeof el.focus === 'function') el.focus();
  if (clear) {
    if ('value' in el) {
      el.value = '';
      el.dispatchEvent(new Event('input', {bubbles: true, composed: true}));
      el.dispatchEvent(new Event('change', {bubbles: true, composed: true}));
    } else if (el.isContentEditable) {
      el.textContent = '';
      el.dispatchEvent(new Event('input', {bubbles: true, composed: true}));
      el.dispatchEvent(new Event('change', {bubbles: true, composed: true}));
    }
  }
  return true;
}`
