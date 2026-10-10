package main

import (
	"encoding/json"
	"strings"
	"sync"
)

// --- Persistent recorder event subscriber ---
//
// Each cdpConn starts a recorder subscriber that continuously captures
// console, exception, and log events into the flight recorder.
//
// This is a non-critical subscriber (events are dropped if buffer is full,
// not counted as critical like Fetch.requestPaused).
//
// The subscriber enables Runtime.enable and Log.enable on the connection
// so that CDP emits these events. Console.enable is also enabled for
// console messages.
//
// Target ID is extracted from the WebSocket URL path.

// startRecorderSubscriber starts a persistent event subscriber on conn
// that records console, exception, log, network, and navigation events
// to the flight recorder. The subscriber exits when the connection's
// event hub closes. Events are stamped with conn.epoch so pre-reconnect
// IDs cannot be silently joined to post-reconnect IDs.
func startRecorderSubscriber(conn *cdpConn, wsURL string) {
	targetID := shortID(wsURL)

	// Subscribe BEFORE enabling domains to avoid missing early events.
	// If we enable first, console messages from page load could be
	// emitted before the subscriber is registered, causing them to be
	// dropped by the event hub.
	// Buffer size 256: network events can be high-volume, so we use
	// a larger buffer than the previous 128. Drops are still counted.
	sub := conn.subscribeEvents("recorder", 256, false) // non-critical
	go runRecorderSubscriber(sub, targetID, conn.epoch)

	// Enable CDP domains for event capture (after subscriber is ready)
	if err := conn.call("Runtime.enable", nil, nil, 0); err != nil {
		rec.RecordDomainEnableError()
		logWarn("recorder: Runtime.enable failed: %v", err)
	}
	if err := conn.call("Log.enable", nil, nil, 0); err != nil {
		rec.RecordDomainEnableError()
		logWarn("recorder: Log.enable failed: %v", err)
	}
	if err := conn.call("Network.enable", nil, nil, 0); err != nil {
		rec.RecordDomainEnableError()
		logWarn("recorder: Network.enable failed: %v", err)
	}
	if err := conn.call("Page.enable", nil, nil, 0); err != nil {
		rec.RecordDomainEnableError()
		logWarn("recorder: Page.enable failed: %v", err)
	}
}

func runRecorderSubscriber(sub *eventSubscription, targetID string, epoch ConnectionEpoch) {
	defer func() {
		if r := recover(); r != nil {
			logWarn("recorder subscriber panic: %v", r)
		}
	}()

	for envelope := range sub.ch {
		if envelope == nil {
			continue
		}
		switch envelope.Method {
		case "Runtime.consoleAPICalled":
			handleConsoleEvent(envelope, targetID, epoch)
		case "Runtime.exceptionThrown":
			handleExceptionEvent(envelope, targetID, epoch)
		case "Log.entryAdded":
			handleLogEntryEvent(envelope, targetID, epoch)
		case "Network.requestWillBeSent":
			handleNetworkRequestEvent(envelope, targetID, epoch)
		case "Network.responseReceived":
			handleNetworkResponseEvent(envelope, targetID, epoch)
		case "Network.loadingFailed":
			handleNetworkFailedEvent(envelope, targetID, epoch)
		case "Page.frameNavigated":
			handleFrameNavigatedEvent(envelope, targetID, epoch)
		case "Page.frameDetached":
			handleFrameDetachedEvent(envelope, targetID, epoch)
		case "Page.frameStartedLoading":
			handleFrameStartedLoadingEvent(envelope, targetID, epoch)
		case "Page.frameStoppedLoading":
			handleFrameStoppedLoadingEvent(envelope, targetID, epoch)
		case "Page.navigatedWithinDocument":
			handleNavigatedWithinDocumentEvent(envelope, targetID, epoch)
		case "Page.lifecycleEvent":
			handleLifecycleEvent(envelope, targetID, epoch)
		}
	}
}

func handleConsoleEvent(envelope *browserCDPEnvelope, targetID string, epoch ConnectionEpoch) {
	var params struct {
		Type string `json:"type"`
		Args []struct {
			Type        string `json:"type"`
			Value       any    `json:"value,omitempty"`
			Description string `json:"description,omitempty"`
		} `json:"args"`
	}
	if err := json.Unmarshal(envelope.Params, &params); err != nil {
		return
	}
	level := firstNonEmpty(params.Type, "log")
	text := consoleArgsText(params.Args)
	rec.RecordEventWithAction("console", targetID, "", epoch, map[string]any{
		"level":  level,
		"text":   text,
		"source": "console",
	})
}

func handleExceptionEvent(envelope *browserCDPEnvelope, targetID string, epoch ConnectionEpoch) {
	var params struct {
		ExceptionDetails browserRuntimeExceptionDetails `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(envelope.Params, &params); err != nil {
		return
	}
	text := exceptionText(&params.ExceptionDetails)
	rec.RecordEventWithAction("exception", targetID, "", epoch, map[string]any{
		"level":  "error",
		"text":   text,
		"source": "exception",
	})
}

func handleLogEntryEvent(envelope *browserCDPEnvelope, targetID string, epoch ConnectionEpoch) {
	var params struct {
		Entry struct {
			Source string `json:"source"`
			Level  string `json:"level"`
			Text   string `json:"text"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(envelope.Params, &params); err != nil {
		return
	}
	rec.RecordEventWithAction("log", targetID, "", epoch, map[string]any{
		"level":  firstNonEmpty(params.Entry.Level, "info"),
		"text":   params.Entry.Text,
		"source": firstNonEmpty(params.Entry.Source, "log"),
	})
}

// targetIDFromWSURL extracts the target ID from a WebSocket URL.
// e.g. ws://127.0.0.1:9224/devtools/page/ABC123 -> ABC123
func targetIDFromWSURL(wsURL string) string {
	return shortID(wsURL)
}

// ensureRecorderEnabled enables the recorder subscriber on a connection
// if not already enabled. Called when a connection is used for the first time.
func ensureRecorderEnabled(conn *cdpConn, wsURL string) {
	// The recorder subscriber is started in dialConn, so this is a no-op.
	// Kept as a hook for future lazy initialization if needed.
	_ = conn
	_ = wsURL
}

// stripRecorderEvents is a helper to check if a method is one the
// recorder subscriber handles. Used for debugging.
func isRecorderEvent(method string) bool {
	return strings.HasPrefix(method, "Runtime.console") ||
		method == "Runtime.exceptionThrown" ||
		method == "Log.entryAdded" ||
		strings.HasPrefix(method, "Network.") ||
		strings.HasPrefix(method, "Page.")
}

// --- Network event handlers ---

// redirectHopTracker tracks ordered redirect hop counts per loaderID.
// CDP reuses the same loaderId across a redirect chain but assigns
// new requestIds (appending :1, :2, etc.). This map records the
// current hop count for each loader so each hop gets an incrementing
// number instead of just 0/1.
var (
	redirectHopMu sync.Mutex
	redirectHops  = map[string]int{} // loaderID -> current hop count
)

// handleNetworkRequestEvent processes Network.requestWillBeSent.
// This event fires when a new request is initiated. It includes
// the requestId, loaderId, frameId, URL, method, resource type,
// and redirect information.
func handleNetworkRequestEvent(envelope *browserCDPEnvelope, targetID string, epoch ConnectionEpoch) {
	var params struct {
		RequestID string `json:"requestId"`
		LoaderID  string `json:"loaderId"`
		FrameID   string `json:"frameId"`
		Request   struct {
			URL      string            `json:"url"`
			Method   string            `json:"method"`
			Headers  map[string]string `json:"headers"`
			PostData string            `json:"postData,omitempty"`
		} `json:"request"`
		Type             string `json:"type"` // resource type
		RedirectResponse *struct {
			Status     int    `json:"status"`
			StatusText string `json:"statusText"`
			URL        string `json:"url"`
		} `json:"redirectResponse,omitempty"`
	}
	if err := json.Unmarshal(envelope.Params, &params); err != nil {
		return
	}

	// Track ordered redirect hops by loaderID.
	// When redirectResponse is present, this is a redirect hop.
	// When absent, it's the initial request (hop 0) and we reset
	// the counter for this loader.
	redirectHopMu.Lock()
	if len(redirectHops) > 1024 {
		// LoaderIDs accumulate forever otherwise; chains are short-lived,
		// so a full reset is safe.
		redirectHops = map[string]int{}
	}
	hopCount := 0
	if params.RedirectResponse != nil {
		hopCount = redirectHops[params.LoaderID] + 1
		if hopCount > maxRedirectHops {
			hopCount = maxRedirectHops
		}
	}
	// Reset on initial request (no redirect), update on redirect
	redirectHops[params.LoaderID] = hopCount
	redirectHopMu.Unlock()

	evidence := NetworkEvidence{
		RequestID:    params.RequestID,
		LoaderID:     params.LoaderID,
		FrameID:      params.FrameID,
		URL:          sanitizeURL(params.Request.URL),
		Method:       params.Request.Method,
		ResourceType: params.Type,
		RedirectHops: hopCount,
		Headers:      filterHeaders(params.Request.Headers),
		Phase:        "request",
	}

	rec.RecordEventWithAction("network", targetID, "", epoch, evidence)
}

// handleNetworkResponseEvent processes Network.responseReceived.
// This event fires when response headers are available. It includes
// status, statusText, mimeType, and response headers.
func handleNetworkResponseEvent(envelope *browserCDPEnvelope, targetID string, epoch ConnectionEpoch) {
	var params struct {
		RequestID string `json:"requestId"`
		LoaderID  string `json:"loaderId"`
		Response  struct {
			Status     int               `json:"status"`
			StatusText string            `json:"statusText"`
			MimeType   string            `json:"mimeType"`
			URL        string            `json:"url"`
			Headers    map[string]string `json:"headers"`
		} `json:"response"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(envelope.Params, &params); err != nil {
		return
	}

	evidence := NetworkEvidence{
		RequestID:    params.RequestID,
		LoaderID:     params.LoaderID,
		URL:          sanitizeURL(params.Response.URL),
		Status:       params.Response.Status,
		StatusText:   params.Response.StatusText,
		MimeType:     params.Response.MimeType,
		ResourceType: params.Type,
		Headers:      filterHeaders(params.Response.Headers),
		Phase:        "response",
	}

	rec.RecordEventWithAction("network", targetID, "", epoch, evidence)
}

// handleNetworkFailedEvent processes Network.loadingFailed.
// This event fires when a request fails (DNS error, connection refused,
// canceled, etc.).
func handleNetworkFailedEvent(envelope *browserCDPEnvelope, targetID string, epoch ConnectionEpoch) {
	var params struct {
		RequestID string `json:"requestId"`
		ErrorText string `json:"errorText"`
		Canceled  bool   `json:"canceled"`
		Type      string `json:"type"`
	}
	if err := json.Unmarshal(envelope.Params, &params); err != nil {
		return
	}

	evidence := NetworkEvidence{
		RequestID:    params.RequestID,
		ResourceType: params.Type,
		Failed:       true,
		ErrorText:    truncateErrorText(params.ErrorText),
		Canceled:     params.Canceled,
		Phase:        "failed",
	}

	rec.RecordEventWithAction("network", targetID, "", epoch, evidence)
}

// --- Page domain event handlers (navigation and frame lifecycle) ---
//
// These handlers capture navigation and frame lifecycle events from the
// Page CDP domain. Each event is recorded as a "navigation" event with
// typed NavigationEvidence in Data and the connection epoch on the
// EventRecord. The evidence preserves exact CDP identifiers (frameId,
// loaderId, parentFrameId) so downstream consumers (B3) can link
// navigations to network requests and actions via exact CDP-ID joins.

// handleFrameNavigatedEvent processes Page.frameNavigated.
// This event fires when a navigation completes for a frame, whether
// a full page load or a same-document navigation. The frame object
// carries the frame ID, parent frame ID (for iframes), loader ID,
// URL, and MIME type. The type field distinguishes "Navigation"
// (full page navigation) from "SameDocument" (URL change without
// a full reload).
func handleFrameNavigatedEvent(envelope *browserCDPEnvelope, targetID string, epoch ConnectionEpoch) {
	var params struct {
		Frame struct {
			ID       string `json:"id"`
			ParentID string `json:"parentId,omitempty"`
			LoaderID string `json:"loaderId,omitempty"`
			URL      string `json:"url"`
			MimeType string `json:"mimeType,omitempty"`
		} `json:"frame"`
		Type string `json:"type"` // "Navigation" or "SameDocument"
	}
	if err := json.Unmarshal(envelope.Params, &params); err != nil {
		return
	}

	evidence := NavigationEvidence{
		FrameID:        truncateCDPID(params.Frame.ID),
		LoaderID:       truncateCDPID(params.Frame.LoaderID),
		ParentFrameID:  truncateCDPID(params.Frame.ParentID),
		URL:            sanitizeURL(params.Frame.URL),
		NavigationType: truncateNavType(params.Type),
		MimeType:       truncateNavType(params.Frame.MimeType),
		Phase:          "frameNavigated",
	}

	rec.RecordEventWithAction("navigation", targetID, "", epoch, evidence)
}

// handleFrameDetachedEvent processes Page.frameDetached.
// This event fires when a frame is detached from its parent. The
// reason field is "remove" (frame removed from DOM) or "swap" (frame
// swapped, e.g. for BFCache restoration).
func handleFrameDetachedEvent(envelope *browserCDPEnvelope, targetID string, epoch ConnectionEpoch) {
	var params struct {
		FrameID string `json:"frameId"`
		Reason  string `json:"reason"` // "remove" or "swap"
	}
	if err := json.Unmarshal(envelope.Params, &params); err != nil {
		return
	}

	evidence := NavigationEvidence{
		FrameID: truncateCDPID(params.FrameID),
		Reason:  truncateNavType(params.Reason),
		Phase:   "frameDetached",
	}

	rec.RecordEventWithAction("navigation", targetID, "", epoch, evidence)
}

// handleFrameStartedLoadingEvent processes Page.frameStartedLoading.
// This event fires when a frame starts loading. It marks the beginning
// of a frame's loading lifecycle.
func handleFrameStartedLoadingEvent(envelope *browserCDPEnvelope, targetID string, epoch ConnectionEpoch) {
	var params struct {
		FrameID string `json:"frameId"`
	}
	if err := json.Unmarshal(envelope.Params, &params); err != nil {
		return
	}

	evidence := NavigationEvidence{
		FrameID: truncateCDPID(params.FrameID),
		Phase:   "frameStartedLoading",
	}

	rec.RecordEventWithAction("navigation", targetID, "", epoch, evidence)
}

// handleFrameStoppedLoadingEvent processes Page.frameStoppedLoading.
// This event fires when a frame stops loading. It marks the end of a
// frame's loading lifecycle.
func handleFrameStoppedLoadingEvent(envelope *browserCDPEnvelope, targetID string, epoch ConnectionEpoch) {
	var params struct {
		FrameID string `json:"frameId"`
	}
	if err := json.Unmarshal(envelope.Params, &params); err != nil {
		return
	}

	evidence := NavigationEvidence{
		FrameID: truncateCDPID(params.FrameID),
		Phase:   "frameStoppedLoading",
	}

	rec.RecordEventWithAction("navigation", targetID, "", epoch, evidence)
}

// handleNavigatedWithinDocumentEvent processes
// Page.navigatedWithinDocument. This event fires for same-document
// navigations (URL fragment changes, pushState, etc.) that do not
// trigger a full page load. The NavigationType is set to "SameDocument"
// to distinguish these from full navigations.
func handleNavigatedWithinDocumentEvent(envelope *browserCDPEnvelope, targetID string, epoch ConnectionEpoch) {
	var params struct {
		FrameID string `json:"frameId"`
		URL     string `json:"url"`
	}
	if err := json.Unmarshal(envelope.Params, &params); err != nil {
		return
	}

	evidence := NavigationEvidence{
		FrameID:        truncateCDPID(params.FrameID),
		URL:            sanitizeURL(params.URL),
		NavigationType: "SameDocument",
		Phase:          "navigatedWithinDocument",
	}

	rec.RecordEventWithAction("navigation", targetID, "", epoch, evidence)
}

// handleLifecycleEvent processes Page.lifecycleEvent.
// This event fires for each lifecycle milestone a frame reaches
// (DOMContentLoaded, load, networkIdle, etc.). Each milestone is
// recorded as a separate "navigation" event with the LifecycleEvent
// field set to the milestone name. The frameId and loaderId link
// the milestone to the navigation it belongs to.
func handleLifecycleEvent(envelope *browserCDPEnvelope, targetID string, epoch ConnectionEpoch) {
	var params struct {
		FrameID  string `json:"frameId"`
		LoaderID string `json:"loaderId"`
		Name     string `json:"name"` // "DOMContentLoaded", "load", "networkIdle", etc.
	}
	if err := json.Unmarshal(envelope.Params, &params); err != nil {
		return
	}

	evidence := NavigationEvidence{
		FrameID:        truncateCDPID(params.FrameID),
		LoaderID:       truncateCDPID(params.LoaderID),
		LifecycleEvent: truncateNavType(params.Name),
		Phase:          "lifecycleEvent",
	}

	rec.RecordEventWithAction("navigation", targetID, "", epoch, evidence)
}
