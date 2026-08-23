package main

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestRecorderSubscriberSkipsBrowserEndpoint verifies that the recorder
// subscriber is NOT started on the Browser endpoint WebSocket (which
// doesn't support Runtime/Log/Network domains). Starting it there
// produces spurious domain_enable_error counters.
func TestRecorderSubscriberSkipsBrowserEndpoint(t *testing.T) {
	tests := []struct {
		wsURL    string
		expected bool // true = should start recorder
	}{
		{"ws://127.0.0.1:9222/devtools/page/ABC123", true},
		{"ws://127.0.0.1:9222/devtools/page/DEF456", true},
		{"ws://127.0.0.1:9222/devtools/browser/abc-def-123", false},
		{"ws://127.0.0.1:9222/devtools/browser/xyz", false},
	}
	for _, tt := range tests {
		got := isPageEndpoint(tt.wsURL)
		if got != tt.expected {
			t.Errorf("isPageEndpoint(%q) = %v, want %v", tt.wsURL, got, tt.expected)
		}
	}
}

// TestSanitizeURLParseFirst verifies that sanitizeURL parses the URL
// first, removes query and fragment, then applies byte limits. On
// parse failure it must NOT preserve anything that looks like a query.
func TestSanitizeURLParseFirst(t *testing.T) {
	// Normal URL with query and fragment
	got := sanitizeURL("https://example.com/path?token=secret#frag")
	if strings.Contains(got, "token=secret") {
		t.Errorf("sanitizeURL should remove query: got %s", got)
	}
	if strings.Contains(got, "#frag") {
		t.Errorf("sanitizeURL should remove fragment: got %s", got)
	}
	if got != "https://example.com/path" {
		t.Errorf("sanitizeURL result = %q, want %q", got, "https://example.com/path")
	}
}

// TestSanitizeURLByteLimitAfterParse verifies that byte truncation
// happens AFTER parsing and query removal, not before. Truncating
// before parsing can corrupt the URL and cause parse failure, which
// would then preserve the raw (possibly secret-bearing) string.
func TestSanitizeURLByteLimitAfterParse(t *testing.T) {
	// URL with long path and query — query should be removed, then
	// the remaining URL should be truncated if still too long.
	longPath := ""
	for i := 0; i < maxURLBytes+200; i++ {
		longPath += "a"
	}
	raw := "https://example.com/" + longPath + "?secret=leaked"
	got := sanitizeURL(raw)
	if strings.Contains(got, "secret=leaked") {
		t.Errorf("sanitizeURL should remove query before truncation: got %s", got[:80])
	}
	if strings.Contains(got, "?") {
		t.Errorf("sanitizeURL should not contain query: got %s", got[:80])
	}
}

// TestSanitizeURLParseFailureNoQuery verifies that on parse failure,
// the returned string does not contain anything that looks like a
// query parameter (no '?' character).
func TestSanitizeURLParseFailureNoQuery(t *testing.T) {
	// A string with a query-like portion that url.Parse will reject
	// or that contains secrets. We must not preserve the query part.
	raw := "://not-a-url?secret=should-not-appear"
	got := sanitizeURL(raw)
	if strings.Contains(got, "?") {
		t.Errorf("sanitizeURL on parse failure should not preserve query: got %s", got)
	}
	if strings.Contains(got, "secret=should-not-appear") {
		t.Errorf("sanitizeURL on parse failure leaked secret: got %s", got)
	}
}

// TestSanitizeURLUTF8SafeTruncation verifies that truncation does not
// split a multi-byte UTF-8 sequence, which would produce invalid UTF-8.
func TestSanitizeURLUTF8SafeTruncation(t *testing.T) {
	// Build a URL with multi-byte UTF-8 chars near the truncation point.
	// Each 'é' is 2 bytes in UTF-8.
	utf8Suffix := ""
	for i := 0; i < maxURLBytes/2+10; i++ {
		utf8Suffix += "é"
	}
	raw := "https://example.com/" + utf8Suffix
	got := sanitizeURL(raw)
	if !utf8.ValidString(got) {
		t.Errorf("sanitizeURL produced invalid UTF-8: %q", got[:50])
	}
}

// TestFilterHeadersURLTypeHeaders verifies that URL-type headers
// (Referer, Location, Origin) are either sanitized as URLs or dropped.
// They must NOT be stored raw, because they can contain query secrets.
func TestFilterHeadersURLTypeHeaders(t *testing.T) {
	headers := map[string]string{
		"Referer":  "https://evil.com/page?token=secret123",
		"Location": "https://evil.com/redirect?api_key=abc456",
		"Origin":   "https://evil.com",
		"Accept":   "text/html",
	}
	got := filterHeaders(headers)
	if got == nil {
		t.Fatal("filterHeaders returned nil")
	}
	// Referer with query secrets must not contain the secret
	if v, ok := got["Referer"]; ok {
		if strings.Contains(v, "token=secret123") {
			t.Errorf("Referer should be sanitized, leaked secret: %s", v)
		}
		if strings.Contains(v, "?") {
			t.Errorf("Referer should have no query: %s", v)
		}
	}
	// Location with query secrets must not contain the secret
	if v, ok := got["Location"]; ok {
		if strings.Contains(v, "api_key=abc456") {
			t.Errorf("Location should be sanitized, leaked secret: %s", v)
		}
		if strings.Contains(v, "?") {
			t.Errorf("Location should have no query: %s", v)
		}
	}
	// Origin is just a host, no query — should be fine
	if v, ok := got["Origin"]; ok {
		if v != "https://evil.com" {
			t.Errorf("Origin should be preserved: got %s", v)
		}
	}
}

// TestRedirectHopTrackingOrdered verifies that redirect hops are tracked
// as an ordered sequence, not just a 0/1 flag. Each hop should be
// recorded as a separate event with an incrementing hop number.
func TestRedirectHopTrackingOrdered(t *testing.T) {
	rec.Clear()

	// Simulate a two-hop redirect: A -> B -> C
	// CDP sends requestWillBeSent for A (no redirectResponse),
	// then requestWillBeSent for B (redirectResponse from A),
	// then requestWillBeSent for C (redirectResponse from B),
	// then responseReceived for C.
	targetID := "test-target"

	// Hop 0: initial request to /a
	handleNetworkRequestEvent(&browserCDPEnvelope{
		Method: "Network.requestWillBeSent",
		Params: json.RawMessage(`{
			"requestId": "req1",
			"loaderId": "loader1",
			"frameId": "frame1",
			"request": {"url": "https://example.com/a", "method": "GET", "headers": {}},
			"type": "Document"
		}`),
	}, targetID, "")

	// Hop 1: redirect to /b (redirectResponse from /a)
	handleNetworkRequestEvent(&browserCDPEnvelope{
		Method: "Network.requestWillBeSent",
		Params: json.RawMessage(`{
			"requestId": "req1:1",
			"loaderId": "loader1",
			"frameId": "frame1",
			"request": {"url": "https://example.com/b", "method": "GET", "headers": {}},
			"type": "Document",
			"redirectResponse": {"status": 302, "statusText": "Found", "url": "https://example.com/a"}
		}`),
	}, targetID, "")

	// Hop 2: redirect to /c (redirectResponse from /b)
	handleNetworkRequestEvent(&browserCDPEnvelope{
		Method: "Network.requestWillBeSent",
		Params: json.RawMessage(`{
			"requestId": "req1:2",
			"loaderId": "loader1",
			"frameId": "frame1",
			"request": {"url": "https://example.com/c", "method": "GET", "headers": {}},
			"type": "Document",
			"redirectResponse": {"status": 301, "statusText": "Moved Permanently", "url": "https://example.com/b"}
		}`),
	}, targetID, "")

	events := rec.QueryEvents(EventFilter{eventType: "network", limit: 100})
	if len(events) < 3 {
		t.Fatalf("expected at least 3 network events for redirect chain, got %d", len(events))
	}

	// QueryEvents returns newest-first, so hops will be in reverse
	// order: [2, 1, 0]. Collect and reverse to verify.
	hops := []int{}
	for _, e := range events {
		var ev NetworkEvidence
		switch d := e.Data.(type) {
		case NetworkEvidence:
			ev = d
		case map[string]any:
			ev = networkEvidenceFromMap(d)
		default:
			continue
		}
		if ev.Phase == "request" {
			hops = append(hops, ev.RedirectHops)
		}
	}
	if len(hops) < 3 {
		t.Fatalf("expected at least 3 request-phase events, got %d", len(hops))
	}
	// Reverse to get chronological order
	for i, j := 0, len(hops)-1; i < j; i, j = i+1, j-1 {
		hops[i], hops[j] = hops[j], hops[i]
	}
	expected := []int{0, 1, 2}
	for i, h := range hops[:3] {
		if h != expected[i] {
			t.Errorf("hop[%d] = %d, want %d (ordered: %v)", i, h, expected[i], hops)
		}
	}
}

// TestRedirectHopTrackingDoesNotMergeByRequestID verifies that redirect
// hops with different request IDs (CDP appends :1, :2 etc.) are NOT
// merged into a single entry, preserving the ordered chain.
func TestRedirectHopTrackingDoesNotMergeByRequestID(t *testing.T) {
	rec.Clear()

	targetID := "test-target"
	// First hop
	handleNetworkRequestEvent(&browserCDPEnvelope{
		Method: "Network.requestWillBeSent",
		Params: json.RawMessage(`{
			"requestId": "req2",
			"loaderId": "loader2",
			"frameId": "frame2",
			"request": {"url": "https://example.com/start", "method": "GET", "headers": {}},
			"type": "Document"
		}`),
	}, targetID, "")
	// Redirect hop (different requestId: req2:1)
	handleNetworkRequestEvent(&browserCDPEnvelope{
		Method: "Network.requestWillBeSent",
		Params: json.RawMessage(`{
			"requestId": "req2:1",
			"loaderId": "loader2",
			"frameId": "frame2",
			"request": {"url": "https://example.com/end", "method": "GET", "headers": {}},
			"type": "Document",
			"redirectResponse": {"status": 302, "statusText": "Found", "url": "https://example.com/start"}
		}`),
	}, targetID, "")

	events := rec.QueryEvents(EventFilter{eventType: "network", limit: 100})
	requestEvents := 0
	for _, e := range events {
		var ev NetworkEvidence
		switch d := e.Data.(type) {
		case NetworkEvidence:
			ev = d
		case map[string]any:
			ev = networkEvidenceFromMap(d)
		default:
			continue
		}
		if ev.Phase == "request" {
			requestEvents++
		}
	}
	if requestEvents < 2 {
		t.Errorf("expected 2 separate request events for redirect chain, got %d", requestEvents)
	}
}

// TestQueryNetworkFromRecorderTargetScope verifies that
// queryNetworkFromRecorder filters by target ID when provided.
func TestQueryNetworkFromRecorderTargetScope(t *testing.T) {
	rec.Clear()

	// Record events for two different targets
	rec.RecordEvent("network", "target-A", NetworkEvidence{
		RequestID: "req-A", URL: "https://a.com", Method: "GET", Phase: "request",
	})
	rec.RecordEvent("network", "target-B", NetworkEvidence{
		RequestID: "req-B", URL: "https://b.com", Method: "GET", Phase: "request",
	})

	// Query with target filter
	entries := queryNetworkFromRecorder(10, true, "target-A", false)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for target-A, got %d", len(entries))
	}
	if entries[0]["id"] != "req-A" {
		t.Errorf("expected req-A, got %v", entries[0]["id"])
	}

	// Query other target
	entries = queryNetworkFromRecorder(10, true, "target-B", false)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for target-B, got %d", len(entries))
	}
	if entries[0]["id"] != "req-B" {
		t.Errorf("expected req-B, got %v", entries[0]["id"])
	}
}

// TestSubscriberEventCarriesEpoch verifies that events recorded by the
// subscriber handlers carry the connection epoch passed to them.
func TestSubscriberEventCarriesEpoch(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	epoch := ConnectionEpoch("epoch_0003")
	handleConsoleEvent(&browserCDPEnvelope{
		Method: "Runtime.consoleAPICalled",
		Params: json.RawMessage(`{"type":"log","args":[{"type":"string","value":"hello"}]}`),
	}, "tab1", epoch)

	events := rec.QueryEvents(EventFilter{eventType: "console"})
	if len(events) != 1 {
		t.Fatalf("expected 1 console event, got %d", len(events))
	}
	if events[0].Epoch != epoch {
		t.Errorf("console event epoch = %q, want %q", events[0].Epoch, epoch)
	}
}

// TestSubscriberNetworkEventCarriesEpoch verifies that network events
// recorded by the subscriber carry the connection epoch.
func TestSubscriberNetworkEventCarriesEpoch(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	epoch := ConnectionEpoch("epoch_0001")
	handleNetworkRequestEvent(&browserCDPEnvelope{
		Method: "Network.requestWillBeSent",
		Params: json.RawMessage(`{
			"requestId": "req-epoch-test",
			"loaderId": "loader1",
			"frameId": "frame1",
			"request": {"url": "https://example.com/test", "method": "GET", "headers": {}},
			"type": "Document"
		}`),
	}, "tab1", epoch)

	events := rec.QueryEvents(EventFilter{eventType: "network"})
	if len(events) != 1 {
		t.Fatalf("expected 1 network event, got %d", len(events))
	}
	if events[0].Epoch != epoch {
		t.Errorf("network event epoch = %q, want %q", events[0].Epoch, epoch)
	}
}

// TestSubscriberExceptionEventCarriesEpoch verifies that exception
// events recorded by the subscriber carry the connection epoch.
func TestSubscriberExceptionEventCarriesEpoch(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	epoch := ConnectionEpoch("epoch_0002")
	handleExceptionEvent(&browserCDPEnvelope{
		Method: "Runtime.exceptionThrown",
		Params: json.RawMessage(`{
			"exceptionDetails": {
				"exceptionId": 1,
				"text": "Uncaught Error: test",
				"lineNumber": 0,
				"columnNumber": 0,
				"exception": {"type": "object", "subtype": "error", "description": "Error: test"}
			}
		}`),
	}, "tab1", epoch)

	events := rec.QueryEvents(EventFilter{eventType: "exception"})
	if len(events) != 1 {
		t.Fatalf("expected 1 exception event, got %d", len(events))
	}
	if events[0].Epoch != epoch {
		t.Errorf("exception event epoch = %q, want %q", events[0].Epoch, epoch)
	}
}

// TestSubscriberLogEventCarriesEpoch verifies that log events recorded
// by the subscriber carry the connection epoch.
func TestSubscriberLogEventCarriesEpoch(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	epoch := ConnectionEpoch("epoch_0005")
	handleLogEntryEvent(&browserCDPEnvelope{
		Method: "Log.entryAdded",
		Params: json.RawMessage(`{
			"entry": {"source": "network", "level": "error", "text": "Failed to load resource"}
		}`),
	}, "tab1", epoch)

	events := rec.QueryEvents(EventFilter{eventType: "log"})
	if len(events) != 1 {
		t.Fatalf("expected 1 log event, got %d", len(events))
	}
	if events[0].Epoch != epoch {
		t.Errorf("log event epoch = %q, want %q", events[0].Epoch, epoch)
	}
}

// TestSubscriberEventsFromDifferentEpochsNotJoinable verifies that
// events recorded under different epochs are distinguishable: an event
// from epoch_0000 and an event from epoch_0001 have different Epoch
// fields, so a consumer can detect the epoch boundary and refuse to
// join CDP IDs across it.
func TestSubscriberEventsFromDifferentEpochsNotJoinable(t *testing.T) {
	restore := withTestRecorder(20, 20)
	defer restore()

	oldEpoch := ConnectionEpoch("epoch_0000")
	newEpoch := ConnectionEpoch("epoch_0001")

	// Simulate a request from the old epoch (before reconnect).
	handleNetworkRequestEvent(&browserCDPEnvelope{
		Method: "Network.requestWillBeSent",
		Params: json.RawMessage(`{
			"requestId": "REQ-1",
			"loaderId": "loader1",
			"frameId": "frame1",
			"request": {"url": "https://example.com/old", "method": "GET", "headers": {}},
			"type": "Document"
		}`),
	}, "tab1", oldEpoch)

	// Simulate a response from the new epoch (after reconnect).
	// CDP may reuse the same requestId "REQ-1" on the new connection,
	// but it's a different request. The epoch makes this visible.
	handleNetworkResponseEvent(&browserCDPEnvelope{
		Method: "Network.responseReceived",
		Params: json.RawMessage(`{
			"requestId": "REQ-1",
			"loaderId": "loader2",
			"response": {"status": 200, "statusText": "OK", "mimeType": "text/html", "url": "https://example.com/new", "headers": {}},
			"type": "Document"
		}`),
	}, "tab1", newEpoch)

	events := rec.QueryEvents(EventFilter{eventType: "network"})
	if len(events) != 2 {
		t.Fatalf("expected 2 network events, got %d", len(events))
	}

	// The two events have the same requestId but different epochs.
	// A consumer MUST check the epoch before joining.
	var oldEvent, newEvent *EventRecord
	for i := range events {
		switch events[i].Epoch {
		case oldEpoch:
			oldEvent = &events[i]
		case newEpoch:
			newEvent = &events[i]
		}
	}
	if oldEvent == nil {
		t.Fatal("no event with old epoch found")
	}
	if newEvent == nil {
		t.Fatal("no event with new epoch found")
	}
	if oldEvent.Epoch == newEvent.Epoch {
		t.Fatal("old and new events have the same epoch — they should be different")
	}
}

// --- Page domain event handler tests ---

// TestHandleFrameNavigatedEvent verifies that Page.frameNavigated is
// parsed correctly: frame ID, parent frame ID, loader ID, URL (sanitized),
// MIME type, and navigation type are all preserved in the evidence.
func TestHandleFrameNavigatedEvent(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	epoch := ConnectionEpoch("epoch_0001")
	handleFrameNavigatedEvent(&browserCDPEnvelope{
		Method: "Page.frameNavigated",
		Params: json.RawMessage(`{
			"frame": {
				"id": "frame.abc",
				"parentId": "frame.parent",
				"loaderId": "loader.xyz",
				"url": "https://example.com/page?token=secret",
				"mimeType": "text/html"
			},
			"type": "Navigation"
		}`),
	}, "tab1", epoch)

	events := rec.QueryEvents(EventFilter{eventType: "navigation"})
	if len(events) != 1 {
		t.Fatalf("expected 1 navigation event, got %d", len(events))
	}
	if events[0].Epoch != epoch {
		t.Errorf("epoch = %q, want %q", events[0].Epoch, epoch)
	}
	ev, ok := events[0].Data.(NavigationEvidence)
	if !ok {
		t.Fatalf("data is %T, not NavigationEvidence", events[0].Data)
	}
	if ev.FrameID != "frame.abc" {
		t.Errorf("FrameID = %q, want frame.abc", ev.FrameID)
	}
	if ev.ParentFrameID != "frame.parent" {
		t.Errorf("ParentFrameID = %q, want frame.parent", ev.ParentFrameID)
	}
	if ev.LoaderID != "loader.xyz" {
		t.Errorf("LoaderID = %q, want loader.xyz", ev.LoaderID)
	}
	if ev.URL != "https://example.com/page" {
		t.Errorf("URL = %q, want https://example.com/page (query removed)", ev.URL)
	}
	if strings.Contains(ev.URL, "token=secret") {
		t.Errorf("URL should not contain query secret: %s", ev.URL)
	}
	if ev.MimeType != "text/html" {
		t.Errorf("MimeType = %q, want text/html", ev.MimeType)
	}
	if ev.NavigationType != "Navigation" {
		t.Errorf("NavigationType = %q, want Navigation", ev.NavigationType)
	}
	if ev.Phase != "frameNavigated" {
		t.Errorf("Phase = %q, want frameNavigated", ev.Phase)
	}
}

// TestHandleFrameNavigatedEventTopLevel verifies that a top-level frame
// navigation (no parentId) is captured with an empty ParentFrameID.
func TestHandleFrameNavigatedEventTopLevel(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	handleFrameNavigatedEvent(&browserCDPEnvelope{
		Method: "Page.frameNavigated",
		Params: json.RawMessage(`{
			"frame": {
				"id": "frame.top",
				"loaderId": "loader.1",
				"url": "https://example.com/",
				"mimeType": "text/html"
			},
			"type": "Navigation"
		}`),
	}, "tab1", ConnectionEpoch("epoch_0000"))

	events := rec.QueryEvents(EventFilter{eventType: "navigation"})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0].Data.(NavigationEvidence)
	if ev.ParentFrameID != "" {
		t.Errorf("ParentFrameID = %q, want empty for top-level frame", ev.ParentFrameID)
	}
}

// TestHandleFrameDetachedEvent verifies that Page.frameDetached is
// parsed correctly: frame ID and detach reason are preserved.
func TestHandleFrameDetachedEvent(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	epoch := ConnectionEpoch("epoch_0002")
	handleFrameDetachedEvent(&browserCDPEnvelope{
		Method: "Page.frameDetached",
		Params: json.RawMessage(`{
			"frameId": "frame.child",
			"reason": "remove"
		}`),
	}, "tab1", epoch)

	events := rec.QueryEvents(EventFilter{eventType: "navigation"})
	if len(events) != 1 {
		t.Fatalf("expected 1 navigation event, got %d", len(events))
	}
	if events[0].Epoch != epoch {
		t.Errorf("epoch = %q, want %q", events[0].Epoch, epoch)
	}
	ev := events[0].Data.(NavigationEvidence)
	if ev.FrameID != "frame.child" {
		t.Errorf("FrameID = %q, want frame.child", ev.FrameID)
	}
	if ev.Reason != "remove" {
		t.Errorf("Reason = %q, want remove", ev.Reason)
	}
	if ev.Phase != "frameDetached" {
		t.Errorf("Phase = %q, want frameDetached", ev.Phase)
	}
}

// TestHandleFrameDetachedEventSwap verifies that the "swap" detach
// reason is preserved (e.g. for BFCache restoration).
func TestHandleFrameDetachedEventSwap(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	handleFrameDetachedEvent(&browserCDPEnvelope{
		Method: "Page.frameDetached",
		Params: json.RawMessage(`{
			"frameId": "frame.bfcache",
			"reason": "swap"
		}`),
	}, "tab1", ConnectionEpoch("epoch_0000"))

	events := rec.QueryEvents(EventFilter{eventType: "navigation"})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0].Data.(NavigationEvidence)
	if ev.Reason != "swap" {
		t.Errorf("Reason = %q, want swap", ev.Reason)
	}
}

// TestHandleFrameStartedLoadingEvent verifies that
// Page.frameStartedLoading is parsed correctly.
func TestHandleFrameStartedLoadingEvent(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	epoch := ConnectionEpoch("epoch_0003")
	handleFrameStartedLoadingEvent(&browserCDPEnvelope{
		Method: "Page.frameStartedLoading",
		Params: json.RawMessage(`{"frameId": "frame.load.start"}`),
	}, "tab1", epoch)

	events := rec.QueryEvents(EventFilter{eventType: "navigation"})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Epoch != epoch {
		t.Errorf("epoch = %q, want %q", events[0].Epoch, epoch)
	}
	ev := events[0].Data.(NavigationEvidence)
	if ev.FrameID != "frame.load.start" {
		t.Errorf("FrameID = %q, want frame.load.start", ev.FrameID)
	}
	if ev.Phase != "frameStartedLoading" {
		t.Errorf("Phase = %q, want frameStartedLoading", ev.Phase)
	}
}

// TestHandleFrameStoppedLoadingEvent verifies that
// Page.frameStoppedLoading is parsed correctly.
func TestHandleFrameStoppedLoadingEvent(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	epoch := ConnectionEpoch("epoch_0004")
	handleFrameStoppedLoadingEvent(&browserCDPEnvelope{
		Method: "Page.frameStoppedLoading",
		Params: json.RawMessage(`{"frameId": "frame.load.stop"}`),
	}, "tab1", epoch)

	events := rec.QueryEvents(EventFilter{eventType: "navigation"})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Epoch != epoch {
		t.Errorf("epoch = %q, want %q", events[0].Epoch, epoch)
	}
	ev := events[0].Data.(NavigationEvidence)
	if ev.FrameID != "frame.load.stop" {
		t.Errorf("FrameID = %q, want frame.load.stop", ev.FrameID)
	}
	if ev.Phase != "frameStoppedLoading" {
		t.Errorf("Phase = %q, want frameStoppedLoading", ev.Phase)
	}
}

// TestHandleNavigatedWithinDocumentEvent verifies that
// Page.navigatedWithinDocument is parsed correctly: frame ID and
// sanitized URL are preserved, and NavigationType is set to
// "SameDocument" to distinguish from full navigations.
func TestHandleNavigatedWithinDocumentEvent(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	epoch := ConnectionEpoch("epoch_0005")
	handleNavigatedWithinDocumentEvent(&browserCDPEnvelope{
		Method: "Page.navigatedWithinDocument",
		Params: json.RawMessage(`{
			"frameId": "frame.same.doc",
			"url": "https://example.com/page#section?token=secret"
		}`),
	}, "tab1", epoch)

	events := rec.QueryEvents(EventFilter{eventType: "navigation"})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Epoch != epoch {
		t.Errorf("epoch = %q, want %q", events[0].Epoch, epoch)
	}
	ev := events[0].Data.(NavigationEvidence)
	if ev.FrameID != "frame.same.doc" {
		t.Errorf("FrameID = %q, want frame.same.doc", ev.FrameID)
	}
	if strings.Contains(ev.URL, "token=secret") {
		t.Errorf("URL should not contain query secret: %s", ev.URL)
	}
	if ev.NavigationType != "SameDocument" {
		t.Errorf("NavigationType = %q, want SameDocument", ev.NavigationType)
	}
	if ev.Phase != "navigatedWithinDocument" {
		t.Errorf("Phase = %q, want navigatedWithinDocument", ev.Phase)
	}
}

// TestHandleLifecycleEvent verifies that Page.lifecycleEvent is parsed
// correctly: frame ID, loader ID, and lifecycle milestone name are
// preserved.
func TestHandleLifecycleEvent(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	epoch := ConnectionEpoch("epoch_0006")
	handleLifecycleEvent(&browserCDPEnvelope{
		Method: "Page.lifecycleEvent",
		Params: json.RawMessage(`{
			"frameId": "frame.life",
			"loaderId": "loader.life",
			"name": "DOMContentLoaded"
		}`),
	}, "tab1", epoch)

	events := rec.QueryEvents(EventFilter{eventType: "navigation"})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Epoch != epoch {
		t.Errorf("epoch = %q, want %q", events[0].Epoch, epoch)
	}
	ev := events[0].Data.(NavigationEvidence)
	if ev.FrameID != "frame.life" {
		t.Errorf("FrameID = %q, want frame.life", ev.FrameID)
	}
	if ev.LoaderID != "loader.life" {
		t.Errorf("LoaderID = %q, want loader.life", ev.LoaderID)
	}
	if ev.LifecycleEvent != "DOMContentLoaded" {
		t.Errorf("LifecycleEvent = %q, want DOMContentLoaded", ev.LifecycleEvent)
	}
	if ev.Phase != "lifecycleEvent" {
		t.Errorf("Phase = %q, want lifecycleEvent", ev.Phase)
	}
}

// TestHandleLifecycleEventMultipleMilestones verifies that multiple
// lifecycle events for the same frame/loader are recorded as separate
// events, each with a different LifecycleEvent name.
func TestHandleLifecycleEventMultipleMilestones(t *testing.T) {
	restore := withTestRecorder(20, 20)
	defer restore()

	epoch := ConnectionEpoch("epoch_0000")
	milestones := []string{"DOMContentLoaded", "load", "networkIdle"}
	for _, name := range milestones {
		handleLifecycleEvent(&browserCDPEnvelope{
			Method: "Page.lifecycleEvent",
			Params: json.RawMessage(`{
				"frameId": "frame.multi",
				"loaderId": "loader.multi",
				"name": "` + name + `"
			}`),
		}, "tab1", epoch)
	}

	events := rec.QueryEvents(EventFilter{eventType: "navigation"})
	if len(events) != 3 {
		t.Fatalf("expected 3 lifecycle events, got %d", len(events))
	}
	// QueryEvents returns newest-first, so reverse to check
	names := make([]string, 0, len(events))
	for _, e := range events {
		ev := e.Data.(NavigationEvidence)
		names = append(names, ev.LifecycleEvent)
	}
	// Reverse to chronological order
	for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
	}
	for i, want := range milestones {
		if names[i] != want {
			t.Errorf("milestone[%d] = %q, want %q", i, names[i], want)
		}
	}
}

// TestIsRecorderEventPageDomain verifies that Page domain events are
// recognized as recorder events.
func TestIsRecorderEventPageDomain(t *testing.T) {
	pageEvents := []string{
		"Page.frameNavigated",
		"Page.frameDetached",
		"Page.frameStartedLoading",
		"Page.frameStoppedLoading",
		"Page.navigatedWithinDocument",
		"Page.lifecycleEvent",
	}
	for _, method := range pageEvents {
		if !isRecorderEvent(method) {
			t.Errorf("isRecorderEvent(%q) = false, want true", method)
		}
	}
}

// --- Navigation type distinction and exact CDP-ID link tests ---

// TestNavigationTypeNormalVsSameDocumentFrameNavigated verifies that
// Page.frameNavigated with type="Navigation" (full page load) and
// type="SameDocument" (same-document navigation via frameNavigated)
// are distinguishable by the NavigationType field.
func TestNavigationTypeNormalVsSameDocumentFrameNavigated(t *testing.T) {
	restore := withTestRecorder(20, 20)
	defer restore()

	// Normal navigation
	handleFrameNavigatedEvent(&browserCDPEnvelope{
		Method: "Page.frameNavigated",
		Params: json.RawMessage(`{
			"frame": {"id": "f1", "loaderId": "l1", "url": "https://example.com/a", "mimeType": "text/html"},
			"type": "Navigation"
		}`),
	}, "tab1", ConnectionEpoch("epoch_0000"))

	// Same-document navigation via frameNavigated
	handleFrameNavigatedEvent(&browserCDPEnvelope{
		Method: "Page.frameNavigated",
		Params: json.RawMessage(`{
			"frame": {"id": "f1", "loaderId": "l1", "url": "https://example.com/b", "mimeType": "text/html"},
			"type": "SameDocument"
		}`),
	}, "tab1", ConnectionEpoch("epoch_0000"))

	events := rec.QueryEvents(EventFilter{eventType: "navigation"})
	if len(events) != 2 {
		t.Fatalf("expected 2 navigation events, got %d", len(events))
	}
	// QueryEvents returns newest-first: [SameDocument, Navigation]
	sameDoc := events[0].Data.(NavigationEvidence)
	normal := events[1].Data.(NavigationEvidence)

	if normal.NavigationType != "Navigation" {
		t.Errorf("normal nav type = %q, want Navigation", normal.NavigationType)
	}
	if sameDoc.NavigationType != "SameDocument" {
		t.Errorf("same-doc nav type = %q, want SameDocument", sameDoc.NavigationType)
	}
	if normal.NavigationType == sameDoc.NavigationType {
		t.Fatal("normal and same-document navigations must be distinguishable")
	}
}

// TestNavigationTypeSameDocumentTwoSources verifies that same-document
// navigations from both Page.frameNavigated (type="SameDocument") and
// Page.navigatedWithinDocument are both labelled "SameDocument" but
// are distinguishable by Phase.
func TestNavigationTypeSameDocumentTwoSources(t *testing.T) {
	restore := withTestRecorder(20, 20)
	defer restore()

	// Same-document via frameNavigated
	handleFrameNavigatedEvent(&browserCDPEnvelope{
		Method: "Page.frameNavigated",
		Params: json.RawMessage(`{
			"frame": {"id": "f1", "url": "https://example.com/a#frag"},
			"type": "SameDocument"
		}`),
	}, "tab1", ConnectionEpoch("epoch_0000"))

	// Same-document via navigatedWithinDocument
	handleNavigatedWithinDocumentEvent(&browserCDPEnvelope{
		Method: "Page.navigatedWithinDocument",
		Params: json.RawMessage(`{
			"frameId": "f1",
			"url": "https://example.com/b#section"
		}`),
	}, "tab1", ConnectionEpoch("epoch_0000"))

	events := rec.QueryEvents(EventFilter{eventType: "navigation"})
	if len(events) != 2 {
		t.Fatalf("expected 2 navigation events, got %d", len(events))
	}
	// newest-first: [navigatedWithinDocument, frameNavigated]
	withinDoc := events[0].Data.(NavigationEvidence)
	frameNav := events[1].Data.(NavigationEvidence)

	if frameNav.NavigationType != "SameDocument" {
		t.Errorf("frameNavigated same-doc type = %q, want SameDocument", frameNav.NavigationType)
	}
	if withinDoc.NavigationType != "SameDocument" {
		t.Errorf("navigatedWithinDocument type = %q, want SameDocument", withinDoc.NavigationType)
	}
	// Both are SameDocument but distinguishable by Phase
	if frameNav.Phase == withinDoc.Phase {
		t.Fatal("same-document navigations from different CDP events should have different Phase")
	}
	if frameNav.Phase != "frameNavigated" {
		t.Errorf("frameNav Phase = %q, want frameNavigated", frameNav.Phase)
	}
	if withinDoc.Phase != "navigatedWithinDocument" {
		t.Errorf("withinDoc Phase = %q, want navigatedWithinDocument", withinDoc.Phase)
	}
}

// TestNavigationExactCDPIDLinks verifies that frameId and loaderId are
// preserved exactly as CDP provides them, so B3 can join navigation
// evidence to network requests via exact CDP-ID matching.
func TestNavigationExactCDPIDLinks(t *testing.T) {
	restore := withTestRecorder(20, 20)
	defer restore()

	epoch := ConnectionEpoch("epoch_0001")
	frameID := "A1B2C3D4E5F6.1"
	loaderID := "LOADER.ABCDEF.123"

	// Record a navigation with specific CDP IDs
	handleFrameNavigatedEvent(&browserCDPEnvelope{
		Method: "Page.frameNavigated",
		Params: json.RawMessage(`{
			"frame": {"id": "` + frameID + `", "loaderId": "` + loaderID + `", "url": "https://example.com/page", "mimeType": "text/html"},
			"type": "Navigation"
		}`),
	}, "tab1", epoch)

	// Record a lifecycle event with the same CDP IDs
	handleLifecycleEvent(&browserCDPEnvelope{
		Method: "Page.lifecycleEvent",
		Params: json.RawMessage(`{
			"frameId": "` + frameID + `",
			"loaderId": "` + loaderID + `",
			"name": "load"
		}`),
	}, "tab1", epoch)

	// Record a network request with the same loaderId and frameId
	handleNetworkRequestEvent(&browserCDPEnvelope{
		Method: "Network.requestWillBeSent",
		Params: json.RawMessage(`{
			"requestId": "req.1",
			"loaderId": "` + loaderID + `",
			"frameId": "` + frameID + `",
			"request": {"url": "https://example.com/page", "method": "GET", "headers": {}},
			"type": "Document"
		}`),
	}, "tab1", epoch)

	navEvents := rec.QueryEvents(EventFilter{eventType: "navigation"})
	if len(navEvents) != 2 {
		t.Fatalf("expected 2 navigation events, got %d", len(navEvents))
	}
	netEvents := rec.QueryEvents(EventFilter{eventType: "network"})
	if len(netEvents) != 1 {
		t.Fatalf("expected 1 network event, got %d", len(netEvents))
	}

	// All events should carry the same epoch
	for _, e := range navEvents {
		if e.Epoch != epoch {
			t.Errorf("nav event epoch = %q, want %q", e.Epoch, epoch)
		}
	}
	if netEvents[0].Epoch != epoch {
		t.Errorf("net event epoch = %q, want %q", netEvents[0].Epoch, epoch)
	}

	// Verify exact CDP-ID links: frameId and loaderId match across events
	var navEv, lifecycleEv NavigationEvidence
	for _, e := range navEvents {
		ev := e.Data.(NavigationEvidence)
		if ev.Phase == "frameNavigated" {
			navEv = ev
		} else if ev.Phase == "lifecycleEvent" {
			lifecycleEv = ev
		}
	}
	netEv := netEvents[0].Data.(NetworkEvidence)

	if navEv.FrameID != frameID {
		t.Errorf("nav FrameID = %q, want %q", navEv.FrameID, frameID)
	}
	if navEv.LoaderID != loaderID {
		t.Errorf("nav LoaderID = %q, want %q", navEv.LoaderID, loaderID)
	}
	if lifecycleEv.FrameID != frameID {
		t.Errorf("lifecycle FrameID = %q, want %q", lifecycleEv.FrameID, frameID)
	}
	if lifecycleEv.LoaderID != loaderID {
		t.Errorf("lifecycle LoaderID = %q, want %q", lifecycleEv.LoaderID, loaderID)
	}
	if netEv.FrameID != frameID {
		t.Errorf("net FrameID = %q, want %q", netEv.FrameID, frameID)
	}
	if netEv.LoaderID != loaderID {
		t.Errorf("net LoaderID = %q, want %q", netEv.LoaderID, loaderID)
	}
}

// TestIframeNavigationPreservesParentFrameID verifies that iframe
// navigations carry the parentFrameID, enabling B3 to reconstruct the
// frame hierarchy.
func TestIframeNavigationPreservesParentFrameID(t *testing.T) {
	restore := withTestRecorder(20, 20)
	defer restore()

	handleFrameNavigatedEvent(&browserCDPEnvelope{
		Method: "Page.frameNavigated",
		Params: json.RawMessage(`{
			"frame": {
				"id": "frame.child",
				"parentId": "frame.parent",
				"loaderId": "loader.child",
				"url": "https://iframe.example.com/content",
				"mimeType": "text/html"
			},
			"type": "Navigation"
		}`),
	}, "tab1", ConnectionEpoch("epoch_0000"))

	events := rec.QueryEvents(EventFilter{eventType: "navigation"})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0].Data.(NavigationEvidence)
	if ev.ParentFrameID != "frame.parent" {
		t.Errorf("ParentFrameID = %q, want frame.parent", ev.ParentFrameID)
	}
	if ev.FrameID != "frame.child" {
		t.Errorf("FrameID = %q, want frame.child", ev.FrameID)
	}
}

// TestFailedNavigationRepresentation verifies that a failed navigation
// (frame starts loading, then frame is detached with reason "remove"
// before completing) is represented by the sequence of events.
func TestFailedNavigationRepresentation(t *testing.T) {
	restore := withTestRecorder(20, 20)
	defer restore()

	epoch := ConnectionEpoch("epoch_0000")

	// Frame starts loading
	handleFrameStartedLoadingEvent(&browserCDPEnvelope{
		Method: "Page.frameStartedLoading",
		Params: json.RawMessage(`{"frameId": "frame.fail"}`),
	}, "tab1", epoch)

	// Frame navigated (navigation begins)
	handleFrameNavigatedEvent(&browserCDPEnvelope{
		Method: "Page.frameNavigated",
		Params: json.RawMessage(`{
			"frame": {"id": "frame.fail", "loaderId": "loader.fail", "url": "https://example.com/fail", "mimeType": "text/html"},
			"type": "Navigation"
		}`),
	}, "tab1", epoch)

	// Frame detached (navigation failed / frame removed)
	handleFrameDetachedEvent(&browserCDPEnvelope{
		Method: "Page.frameDetached",
		Params: json.RawMessage(`{"frameId": "frame.fail", "reason": "remove"}`),
	}, "tab1", epoch)

	events := rec.QueryEvents(EventFilter{eventType: "navigation"})
	if len(events) != 3 {
		t.Fatalf("expected 3 events for failed navigation, got %d", len(events))
	}

	// Verify the sequence (newest-first: [detach, navigated, startedLoading])
	detach := events[0].Data.(NavigationEvidence)
	navigated := events[1].Data.(NavigationEvidence)
	started := events[2].Data.(NavigationEvidence)

	if started.Phase != "frameStartedLoading" {
		t.Errorf("first event Phase = %q, want frameStartedLoading", started.Phase)
	}
	if navigated.Phase != "frameNavigated" {
		t.Errorf("second event Phase = %q, want frameNavigated", navigated.Phase)
	}
	if detach.Phase != "frameDetached" {
		t.Errorf("third event Phase = %q, want frameDetached", detach.Phase)
	}
	if detach.Reason != "remove" {
		t.Errorf("detach Reason = %q, want remove", detach.Reason)
	}
	// All events carry the same frameId
	if detach.FrameID != "frame.fail" || navigated.FrameID != "frame.fail" || started.FrameID != "frame.fail" {
		t.Errorf("frame IDs should all be frame.fail: detach=%q nav=%q started=%q",
			detach.FrameID, navigated.FrameID, started.FrameID)
	}
}

// TestNavigationEventsTargetScoped verifies that navigation events from
// one target do not appear in a query scoped to a different target.
func TestNavigationEventsTargetScoped(t *testing.T) {
	restore := withTestRecorder(20, 20)
	defer restore()

	// Record navigation events for two different targets
	handleFrameNavigatedEvent(&browserCDPEnvelope{
		Method: "Page.frameNavigated",
		Params: json.RawMessage(`{
			"frame": {"id": "f.A", "url": "https://a.com"},
			"type": "Navigation"
		}`),
	}, "target-A", ConnectionEpoch("epoch_0000"))

	handleFrameNavigatedEvent(&browserCDPEnvelope{
		Method: "Page.frameNavigated",
		Params: json.RawMessage(`{
			"frame": {"id": "f.B", "url": "https://b.com"},
			"type": "Navigation"
		}`),
	}, "target-B", ConnectionEpoch("epoch_0000"))

	// Query target-A only
	eventsA := rec.QueryEvents(EventFilter{eventType: "navigation", targetID: "target-A"})
	if len(eventsA) != 1 {
		t.Fatalf("expected 1 event for target-A, got %d", len(eventsA))
	}
	evA := eventsA[0].Data.(NavigationEvidence)
	if evA.FrameID != "f.A" {
		t.Errorf("target-A FrameID = %q, want f.A", evA.FrameID)
	}

	// Query target-B only
	eventsB := rec.QueryEvents(EventFilter{eventType: "navigation", targetID: "target-B"})
	if len(eventsB) != 1 {
		t.Fatalf("expected 1 event for target-B, got %d", len(eventsB))
	}
	evB := eventsB[0].Data.(NavigationEvidence)
	if evB.FrameID != "f.B" {
		t.Errorf("target-B FrameID = %q, want f.B", evB.FrameID)
	}
}

// TestNavigationEventsFromDifferentEpochsNotJoinable verifies that
// navigation events recorded under different epochs are distinguishable:
// a consumer can detect the epoch boundary and refuse to join CDP IDs
// (frameId, loaderId) across it.
func TestNavigationEventsFromDifferentEpochsNotJoinable(t *testing.T) {
	restore := withTestRecorder(20, 20)
	defer restore()

	oldEpoch := ConnectionEpoch("epoch_0000")
	newEpoch := ConnectionEpoch("epoch_0001")

	// Navigation from old epoch (before reconnect)
	handleFrameNavigatedEvent(&browserCDPEnvelope{
		Method: "Page.frameNavigated",
		Params: json.RawMessage(`{
			"frame": {"id": "FRAME-1", "loaderId": "LOADER-1", "url": "https://example.com/old"},
			"type": "Navigation"
		}`),
	}, "tab1", oldEpoch)

	// Navigation from new epoch (after reconnect) — CDP may reuse the
	// same frameId "FRAME-1", but it's a different navigation.
	handleFrameNavigatedEvent(&browserCDPEnvelope{
		Method: "Page.frameNavigated",
		Params: json.RawMessage(`{
			"frame": {"id": "FRAME-1", "loaderId": "LOADER-2", "url": "https://example.com/new"},
			"type": "Navigation"
		}`),
	}, "tab1", newEpoch)

	events := rec.QueryEvents(EventFilter{eventType: "navigation"})
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	var oldEvent, newEvent *EventRecord
	for i := range events {
		switch events[i].Epoch {
		case oldEpoch:
			oldEvent = &events[i]
		case newEpoch:
			newEvent = &events[i]
		}
	}
	if oldEvent == nil {
		t.Fatal("no event with old epoch found")
	}
	if newEvent == nil {
		t.Fatal("no event with new epoch found")
	}
	if oldEvent.Epoch == newEvent.Epoch {
		t.Fatal("old and new events have the same epoch — they should be different")
	}
}

// TestNavigationHandlerMalformedJSON verifies that handlers do not
// panic on malformed JSON input — they silently skip the event.
func TestNavigationHandlerMalformedJSON(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	handlers := []func(*browserCDPEnvelope, string, ConnectionEpoch){
		handleFrameNavigatedEvent,
		handleFrameDetachedEvent,
		handleFrameStartedLoadingEvent,
		handleFrameStoppedLoadingEvent,
		handleNavigatedWithinDocumentEvent,
		handleLifecycleEvent,
	}
	malformed := json.RawMessage(`{invalid json`)

	for _, h := range handlers {
		// Should not panic
		h(&browserCDPEnvelope{
			Method: "Page.test",
			Params: malformed,
		}, "tab1", ConnectionEpoch("epoch_0000"))
	}

	// No events should have been recorded (all malformed JSON skipped)
	events := rec.QueryEvents(EventFilter{eventType: "navigation"})
	if len(events) != 0 {
		t.Errorf("expected 0 events after malformed JSON, got %d", len(events))
	}
}

// TestNavigationHandlersConcurrent is a race test verifying that
// concurrent calls to navigation event handlers do not panic or corrupt
// the recorder. Run with -race to detect data races.
func TestNavigationHandlersConcurrent(t *testing.T) {
	restore := withTestRecorder(100, 1000)
	defer restore()

	epoch := ConnectionEpoch("epoch_0000")
	targetID := "tab-race"

	// Pre-build envelopes to avoid JSON marshaling in the hot loop.
	envelopes := []*browserCDPEnvelope{
		{Method: "Page.frameNavigated", Params: json.RawMessage(`{"frame":{"id":"f1","loaderId":"l1","url":"https://example.com/a","mimeType":"text/html"},"type":"Navigation"}`)},
		{Method: "Page.frameDetached", Params: json.RawMessage(`{"frameId":"f1","reason":"remove"}`)},
		{Method: "Page.frameStartedLoading", Params: json.RawMessage(`{"frameId":"f1"}`)},
		{Method: "Page.frameStoppedLoading", Params: json.RawMessage(`{"frameId":"f1"}`)},
		{Method: "Page.navigatedWithinDocument", Params: json.RawMessage(`{"frameId":"f1","url":"https://example.com/b"}`)},
		{Method: "Page.lifecycleEvent", Params: json.RawMessage(`{"frameId":"f1","loaderId":"l1","name":"load"}`)},
	}
	handlers := []func(*browserCDPEnvelope, string, ConnectionEpoch){
		handleFrameNavigatedEvent,
		handleFrameDetachedEvent,
		handleFrameStartedLoadingEvent,
		handleFrameStoppedLoadingEvent,
		handleNavigatedWithinDocumentEvent,
		handleLifecycleEvent,
	}

	const goroutines = 10
	const iterations = 50

	done := make(chan struct{}, goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < iterations; i++ {
				idx := i % len(handlers)
				handlers[idx](envelopes[idx], targetID, epoch)
			}
		}()
	}
	for g := 0; g < goroutines; g++ {
		<-done
	}

	// Verify all events were recorded without panic
	events := rec.QueryEvents(EventFilter{eventType: "navigation"})
	expected := goroutines * iterations
	if len(events) != expected {
		t.Errorf("expected %d events, got %d", expected, len(events))
	}
}
