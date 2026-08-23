package main

// automator_protocol.go — typed WeChat automator protocol DTOs.
//
// These types encode the exact JSON-RPC request parameters and response
// envelopes used by the WeChat DevTools automator, as derived from the
// official miniprogram-automator@0.12.1 package (out/Page.js,
// out/Element.js, out/MiniProgram.js).
//
// Design:
//   - Typed DTOs replace ad-hoc map[string]any protocol code.
//   - Response envelopes (getData, callWxMethod, callFunction) are
//     unwrapped at the protocol layer so callers receive the inner value.
//   - Element identity is carried as elementId + pageId (+ nodeId/videoId),
//     matching the official SDK's Element.send method.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// --- Element identity ---

// wxElementRef is the typed identity of a WeChat page element, as returned
// by Page.getElement / Page.getElements. It carries all fields the official
// SDK's Element.send method needs to address an element.
type wxElementRef struct {
	ElementID string `json:"elementId"`
	TagName   string `json:"tagName"`
	NodeID    string `json:"nodeId,omitempty"`
	VideoID   string `json:"videoId,omitempty"`
}

// elementParams builds the JSON-RPC params for an Element.* call, carrying
// elementId + pageId (+ nodeId/videoId when present). This mirrors the
// official SDK's Element.send method exactly.
func (e *wxElementRef) elementParams(pageID int) map[string]any {
	params := map[string]any{
		"elementId": e.ElementID,
		"pageId":    pageID,
	}
	if e.NodeID != "" {
		params["nodeId"] = e.NodeID
	}
	if e.VideoID != "" {
		params["videoId"] = e.VideoID
	}
	return params
}

// --- Response envelopes ---

// wxGetDataResp is the Page.getData response: {data: ...}.
type wxGetDataResp struct {
	Data json.RawMessage `json:"data"`
}

// wxCallResultResp is the App.callWxMethod / App.callFunction /
// Element.callFunction / Page.callMethod response: {result: ...}.
type wxCallResultResp struct {
	Result json.RawMessage `json:"result"`
}

// wxGetElementResp is the Page.getElement response.
type wxGetElementResp struct {
	ElementID string `json:"elementId"`
	TagName   string `json:"tagName"`
	NodeID    string `json:"nodeId,omitempty"`
	VideoID   string `json:"videoId,omitempty"`
}

// wxGetElementsResp is the Page.getElements response: {elements: [...]}.
type wxGetElementsResp struct {
	Elements []wxGetElementResp `json:"elements"`
}

// wxGetWXMLResp is the Element.getWXML response: {wxml: ...}.
type wxGetWXMLResp struct {
	WXML string `json:"wxml"`
}

// wxGetAttributesResp is the Element.getAttributes response: {attributes: {...}}.
type wxGetAttributesResp struct {
	Attributes map[string]string `json:"attributes"`
}

// wxScreenshotResp is the App.captureScreenshot response: {data: base64}.
type wxScreenshotResp struct {
	Data string `json:"data"`
}

// wxCurrentPageResp is the App.getCurrentPage response.
type wxCurrentPageResp struct {
	PageID int             `json:"pageId"`
	Path   string          `json:"path"`
	Query  json.RawMessage `json:"query,omitempty"`
}

// wxPageStackResp is the App.getPageStack response.
type wxPageStackResp struct {
	PageStack []wxCurrentPageResp `json:"pageStack"`
}

// wxGetElementWXMLResp is the Element.getWXML response with type param.
type wxGetElementWXMLResp struct {
	WXML string `json:"wxml"`
}

// --- Typed protocol functions ---
//
// These are the protocol-correct wrappers used by all tool implementations.
// They handle response unwrapping and return structured errors.

// automatorGetElement calls Page.getElement and returns the typed element ref.
func automatorGetElement(pageID int, selector string) (wxElementRef, error) {
	ctx, cancel := defaultAutomatorCtx()
	defer cancel()
	var resp wxGetElementResp
	if err := automatorCallContext(ctx, "Page.getElement", map[string]any{
		"pageId":   pageID,
		"selector": selector,
	}, &resp); err != nil {
		return wxElementRef{}, err
	}
	if resp.ElementID == "" {
		return wxElementRef{}, &ToolError{
			Code:    ErrElementNotFound,
			Op:      "Page.getElement",
			Message: fmt.Sprintf("element not found: selector=%q", selector),
		}
	}
	return wxElementRef{
		ElementID: resp.ElementID,
		TagName:   resp.TagName,
		NodeID:    resp.NodeID,
		VideoID:   resp.VideoID,
	}, nil
}

// automatorGetElements calls Page.getElements and returns all matching elements.
func automatorGetElements(pageID int, selector string) ([]wxElementRef, error) {
	ctx, cancel := defaultAutomatorCtx()
	defer cancel()
	var resp wxGetElementsResp
	if err := automatorCallContext(ctx, "Page.getElements", map[string]any{
		"pageId":   pageID,
		"selector": selector,
	}, &resp); err != nil {
		return nil, err
	}
	refs := make([]wxElementRef, len(resp.Elements))
	for i, e := range resp.Elements {
		refs[i] = wxElementRef{
			ElementID: e.ElementID,
			TagName:   e.TagName,
			NodeID:    e.NodeID,
			VideoID:   e.VideoID,
		}
	}
	return refs, nil
}

// automatorTapElement calls Element.tap with the correct elementId + pageId.
func automatorTapElement(pageID int, elem wxElementRef) error {
	ctx, cancel := defaultAutomatorCtx()
	defer cancel()
	return automatorCallContext(ctx, "Element.tap", elem.elementParams(pageID), nil)
}

// automatorFillElement calls Element.callFunction with the correct
// functionName for input/textarea elements. Rejects non-input/textarea
// tags with an actionable error.
func automatorFillElement(pageID int, elem wxElementRef, value string) error {
	tag := elem.TagName
	var functionName string
	switch tag {
	case "input":
		functionName = "input.input"
	case "textarea":
		functionName = "textarea.input"
	default:
		return &ToolError{
			Code:    ErrInvalidInput,
			Op:      "fill",
			Message: fmt.Sprintf("fill supports input/textarea elements, got tag=%q (elementId=%s)", tag, elem.ElementID),
		}
	}
	params := elem.elementParams(pageID)
	params["functionName"] = functionName
	params["args"] = []any{value}
	var resp wxCallResultResp
	ctx, cancel := defaultAutomatorCtx()
	defer cancel()
	return automatorCallContext(ctx, "Element.callFunction", params, &resp)
}

// automatorPageData calls Page.getData, unwraps the {data: ...} envelope,
// and returns the inner data value.
func automatorPageData(pageID int, dataPath string) (any, error) {
	ctx, cancel := defaultAutomatorCtx()
	defer cancel()
	params := map[string]any{"pageId": pageID}
	if dataPath != "" {
		params["path"] = dataPath
	}
	var resp wxGetDataResp
	if err := automatorCallContext(ctx, "Page.getData", params, &resp); err != nil {
		return nil, err
	}
	if len(resp.Data) == 0 {
		return nil, nil
	}
	var data any
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return nil, fmt.Errorf("decode Page.getData data: %w", err)
	}
	return data, nil
}

// automatorCallWxMethod calls App.callWxMethod, unwraps the {result: ...}
// envelope, and returns the inner result.
func automatorCallWxMethod(method string, args []any) (any, error) {
	ctx, cancel := defaultAutomatorCtx()
	defer cancel()
	if args == nil {
		args = []any{}
	}
	var resp wxCallResultResp
	if err := automatorCallContext(ctx, "App.callWxMethod", map[string]any{
		"method": method,
		"args":   args,
	}, &resp); err != nil {
		return nil, err
	}
	if len(resp.Result) == 0 {
		return nil, nil
	}
	var result any
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("decode App.callWxMethod result: %w", err)
	}
	return result, nil
}

// automatorEvaluate calls App.callFunction with functionDeclaration (not
// "function"), unwraps the {result: ...} envelope, and returns the inner result.
func automatorEvaluate(script string) (any, error) {
	ctx, cancel := defaultAutomatorCtx()
	defer cancel()
	var resp wxCallResultResp
	if err := automatorCallContext(ctx, "App.callFunction", map[string]any{
		"functionDeclaration": script,
		"args":                []any{},
	}, &resp); err != nil {
		return nil, err
	}
	if len(resp.Result) == 0 {
		return nil, nil
	}
	var result any
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("decode App.callFunction result: %w", err)
	}
	return result, nil
}

// automatorCurrentPage calls App.getCurrentPage and returns (path, pageID).
func automatorCurrentPage() (path string, pageID int, err error) {
	ctx, cancel := defaultAutomatorCtx()
	defer cancel()
	var resp wxCurrentPageResp
	if err := automatorCallContext(ctx, "App.getCurrentPage", map[string]any{}, &resp); err != nil {
		return "", 0, err
	}
	return resp.Path, resp.PageID, nil
}

// automatorPageStack calls App.getPageStack and returns the stack.
func automatorPageStack() ([]wxCurrentPageResp, error) {
	ctx, cancel := defaultAutomatorCtx()
	defer cancel()
	var resp wxPageStackResp
	if err := automatorCallContext(ctx, "App.getPageStack", map[string]any{}, &resp); err != nil {
		return nil, err
	}
	return resp.PageStack, nil
}

// automatorScreenshot calls App.captureScreenshot and returns the PNG bytes.
func automatorScreenshot() ([]byte, error) {
	ctx, cancel := defaultAutomatorCtx()
	defer cancel()
	var resp wxScreenshotResp
	if err := automatorCallContext(ctx, "App.captureScreenshot", map[string]any{}, &resp); err != nil {
		return nil, err
	}
	return decodeScreenshotBase64(resp.Data)
}

// automatorGetWXML calls Element.getWXML for the given element.
func automatorGetWXML(pageID int, elem wxElementRef, wxmlType string) (string, error) {
	ctx, cancel := defaultAutomatorCtx()
	defer cancel()
	if wxmlType == "" {
		wxmlType = "inner"
	}
	params := elem.elementParams(pageID)
	params["type"] = wxmlType
	var resp wxGetWXMLResp
	if err := automatorCallContext(ctx, "Element.getWXML", params, &resp); err != nil {
		return "", err
	}
	return resp.WXML, nil
}

// automatorGetAttributes calls Element.getAttributes for the given element.
// Returns a map of attribute name to value (e.g., "class", "id", "disabled",
// "value", "data-*"). This is the primary source of element metadata for
// snapshot enrichment.
func automatorGetAttributes(pageID int, elem wxElementRef) (map[string]string, error) {
	ctx, cancel := defaultAutomatorCtx()
	defer cancel()
	params := elem.elementParams(pageID)
	var resp wxGetAttributesResp
	if err := automatorCallContext(ctx, "Element.getAttributes", params, &resp); err != nil {
		return nil, err
	}
	return resp.Attributes, nil
}

// automatorEnableLog calls App.enableLog to start log event subscription.
func automatorEnableLog() error {
	ctx, cancel := defaultAutomatorCtx()
	defer cancel()
	return automatorCallContext(ctx, "App.enableLog", map[string]any{}, nil)
}

// --- Navigation ---

// wxNavMode is a typed Mini Program navigation mode.
type wxNavMode string

const (
	wxNavRelaunch     wxNavMode = "reLaunch"
	wxNavNavigateTo   wxNavMode = "navigateTo"
	wxNavRedirectTo   wxNavMode = "redirectTo"
	wxNavSwitchTab    wxNavMode = "switchTab"
	wxNavNavigateBack wxNavMode = "navigateBack"
)

// validNavModes is the set of accepted navigation modes.
var validNavModes = map[wxNavMode]bool{
	wxNavRelaunch: true, wxNavNavigateTo: true, wxNavRedirectTo: true,
	wxNavSwitchTab: true, wxNavNavigateBack: true,
}

// automatorNavigateTyped performs a Mini Program navigation with a typed
// mode. It calls App.callWxMethod(method, {url}), waits for bounded
// settlement, and re-reads the current page. Returns before/after route.
// An empty mode defaults to reLaunch. An invalid mode returns ErrInvalidInput.
func automatorNavigateTyped(modeStr, url string) (before, after string, err error) {
	mode := wxNavMode(modeStr)
	if mode == "" {
		mode = wxNavRelaunch
	}
	if !validNavModes[mode] {
		return "", "", &ToolError{
			Code:    ErrInvalidInput,
			Op:      "navigate",
			Message: fmt.Sprintf("invalid navigation mode %q (valid: reLaunch, navigateTo, redirectTo, switchTab, navigateBack)", modeStr),
		}
	}
	// Read before-route.
	before, _, err = automatorCurrentPage()
	if err != nil {
		return "", "", fmt.Errorf("read before-route: %w", err)
	}
	// callWxMethod with the navigation method.
	args := []any{}
	if mode != wxNavNavigateBack {
		if url == "" {
			return "", "", &ToolError{
				Code:    ErrInvalidInput,
				Op:      "navigate",
				Message: fmt.Sprintf("navigate mode %q requires url", mode),
			}
		}
		// WeChat Mini Program navigation APIs require a leading slash.
		// Normalize so agents can pass the path from status/snapshot directly.
		if !strings.HasPrefix(url, "/") {
			url = "/" + url
		}
		args = []any{map[string]any{"url": url}}
	}
	if _, err := automatorCallWxMethod(string(mode), args); err != nil {
		return "", "", fmt.Errorf("callWxMethod %s: %w", mode, err)
	}
	// Bounded settlement: wait and re-read the current page.
	// The official SDK waits 3s; we use a bounded poll that returns as
	// soon as the route changes or a short quiet period elapses.
	after, err = waitForRouteSettlement(before)
	if err != nil {
		return before, "", fmt.Errorf("route settlement: %w", err)
	}
	// Invalidate all refs after successful navigation. This is critical
	// because reLaunch/redirectTo may reuse the same numeric pageID for
	// the new page, which would allow old refs to silently resolve against
	// the wrong page. Invalidating all refs forces the agent to re-snapshot
	// after any navigation, which is the correct semantic loop.
	wxRefs.invalidateAll()
	return before, after, nil
}

// waitForRouteSettlement polls App.getCurrentPage until the route changes
// or a bounded timeout elapses. Returns the final route.
func waitForRouteSettlement(beforeRoute string) (string, error) {
	const (
		maxWait   = 5 * time.Second
		pollEvery = 200 * time.Millisecond
	)
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		path, _, err := automatorCurrentPage()
		if err != nil {
			return "", err
		}
		if path != beforeRoute {
			return path, nil
		}
		time.Sleep(pollEvery)
	}
	// Route didn't change — return current route as-is (may be same-page nav).
	path, _, err := automatorCurrentPage()
	if err != nil {
		return "", err
	}
	return path, nil
}
