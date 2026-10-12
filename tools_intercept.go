package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mockRouteInput struct {
	Action      string            `json:"action,omitempty"`       // mock | block | list | clear | remove (default: mock)
	URLPattern  string            `json:"url_pattern,omitempty"`  // glob (e.g. "*/api/user*") or regex (e.g. "^https://.*")
	Pattern     string            `json:"pattern,omitempty"`      // alias for url_pattern
	URL         string            `json:"url,omitempty"`          // alias for url_pattern
	Method      string            `json:"method,omitempty"`       // HTTP method filter (GET, POST, etc.)
	Status      int               `json:"status,omitempty"`       // HTTP status code (default 200 for mock)
	Headers     map[string]string `json:"headers,omitempty"`      // Custom response headers
	ContentType string            `json:"content_type,omitempty"` // Convenience helper (e.g. "application/json")
	Body        any               `json:"body,omitempty"`         // string or JSON object/array/primitive
	Remove      bool              `json:"remove,omitempty"`       // Convenience boolean alias for action="remove"
	Target      string            `json:"target,omitempty"`
	TargetID    string            `json:"target_id,omitempty"`
	TabID       string            `json:"tab_id,omitempty"`
}

type interceptInput = mockRouteInput

type interceptRule struct {
	URLPattern  string
	Method      string
	Status      int
	Body        string
	Headers     map[string]string
	ContentType string
	Action      string
	matcher     *regexp.Regexp
}

// per-target intercept state. Each target (tab) has its own Fetch.enable
// state, listener goroutine, and subscription. This replaces the global
// sync.Once which leaked across tab switches and reconnections.
type interceptState struct {
	mu           sync.RWMutex
	rules        []interceptRule
	active       bool               // whether Fetch.enable has been called
	conn         *cdpConn           // conn the listener/Fetch.enable is bound to
	sub          *eventSubscription // hub subscription for Fetch.requestPaused
	stopCh       chan struct{}      // closed to stop listener goroutine
	listenerDone chan struct{}      // closed when listener goroutine exits
}

var (
	interceptTargetsMu sync.Mutex
	interceptTargets   = map[string]*interceptState{} // keyed by wsURL
)

func getInterceptState(wsURL string) *interceptState {
	interceptTargetsMu.Lock()
	defer interceptTargetsMu.Unlock()
	if s, ok := interceptTargets[wsURL]; ok {
		return s
	}
	s := &interceptState{}
	interceptTargets[wsURL] = s
	return s
}

// removeInterceptState drops a target's rules and stops its listener.
// If a rearm raced us onto a fresh live conn, Fetch.disable is sent on
// that conn directly (not via the pool) so paused requests cannot hang
// with no responder.
func removeInterceptState(wsURL string) {
	interceptTargetsMu.Lock()
	s, ok := interceptTargets[wsURL]
	if ok {
		delete(interceptTargets, wsURL)
	}
	interceptTargetsMu.Unlock()
	if !ok {
		return
	}
	s.mu.Lock()
	conn := s.conn
	wasActive := s.active
	s.active = false
	s.mu.Unlock()

	stopInterceptListener(s, wsURL)
	if wasActive && conn != nil && conn.alive() {
		_ = conn.call("Fetch.disable", map[string]any{}, nil, 3*time.Second)
	}
}

// removeInterceptStateForTarget drops rules and stops listener for a given target ID.
func removeInterceptStateForTarget(targetID string) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return
	}
	interceptTargetsMu.Lock()
	var matchingURLs []string
	for wsURL := range interceptTargets {
		if shortID(wsURL) == targetID || strings.HasSuffix(wsURL, "/"+targetID) {
			matchingURLs = append(matchingURLs, wsURL)
		}
	}
	interceptTargetsMu.Unlock()
	for _, wsURL := range matchingURLs {
		removeInterceptState(wsURL)
	}
}

// rearmInterceptState restores Fetch interception on a freshly dialed page
// connection. Without this, rules silently stop working after a reconnect:
// the old subscription died with the old hub and Fetch.enable was bound to
// the dead connection. Called from getConn after the pool entry is set —
// never call it while holding state.mu acquired from a cdpRequest path.
func rearmInterceptState(conn *cdpConn, wsURL string) {
	interceptTargetsMu.Lock()
	state, ok := interceptTargets[wsURL]
	interceptTargetsMu.Unlock()
	if !ok {
		return
	}
	stopInterceptListener(state, wsURL) // stop stale listener if one survived
	state.mu.Lock()
	defer state.mu.Unlock()
	state.active = false
	if len(state.rules) == 0 {
		return
	}
	if err := conn.call("Fetch.enable", map[string]any{
		"patterns": []map[string]any{
			{"urlPattern": "*", "requestStage": "Request"},
		},
	}, nil, 10*time.Second); err != nil {
		logWarn("intercept: Fetch re-enable after reconnect failed: %v", err)
		return
	}
	state.active = true
	armInterceptListener(state, conn, wsURL)
}

func registerInterceptTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "mock_route",
		Description: "Intercept and mock network requests matching a URL pattern (glob or regex). Returns custom HTTP status, headers, and body. Supports actions: mock (default), block, list, clear, remove.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input mockRouteInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("mock_route")
		defer rec.End()
		res, err := executeMockRouteAction(ctx, rec, input)
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetResult(res)
		return nil, res, nil
	})
}

func executeMockRouteAction(ctx context.Context, rec *ActionRecord, input mockRouteInput) (map[string]any, error) {
	if targetID := resolveFirstNonEmpty(input.TargetID, input.TabID, input.Target); targetID != "" {
		setCurrentTargetID(targetID)
	}
	ws, err := requireTargetWS()
	if err != nil {
		return nil, err
	}
	if rec != nil {
		rec.SetTarget(getCurrentTargetID())
		rec.SetParams(input)
	}
	action := strings.ToLower(strings.TrimSpace(input.Action))
	if input.Remove {
		action = "remove"
	}
	if action == "" {
		if input.Status > 0 || input.Body != nil || len(input.Headers) > 0 || input.ContentType != "" {
			action = "mock"
		} else {
			action = "mock"
		}
	}

	state := getInterceptState(ws)

	switch action {
	case "list":
		state.mu.RLock()
		routes := make([]map[string]any, 0, len(state.rules))
		for _, r := range state.rules {
			item := map[string]any{
				"url_pattern":  r.URLPattern,
				"method":       r.Method,
				"action":       r.Action,
				"status":       r.Status,
				"headers":      r.Headers,
				"content_type": r.ContentType,
				"body":         r.Body,
			}
			routes = append(routes, item)
		}
		active := state.active
		state.mu.RUnlock()
		return map[string]any{"routes": routes, "count": len(routes), "active": active}, nil

	case "clear":
		state.mu.Lock()
		count := len(state.rules)
		state.rules = state.rules[:0]
		wasActive := state.active
		state.active = false
		state.mu.Unlock()
		if wasActive {
			_ = cdpRequest(ws, "Fetch.disable", map[string]any{}, nil)
			stopInterceptListener(state, ws)
		}
		res := map[string]any{"cleared": count}
		if rec != nil {
			res["action_id"] = string(rec.ID)
		}
		return res, nil

	case "remove":
		pattern := resolveFirstNonEmpty(input.URLPattern, input.URL, input.Pattern)
		if pattern == "" {
			return nil, fmt.Errorf("remove requires url_pattern or pattern")
		}
		method := strings.ToUpper(strings.TrimSpace(input.Method))
		state.mu.Lock()
		filtered := state.rules[:0]
		removedCount := 0
		for _, rule := range state.rules {
			if rule.URLPattern == pattern && (method == "" || rule.Method == method) {
				removedCount++
				continue
			}
			filtered = append(filtered, rule)
		}
		state.rules = filtered
		remaining := len(state.rules)
		wasActive := state.active && remaining == 0
		if wasActive {
			state.active = false
		}
		state.mu.Unlock()
		if wasActive {
			_ = cdpRequest(ws, "Fetch.disable", map[string]any{}, nil)
			stopInterceptListener(state, ws)
		}
		res := map[string]any{
			"removed":          pattern,
			"removed_count":    removedCount,
			"remaining_routes": remaining,
		}
		if rec != nil {
			res["action_id"] = string(rec.ID)
		}
		return res, nil

	case "mock", "block", "passthrough":
		pattern := resolveFirstNonEmpty(input.URLPattern, input.URL, input.Pattern)
		if pattern == "" {
			return nil, fmt.Errorf("mock_route requires url_pattern")
		}
		method := strings.ToUpper(strings.TrimSpace(input.Method))

		matcher, isRegex, err := compileRoutePattern(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid route pattern %q: %w", pattern, err)
		}
		if matcher == nil {
			return nil, fmt.Errorf("failed to compile route pattern %q: nil matcher", pattern)
		}

		status := input.Status
		if action == "mock" && status <= 0 {
			status = 200
		}

		bodyStr := ""
		isJSON := false
		if input.Body != nil {
			switch b := input.Body.(type) {
			case string:
				bodyStr = b
				trimmed := strings.TrimSpace(b)
				if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
					(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
					isJSON = true
				}
			default:
				marshaled, err := json.Marshal(b)
				if err != nil {
					return nil, fmt.Errorf("failed to encode body as JSON: %w", err)
				}
				bodyStr = string(marshaled)
				isJSON = true
			}
		}

		headers := make(map[string]string, len(input.Headers)+1)
		for k, v := range input.Headers {
			headers[k] = v
		}
		contentType := strings.TrimSpace(input.ContentType)
		if contentType == "" && isJSON {
			contentType = "application/json"
		}
		if contentType != "" {
			hasCT := false
			for k := range headers {
				if strings.EqualFold(k, "content-type") {
					hasCT = true
					break
				}
			}
			if !hasCT {
				headers["Content-Type"] = contentType
			}
		}

		rule := interceptRule{
			URLPattern:  pattern,
			Method:      method,
			Status:      status,
			Body:        bodyStr,
			Headers:     headers,
			ContentType: contentType,
			Action:      action,
			matcher:     matcher,
		}

		state.mu.Lock()
		replaced := false
		for i := range state.rules {
			if state.rules[i].URLPattern == pattern && state.rules[i].Method == method {
				state.rules[i] = rule
				replaced = true
				break
			}
		}
		if !replaced {
			state.rules = append(state.rules, rule)
		}
		needEnable := !state.active
		totalRules := len(state.rules)
		state.mu.Unlock()

		if needEnable {
			if err := cdpRequest(ws, "Fetch.enable", map[string]any{
				"patterns": []map[string]any{
					{"urlPattern": "*", "requestStage": "Request"},
				},
			}, nil); err != nil {
				state.mu.Lock()
				if !replaced {
					for i := len(state.rules) - 1; i >= 0; i-- {
						if state.rules[i].URLPattern == pattern && state.rules[i].Method == method {
							state.rules = append(state.rules[:i], state.rules[i+1:]...)
							break
						}
					}
				}
				state.mu.Unlock()
				return nil, fmt.Errorf("failed to enable Fetch interception: %w", err)
			}
			state.mu.Lock()
			state.active = true
			state.mu.Unlock()
			startInterceptListener(state, ws)
		}

		res := map[string]any{
			"rule_added":   true,
			"replaced":     replaced,
			"action":       action,
			"url_pattern":  pattern,
			"method":       method,
			"status":       status,
			"content_type": contentType,
			"is_regex":     isRegex,
			"total_rules":  totalRules,
		}
		if rec != nil {
			res["action_id"] = string(rec.ID)
		}
		return res, nil

	default:
		return nil, fmt.Errorf("unknown mock_route action: %q", action)
	}
}

// compileRoutePattern compiles a pattern into an anchored or unanchored regex.
func compileRoutePattern(pattern string) (*regexp.Regexp, bool, error) {
	trimmed := strings.TrimSpace(pattern)
	if trimmed == "" {
		return nil, false, fmt.Errorf("empty pattern")
	}

	// 1. Slash-wrapped regex /pattern/ or /pattern/i
	if strings.HasPrefix(trimmed, "/") && len(trimmed) > 2 {
		lastSlash := strings.LastIndex(trimmed, "/")
		if lastSlash > 0 {
			flags := trimmed[lastSlash+1:]
			core := trimmed[1:lastSlash]
			if flags == "" || flags == "i" {
				prefix := ""
				if flags == "i" {
					prefix = "(?i)"
				}
				re, err := regexp.Compile(prefix + core)
				if err == nil {
					return re, true, nil
				}
			}
		}
	}

	// 2. Explicit regex starts with ^, ( or ends with $
	if strings.HasPrefix(trimmed, "^") || strings.HasSuffix(trimmed, "$") || strings.HasPrefix(trimmed, "(?") {
		re, err := regexp.Compile(trimmed)
		if err != nil {
			return nil, true, fmt.Errorf("invalid regex pattern: %w", err)
		}
		return re, true, nil
	}

	// 3. Glob pattern containing * or ?
	if strings.ContainsAny(trimmed, "*?") {
		re, err := globToRegex(trimmed)
		if err != nil {
			return nil, false, fmt.Errorf("invalid glob pattern: %w", err)
		}
		return re, false, nil
	}

	// 4. Plain substring match fallback (compatible with strings.Contains)
	re, err := regexp.Compile(regexp.QuoteMeta(trimmed))
	if err != nil {
		return nil, false, err
	}
	return re, false, nil
}

func globToRegex(glob string) (*regexp.Regexp, error) {
	glob = strings.ReplaceAll(glob, `\/`, `/`)
	glob = strings.ReplaceAll(glob, `\*`, `*`)
	glob = strings.ReplaceAll(glob, `\?`, `?`)
	if !strings.Contains(glob, "://") && !strings.HasPrefix(glob, "*") {
		glob = "*" + glob
	}
	var b strings.Builder
	b.WriteString("^")
	i := 0
	for i < len(glob) {
		c := glob[i]
		switch c {
		case '*':
			for i+1 < len(glob) && glob[i+1] == '*' {
				i++
			}
			b.WriteString(".*")
			i++
		case '?':
			b.WriteString(".")
			i++
		default:
			start := i
			for i < len(glob) && glob[i] != '*' && glob[i] != '?' {
				i++
			}
			b.WriteString(regexp.QuoteMeta(glob[start:i]))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// startInterceptListener dials (or reuses) the pooled conn and arms the
// listener. Safe to call without state.mu held.
func startInterceptListener(state *interceptState, wsURL string) {
	state.mu.Lock()
	if state.sub != nil {
		state.mu.Unlock()
		return // already running
	}
	state.mu.Unlock()

	conn, err := getConn(wsURL)
	if err != nil {
		logWarn("intercept: failed to get connection for listener: %v", err)
		return
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.sub != nil {
		return // already armed (e.g. by rearmInterceptState)
	}
	armInterceptListener(state, conn, wsURL)
}

// armInterceptListener subscribes on conn and spawns the listener.
// Called with state.mu held.
func armInterceptListener(state *interceptState, conn *cdpConn, wsURL string) {
	state.conn = conn
	state.stopCh = make(chan struct{})
	state.listenerDone = make(chan struct{})
	// critical: Fetch.requestPaused must not be dropped
	state.sub = conn.subscribeEvents("intercept", 64, true)
	go runInterceptListener(state, wsURL, state.sub, state.stopCh, state.listenerDone)
}

// stopInterceptListener stops the listener goroutine and unsubscribes from events.
// Must be called without state.mu held so handleRequestPaused can acquire RLock and exit cleanly.
func stopInterceptListener(state *interceptState, wsURL string) {
	sub := state.sub
	state.conn = nil
	if sub == nil {
		return
	}
	state.sub = nil
	stopCh, doneCh := state.stopCh, state.listenerDone
	select {
	case <-stopCh:
		// already closed
	default:
		close(stopCh)
	}
	// Unsubscribe from hub so events stop flowing
	sub.hub.unsubscribe(sub)
	// Wait for listener to exit (with timeout; the listener may be inside
	// handleRequestPaused which needs state.mu — do not hold it longer).
	select {
	case <-doneCh:
	case <-listenerWaitTimeout():
	}
}

func listenerWaitTimeout() <-chan time.Time {
	return time.After(2 * time.Second)
}

// runInterceptListener reads Fetch.requestPaused events from the hub
// subscription and fulfills/blocks/continues them based on active rules.
// Exits cleanly when the subscription channel closes or stopCh is closed.
// Uses fail-open: if cdpRequest to continue/fulfill/fail fails, the request
// is allowed to proceed (better than hanging forever).
func runInterceptListener(state *interceptState, wsURL string, sub *eventSubscription, stopCh chan struct{}, doneCh chan struct{}) {
	log := NewLogger("intercept:" + shortID(wsURL))
	defer func() {
		if r := recover(); r != nil {
			log.Error("listener panic: %v", r)
		}
		close(doneCh)
		log.Debug("listener exited")
	}()

	for {
		select {
		case <-stopCh:
			return
		case envelope, ok := <-sub.ch:
			if !ok {
				// Hub closed (connection death or shutdown)
				return
			}
			if envelope == nil {
				continue
			}
			if envelope.Method != "Fetch.requestPaused" {
				continue
			}
			handleRequestPaused(state, wsURL, envelope, log)
		}
	}
}

func handleRequestPaused(state *interceptState, wsURL string, envelope *browserCDPEnvelope, log *Logger) {
	var params struct {
		RequestID string `json:"requestId"`
		Request   struct {
			URL    string `json:"url"`
			Method string `json:"method"`
		} `json:"request"`
	}
	if err := json.Unmarshal(envelope.Params, &params); err != nil {
		log.Warn("failed to decode requestPaused params: %v", err)
		return
	}

	state.mu.RLock()
	var matched *interceptRule
	for i := range state.rules {
		rule := &state.rules[i]
		if rule.matcher != nil {
			if !rule.matcher.MatchString(params.Request.URL) {
				continue
			}
		} else {
			re, _, err := compileRoutePattern(rule.URLPattern)
			if err == nil {
				if !re.MatchString(params.Request.URL) {
					continue
				}
			} else if !strings.Contains(params.Request.URL, rule.URLPattern) {
				continue
			}
		}
		if rule.Method != "" && !strings.EqualFold(rule.Method, params.Request.Method) {
			continue
		}
		ruleCopy := *rule
		matched = &ruleCopy
		break
	}
	state.mu.RUnlock()

	if matched == nil {
		// No matching rule — continue the request
		if err := cdpRequest(wsURL, "Fetch.continueRequest", map[string]any{"requestId": params.RequestID}, nil); err != nil {
			log.Warn("continueRequest failed for %s: %v (fail-open)", params.Request.URL, err)
		}
		return
	}

	switch matched.Action {
	case "block":
		if err := cdpRequest(wsURL, "Fetch.failRequest", map[string]any{
			"requestId":   params.RequestID,
			"errorReason": "Failed",
		}, nil); err != nil {
			log.Warn("failRequest failed for %s: %v (fail-open)", params.Request.URL, err)
			// fail-open: continue instead of leaving request hanging
			_ = cdpRequest(wsURL, "Fetch.continueRequest", map[string]any{"requestId": params.RequestID}, nil)
		}
	case "mock":
		responseHeaders := []map[string]any{}
		for key, value := range matched.Headers {
			responseHeaders = append(responseHeaders, map[string]any{"name": key, "value": value})
		}
		// Fetch.fulfillRequest body must be base64-encoded
		bodyB64 := base64.StdEncoding.EncodeToString([]byte(matched.Body))
		phrase := http.StatusText(matched.Status)
		if phrase == "" {
			phrase = "Custom"
		}
		if err := cdpRequest(wsURL, "Fetch.fulfillRequest", map[string]any{
			"requestId":       params.RequestID,
			"responseCode":    matched.Status,
			"responsePhrase":  phrase,
			"responseHeaders": responseHeaders,
			"body":            bodyB64,
		}, nil); err != nil {
			log.Warn("fulfillRequest failed for %s: %v (fail-open)", params.Request.URL, err)
			_ = cdpRequest(wsURL, "Fetch.continueRequest", map[string]any{"requestId": params.RequestID}, nil)
		}
	default: // passthrough
		if err := cdpRequest(wsURL, "Fetch.continueRequest", map[string]any{"requestId": params.RequestID}, nil); err != nil {
			log.Warn("continueRequest failed for %s: %v (fail-open)", params.Request.URL, err)
		}
	}
}
