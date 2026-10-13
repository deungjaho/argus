package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- input types ---

type clickInput struct {
	Ref            string   `json:"ref,omitempty"`
	Selector       string   `json:"selector,omitempty"`
	Text           string   `json:"text,omitempty"`
	NodeID         string   `json:"node_id,omitempty"`
	BackendNodeID  int      `json:"backend_node_id,omitempty"`
	Snapshot       string   `json:"snapshot,omitempty"` // snapshot_id the ref came from — enables stale-ref detection
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
	Timeout        int      `json:"timeout,omitempty"`
	Delay          int      `json:"delay,omitempty"`
	DelayMs        int      `json:"delay_ms,omitempty"`
	Target         string   `json:"target,omitempty"`
	CSS            string   `json:"css,omitempty"`
	XPath          string   `json:"xpath,omitempty"`
	Query          string   `json:"query,omitempty"`
	WaitUntil      string   `json:"wait_until,omitempty"`
	NoWait         bool     `json:"no_wait,omitempty"`
}

type typeInput struct {
	Ref       string `json:"ref,omitempty"`
	Selector  string `json:"selector,omitempty"`
	Text      string `json:"text,omitempty"`
	Value     string `json:"value,omitempty"`
	Input     string `json:"input,omitempty"`
	Key       string `json:"key,omitempty"`
	Modifiers string `json:"modifiers,omitempty"` // e.g. "ctrl", "cmd+shift" — with key only
	Clear     bool   `json:"clear,omitempty"`
	TimeoutMs int    `json:"timeout_ms,omitempty"`
	Timeout   int    `json:"timeout,omitempty"`
	Delay     int    `json:"delay,omitempty"`
	DelayMs   int    `json:"delay_ms,omitempty"`
	Target    string `json:"target,omitempty"`
	CSS       string `json:"css,omitempty"`
	XPath     string `json:"xpath,omitempty"`
	Query     string `json:"query,omitempty"`
	NoWait    bool   `json:"no_wait,omitempty"`
}

type fillInput struct {
	Ref           string   `json:"ref,omitempty"`
	Selector      string   `json:"selector,omitempty"`
	Snapshot      string   `json:"snapshot,omitempty"` // snapshot_id the ref came from — enables stale-ref detection
	NodeID        string   `json:"node_id,omitempty"`
	BackendNodeID int      `json:"backend_node_id,omitempty"`
	Value         string   `json:"value,omitempty"`
	Text          string   `json:"text,omitempty"`
	Input         string   `json:"input,omitempty"`
	Values        []string `json:"values,omitempty"`
	Fields        []struct {
		Ref      string `json:"ref,omitempty"`
		Selector string `json:"selector,omitempty"`
		Value    string `json:"value"`
	} `json:"fields,omitempty"`
	Clear          bool   `json:"clear,omitempty"`
	TimeoutMs      int    `json:"timeout_ms,omitempty"`
	Timeout        int    `json:"timeout,omitempty"`
	Delay          int    `json:"delay,omitempty"`
	DelayMs        int    `json:"delay_ms,omitempty"`
	Target         string `json:"target,omitempty"`
	CSS            string `json:"css,omitempty"`
	XPath          string `json:"xpath,omitempty"`
	Query          string `json:"query,omitempty"`
	NoWait         bool   `json:"no_wait,omitempty"`
	SettleMs       int    `json:"settle_ms,omitempty"`
	WaitTransition bool   `json:"wait_transition,omitempty"`
}

type scrollInput struct {
	X         *float64 `json:"x,omitempty"`
	Y         *float64 `json:"y,omitempty"`
	DeltaX    float64  `json:"delta_x,omitempty"`
	DeltaY    float64  `json:"delta_y,omitempty"`
	Ref       string   `json:"ref,omitempty"`
	Selector  string   `json:"selector,omitempty"`
	Target    string   `json:"target,omitempty"`
	CSS       string   `json:"css,omitempty"`
	XPath     string   `json:"xpath,omitempty"`
	Text      string   `json:"text,omitempty"`
	Direction string   `json:"direction,omitempty"`
	Amount    float64  `json:"amount,omitempty"`
	Distance  float64  `json:"distance,omitempty"`
}

type hoverInput struct {
	Ref       string   `json:"ref,omitempty"`
	Selector  string   `json:"selector,omitempty"`
	Text      string   `json:"text,omitempty"`
	Target    string   `json:"target,omitempty"`
	CSS       string   `json:"css,omitempty"`
	XPath     string   `json:"xpath,omitempty"`
	Query     string   `json:"query,omitempty"`
	X         *float64 `json:"x,omitempty"`
	Y         *float64 `json:"y,omitempty"`
	TimeoutMs int      `json:"timeout_ms,omitempty"`
	Timeout   int      `json:"timeout,omitempty"`
	NoWait    bool     `json:"no_wait,omitempty"`
}

type dragInput struct {
	FromRef      string `json:"from_ref,omitempty"`
	FromSelector string `json:"from_selector,omitempty"`
	FromText     string `json:"from_text,omitempty"`
	FromTarget   string `json:"from_target,omitempty"`
	ToRef        string `json:"to_ref,omitempty"`
	ToSelector   string `json:"to_selector,omitempty"`
	ToText       string `json:"to_text,omitempty"`
	ToTarget     string `json:"to_target,omitempty"`
	TimeoutMs    int    `json:"timeout_ms,omitempty"`
	Timeout      int    `json:"timeout,omitempty"`
	NoWait       bool   `json:"no_wait,omitempty"`
}

type batchActionStep struct {
	Action         string   `json:"action"` // click, fill, type, key, press, wait_for, hover, scroll, navigate, assert, extract
	Target         string   `json:"target,omitempty"`
	Ref            string   `json:"ref,omitempty"`
	Selector       string   `json:"selector,omitempty"`
	CSS            string   `json:"css,omitempty"`
	XPath          string   `json:"xpath,omitempty"`
	Text           string   `json:"text,omitempty"`
	Value          string   `json:"value,omitempty"`
	Input          string   `json:"input,omitempty"`
	Values         []string `json:"values,omitempty"`
	Key            string   `json:"key,omitempty"`
	Modifiers      string   `json:"modifiers,omitempty"`
	Clear          bool     `json:"clear,omitempty"`
	DeltaX         float64  `json:"delta_x,omitempty"`
	DeltaY         float64  `json:"delta_y,omitempty"`
	TimeoutMs      int      `json:"timeout_ms,omitempty"`
	Timeout        int      `json:"timeout,omitempty"`
	Delay          int      `json:"delay,omitempty"`
	DelayMs        int      `json:"delay_ms,omitempty"`
	NoWait         bool     `json:"no_wait,omitempty"`
	SettleMs       int      `json:"settle_ms,omitempty"`
	WaitTransition bool     `json:"wait_transition,omitempty"`
	URL            string   `json:"url,omitempty"`
	Href           string   `json:"href,omitempty"`
	Name           string   `json:"name,omitempty"`     // extract: variable name for {{name}} substitution in later steps
	Attr           string   `json:"attr,omitempty"`     // extract: text (default) | value | html | attr:<name>
	Optional       bool     `json:"optional,omitempty"` // on failure: record error but continue the batch
}

type batchActionsInput struct {
	Actions []batchActionStep `json:"actions"`
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

		if delay := resolveTimeout(input.DelayMs, input.Delay); delay > 0 {
			time.Sleep(time.Duration(delay) * time.Millisecond)
		}
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
		delivered, err := cdpDispatchMouseClick(ws, x, y, button, clickCount)
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		result := map[string]any{
			"action_id": string(rec.ID),
			"clicked":   map[string]any{"x": x, "y": y, "button": button, "click_count": clickCount},
			"delivered": delivered,
		}
		if delivered == deliverJS {
			result["warning"] = "tab is hidden in background; used untrusted JS events (isTrusted=false). Use configure{focus:true} or bring the tab to front for trusted input."
		}
		settleAndCaptureVerification(rec, ws, result)
		return nil, result, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "type",
		Description: "Type text into the current focus or into a CSS ref/selector, or send a keyboard key (Enter, Escape, Tab, Backspace, Delete, ArrowLeft/Right/Up/Down, Home, End, PageUp, PageDown, Space, or a single character). Auto-waits for element. Set clear=true to clear before typing, no_wait=true to skip auto-wait. Use text to type text or key to send a keyboard key; combine with modifiers (e.g. \"cmd+a\", \"ctrl+shift\") for shortcuts.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input typeInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("type")
		defer rec.End()
		rec.SetParams(input)

		if delay := resolveTimeout(input.DelayMs, input.Delay); delay > 0 {
			time.Sleep(time.Duration(delay) * time.Millisecond)
		}
		timeoutMs := resolveTimeout(input.TimeoutMs, input.Timeout)

		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())
		key := strings.TrimSpace(input.Key)
		if key != "" {
			delivered, err := cdpDispatchKey(ws, key, modifierMask(input.Modifiers))
			if err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			result := map[string]any{"action_id": string(rec.ID), "key": key, "delivered": delivered}
			if delivered == deliverJS {
				result["warning"] = "tab is hidden; key sent via untrusted JS events (isTrusted=false). Use configure{focus:true} for trusted input."
			}
			settleAndCaptureVerification(rec, ws, result)
			return nil, result, nil
		}
		text := resolveValue(input.Text, input.Value, input.Input)
		if text == "" && !input.Clear {
			rec.SetErrorFromError(fmt.Errorf("text or key is required"))
			return nil, nil, fmt.Errorf("text or key is required")
		}
		selector := resolveSelector(input.Ref, input.Selector, input.CSS, input.Target, input.XPath, input.Query)
		if selector != "" {
			opts := autoWaitOptions{TimeoutMs: timeoutMs, NoWait: input.NoWait}
			if a11yNode, isA11y, err := resolveA11yNode(ws, selector); err == nil && isA11y {
				if _, _, _, waitErr := autoWaitForElement(ctx, ws, selector, "", opts); waitErr != nil {
					rec.SetErrorFromError(waitErr)
					return nil, nil, waitErr
				}
				objectID, rErr := cdpResolveNode(ws, a11yNode.BackendDOMNodeID)
				if rErr != nil {
					rec.SetErrorFromError(rErr)
					return nil, nil, rErr
				}
				defer cdpRequest(ws, "Runtime.releaseObject", map[string]any{"objectId": objectID}, nil)
				_, _ = cdpCallFunctionOn(ws, objectID, a11yFocusFunction, []any{input.Clear}, true)
			} else {
				if _, _, _, err := autoWaitForElement(ctx, ws, selector, "", opts); err != nil {
					rec.SetErrorFromError(err)
					return nil, nil, err
				}
			}
		}
		if selector != "" || input.Clear {
			if !isLikelyA11yRef(selector) {
				if _, err := cdpEvaluate(ws, focusExpression(selector, input.Clear), true); err != nil {
					rec.SetErrorFromError(err)
					return nil, nil, err
				}
			}
		}
		if text != "" {
			delivered, err := cdpInsertText(ws, text)
			if err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			result := map[string]any{"action_id": string(rec.ID), "typed": len([]rune(text)), "selector": selector, "cleared": input.Clear, "delivered": delivered}
			if delivered == deliverJS {
				result["warning"] = "tab is hidden; text inserted via untrusted JS (isTrusted=false). Use configure{focus:true} for trusted input."
			}
			settleAndCaptureVerification(rec, ws, result)
			return nil, result, nil
		}
		result := map[string]any{"action_id": string(rec.ID), "typed": len([]rune(text)), "selector": selector, "cleared": input.Clear}
		settleAndCaptureVerification(rec, ws, result)
		return nil, result, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "fill",
		Description: "Fill a single input/textarea/select with a value, fill multiple form fields at once, or select dropdown options. Auto-waits for element and settles reactive transitions before taking page snapshot. More reliable than type for form fields. Use value for a single field, fields to fill multiple fields at once, or values (with ref/selector) to select dropdown options.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input fillInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("fill")
		defer rec.End()
		rec.SetParams(input)

		if delay := resolveTimeout(input.DelayMs, input.Delay); delay > 0 {
			time.Sleep(time.Duration(delay) * time.Millisecond)
		}
		timeoutMs := resolveTimeout(input.TimeoutMs, input.Timeout)

		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())
		opts := autoWaitOptions{TimeoutMs: timeoutMs, NoWait: input.NoWait}
		if len(input.Fields) > 0 {
			results := []map[string]any{}
			for _, field := range input.Fields {
				selector := resolveSelector(field.Ref, field.Selector, "", "", "", "")
				if selector == "" {
					results = append(results, map[string]any{"error": "no ref or selector", "value": field.Value})
					continue
				}
				if _, _, _, err := autoWaitForElement(ctx, ws, selector, "", opts); err != nil {
					results = append(results, map[string]any{"selector": selector, "error": err.Error()})
					continue
				}
				if _, err := cdpEvaluate(ws, fillExpression(selector, field.Value), true); err != nil {
					results = append(results, map[string]any{"selector": selector, "error": err.Error()})
					continue
				}
				results = append(results, map[string]any{"selector": selector, "filled": true})
			}
			if !input.NoWait {
				settleMs := input.SettleMs
				if settleMs <= 0 {
					settleMs = 50
				}
				waitTransitionSettle(ws, settleMs)
			}
			result := map[string]any{"action_id": string(rec.ID), "results": results, "count": len(results)}
			settleAndCaptureVerification(rec, ws, result)
			return nil, result, nil
		}
		selector := resolveSelector(input.Ref, input.Selector, input.CSS, input.Target, input.XPath, input.Query)
		if selector == "" {
			if input.NodeID != "" {
				selector = "ax:" + input.NodeID
			} else if input.BackendNodeID > 0 {
				selector = fmt.Sprintf("backend:%d", input.BackendNodeID)
			}
		}
		val := resolveValue(input.Value, input.Text, input.Input)
		if selector == "" {
			rec.SetErrorFromError(fmt.Errorf("fill requires ref or selector"))
			return nil, nil, fmt.Errorf("fill requires ref or selector")
		}
		if snapID := strings.TrimSpace(input.Snapshot); snapID != "" {
			mark, err := resolveSnapshotMark(snapID, selector)
			if err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			opts.ExpectMark = mark
		}

		a11yNode, isA11y, a11yErr := resolveA11yNode(ws, selector)
		if a11yErr != nil {
			rec.SetErrorFromError(a11yErr)
			return nil, nil, a11yErr
		}
		if isA11y {
			if _, _, _, err := autoWaitForElement(ctx, ws, selector, "", opts); err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			objectID, err := cdpResolveNode(ws, a11yNode.BackendDOMNodeID)
			if err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			defer cdpRequest(ws, "Runtime.releaseObject", map[string]any{"objectId": objectID}, nil)
			if len(input.Values) > 0 {
				value, err := cdpCallFunctionOn(ws, objectID, a11ySelectOptionsFunction, []any{input.Values}, true)
				if err != nil {
					rec.SetErrorFromError(err)
					return nil, nil, err
				}
				if !input.NoWait {
					settleMs := input.SettleMs
					if settleMs <= 0 {
						settleMs = 50
					}
					waitTransitionSettle(ws, settleMs)
				}
				result := map[string]any{"action_id": string(rec.ID), "selected": value}
				settleAndCaptureVerification(rec, ws, result)
				return nil, result, nil
			}
			if _, err := cdpCallFunctionOn(ws, objectID, a11yFillFunction, []any{val}, true); err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			if !input.NoWait {
				settleMs := input.SettleMs
				if settleMs <= 0 {
					settleMs = 50
				}
				waitTransitionSettle(ws, settleMs)
			}
			result := map[string]any{"action_id": string(rec.ID), "filled": selector, "backend_dom_node_id": a11yNode.BackendDOMNodeID, "value_length": len([]rune(val))}
			settleAndCaptureVerification(rec, ws, result)
			return nil, result, nil
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
			if !input.NoWait {
				settleMs := input.SettleMs
				if settleMs <= 0 {
					settleMs = 50
				}
				waitTransitionSettle(ws, settleMs)
			}
			result := map[string]any{"action_id": string(rec.ID), "selected": value}
			settleAndCaptureVerification(rec, ws, result)
			return nil, result, nil
		}
		if _, _, _, err := autoWaitForElement(ctx, ws, selector, "", opts); err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		if _, err := cdpEvaluate(ws, fillExpression(selector, val), true); err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		if !input.NoWait {
			settleMs := input.SettleMs
			if settleMs <= 0 {
				settleMs = 50
			}
			waitTransitionSettle(ws, settleMs)
		}
		result := map[string]any{"action_id": string(rec.ID), "filled": selector, "value_length": len([]rune(val))}
		settleAndCaptureVerification(rec, ws, result)
		return nil, result, nil
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
			amount := input.Amount
			if amount == 0 {
				amount = input.Distance
			}
			if amount == 0 {
				amount = 500
			}
			switch strings.ToLower(strings.TrimSpace(input.Direction)) {
			case "up":
				deltaY = -amount
			case "right":
				deltaX = amount
			case "left":
				deltaX = -amount
			default:
				deltaY = amount
			}
		}
		delivered, result, err := cdpDispatchScroll(ws, x, y, deltaX, deltaY)
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		out := map[string]any{"action_id": string(rec.ID), "x": x, "y": y, "delta_x": deltaX, "delta_y": deltaY, "result": result, "delivered": delivered}
		if delivered == deliverJS {
			out["warning"] = "tab is hidden; scrolled via untrusted JS scrollBy. Use configure{focus:true} for trusted wheel events."
		}
		settleAndCaptureVerification(rec, ws, out)
		return nil, out, nil
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
			selector := resolveSelector(input.Ref, input.Selector, input.CSS, input.Target, input.XPath, "")
			text := resolveFirstNonEmpty(input.Text, input.Query)
			if selector == "" && text == "" {
				rec.SetErrorFromError(fmt.Errorf("hover requires ref, selector, text, or x/y"))
				return nil, nil, fmt.Errorf("hover requires ref, selector, text, or x/y")
			}
			timeoutMs := resolveTimeout(input.TimeoutMs, input.Timeout)
			opts := autoWaitOptions{TimeoutMs: timeoutMs, NoWait: input.NoWait}
			px, py, _, err := autoWaitForElement(ctx, ws, selector, text, opts)
			if err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			x, y = px, py
		}
		delivered, err := cdpDispatchMouseHover(ws, x, y)
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		out := map[string]any{"action_id": string(rec.ID), "hovered": map[string]any{"x": x, "y": y}, "delivered": delivered}
		if delivered == deliverJS {
			out["warning"] = "tab is hidden; hover dispatched via untrusted JS events (isTrusted=false). Use configure{focus:true} for trusted input."
		}
		attachEvidence(rec, ws, out)
		rec.SetResult(out)
		return nil, out, nil
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
		fromSelector := resolveSelector(input.FromRef, input.FromSelector, "", input.FromTarget, "", "")
		toSelector := resolveSelector(input.ToRef, input.ToSelector, "", input.ToTarget, "", "")
		if fromSelector == "" && input.FromText == "" {
			rec.SetErrorFromError(fmt.Errorf("drag requires from_ref/from_selector/from_text"))
			return nil, nil, fmt.Errorf("drag requires from_ref/from_selector/from_text")
		}
		if toSelector == "" && input.ToText == "" {
			rec.SetErrorFromError(fmt.Errorf("drag requires to_ref/to_selector/to_text"))
			return nil, nil, fmt.Errorf("drag requires to_ref/to_selector/to_text")
		}
		timeoutMs := resolveTimeout(input.TimeoutMs, input.Timeout)
		opts := autoWaitOptions{TimeoutMs: timeoutMs, NoWait: input.NoWait}
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
		out := map[string]any{"action_id": string(rec.ID), "dragged": value}
		attachEvidence(rec, ws, out)
		rec.SetResult(out)
		return nil, out, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "batch_actions",
		Description: "Execute a sequence of UI interactions (click, fill, type, key, press, wait_for, hover, scroll, navigate, assert, extract) in a single turn. Replays until the first error (unless optional: true). Reduces multi-turn latency.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input batchActionsInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("batch_actions")
		defer rec.End()
		rec.SetParams(input)

		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())

		if len(input.Actions) == 0 {
			err := fmt.Errorf("no actions provided in batch")
			rec.SetErrorFromError(err)
			return nil, nil, err
		}

		stepResults := make([]map[string]any, 0, len(input.Actions))
		vars := map[string]string{}

		// failStep records the failed step (with a post-failure snapshot so
		// the agent can self-correct) and returns a wrapped error.
		failStep := func(i int, actionType string, stepRes map[string]any, err error) (*mcp.CallToolResult, any, error) {
			rec.SetErrorFromError(err)
			stepRes["error"] = err.Error()
			stepResults = append(stepResults, stepRes)
			res := map[string]any{
				"action_id":   string(rec.ID),
				"total":       len(input.Actions),
				"completed":   len(stepResults) - 1,
				"failed_step": i + 1,
				"results":     stepResults,
			}
			settleAndCaptureVerification(rec, ws, res)
			return nil, res, fmt.Errorf("step %d (%s) failed: %w", i+1, actionType, err)
		}

		varRef := regexp.MustCompile(`\{\{([a-zA-Z_][a-zA-Z0-9_]*)\}\}`)
		expandVars := func(s string) string {
			if !strings.Contains(s, "{{") {
				return s
			}
			return varRef.ReplaceAllStringFunc(s, func(m string) string {
				if v, ok := vars[m[2:len(m)-2]]; ok {
					return v
				}
				return m // unknown variable stays literal
			})
		}

		runStep := func(step batchActionStep, actionType, target string, stepRes map[string]any) error {
			if delay := resolveTimeout(step.DelayMs, step.Delay); delay > 0 {
				time.Sleep(time.Duration(delay) * time.Millisecond)
			}
			timeoutMs := resolveTimeout(step.TimeoutMs, step.Timeout)
			opts := autoWaitOptions{TimeoutMs: timeoutMs, NoWait: step.NoWait}
			switch actionType {
			case "click":
				x, y, err := resolveClickPoint(ctx, ws, clickInput{
					Ref: target, Selector: target, Text: expandVars(step.Text),
					TimeoutMs: timeoutMs, NoWait: step.NoWait,
				})
				if err != nil {
					return err
				}
				delivered, err := cdpDispatchMouseClick(ws, x, y, "left", 1)
				if err != nil {
					return err
				}
				stepRes["clicked"] = map[string]any{"x": x, "y": y}
				stepRes["delivered"] = delivered

			case "fill":
				val := expandVars(resolveValue(step.Value, step.Text, step.Input))
				if target == "" {
					return fmt.Errorf("fill requires target or selector")
				}
				if _, _, _, err := autoWaitForElement(ctx, ws, target, "", opts); err != nil {
					return err
				}
				if len(step.Values) > 0 {
					vals := make([]string, len(step.Values))
					for j, v := range step.Values {
						vals[j] = expandVars(v)
					}
					if _, err := cdpEvaluate(ws, selectOptionExpression(target, vals), true); err != nil {
						return err
					}
					stepRes["selected"] = vals
				} else {
					if _, err := cdpEvaluate(ws, fillExpression(target, val), true); err != nil {
						return err
					}
					if !step.NoWait && (step.WaitTransition || step.SettleMs > 0) {
						settleMs := step.SettleMs
						if settleMs <= 0 {
							settleMs = 50
						}
						waitTransitionSettle(ws, settleMs)
					}
					stepRes["filled"] = target
				}

			case "type", "key", "press":
				var key string
				if actionType == "key" || actionType == "press" {
					key = expandVars(resolveFirstNonEmpty(step.Key, step.Value, step.Text, step.Input))
					if key == "" {
						return fmt.Errorf("%s action requires key, value, text, or input", actionType)
					}
				} else {
					key = expandVars(strings.TrimSpace(step.Key))
				}

				if key != "" {
					if target != "" {
						if _, _, _, err := autoWaitForElement(ctx, ws, target, "", opts); err != nil {
							return err
						}
						if _, err := cdpEvaluate(ws, focusExpression(target, step.Clear), true); err != nil {
							return err
						}
					}
					delivered, err := cdpDispatchKey(ws, key, modifierMask(step.Modifiers))
					if err != nil {
						return err
					}
					stepRes["key"] = key
					stepRes["delivered"] = delivered
				} else {
					text := resolveValue(step.Text, step.Value, step.Input)
					text = expandVars(text)
					if target != "" {
						if _, _, _, err := autoWaitForElement(ctx, ws, target, "", opts); err != nil {
							return err
						}
					}
					if target != "" || step.Clear {
						if _, err := cdpEvaluate(ws, focusExpression(target, step.Clear), true); err != nil {
							return err
						}
					}
					if text != "" {
						delivered, err := cdpInsertText(ws, text)
						if err != nil {
							return err
						}
						stepRes["delivered"] = delivered
					}
					stepRes["typed"] = len([]rune(text))
				}

			case "wait_for":
				if _, _, _, err := autoWaitForElement(ctx, ws, target, expandVars(step.Text), opts); err != nil {
					return err
				}
				stepRes["waited"] = true

			case "hover":
				px, py, _, err := autoWaitForElement(ctx, ws, target, expandVars(step.Text), opts)
				if err != nil {
					return err
				}
				delivered, err := cdpDispatchMouseHover(ws, px, py)
				if err != nil {
					return err
				}
				stepRes["hovered"] = map[string]any{"x": px, "y": py}
				stepRes["delivered"] = delivered

			case "scroll":
				deltaX := step.DeltaX
				deltaY := step.DeltaY
				if deltaX == 0 && deltaY == 0 {
					deltaY = 500
				}
				x, y, _ := resolveScrollPoint(ws, scrollInput{})
				delivered, _, err := cdpDispatchScroll(ws, x, y, deltaX, deltaY)
				if err != nil {
					return err
				}
				stepRes["delivered"] = delivered
				stepRes["scrolled"] = map[string]any{"delta_x": deltaX, "delta_y": deltaY}

			case "navigate":
				url := expandVars(resolveURL(step.URL, step.Href, step.Target))
				if url == "" {
					return fmt.Errorf("navigate requires url")
				}
				url = normalizeURL(url)
				if _, err := cdpPageNavigate(ws, url, 10*time.Second); err != nil {
					return err
				}
				stepRes["navigated"] = url

			case "assert":
				// Verify state: element present (target), text on page,
				// or URL contains. Fails the batch unless optional.
				url := expandVars(resolveFirstNonEmpty(step.URL, step.Href))
				if url == "" && (looksLikeURL(step.Target) || looksLikeURL(target)) {
					url = expandVars(resolveFirstNonEmpty(step.Target, target))
				}
				text := expandVars(step.Text)
				if url != "" {
					v, err := cdpEvaluate(ws, fmt.Sprintf(`location.href.includes(%s)`, jsString(url)), true)
					if err != nil {
						return err
					}
					if ok, _ := v.(bool); !ok {
						return fmt.Errorf("assert failed: url does not contain %q", url)
					}
					stepRes["asserted_url"] = url
					return nil
				}
				if target == "" && text == "" {
					return fmt.Errorf("assert requires target/selector, text, or url")
				}
				if _, _, _, err := autoWaitForElement(ctx, ws, target, text, opts); err != nil {
					return fmt.Errorf("assert failed: %w", err)
				}
				stepRes["asserted"] = true

			case "extract":
				// Pull a value out of the page into a named variable;
				// later steps reference it as {{name}}.
				name := strings.TrimSpace(step.Name)
				if name == "" {
					return fmt.Errorf("extract requires name (variable to store)")
				}
				if target == "" {
					return fmt.Errorf("extract requires target or selector")
				}
				attr := strings.TrimSpace(step.Attr)
				v, err := cdpEvaluate(ws, extractExpression(target, attr), true)
				if err != nil {
					return err
				}
				s, _ := v.(string)
				vars[name] = s
				stepRes["extracted"] = map[string]any{"name": name, "value": s}

			default:
				return fmt.Errorf("unsupported action type: %s", actionType)
			}
			return nil
		}

		for i, step := range input.Actions {
			actionType := strings.ToLower(strings.TrimSpace(step.Action))
			target := expandVars(resolveSelector(step.Ref, step.Selector, step.CSS, step.Target, step.XPath, ""))
			stepRes := map[string]any{"index": i + 1, "action": actionType}

			if err := runStep(step, actionType, target, stepRes); err != nil {
				if step.Optional {
					stepRes["error"] = err.Error()
					stepRes["skipped"] = true
				} else {
					return failStep(i, actionType, stepRes, err)
				}
			}
			stepResults = append(stepResults, stepRes)
		}

		result := map[string]any{
			"action_id": string(rec.ID),
			"total":     len(input.Actions),
			"completed": len(stepResults),
			"results":   stepResults,
		}
		if len(vars) > 0 {
			result["vars"] = vars
		}
		settleAndCaptureVerification(rec, ws, result)
		return nil, result, nil
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
	selector := resolveSelector(input.Ref, input.Selector, input.CSS, input.Target, input.XPath, "")
	if selector == "" {
		if input.NodeID != "" {
			selector = "ax:" + input.NodeID
		} else if input.BackendNodeID > 0 {
			selector = fmt.Sprintf("backend:%d", input.BackendNodeID)
		}
	}
	text := resolveFirstNonEmpty(input.Text, input.Query)
	if selector == "" && text == "" {
		return 0, 0, fmt.Errorf("click requires ref, selector, text, x/y, or image_x/image_y")
	}
	timeoutMs := resolveTimeout(input.TimeoutMs, input.Timeout)
	opts := autoWaitOptions{TimeoutMs: timeoutMs, NoWait: input.NoWait}
	if snapID := strings.TrimSpace(input.Snapshot); snapID != "" {
		mark, err := resolveSnapshotMark(snapID, selector)
		if err != nil {
			return 0, 0, err
		}
		opts.ExpectMark = mark
	}
	x, y, _, err := autoWaitForElement(ctx, ws, selector, text, opts)
	return x, y, err
}

// extractExpression reads a value off an element for batch_actions
// extract steps. attr: "text" (default), "value", "html", or "attr:<name>".
func extractExpression(selector, attr string) string {
	return fmt.Sprintf(`(() => {
const selector = %s;
const attr = %s;
let el = null;
try {
  el = document.querySelector(selector);
} catch (e) {
  el = document.getElementById(selector) ||
       document.querySelector('[data-testid=' + JSON.stringify(selector) + ']') ||
       document.querySelector('[data-test-id=' + JSON.stringify(selector) + ']') ||
       document.querySelector('[aria-label=' + JSON.stringify(selector) + ']');
}
if (!el) throw new Error('extract: element not found');
if (attr.startsWith('attr:')) return el.getAttribute(attr.slice(5)) || '';
if (attr === 'value') return 'value' in el ? String(el.value) : '';
if (attr === 'html') return el.innerHTML;
return (el.innerText || '').trim();
})()`, jsString(selector), jsString(attr))
}

func resolveScrollPoint(ws string, input scrollInput) (float64, float64, error) {
	if input.X != nil && input.Y != nil {
		return *input.X, *input.Y, nil
	}
	if sel := resolveSelector(input.Ref, input.Selector, input.CSS, input.Target, input.XPath, ""); sel != "" || input.Text != "" {
		if x, y, _, err := autoWaitForElement(context.Background(), ws, sel, input.Text, autoWaitOptions{TimeoutMs: 1500}); err == nil {
			return x, y, nil
		}
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

func focusExpression(selector string, clear bool) string {
	return fmt.Sprintf(`(() => {
const selector = %s;
const clear = %s;
let el = null;
if (selector) {
  try {
    el = document.querySelector(selector);
  } catch (e) {
    el = document.getElementById(selector) ||
         document.querySelector('[data-testid=' + JSON.stringify(selector) + ']') ||
         document.querySelector('[data-test-id=' + JSON.stringify(selector) + ']') ||
         document.querySelector('[aria-label=' + JSON.stringify(selector) + ']');
  }
} else {
  el = document.activeElement;
}
if (!el) throw new Error(selector ? 'element not found' : 'no active element');

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

if (el.scrollIntoView) el.scrollIntoView({block: 'center', inline: 'center', behavior: 'instant'});
if (el.focus) el.focus();
if (clear) {
  if ('value' in el) {
    const proto = el instanceof HTMLTextAreaElement ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype;
    const desc = Object.getOwnPropertyDescriptor(proto, 'value') || Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
    if (desc && desc.set) {
      desc.set.call(el, '');
    } else {
      el.value = '';
    }
    if (el._valueTracker) {
      el._valueTracker.setValue('__argus_cleared__');
    }
    el.dispatchEvent(new Event('input', {bubbles: true, composed: true}));
    el.dispatchEvent(new Event('change', {bubbles: true, composed: true}));
  } else if (el.isContentEditable) {
    el.textContent = '';
    el.dispatchEvent(new InputEvent('input', {bubbles: true, composed: true, inputType: 'deleteContentBackward'}));
    el.dispatchEvent(new Event('change', {bubbles: true, composed: true}));
  }
}
return true;
})()`, jsString(selector), fmt.Sprintf("%t", clear))
}

func fillExpression(selector, text string) string {
	return fmt.Sprintf(`(() => {
const selector = %s;
const text = %s;
let el = null;
if (selector) {
  try {
    el = document.querySelector(selector);
  } catch (e) {
    el = document.getElementById(selector) ||
         document.querySelector('[data-testid=' + JSON.stringify(selector) + ']') ||
         document.querySelector('[data-test-id=' + JSON.stringify(selector) + ']') ||
         document.querySelector('[aria-label=' + JSON.stringify(selector) + ']');
  }
} else {
  el = document.activeElement;
}
if (!el) throw new Error(selector ? 'element not found' : 'no active element');

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
})()`, jsString(selector), jsString(text))
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
`+rankedTextMatchJS+`
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
