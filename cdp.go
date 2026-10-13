package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
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

// isAnti9224BarrierActive reports whether the anti-9224 security barrier is active.
// Active in test mode (testing.Testing()) or when explicitly requested via env.
func isAnti9224BarrierActive() bool {
	if testing.Testing() {
		return true
	}
	if os.Getenv("ARGUS_STRICT_ISOLATION") == "1" || os.Getenv("ARGUS_FORBID_PORT_9224") == "1" {
		return true
	}
	return false
}

var (
	portColonRegex   = regexp.MustCompile(`:([0-9]+)`)
	ipv6BracketRegex = regexp.MustCompile(`\[.*?\]`)
)

func isPort9224Target(target string, ports ...int) bool {
	for _, p := range ports {
		if p == 9224 {
			return true
		}
	}

	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return false
	}

	// Direct numeric string check (e.g. "9224", "09224", "009224")
	if p, err := strconv.Atoi(trimmed); err == nil && p == 9224 {
		return true
	}

	// 1. If target has a scheme (http://, https://, ws://, wss://, tcp://, etc.), parse via url.Parse
	if strings.Contains(trimmed, "://") {
		if u, err := url.Parse(trimmed); err == nil && u.Host != "" {
			if portStr := u.Port(); portStr != "" {
				if p, err := strconv.Atoi(portStr); err == nil && p == 9224 {
					return true
				}
				// Explicitly targets a non-9224 port
				return false
			}
			// Default scheme ports (80, 443) are not 9224
			return false
		}
	}

	// 2. Try net.SplitHostPort (covers host:port, [ipv6]:port, :port)
	if _, portStr, err := net.SplitHostPort(trimmed); err == nil {
		if p, err := strconv.Atoi(portStr); err == nil {
			if p == 9224 {
				return true
			}
			return false
		}
	}

	// 3. Schemeless target with path/query (e.g. "127.0.0.1:09224/json/version")
	if u, err := url.Parse("http://" + trimmed); err == nil && u.Host != "" {
		if portStr := u.Port(); portStr != "" {
			if p, err := strconv.Atoi(portStr); err == nil && p == 9224 {
				return true
			}
			return false
		}
	}

	// 4. Fallback: extract any :<digits> outside IPv6 brackets
	cleaned := ipv6BracketRegex.ReplaceAllString(trimmed, "")
	for _, m := range portColonRegex.FindAllStringSubmatch(cleaned, -1) {
		if len(m) > 1 {
			if p, err := strconv.Atoi(m[1]); err == nil && p == 9224 {
				return true
			}
		}
	}

	return false
}

// assertNotPort9224 panics immediately if host port 9224 is targeted during tests.
func assertNotPort9224(target string, ports ...int) {
	if !isAnti9224BarrierActive() {
		return
	}
	if isPort9224Target(target, ports...) {
		panic(fmt.Sprintf("CRITICAL SAFETY VIOLATION: Anti-9224 barrier triggered: test attempted connection to forbidden host CDP port 9224 (target=%s, ports=%v)", target, ports))
	}
}

type anti9224Transport struct {
	base http.RoundTripper
}

func (t *anti9224Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req != nil && req.URL != nil {
		assertNotPort9224(req.URL.String())
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

// cdpHTTPClient is used for all CDP HTTP requests with a timeout and Anti-9224 barrier.
var cdpHTTPClient = &http.Client{
	Timeout:   5 * time.Second,
	Transport: &anti9224Transport{base: http.DefaultTransport},
}

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
	targets, err := listCDPTargets()
	_, _, isAttach := getConfig()
	if err != nil && isAttach {
		// Attach mode: the configured browser may simply not be running.
		// Try to launch Chrome Argus once and give it a moment to come up.
		tryAutostartBrowser()
		targets, err = listCDPTargets()
	}
	return targets, err
}

func listCDPTargets() ([]browserTarget, error) {
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

var autostartBrowserOnce sync.Once

// tryAutostartBrowser launches Chrome when the attach endpoint is dead,
// then polls the CDP target list until it responds or ~8s elapse.
func tryAutostartBrowser() {
	autostartBrowserOnce.Do(func() {
		logInfo("attach target unreachable on :%d — attempting to launch Chrome", cdpPort)
		if err := launchChrome(); err != nil {
			logWarn("autostart launch failed: %v", err)
			return
		}
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := listCDPTargets(); err == nil {
				logInfo("autostart: CDP endpoint up on :%d", cdpPort)
				return
			}
			time.Sleep(300 * time.Millisecond)
		}
		logWarn("autostart: CDP endpoint still unreachable after 8s")
	})
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
	return cdpRequestTimeout(wsURL, method, params, result, 10*time.Second)
}

func cdpRequestTimeout(wsURL, method string, params any, result any, timeout time.Duration) error {
	conn, err := getConn(wsURL)
	if err != nil {
		return err
	}
	return conn.call(method, params, result, timeout)
}

func cdpEvaluate(pageWSURL, expression string, awaitPromise bool) (any, error) {
	return cdpEvaluateTimeout(pageWSURL, expression, awaitPromise, 10*time.Second)
}

func cdpEvaluateTimeout(pageWSURL, expression string, awaitPromise bool, timeout time.Duration) (any, error) {
	if isPageEndpoint(pageWSURL) {
		targetID := targetIDFromWSURL(pageWSURL)
		if dialogMgr.HasPendingDialog(targetID) {
			d := dialogMgr.GetPendingDialog(targetID)
			return nil, fmt.Errorf("cannot evaluate JavaScript: dialog '%s' is open (%q); accept or dismiss with handle_dialog first", d.Type, d.Message)
		}
	}
	var result browserRuntimeEvaluateResult
	if err := cdpRequestTimeout(pageWSURL, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  awaitPromise,
	}, &result, timeout); err != nil {
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
	tabID := strings.TrimSpace(result.TargetID)
	beaconRegisterTab(tabID, targetURL)
	return tabID, nil
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
	err = cdpRequest(version.WebSocketDebuggerURL, "Target.closeTarget", map[string]any{"targetId": targetID}, nil)
	if err == nil {
		beaconUnregisterTab(targetID)
	}
	return err
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

// cdpPageNavigate navigates via Page.navigate (not location.href eval) so
// errorText (net::ERR_*) is available for error classification, then waits
// for Page.loadEventFired up to loadTimeout. A timed-out load is reported
// in the result, not returned as an error.
func cdpPageNavigate(pageWSURL, url string, loadTimeout time.Duration) (map[string]any, error) {
	conn, err := getConn(pageWSURL)
	if err != nil {
		return nil, err
	}
	sub := conn.subscribeEvents("navigate", 16, false)
	defer conn.hub.unsubscribe(sub)
	_ = cdpRequest(pageWSURL, "Page.enable", map[string]any{}, nil)
	var nav struct {
		FrameID   string `json:"frameId"`
		LoaderID  string `json:"loaderId"`
		ErrorText string `json:"errorText"`
	}
	if err := cdpRequest(pageWSURL, "Page.navigate", map[string]any{"url": url}, &nav); err != nil {
		return nil, err
	}
	if strings.TrimSpace(nav.ErrorText) != "" {
		return nil, fmt.Errorf("%s", nav.ErrorText)
	}
	result := map[string]any{"frame_id": nav.FrameID}
	if nav.LoaderID != "" {
		result["loader_id"] = nav.LoaderID
	}
	if loadTimeout <= 0 {
		loadTimeout = 15 * time.Second
	}
	timer := time.NewTimer(loadTimeout)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-sub.ch:
			if !ok || ev == nil {
				return result, nil
			}
			if ev.Method == "Page.loadEventFired" {
				result["loaded"] = true
				return result, nil
			}
		case <-timer.C:
			result["load_timed_out"] = true
			return result, nil
		}
	}
}

// ScreenshotClip defines the clipping bounding box for Page.captureScreenshot.
type ScreenshotClip struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Scale  float64 `json:"scale"`
}

func cdpCaptureScreenshotWithClip(pageWSURL string, fullPage bool, clip *ScreenshotClip) ([]byte, error) {
	var result struct {
		Data string `json:"data"`
	}
	params := map[string]any{
		"format": "png",
	}
	if fullPage || clip != nil {
		params["captureBeyondViewport"] = true
	}
	if clip != nil {
		scale := clip.Scale
		if scale <= 0 {
			scale = 1.0
		}
		params["clip"] = map[string]any{
			"x":      clip.X,
			"y":      clip.Y,
			"width":  clip.Width,
			"height": clip.Height,
			"scale":  scale,
		}
	}
	if err := cdpRequest(pageWSURL, "Page.captureScreenshot", params, &result); err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.Data) == "" {
		return nil, fmt.Errorf("browser returned an empty screenshot")
	}
	return base64.StdEncoding.DecodeString(result.Data)
}

func cdpCaptureScreenshot(pageWSURL string, fullPage bool) ([]byte, error) {
	return cdpCaptureScreenshotWithClip(pageWSURL, fullPage, nil)
}

// --- CDP input dispatch ---

// Input delivery modes reported back to callers.
const (
	deliverTrusted = "trusted"      // real Input.dispatch* on a visible page
	deliverJS      = "untrusted_js" // JS event fallback on a hidden page
)

// pageVisibility returns document.visibilityState ("" on error).
func pageVisibility(ws string) string {
	v, err := cdpEvaluateTimeout(ws, "document.visibilityState", false, 3*time.Second)
	if err != nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// ensureInputDelivery picks the input path. Headful Chrome silently drops
// Input.dispatch* events on background tabs (visibilityState != visible),
// so hidden pages must either be brought to front (focus mode only) or take
// the untrusted JS fallback.
func ensureInputDelivery(ws string) string {
	if pageVisibility(ws) == "visible" {
		ledgerStamp(ws)
		return deliverTrusted
	}
	if wantFocus() {
		if targetID := targetIDFromWSURL(ws); targetID != "" {
			if err := cdpActivateTarget(targetID); err == nil && pageVisibility(ws) == "visible" {
				ledgerStamp(ws)
				return deliverTrusted
			}
		}
	}
	return deliverJS
}

// jsHitPickJS picks the element for a JS-dispatched input at (x, y):
// prefer the element argus just resolved (window.__argusHitEl) when its
// rect still contains the point — this preserves shadow-DOM targets that
// elementFromPoint retargets to the host. Otherwise pierce shadow roots.
const jsHitPickJS = `
const argusHitPick = (x, y) => {
  const h = window.__argusHitEl;
  if (h && h.isConnected) {
    const r = h.getBoundingClientRect();
    if (x >= r.left - 4 && x <= r.right + 4 && y >= r.top - 4 && y <= r.bottom + 4) return h;
  }
  let el = document.elementFromPoint(x, y);
  let guard = 0;
  while (el && el.shadowRoot && guard++ < 8) {
    const deeper = el.shadowRoot.elementFromPoint(x, y);
    if (!deeper || deeper === el) break;
    el = deeper;
  }
  return el;
};`

func jsClickAt(ws string, x, y float64, button string, clickCount int) error {
	btn := 0
	switch button {
	case "middle":
		btn = 1
	case "right":
		btn = 2
	}
	expr := fmt.Sprintf(`(() => {
`+ledgerStampJS()+`
%s
const el = argusHitPick(%v, %v);
if (!el) throw new Error('no element at point (%v, %v)');
const Ctor = t => t.startsWith('pointer') ? PointerEvent : MouseEvent;
for (let i = 0; i < %d; i++) {
  for (const t of ['pointerover','mouseover','pointerdown','mousedown','pointerup','mouseup','click']) {
    el.dispatchEvent(new (Ctor(t))(t, {bubbles:true, composed:true, clientX:%v, clientY:%v, button:%d}));
  }
}
return el.tagName + (el.id ? '#'+el.id : '');
})()`, jsHitPickJS, x, y, x, y, clickCount, x, y, btn)
	_, err := cdpEvaluate(ws, expr, true)
	return err
}

func jsMouseMoveAt(ws string, x, y float64) error {
	expr := fmt.Sprintf(`(() => {
`+ledgerStampJS()+`
%s
const el = argusHitPick(%v, %v);
if (!el) return null;
for (const [t, E] of [['pointerover',PointerEvent],['mouseover',MouseEvent],['mouseenter',MouseEvent],['mousemove',MouseEvent]]) {
  el.dispatchEvent(new E(t, {bubbles: t !== 'mouseenter', composed:true, clientX:%v, clientY:%v}));
}
return true;
})()`, jsHitPickJS, x, y, x, y)
	_, err := cdpEvaluate(ws, expr, true)
	return err
}

func jsInsertText(ws, text string) error {
	expr := fmt.Sprintf(`(() => {
`+ledgerStampJS()+`
if (document.execCommand('insertText', false, %s)) return true;
const el = document.activeElement;
if (el && 'value' in el) {
  el.value += %s;
  el.dispatchEvent(new InputEvent('input', {bubbles:true, composed:true, data:%s, inputType:'insertText'}));
  return true;
}
if (el && el.isContentEditable) {
  el.textContent += %s;
  el.dispatchEvent(new InputEvent('input', {bubbles:true, composed:true, data:%s, inputType:'insertText'}));
  return true;
}
throw new Error('no editable target for insertText');
})()`, jsString(text), jsString(text), jsString(text), jsString(text), jsString(text))
	_, err := cdpEvaluate(ws, expr, true)
	return err
}

func jsDispatchKey(ws, key string, modifiers int) error {
	expr := fmt.Sprintf(`(() => {
`+ledgerStampJS()+`
const el = document.activeElement || document.body;
const mods = {altKey: %t, ctrlKey: %t, metaKey: %t, shiftKey: %t};
const key = %s;
const opts = Object.assign({bubbles:true, cancelable:true, composed:true, key}, mods);
el.dispatchEvent(new KeyboardEvent('keydown', opts));
if (key.length === 1) el.dispatchEvent(new KeyboardEvent('keypress', opts));
el.dispatchEvent(new KeyboardEvent('keyup', opts));
if ((key === 'Enter' || key === ' ') && (el.tagName === 'BUTTON' || el.tagName === 'A' || el.getAttribute('role') === 'button' || typeof el.click === 'function')) el.click();
return true;
})()`, modifiers&1 != 0, modifiers&2 != 0, modifiers&4 != 0, modifiers&8 != 0, jsString(key))
	_, err := cdpEvaluate(ws, expr, true)
	return err
}

func cdpDispatchMouseClick(pageWSURL string, x, y float64, button string, clickCount int) (string, error) {
	if ensureInputDelivery(pageWSURL) == deliverJS {
		return deliverJS, jsClickAt(pageWSURL, x, y, button, clickCount)
	}
	params := map[string]any{"x": x, "y": y, "button": button, "clickCount": clickCount}
	pressed := cloneMap(params)
	pressed["type"] = "mousePressed"
	if err := cdpRequest(pageWSURL, "Input.dispatchMouseEvent", pressed, nil); err != nil {
		return deliverTrusted, err
	}
	released := cloneMap(params)
	released["type"] = "mouseReleased"
	return deliverTrusted, cdpRequest(pageWSURL, "Input.dispatchMouseEvent", released, nil)
}

func cdpDispatchMouseHover(pageWSURL string, x, y float64) (string, error) {
	if ensureInputDelivery(pageWSURL) == deliverJS {
		return deliverJS, jsMouseMoveAt(pageWSURL, x, y)
	}
	moved := map[string]any{"type": "mouseMoved", "x": x, "y": y}
	if err := cdpRequest(pageWSURL, "Input.dispatchMouseEvent", moved, nil); err != nil {
		return deliverTrusted, err
	}
	return deliverTrusted, nil
}

func cdpDispatchScroll(pageWSURL string, x, y, deltaX, deltaY float64) (string, any, error) {
	// mouseWheel goes through the real input pipeline: trusted wheel events
	// fire scroll listeners and lazy loaders, unlike synthetic dispatchEvent.
	if ensureInputDelivery(pageWSURL) == deliverJS {
		expr := fmt.Sprintf(`(() => { `+ledgerStampJS()+` window.scrollBy(%v, %v); return {scrollX: window.scrollX, scrollY: window.scrollY}; })()`, deltaX, deltaY)
		v, err := cdpEvaluate(pageWSURL, expr, true)
		return deliverJS, v, err
	}
	if err := cdpRequest(pageWSURL, "Input.dispatchMouseEvent", map[string]any{
		"type": "mouseWheel", "x": x, "y": y, "deltaX": deltaX, "deltaY": deltaY,
	}, nil); err != nil {
		return deliverTrusted, nil, err
	}
	v, err := cdpEvaluate(pageWSURL, `(() => ({scrollX: window.scrollX, scrollY: window.scrollY}))()`, true)
	return deliverTrusted, v, err
}

func cdpInsertText(pageWSURL, text string) (string, error) {
	if ensureInputDelivery(pageWSURL) == deliverJS {
		return deliverJS, jsInsertText(pageWSURL, text)
	}
	return deliverTrusted, cdpRequest(pageWSURL, "Input.insertText", map[string]any{"text": text}, nil)
}

func cdpDispatchKey(pageWSURL, key string, modifiers int) (string, error) {
	if ensureInputDelivery(pageWSURL) == deliverJS {
		return deliverJS, jsDispatchKey(pageWSURL, key, modifiers)
	}
	info := keyInfo(key)
	down := map[string]any{"type": "keyDown", "key": info.Key, "code": info.Code, "windowsVirtualKeyCode": info.VirtualKeyCode, "modifiers": modifiers}
	if info.Text != "" {
		down["text"] = info.Text
		down["unmodifiedText"] = info.Text
	}
	if err := cdpRequest(pageWSURL, "Input.dispatchKeyEvent", down, nil); err != nil {
		return deliverTrusted, err
	}
	up := map[string]any{"type": "keyUp", "key": info.Key, "code": info.Code, "windowsVirtualKeyCode": info.VirtualKeyCode, "modifiers": modifiers}
	return deliverTrusted, cdpRequest(pageWSURL, "Input.dispatchKeyEvent", up, nil)
}

// modifierMask parses "ctrl+shift", "cmd", etc. into the CDP modifiers
// bitmask (Alt=1, Ctrl=2, Meta=4, Shift=8).
func modifierMask(s string) int {
	mask := 0
	for _, tok := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return r == '+' || r == ',' || r == ' '
	}) {
		switch tok {
		case "alt", "option":
			mask |= 1
		case "ctrl", "control":
			mask |= 2
		case "meta", "cmd", "command":
			mask |= 4
		case "shift":
			mask |= 8
		}
	}
	return mask
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
	assertNotPort9224(pageWSURL)
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
