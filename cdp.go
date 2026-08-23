package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// --- CDP types ---

type browserCDPVersionInfo struct {
	Browser              string `json:"Browser"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type browserTarget struct {
	Description          string `json:"description,omitempty"`
	DevtoolsFrontendURL  string `json:"devtoolsFrontendUrl,omitempty"`
	ID                   string `json:"id"`
	ParentID             string `json:"parentId,omitempty"`
	Title                string `json:"title,omitempty"`
	Type                 string `json:"type,omitempty"`
	URL                  string `json:"url,omitempty"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl,omitempty"`
}

type browserCDPEnvelope struct {
	ID     int              `json:"id,omitempty"`
	Method string           `json:"method,omitempty"`
	Params json.RawMessage  `json:"params,omitempty"`
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *browserCDPError `json:"error,omitempty"`
}

type browserCDPError struct {
	Message string `json:"message,omitempty"`
}

type browserRuntimeEvaluateResult struct {
	Result           browserRuntimeRemoteObject      `json:"result"`
	ExceptionDetails *browserRuntimeExceptionDetails `json:"exceptionDetails,omitempty"`
}

type browserRuntimeRemoteObject struct {
	Type        string `json:"type,omitempty"`
	Subtype     string `json:"subtype,omitempty"`
	Value       any    `json:"value,omitempty"`
	Description string `json:"description,omitempty"`
}

type browserRuntimeExceptionDetails struct {
	Text      string `json:"text,omitempty"`
	Exception struct {
		Description string `json:"description,omitempty"`
		Value       any    `json:"value,omitempty"`
	} `json:"exception"`
}

// --- CDP HTTP endpoints ---

// cdpHTTPClient is used for all CDP HTTP requests with a timeout.
// No CDP HTTP call should ever hang indefinitely.
var cdpHTTPClient = &http.Client{Timeout: 5 * time.Second}

func cdpVersion() (browserCDPVersionInfo, error) {
	resp, err := cdpHTTPClient.Get(cdpBaseURL() + "/json/version")
	if err != nil {
		return browserCDPVersionInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return browserCDPVersionInfo{}, fmt.Errorf("browser version request failed: %s", resp.Status)
	}
	var version browserCDPVersionInfo
	if err := json.NewDecoder(resp.Body).Decode(&version); err != nil {
		return browserCDPVersionInfo{}, err
	}
	if strings.TrimSpace(version.WebSocketDebuggerURL) == "" {
		return browserCDPVersionInfo{}, fmt.Errorf("browser websocket debugger URL missing")
	}
	return version, nil
}

func cdpTargets() ([]browserTarget, error) {
	resp, err := cdpHTTPClient.Get(cdpBaseURL() + "/json/list")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("browser target list request failed: %s", resp.Status)
	}
	var targets []browserTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func cdpTargetForURL(targetURL string) (*browserTarget, error) {
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return nil, nil
	}
	targets, err := cdpTargets()
	if err != nil {
		return nil, err
	}
	for _, target := range targets {
		if target.Type != "page" || strings.TrimSpace(target.ParentID) != "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(target.URL), targetURL) {
			copy := target
			return &copy, nil
		}
	}
	return nil, nil
}

// --- CDP WebSocket request (uses connection pool) ---

func cdpRequest(wsURL, method string, params any, result any) error {
	conn, err := getConn(wsURL)
	if err != nil {
		return err
	}
	return conn.call(method, params, result, 10*time.Second)
}

func cdpEvaluate(pageWSURL, expression string, awaitPromise bool) (any, error) {
	var result browserRuntimeEvaluateResult
	if err := cdpRequest(pageWSURL, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  awaitPromise,
	}, &result); err != nil {
		return nil, err
	}
	if result.ExceptionDetails != nil {
		return nil, fmt.Errorf("browser evaluation failed: %s", exceptionText(result.ExceptionDetails))
	}
	if result.Result.Value != nil {
		return result.Result.Value, nil
	}
	return map[string]any{"type": result.Result.Type, "subtype": result.Result.Subtype, "description": result.Result.Description}, nil
}

func exceptionText(details *browserRuntimeExceptionDetails) string {
	if details == nil {
		return "unknown exception"
	}
	return firstNonEmpty(strings.TrimSpace(details.Exception.Description), strings.TrimSpace(details.Text), fmt.Sprint(details.Exception.Value), "unknown exception")
}

// --- CDP target management ---

func cdpCreateTarget(browserWSURL, targetURL string, background bool) (string, error) {
	var result struct {
		TargetID string `json:"targetId"`
	}
	if err := cdpRequest(browserWSURL, "Target.createTarget", map[string]any{
		"url":        targetURL,
		"background": background,
	}, &result); err != nil {
		return "", err
	}
	return strings.TrimSpace(result.TargetID), nil
}

func cdpCloseTarget(targetID string) error {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return nil
	}
	version, err := cdpVersion()
	if err != nil {
		return err
	}
	return cdpRequest(version.WebSocketDebuggerURL, "Target.closeTarget", map[string]any{"targetId": targetID}, nil)
}

func cdpActivateTarget(targetID string) error {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return nil
	}
	version, err := cdpVersion()
	if err != nil {
		return err
	}
	return cdpRequest(version.WebSocketDebuggerURL, "Target.activateTarget", map[string]any{"targetId": targetID}, nil)
}

func cdpPageReload(pageWSURL string, ignoreCache bool) error {
	params := map[string]any{}
	if ignoreCache {
		params["ignoreCache"] = true
	}
	return cdpRequest(pageWSURL, "Page.reload", params, nil)
}

func cdpCaptureScreenshot(pageWSURL string, fullPage bool) ([]byte, error) {
	var result struct {
		Data string `json:"data"`
	}
	params := map[string]any{
		"format": "png",
	}
	if fullPage {
		params["captureBeyondViewport"] = true
	}
	if err := cdpRequest(pageWSURL, "Page.captureScreenshot", params, &result); err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.Data) == "" {
		return nil, fmt.Errorf("browser returned an empty screenshot")
	}
	return base64.StdEncoding.DecodeString(result.Data)
}

// --- CDP input dispatch ---

func cdpDispatchMouseClick(pageWSURL string, x, y float64, button string, clickCount int) error {
	params := map[string]any{"x": x, "y": y, "button": button, "clickCount": clickCount}
	pressed := cloneMap(params)
	pressed["type"] = "mousePressed"
	if err := cdpRequest(pageWSURL, "Input.dispatchMouseEvent", pressed, nil); err != nil {
		return err
	}
	released := cloneMap(params)
	released["type"] = "mouseReleased"
	return cdpRequest(pageWSURL, "Input.dispatchMouseEvent", released, nil)
}

func cdpDispatchMouseHover(pageWSURL string, x, y float64) error {
	moved := map[string]any{"type": "mouseMoved", "x": x, "y": y}
	if err := cdpRequest(pageWSURL, "Input.dispatchMouseEvent", moved, nil); err != nil {
		return err
	}
	return nil
}

func cdpDispatchScroll(pageWSURL string, x, y, deltaX, deltaY float64) (any, error) {
	return cdpEvaluate(pageWSURL, fmt.Sprintf(`(() => {
const x = %s;
const y = %s;
const deltaX = %s;
const deltaY = %s;
const target = document.elementFromPoint(x, y) || document.scrollingElement || document.body;
const event = new WheelEvent('wheel', {deltaX, deltaY, clientX: x, clientY: y, bubbles: true, cancelable: true});
const defaultAllowed = target.dispatchEvent(event);
if (defaultAllowed) window.scrollBy(deltaX, deltaY);
return {defaultAllowed, scrollX: window.scrollX, scrollY: window.scrollY};
})()`, jsNumber(x), jsNumber(y), jsNumber(deltaX), jsNumber(deltaY)), true)
}

func cdpInsertText(pageWSURL, text string) error {
	return cdpRequest(pageWSURL, "Input.insertText", map[string]any{"text": text}, nil)
}

func cdpDispatchKey(pageWSURL, key string) error {
	info := keyInfo(key)
	down := map[string]any{"type": "keyDown", "key": info.Key, "code": info.Code, "windowsVirtualKeyCode": info.VirtualKeyCode}
	if info.Text != "" {
		down["text"] = info.Text
		down["unmodifiedText"] = info.Text
	}
	if err := cdpRequest(pageWSURL, "Input.dispatchKeyEvent", down, nil); err != nil {
		return err
	}
	up := map[string]any{"type": "keyUp", "key": info.Key, "code": info.Code, "windowsVirtualKeyCode": info.VirtualKeyCode}
	return cdpRequest(pageWSURL, "Input.dispatchKeyEvent", up, nil)
}

type browserKey struct {
	Key            string
	Code           string
	VirtualKeyCode int
	Text           string
}

func keyInfo(key string) browserKey {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "enter", "return":
		return browserKey{Key: "Enter", Code: "Enter", VirtualKeyCode: 13, Text: "\r"}
	case "escape", "esc":
		return browserKey{Key: "Escape", Code: "Escape", VirtualKeyCode: 27}
	case "tab":
		return browserKey{Key: "Tab", Code: "Tab", VirtualKeyCode: 9, Text: "\t"}
	case "backspace":
		return browserKey{Key: "Backspace", Code: "Backspace", VirtualKeyCode: 8}
	case "delete":
		return browserKey{Key: "Delete", Code: "Delete", VirtualKeyCode: 46}
	case "arrowleft", "left":
		return browserKey{Key: "ArrowLeft", Code: "ArrowLeft", VirtualKeyCode: 37}
	case "arrowup", "up":
		return browserKey{Key: "ArrowUp", Code: "ArrowUp", VirtualKeyCode: 38}
	case "arrowright", "right":
		return browserKey{Key: "ArrowRight", Code: "ArrowRight", VirtualKeyCode: 39}
	case "arrowdown", "down":
		return browserKey{Key: "ArrowDown", Code: "ArrowDown", VirtualKeyCode: 40}
	case "home":
		return browserKey{Key: "Home", Code: "Home", VirtualKeyCode: 36}
	case "end":
		return browserKey{Key: "End", Code: "End", VirtualKeyCode: 35}
	case "pageup":
		return browserKey{Key: "PageUp", Code: "PageUp", VirtualKeyCode: 33}
	case "pagedown":
		return browserKey{Key: "PageDown", Code: "PageDown", VirtualKeyCode: 34}
	case "space":
		return browserKey{Key: " ", Code: "Space", VirtualKeyCode: 32, Text: " "}
	}
	if len([]rune(key)) == 1 {
		r := []rune(key)[0]
		code := ""
		vk := int(r)
		if r >= 'a' && r <= 'z' {
			code = "Key" + strings.ToUpper(string(r))
			vk = int(r - 32)
		} else if r >= 'A' && r <= 'Z' {
			code = "Key" + string(r)
		} else if r >= '0' && r <= '9' {
			code = "Digit" + string(r)
		}
		return browserKey{Key: key, Code: code, VirtualKeyCode: vk, Text: key}
	}
	return browserKey{Key: key, Code: key, VirtualKeyCode: 0}
}

// --- viewport / coordinate helpers ---

func viewportMetrics(pageWSURL string) (map[string]any, error) {
	value, err := cdpEvaluate(pageWSURL, `(() => ({width: innerWidth, height: innerHeight, devicePixelRatio: devicePixelRatio || 1, scrollX, scrollY, visibilityState: document.visibilityState}))()`, true)
	if err != nil {
		return nil, err
	}
	metrics, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unable to read viewport metrics")
	}
	return metrics, nil
}

func imageToViewportScale(viewport map[string]any, imageWidth, imageHeight int) map[string]any {
	if viewport == nil || imageWidth <= 0 || imageHeight <= 0 {
		return nil
	}
	viewportWidth, okWidth := numberValue(viewport["width"])
	viewportHeight, okHeight := numberValue(viewport["height"])
	if !okWidth || !okHeight || viewportWidth <= 0 || viewportHeight <= 0 {
		return nil
	}
	return map[string]any{
		"x_multiplier": viewportWidth / float64(imageWidth),
		"y_multiplier": viewportHeight / float64(imageHeight),
		"formula":      "viewport_x = image_x * x_multiplier; viewport_y = image_y * y_multiplier",
	}
}

func pointFromImageCoordinates(pageWSURL string, imageX, imageY, imageWidth, imageHeight *float64, screenshotPath string) (float64, float64, bool, error) {
	if imageX == nil || imageY == nil {
		return 0, 0, false, nil
	}
	width, height := 0.0, 0.0
	if imageWidth != nil && imageHeight != nil && *imageWidth > 0 && *imageHeight > 0 {
		width = *imageWidth
		height = *imageHeight
	} else if strings.TrimSpace(screenshotPath) != "" {
		data, err := os.ReadFile(strings.TrimSpace(screenshotPath))
		if err != nil {
			return 0, 0, true, err
		}
		imageW, imageH, ok := imageDimensions(data)
		if !ok {
			return 0, 0, true, fmt.Errorf("unable to read screenshot dimensions")
		}
		width = float64(imageW)
		height = float64(imageH)
	}
	if width <= 0 || height <= 0 {
		return *imageX, *imageY, true, nil
	}
	viewport, err := viewportMetrics(pageWSURL)
	if err != nil {
		return 0, 0, true, err
	}
	viewportWidth, okWidth := numberValue(viewport["width"])
	viewportHeight, okHeight := numberValue(viewport["height"])
	if !okWidth || !okHeight || viewportWidth <= 0 || viewportHeight <= 0 {
		return 0, 0, true, fmt.Errorf("unable to read viewport dimensions")
	}
	return (*imageX) * viewportWidth / width, (*imageY) * viewportHeight / height, true, nil
}

// --- log collection ---

func collectLogs(ctx context.Context, pageWSURL string, duration time.Duration) (entries []map[string]any, err error) {
	defer func() {
		if r := recover(); r != nil {
			logWarn("collectLogs panic: %v", r)
			err = fmt.Errorf("collectLogs crashed: %v", r)
		}
	}()
	conn, _, err := websocket.DefaultDialer.Dial(pageWSURL, nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	// Close connection on context cancellation
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-time.After(duration + time.Second):
		}
	}()
	deadline := time.Now().Add(duration)
	if err := conn.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	for id, method := range []string{"Runtime.enable", "Log.enable", "Console.enable"} {
		if err := conn.WriteJSON(map[string]any{"id": id + 1, "method": method, "params": map[string]any{}}); err != nil {
			return nil, err
		}
	}
	entries = []map[string]any{}
	for {
		var envelope browserCDPEnvelope
		if err := conn.ReadJSON(&envelope); err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return entries, nil
			}
			return entries, err
		}
		switch envelope.Method {
		case "Runtime.consoleAPICalled":
			var params struct {
				Type string `json:"type"`
				Args []struct {
					Type        string `json:"type"`
					Value       any    `json:"value,omitempty"`
					Description string `json:"description,omitempty"`
				} `json:"args"`
			}
			if err := json.Unmarshal(envelope.Params, &params); err == nil {
				entries = append(entries, map[string]any{"source": "console", "level": firstNonEmpty(params.Type, "log"), "text": consoleArgsText(params.Args)})
			}
		case "Runtime.exceptionThrown":
			var params struct {
				ExceptionDetails browserRuntimeExceptionDetails `json:"exceptionDetails"`
			}
			if err := json.Unmarshal(envelope.Params, &params); err == nil {
				entries = append(entries, map[string]any{"source": "exception", "level": "error", "text": exceptionText(&params.ExceptionDetails)})
			}
		case "Log.entryAdded":
			var params struct {
				Entry struct {
					Source string `json:"source"`
					Level  string `json:"level"`
					Text   string `json:"text"`
				} `json:"entry"`
			}
			if err := json.Unmarshal(envelope.Params, &params); err == nil {
				entries = append(entries, map[string]any{"source": firstNonEmpty(params.Entry.Source, "log"), "level": firstNonEmpty(params.Entry.Level, "info"), "text": params.Entry.Text})
			}
		}
	}
}

func prepareLogEntries(entries []map[string]any, all bool, limit, maxChars int) ([]map[string]any, int) {
	if limit == 0 {
		limit = 40
	}
	if limit < 0 {
		limit = 0
	}
	if maxChars == 0 {
		maxChars = 600
	}
	if maxChars < 0 {
		maxChars = 0
	}
	prepared := []map[string]any{}
	indexes := map[string]int{}
	counts := map[string]int{}
	for _, entry := range entries {
		if !all && !logEntryImportant(entry) {
			continue
		}
		text := logString(entry, "text")
		key := logString(entry, "source") + "\x00" + logString(entry, "level") + "\x00" + text
		if index, ok := indexes[key]; ok {
			counts[key]++
			prepared[index]["count"] = counts[key]
			continue
		}
		copy := cloneMap(entry)
		if maxChars > 0 {
			copy["text"] = truncateRunes(text, maxChars)
		}
		indexes[key] = len(prepared)
		counts[key] = 1
		prepared = append(prepared, copy)
	}
	omitted := 0
	if limit > 0 && len(prepared) > limit {
		omitted = len(prepared) - limit
		prepared = prepared[:limit]
	}
	return prepared, omitted
}

func logEntryImportant(entry map[string]any) bool {
	source := strings.ToLower(logString(entry, "source"))
	level := strings.ToLower(logString(entry, "level"))
	text := strings.ToLower(logString(entry, "text"))
	if logLooksLikeStackFrame(text) {
		return false
	}
	if source == "exception" || level == "warning" || level == "error" || level == "fatal" {
		return true
	}
	for _, fragment := range []string{"exception", "error", "failed", "invalid argument", "boxconstraints", "overflowed", "file://"} {
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
}

func logLooksLikeStackFrame(text string) bool {
	text = strings.TrimSpace(text)
	if strings.Contains(text, "this was the stack") {
		return true
	}
	for _, prefix := range []string{"dart-sdk/", "package:flutter/", "package:collection/", "lib/_engine/"} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func logString(entry map[string]any, key string) string {
	if entry == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(entry[key]))
}
