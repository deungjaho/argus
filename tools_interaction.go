package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- input types ---

type clickInput struct {
	Ref            string   `json:"ref,omitempty"`
	Selector       string   `json:"selector,omitempty"`
	Text           string   `json:"text,omitempty"`
	X              *float64 `json:"x,omitempty"`
	Y              *float64 `json:"y,omitempty"`
	ImageX         *float64 `json:"image_x,omitempty"`
	ImageY         *float64 `json:"image_y,omitempty"`
	ImageWidth     *float64 `json:"image_width,omitempty"`
	ImageHeight    *float64 `json:"image_height,omitempty"`
	ScreenshotPath string   `json:"screenshot_path,omitempty"`
	Button         string   `json:"button,omitempty"`
	ClickCount     int      `json:"click_count,omitempty"`
	TimeoutMs      int      `json:"timeout_ms,omitempty"`
	NoWait         bool     `json:"no_wait,omitempty"`
}

type typeInput struct {
	Ref       string `json:"ref,omitempty"`
	Selector  string `json:"selector,omitempty"`
	Text      string `json:"text,omitempty"`
	Key       string `json:"key,omitempty"`
	Clear     bool   `json:"clear,omitempty"`
	TimeoutMs int    `json:"timeout_ms,omitempty"`
	NoWait    bool   `json:"no_wait,omitempty"`
}

type fillInput struct {
	Ref      string   `json:"ref,omitempty"`
	Selector string   `json:"selector,omitempty"`
	Value    string   `json:"value,omitempty"`
	Values   []string `json:"values,omitempty"`
	Fields   []struct {
		Ref      string `json:"ref,omitempty"`
		Selector string `json:"selector,omitempty"`
		Value    string `json:"value"`
	} `json:"fields,omitempty"`
	TimeoutMs int  `json:"timeout_ms,omitempty"`
	NoWait    bool `json:"no_wait,omitempty"`
}

type scrollInput struct {
	X      *float64 `json:"x,omitempty"`
	Y      *float64 `json:"y,omitempty"`
	DeltaX float64  `json:"delta_x,omitempty"`
	DeltaY float64  `json:"delta_y,omitempty"`
}

type hoverInput struct {
	Ref       string   `json:"ref,omitempty"`
	Selector  string   `json:"selector,omitempty"`
	Text      string   `json:"text,omitempty"`
	X         *float64 `json:"x,omitempty"`
	Y         *float64 `json:"y,omitempty"`
	TimeoutMs int      `json:"timeout_ms,omitempty"`
	NoWait    bool     `json:"no_wait,omitempty"`
}

type dragInput struct {
	FromRef      string `json:"from_ref,omitempty"`
	FromSelector string `json:"from_selector,omitempty"`
	FromText     string `json:"from_text,omitempty"`
	ToRef        string `json:"to_ref,omitempty"`
	ToSelector   string `json:"to_selector,omitempty"`
	ToText       string `json:"to_text,omitempty"`
	TimeoutMs    int    `json:"timeout_ms,omitempty"`
	NoWait       bool   `json:"no_wait,omitempty"`
}

// --- tool registrations ---

func registerInteractionTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "click",
		Description: "Click by CSS ref/selector, visible text, viewport x/y, or screenshot image_x/image_y. Auto-waits up to 3s (configurable via timeout_ms) for element to be present+visible+enabled. Set no_wait=true to skip auto-wait.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input clickInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("click")
		defer rec.End()
		rec.SetParams(input)

		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())
		x, y, err := resolveClickPoint(ctx, ws, input)
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		button := strings.TrimSpace(input.Button)
		if button == "" {
			button = "left"
		}
		clickCount := input.ClickCount
		if clickCount <= 0 {
			clickCount = 1
		}
		if err := cdpDispatchMouseClick(ws, x, y, button, clickCount); err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetResult(map[string]any{
			"action_id": string(rec.ID),
			"clicked":   map[string]any{"x": x, "y": y, "button": button, "click_count": clickCount},
		})
		return nil, map[string]any{
			"action_id": string(rec.ID),
			"clicked":   map[string]any{"x": x, "y": y, "button": button, "click_count": clickCount},
		}, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "type",
		Description: "Type text into the current focus or into a CSS ref/selector, or send a keyboard key (Enter, Escape, Tab, Backspace, Delete, ArrowLeft/Right/Up/Down, Home, End, PageUp, PageDown, Space, or a single character). Auto-waits for element. Set clear=true to clear before typing, no_wait=true to skip auto-wait. Use text to type text or key to send a keyboard key.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input typeInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("type")
		defer rec.End()
		rec.SetParams(input)

		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())
		key := strings.TrimSpace(input.Key)
		if key != "" {
			if err := cdpDispatchKey(ws, key); err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			rec.SetResult(map[string]any{"action_id": string(rec.ID), "key": key})
			return nil, map[string]any{"action_id": string(rec.ID), "key": key}, nil
		}
		if input.Text == "" && !input.Clear {
			rec.SetErrorFromError(fmt.Errorf("text or key is required"))
			return nil, nil, fmt.Errorf("text or key is required")
		}
		selector := firstNonEmpty(strings.TrimSpace(input.Ref), strings.TrimSpace(input.Selector))
		if selector != "" {
			opts := autoWaitOptions{TimeoutMs: input.TimeoutMs, NoWait: input.NoWait}
			if _, _, _, err := autoWaitForElement(ctx, ws, selector, "", opts); err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
		}
		if selector != "" || input.Clear {
			if _, err := cdpEvaluate(ws, focusExpression(selector, input.Clear), true); err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
		}
		if input.Text != "" {
			if err := cdpInsertText(ws, input.Text); err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
		}
		rec.SetResult(map[string]any{"action_id": string(rec.ID), "typed": len([]rune(input.Text)), "selector": selector, "cleared": input.Clear})
		return nil, map[string]any{"action_id": string(rec.ID), "typed": len([]rune(input.Text)), "selector": selector, "cleared": input.Clear}, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "fill",
		Description: "Fill a single input/textarea/select with a value, fill multiple form fields at once, or select dropdown options. Auto-waits for element. More reliable than type for form fields. Use value for a single field, fields to fill multiple fields at once, or values (with ref/selector) to select dropdown options.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input fillInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("fill")
		defer rec.End()
		rec.SetParams(input)

		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())
		opts := autoWaitOptions{TimeoutMs: input.TimeoutMs, NoWait: input.NoWait}
		if len(input.Fields) > 0 {
			results := []map[string]any{}
			for _, field := range input.Fields {
				selector := firstNonEmpty(strings.TrimSpace(field.Ref), strings.TrimSpace(field.Selector))
				if selector == "" {
					results = append(results, map[string]any{"error": "no ref or selector", "value": field.Value})
					continue
				}
				if _, _, _, err := autoWaitForElement(ctx, ws, selector, "", opts); err != nil {
					results = append(results, map[string]any{"selector": selector, "error": err.Error()})
					continue
				}
				if _, err := cdpEvaluate(ws, focusExpression(selector, true), true); err != nil {
					results = append(results, map[string]any{"selector": selector, "error": err.Error()})
					continue
				}
				if err := cdpInsertText(ws, field.Value); err != nil {
					results = append(results, map[string]any{"selector": selector, "error": err.Error()})
					continue
				}
				results = append(results, map[string]any{"selector": selector, "filled": true})
			}
			rec.SetResult(map[string]any{"action_id": string(rec.ID), "results": results, "count": len(results)})
			return nil, map[string]any{"action_id": string(rec.ID), "results": results, "count": len(results)}, nil
		}
		selector := firstNonEmpty(strings.TrimSpace(input.Ref), strings.TrimSpace(input.Selector))
		if selector == "" {
			rec.SetErrorFromError(fmt.Errorf("fill requires ref or selector"))
			return nil, nil, fmt.Errorf("fill requires ref or selector")
		}
		if len(input.Values) > 0 {
			if _, _, _, err := autoWaitForElement(ctx, ws, selector, "", opts); err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			value, err := cdpEvaluate(ws, selectOptionExpression(selector, input.Values), true)
			if err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			rec.SetResult(map[string]any{"action_id": string(rec.ID), "selected": value})
			return nil, map[string]any{"action_id": string(rec.ID), "selected": value}, nil
		}
		if _, _, _, err := autoWaitForElement(ctx, ws, selector, "", opts); err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		if _, err := cdpEvaluate(ws, focusExpression(selector, true), true); err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		if err := cdpInsertText(ws, input.Value); err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetResult(map[string]any{"action_id": string(rec.ID), "filled": selector, "value_length": len([]rune(input.Value))})
		return nil, map[string]any{"action_id": string(rec.ID), "filled": selector, "value_length": len([]rune(input.Value))}, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "scroll",
		Description: "Scroll using mouse wheel deltas at optional viewport coordinates. Defaults to page center and delta_y=500 (scroll down).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input scrollInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("scroll")
		defer rec.End()
		rec.SetParams(input)

		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())
		x, y, err := resolveScrollPoint(ws, input)
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		deltaX := input.DeltaX
		deltaY := input.DeltaY
		if deltaX == 0 && deltaY == 0 {
			deltaY = 500
		}
		result, err := cdpDispatchScroll(ws, x, y, deltaX, deltaY)
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetResult(map[string]any{"action_id": string(rec.ID), "x": x, "y": y, "delta_x": deltaX, "delta_y": deltaY, "result": result})
		return nil, map[string]any{"action_id": string(rec.ID), "x": x, "y": y, "delta_x": deltaX, "delta_y": deltaY, "result": result}, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "hover",
		Description: "Hover over an element by ref/selector/text or viewport coordinates. Auto-waits for element when using ref/selector/text.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input hoverInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("hover")
		defer rec.End()
		rec.SetParams(input)

		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())
		var x, y float64
		if input.X != nil && input.Y != nil {
			x, y = *input.X, *input.Y
		} else {
			selector := firstNonEmpty(strings.TrimSpace(input.Ref), strings.TrimSpace(input.Selector))
			text := strings.TrimSpace(input.Text)
			if selector == "" && text == "" {
				rec.SetErrorFromError(fmt.Errorf("hover requires ref, selector, text, or x/y"))
				return nil, nil, fmt.Errorf("hover requires ref, selector, text, or x/y")
			}
			opts := autoWaitOptions{TimeoutMs: input.TimeoutMs, NoWait: input.NoWait}
			px, py, _, err := autoWaitForElement(ctx, ws, selector, text, opts)
			if err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			x, y = px, py
		}
		if err := cdpDispatchMouseHover(ws, x, y); err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetResult(map[string]any{"action_id": string(rec.ID), "hovered": map[string]any{"x": x, "y": y}})
		return nil, map[string]any{"action_id": string(rec.ID), "hovered": map[string]any{"x": x, "y": y}}, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "drag",
		Description: "Drag from one element to another. Auto-waits for both source and target. Specify by ref/selector/text.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input dragInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("drag")
		defer rec.End()
		rec.SetParams(input)

		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())
		fromSelector := firstNonEmpty(strings.TrimSpace(input.FromRef), strings.TrimSpace(input.FromSelector))
		toSelector := firstNonEmpty(strings.TrimSpace(input.ToRef), strings.TrimSpace(input.ToSelector))
		if fromSelector == "" && input.FromText == "" {
			rec.SetErrorFromError(fmt.Errorf("drag requires from_ref/from_selector/from_text"))
			return nil, nil, fmt.Errorf("drag requires from_ref/from_selector/from_text")
		}
		if toSelector == "" && input.ToText == "" {
			rec.SetErrorFromError(fmt.Errorf("drag requires to_ref/to_selector/to_text"))
			return nil, nil, fmt.Errorf("drag requires to_ref/to_selector/to_text")
		}
		opts := autoWaitOptions{TimeoutMs: input.TimeoutMs, NoWait: input.NoWait}
		if _, _, _, err := autoWaitForElement(ctx, ws, fromSelector, input.FromText, opts); err != nil {
			rec.SetErrorFromError(fmt.Errorf("drag source: %w", err))
			return nil, nil, fmt.Errorf("drag source: %w", err)
		}
		if _, _, _, err := autoWaitForElement(ctx, ws, toSelector, input.ToText, opts); err != nil {
			rec.SetErrorFromError(fmt.Errorf("drag target: %w", err))
			return nil, nil, fmt.Errorf("drag target: %w", err)
		}
		value, err := cdpEvaluate(ws, dragExpression(fromSelector, input.FromText, toSelector, input.ToText), true)
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetResult(map[string]any{"action_id": string(rec.ID), "dragged": value})
		return nil, map[string]any{"action_id": string(rec.ID), "dragged": value}, nil
	})
}

// --- helpers ---

func resolveClickPoint(ctx context.Context, ws string, input clickInput) (float64, float64, error) {
	if x, y, ok, err := pointFromImageCoordinates(ws, input.ImageX, input.ImageY, input.ImageWidth, input.ImageHeight, input.ScreenshotPath); ok || err != nil {
		return x, y, err
	}
	if input.X != nil && input.Y != nil {
		return *input.X, *input.Y, nil
	}
	selector := firstNonEmpty(strings.TrimSpace(input.Ref), strings.TrimSpace(input.Selector))
	text := strings.TrimSpace(input.Text)
	if selector == "" && text == "" {
		return 0, 0, fmt.Errorf("click requires ref, selector, text, x/y, or image_x/image_y")
	}
	opts := autoWaitOptions{TimeoutMs: input.TimeoutMs, NoWait: input.NoWait}
	x, y, _, err := autoWaitForElement(ctx, ws, selector, text, opts)
	return x, y, err
}

func resolveScrollPoint(ws string, input scrollInput) (float64, float64, error) {
	if input.X != nil && input.Y != nil {
		return *input.X, *input.Y, nil
	}
	value, err := cdpEvaluate(ws, `(() => ({x: innerWidth / 2, y: innerHeight / 2}))()`, true)
	if err != nil {
		return 0, 0, err
	}
	point, ok := value.(map[string]any)
	if !ok {
		return 0, 0, fmt.Errorf("unable to compute viewport center")
	}
	x, okX := numberValue(point["x"])
	y, okY := numberValue(point["y"])
	if !okX || !okY {
		return 0, 0, fmt.Errorf("unable to compute viewport center")
	}
	return x, y, nil
}

func pointExpression(selector, text string) string {
	return fmt.Sprintf(`(() => {
const selector = %s;
const text = %s;
const visible = (el) => {
  const style = getComputedStyle(el);
  const rect = el.getBoundingClientRect();
  return style.visibility !== 'hidden' && style.display !== 'none' && rect.width > 0 && rect.height > 0;
};
let el = null;
if (selector) el = document.querySelector(selector);
if (!el && text) {
  const needle = text.toLowerCase();
  const candidates = Array.from(document.querySelectorAll('a,button,input,textarea,select,summary,canvas,[role],[tabindex],[aria-label],[data-testid],[data-test-id]'));
  el = candidates.find((node) => visible(node) && (((node.innerText || '') + ' ' + (node.getAttribute('aria-label') || '') + ' ' + (node.getAttribute('placeholder') || '')).toLowerCase().includes(needle)));
}
if (!el || !visible(el)) return null;
el.scrollIntoView({block: 'center', inline: 'center', behavior: 'instant'});
const rect = el.getBoundingClientRect();
return {x: Math.max(0, Math.min(innerWidth - 1, rect.left + rect.width / 2)), y: Math.max(0, Math.min(innerHeight - 1, rect.top + rect.height / 2))};
})()`, jsString(selector), jsString(text))
}

func focusExpression(selector string, clear bool) string {
	return fmt.Sprintf(`(() => {
const selector = %s;
const clear = %s;
const el = selector ? document.querySelector(selector) : document.activeElement;
if (!el) throw new Error(selector ? 'element not found' : 'no active element');
if (el.scrollIntoView) el.scrollIntoView({block: 'center', inline: 'center', behavior: 'instant'});
if (el.focus) el.focus();
if (clear) {
  if ('value' in el) {
    el.value = '';
    el.dispatchEvent(new Event('input', {bubbles: true}));
    el.dispatchEvent(new Event('change', {bubbles: true}));
  } else if (el.isContentEditable) {
    el.textContent = '';
    el.dispatchEvent(new InputEvent('input', {bubbles: true, inputType: 'deleteContentBackward'}));
  }
}
return true;
})()`, jsString(selector), fmt.Sprintf("%t", clear))
}

func selectOptionExpression(selector string, values []string) string {
	valuesJSON := "["
	for i, v := range values {
		if i > 0 {
			valuesJSON += ","
		}
		valuesJSON += jsString(v)
	}
	valuesJSON += "]"
	return fmt.Sprintf(`(() => {
const selector = %s;
const values = %s;
const el = document.querySelector(selector);
if (!el) throw new Error('element not found');
if (el.tagName !== 'SELECT') throw new Error('element is not a <select>');
const opts = Array.from(el.options);
for (const opt of opts) opt.selected = false;
for (const value of values) {
  const opt = opts.find(o => o.value === value || o.text.trim() === value);
  if (opt) opt.selected = true;
}
el.dispatchEvent(new Event('change', {bubbles: true}));
el.dispatchEvent(new Event('input', {bubbles: true}));
return {selected: Array.from(el.selectedOptions).map(o => ({value: o.value, text: o.text.trim()}))};
})()`, jsString(selector), valuesJSON)
}

func dragExpression(fromSelector, fromText, toSelector, toText string) string {
	return fmt.Sprintf(`(() => {
const fromSelector = %s;
const fromText = %s;
const toSelector = %s;
const toText = %s;
const visible = (el) => {
  const style = getComputedStyle(el);
  const rect = el.getBoundingClientRect();
  return style.visibility !== 'hidden' && style.display !== 'none' && rect.width > 0 && rect.height > 0;
};
const findEl = (selector, text) => {
  let el = null;
  if (selector) el = document.querySelector(selector);
  if (!el && text) {
    const needle = text.toLowerCase();
    el = Array.from(document.querySelectorAll('*')).find(node => visible(node) && (node.innerText || '').toLowerCase().includes(needle));
  }
  return el;
};
const from = findEl(fromSelector, fromText);
const to = findEl(toSelector, toText);
if (!from || !to) return {error: 'source or target element not found'};
const fromRect = from.getBoundingClientRect();
const toRect = to.getBoundingClientRect();
const fromX = fromRect.left + fromRect.width / 2;
const fromY = fromRect.top + fromRect.height / 2;
const toX = toRect.left + toRect.width / 2;
const toY = toRect.top + toRect.height / 2;
from.dispatchEvent(new MouseEvent('mousedown', {clientX: fromX, clientY: fromY, bubbles: true}));
from.dispatchEvent(new MouseEvent('dragstart', {clientX: fromX, clientY: fromY, bubbles: true}));
to.dispatchEvent(new MouseEvent('dragenter', {clientX: toX, clientY: toY, bubbles: true}));
to.dispatchEvent(new MouseEvent('dragover', {clientX: toX, clientY: toY, bubbles: true}));
to.dispatchEvent(new MouseEvent('drop', {clientX: toX, clientY: toY, bubbles: true}));
from.dispatchEvent(new MouseEvent('dragend', {clientX: toX, clientY: toY, bubbles: true}));
to.dispatchEvent(new MouseEvent('mouseup', {clientX: toX, clientY: toY, bubbles: true}));
return {dragged: {from: {x: fromX, y: fromY}, to: {x: toX, y: toY}}};
})()`, jsString(fromSelector), jsString(fromText), jsString(toSelector), jsString(toText))
}
