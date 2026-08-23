package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type networkRequestsInput struct {
	DurationMs               int    `json:"duration_ms,omitempty"`
	All                      bool   `json:"all,omitempty"`
	Limit                    int    `json:"limit,omitempty"`
	FromRecorder             bool   `json:"from_recorder,omitempty"`
	TargetID                 string `json:"target_id,omitempty"`
	IncludeNavigationContext bool   `json:"include_navigation_context,omitempty"`
}

type networkRequestInput struct {
	URL       string            `json:"url"`
	Method    string            `json:"method,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body,omitempty"`
	TimeoutMs int               `json:"timeout_ms,omitempty"`
}

type consoleMessagesInput struct {
	DurationMs int  `json:"duration_ms,omitempty"`
	All        bool `json:"all,omitempty"`
	Limit      int  `json:"limit,omitempty"`
	MaxChars   int  `json:"max_chars,omitempty"`
}

func registerNetworkTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "network_requests",
		Description: "Capture network requests for a short duration (default 3s). Returns request/response pairs with URL, method, status, headers, and response body snippets. Set all=true to include non-error requests (default only shows 4xx/5xx and failed requests). Set from_recorder=true to query the persistent flight recorder instead of doing a live capture (faster, but no response bodies and only covers events still in the ring buffer). When from_recorder=true, set target_id to query a specific tab (defaults to the currently selected target). Set include_navigation_context=true (from_recorder only) to enrich each request with the navigation URL of its loaderId, so requests can be joined to navigations without a separate timeline query.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input networkRequestsInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit == 0 {
			limit = 40
		}

		// FromRecorder mode: query the persistent recorder for network events.
		// This is a compatibility view over recorder data — no live capture,
		// no response bodies, no new WebSocket connection.
		// Defaults to the currently selected target for isolation; an
		// explicit target_id overrides this.
		if input.FromRecorder {
			targetID := input.TargetID
			if targetID == "" {
				targetID = getCurrentTargetID()
			}
			entries := queryNetworkFromRecorder(limit, input.All, targetID, input.IncludeNavigationContext)
			return nil, map[string]any{
				"requests":  entries,
				"count":     len(entries),
				"source":    "recorder",
				"target_id": targetID,
				"note":      "Queried from flight recorder. No response bodies. Use default mode for live capture with bodies.",
			}, nil
		}

		// Live capture mode (original behavior)
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		duration := time.Duration(input.DurationMs) * time.Millisecond
		if duration <= 0 {
			duration = 3 * time.Second
		}
		if duration > 30*time.Second {
			duration = 30 * time.Second
		}
		entries, err := collectNetworkRequests(ctx, ws, duration)
		if err != nil {
			return nil, nil, err
		}
		filtered := []map[string]any{}
		for _, entry := range entries {
			if !input.All && !networkEntryImportant(entry) {
				continue
			}
			filtered = append(filtered, entry)
			if len(filtered) >= limit {
				break
			}
		}
		return nil, map[string]any{"requests": filtered, "count": len(filtered), "total_captured": len(entries), "duration_ms": duration.Milliseconds(), "source": "live"}, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "network_request",
		Description: "Make an HTTP request from the MCP server (not from the browser). Useful for testing API endpoints directly. Returns status, headers, and body.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input networkRequestInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("network_request")
		defer rec.End()
		rec.SetParams(input)

		result, err := makeHTTPRequest(input)
		if result == nil {
			result = map[string]any{}
		}
		result["action_id"] = string(rec.ID)
		if err != nil {
			rec.SetErrorFromError(err)
		}
		rec.SetResult(result)
		return nil, result, err
	})

	addTool(server, &mcp.Tool{
		Name:        "console_messages",
		Description: "Capture console messages for a short duration (default 3s). Returns console.log/warn/error and uncaught exceptions. Set all=true to include all log levels (default only shows warnings, errors, and exceptions).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input consoleMessagesInput) (*mcp.CallToolResult, any, error) {
		if activeBackend() == BackendWeChat {
			// Ensure log subscription is on so events accumulate.
			_ = automatorEnableLog()
			limit := input.Limit
			if limit == 0 {
				limit = 50
			}
			messages, dropped, total := automatorRecentLogs(input.All, limit, input.MaxChars)
			return nil, map[string]any{
				"backend":        BackendWeChat,
				"messages":       messages,
				"count":          len(messages),
				"events_total":   total,
				"events_dropped": dropped,
				"source":         "recorder",
			}, nil
		}
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		duration := time.Duration(input.DurationMs) * time.Millisecond
		if duration <= 0 {
			duration = 3 * time.Second
		}
		if duration > 30*time.Second {
			duration = 30 * time.Second
		}
		entries, err := collectLogs(ctx, ws, duration)
		if err != nil {
			return nil, nil, err
		}
		prepared, omitted := prepareLogEntries(entries, input.All, input.Limit, input.MaxChars)
		return nil, map[string]any{"messages": prepared, "count": len(prepared), "omitted": omitted, "total_captured": len(entries), "duration_ms": duration.Milliseconds()}, nil
	})
}

func collectNetworkRequests(ctx context.Context, pageWSURL string, duration time.Duration) (result []map[string]any, err error) {
	defer func() {
		if r := recover(); r != nil {
			logWarn("collectNetworkRequests panic: %v", r)
			err = fmt.Errorf("collectNetworkRequests crashed: %v", r)
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
	if err := conn.WriteJSON(map[string]any{"id": 1, "method": "Network.enable", "params": map[string]any{}}); err != nil {
		return nil, err
	}
	entries := map[string]*map[string]any{}
	order := []string{}
	for {
		var envelope browserCDPEnvelope
		if err := conn.ReadJSON(&envelope); err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				break
			}
			break
		}
		switch envelope.Method {
		case "Network.requestWillBeSent":
			var params struct {
				RequestID string `json:"requestId"`
				Request   struct {
					URL      string            `json:"url"`
					Method   string            `json:"method"`
					Headers  map[string]string `json:"headers"`
					PostData string            `json:"postData,omitempty"`
				} `json:"request"`
				Type string `json:"type,omitempty"`
			}
			if err := json.Unmarshal(envelope.Params, &params); err == nil {
				id := params.RequestID
				if _, exists := entries[id]; !exists {
					entry := map[string]any{
						"id":     id,
						"url":    params.Request.URL,
						"method": params.Request.Method,
						"type":   params.Type,
					}
					if params.Request.PostData != "" {
						entry["request_body"] = truncateRunes(params.Request.PostData, 500)
					}
					entries[id] = &entry
					order = append(order, id)
				}
			}
		case "Network.responseReceived":
			var params struct {
				RequestID string `json:"requestId"`
				Response  struct {
					Status     int               `json:"status"`
					StatusText string            `json:"statusText"`
					Headers    map[string]string `json:"headers"`
					MimeType   string            `json:"mimeType"`
					URL        string            `json:"url"`
				} `json:"response"`
			}
			if err := json.Unmarshal(envelope.Params, &params); err == nil {
				if entry, ok := entries[params.RequestID]; ok {
					(*entry)["status"] = params.Response.Status
					(*entry)["status_text"] = params.Response.StatusText
					(*entry)["mime_type"] = params.Response.MimeType
				}
			}
		case "Network.loadingFailed":
			var params struct {
				RequestID string `json:"requestId"`
				ErrorText string `json:"errorText"`
				Canceled  bool   `json:"canceled"`
			}
			if err := json.Unmarshal(envelope.Params, &params); err == nil {
				if entry, ok := entries[params.RequestID]; ok {
					(*entry)["failed"] = true
					(*entry)["error"] = params.ErrorText
					(*entry)["canceled"] = params.Canceled
				}
			}
		case "Network.loadingFinished":
			var params struct {
				RequestID string `json:"requestId"`
			}
			if err := json.Unmarshal(envelope.Params, &params); err == nil {
				if entry, ok := entries[params.RequestID]; ok {
					(*entry)["finished"] = true
					// Fetch response body for error responses (4xx/5xx)
					// to help debugging. Skip for successful responses to save tokens.
					if networkEntryImportant(*entry) {
						if body, bodyErr := fetchResponseBody(conn, params.RequestID); bodyErr == nil && body != "" {
							(*entry)["response_body"] = truncateRunes(body, 500)
						}
					}
				}
			}
		}
	}
	result = []map[string]any{}
	for _, id := range order {
		if entry, ok := entries[id]; ok {
			result = append(result, *entry)
		}
	}
	return result, nil
}

func networkEntryImportant(entry map[string]any) bool {
	if failed, ok := entry["failed"].(bool); ok && failed {
		return true
	}
	status, ok := numberValue(entry["status"])
	if !ok {
		return false
	}
	return status >= 400
}

// fetchResponseBody calls Network.getResponseBody for a completed request.
// Uses a separate request ID on the same connection.
func fetchResponseBody(conn *websocket.Conn, requestID string) (string, error) {
	// Use a high ID to avoid collision with enable/disable
	bodyReq := map[string]any{
		"id":     9000,
		"method": "Network.getResponseBody",
		"params": map[string]any{"requestId": requestID},
	}
	if err := conn.WriteJSON(bodyReq); err != nil {
		return "", err
	}
	// Read response (may get other events in between, skip them)
	for {
		var resp browserCDPEnvelope
		if err := conn.ReadJSON(&resp); err != nil {
			return "", err
		}
		if resp.ID == 9000 {
			if resp.Error != nil {
				return "", fmt.Errorf("%s", resp.Error.Message)
			}
			var body struct {
				Body   string `json:"body"`
				Base64 bool   `json:"base64Encoded"`
			}
			if err := json.Unmarshal(resp.Result, &body); err != nil {
				return "", err
			}
			return body.Body, nil
		}
		// Skip other events/responses
	}
}

func makeHTTPRequest(input networkRequestInput) (map[string]any, error) {
	method := strings.TrimSpace(input.Method)
	if method == "" {
		method = "GET"
	}
	// SSRF protection: only allow http and https schemes.
	// Reject file://, data:, gopher:, etc. that could read local files
	// or access internal services unexpectedly.
	parsedURL, err := url.Parse(input.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	scheme := strings.ToLower(parsedURL.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q: only http and https are allowed", scheme)
	}
	timeout := time.Duration(input.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	var bodyReader io.Reader = nil
	if input.Body != "" {
		bodyReader = strings.NewReader(input.Body)
	}
	req, err := http.NewRequest(method, input.URL, bodyReader)
	if err != nil {
		return nil, err
	}
	for key, value := range input.Headers {
		req.Header.Set(key, value)
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return map[string]any{"error": err.Error(), "url": input.URL, "method": method}, nil
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return map[string]any{
		"url":         input.URL,
		"method":      method,
		"status":      resp.StatusCode,
		"status_text": resp.Status,
		"headers":     flattenHeaders(resp.Header),
		"body":        truncateRunes(string(body), 2000),
		"elapsed_ms":  elapsed.Milliseconds(),
	}, nil
}

func flattenHeaders(header map[string][]string) map[string]string {
	flat := map[string]string{}
	for key, values := range header {
		if len(values) > 0 {
			flat[key] = values[0]
		}
	}
	return flat
}

// queryNetworkFromRecorder queries the flight recorder for network events
// and merges them by requestID into a compatibility view that matches
// the shape of the live network_requests tool output.
//
// When includeNavigationContext is true, each request entry is enriched
// with a navigation_url field: the URL of the navigation event whose
// loaderId matches the request's loaderId. This lets a consumer see
// which navigation a request belongs to without a separate timeline
// query. Navigation events are queried from the same target scope.
//
// This is a read-only query over recorder data. No response bodies are
// available (they are never recorded). Headers are already filtered
// through the allowlist at ingestion time.
func queryNetworkFromRecorder(limit int, all bool, targetID string, includeNavigationContext bool) []map[string]any {
	events := rec.QueryEvents(EventFilter{eventType: "network", targetID: targetID, limit: 1000})

	// Build a loaderId -> navigation URL map for navigation context.
	// Only frameNavigated events carry both a loaderId and a URL.
	var navLoaderURLs map[string]string
	if includeNavigationContext {
		navLoaderURLs = buildNavigationLoaderURLMap(targetID)
	}

	// Merge by requestID: request phase sets URL/method, response phase
	// sets status, failed phase sets error.
	merged := map[string]*map[string]any{}
	order := []string{} // preserve insertion order
	for _, e := range events {
		data, ok := e.Data.(NetworkEvidence)
		if !ok {
			// Data might be a map after JSON round-trip; try that
			if m, ok := e.Data.(map[string]any); ok {
				data = networkEvidenceFromMap(m)
			} else {
				continue
			}
		}

		reqID := data.RequestID
		if reqID == "" {
			continue
		}

		entry, exists := merged[reqID]
		if !exists {
			newEntry := map[string]any{
				"id":        reqID,
				"loader_id": data.LoaderID,
				"frame_id":  data.FrameID,
			}
			// Enrich with navigation URL when context is requested
			// and the loaderId matches a known navigation.
			if includeNavigationContext && data.LoaderID != "" {
				if navURL, ok := navLoaderURLs[data.LoaderID]; ok && navURL != "" {
					newEntry["navigation_url"] = navURL
				}
			}
			merged[reqID] = &newEntry
			entry = &newEntry
			order = append(order, reqID)
		}

		switch data.Phase {
		case "request":
			(*entry)["url"] = data.URL
			(*entry)["method"] = data.Method
			(*entry)["type"] = data.ResourceType
			if data.RedirectHops > 0 {
				(*entry)["redirect_hops"] = data.RedirectHops
			}
			if data.Headers != nil {
				(*entry)["request_headers"] = data.Headers
			}
		case "response":
			(*entry)["url"] = data.URL
			(*entry)["status"] = data.Status
			(*entry)["status_text"] = data.StatusText
			(*entry)["mime_type"] = data.MimeType
			if data.Headers != nil {
				(*entry)["response_headers"] = data.Headers
			}
		case "failed":
			(*entry)["failed"] = true
			(*entry)["error"] = data.ErrorText
			(*entry)["canceled"] = data.Canceled
		}
	}

	// Build result in order, applying filter
	result := []map[string]any{}
	for _, reqID := range order {
		entry := merged[reqID]
		if entry == nil {
			continue
		}
		if !all && !networkEntryImportant(*entry) {
			continue
		}
		// Redact at output time (defense in depth)
		redacted := redactData(*entry).(map[string]any)
		result = append(result, redacted)
		if len(result) >= limit {
			break
		}
	}
	return result
}

// networkEvidenceFromMap converts a map[string]any (from JSON round-trip)
// back to NetworkEvidence. Used when event data has been through
// truncation or JSON serialization.
func networkEvidenceFromMap(m map[string]any) NetworkEvidence {
	e := NetworkEvidence{
		RequestID:    getString(m, "request_id"),
		LoaderID:     getString(m, "loader_id"),
		FrameID:      getString(m, "frame_id"),
		URL:          getString(m, "url"),
		Method:       getString(m, "method"),
		ResourceType: getString(m, "resource_type"),
		StatusText:   getString(m, "status_text"),
		MimeType:     getString(m, "mime_type"),
		ErrorText:    getString(m, "error_text"),
		Phase:        getString(m, "phase"),
	}
	e.Status = getInt(m, "status")
	e.RedirectHops = getInt(m, "redirect_hops")
	e.Failed = getBool(m, "failed")
	e.Canceled = getBool(m, "canceled")
	if h, ok := m["headers"].(map[string]any); ok {
		e.Headers = make(map[string]string, len(h))
		for k, v := range h {
			if s, ok := v.(string); ok {
				e.Headers[k] = s
			}
		}
	}
	return e
}

func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getInt(m map[string]any, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}

func getBool(m map[string]any, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

// buildNavigationLoaderURLMap queries navigation events for the given
// target and builds a loaderId -> URL map. Only frameNavigated events
// carry both a loaderId and a URL. If multiple navigations share a
// loaderId (e.g. redirects), the last one wins — this is acceptable
// because the consumer can cross-reference the timeline for the full
// chain.
//
// This is a read-only query over recorder data, scoped to the same
// target as the network requests so navigation context never leaks
// across tabs.
func buildNavigationLoaderURLMap(targetID string) map[string]string {
	urls := map[string]string{}
	events := rec.QueryEvents(EventFilter{eventType: "navigation", targetID: targetID, limit: 500})
	for _, e := range events {
		nav, ok := e.Data.(NavigationEvidence)
		if !ok {
			if m, ok := e.Data.(map[string]any); ok {
				nav = navigationEvidenceFromMap(m)
			} else {
				continue
			}
		}
		if nav.Phase == "frameNavigated" && nav.LoaderID != "" && nav.URL != "" {
			urls[nav.LoaderID] = nav.URL
		}
	}
	return urls
}
