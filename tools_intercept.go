package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type interceptInput struct {
	URLPattern string            `json:"url_pattern,omitempty"`
	URL        string            `json:"url,omitempty"`
	Pattern    string            `json:"pattern,omitempty"`
	Method     string            `json:"method,omitempty"`
	Status     int               `json:"status,omitempty"`
	Body       string            `json:"body,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Action     string            `json:"action,omitempty"` // mock | block | passthrough
	Remove     bool              `json:"remove,omitempty"`
	Target     string            `json:"target,omitempty"`
	TargetID   string            `json:"target_id,omitempty"`
	TabID      string            `json:"tab_id,omitempty"`
}

type interceptRule struct {
	URLPattern string
	Method     string
	Status     int
	Body       string
	Headers    map[string]string
	Action     string
}

// per-target intercept state. Each target (tab) has its own Fetch.enable
// state, listener goroutine, and subscription. This replaces the global
// sync.Once which leaked across tab switches and reconnections.
type interceptState struct {
	mu           sync.Mutex
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
	stopInterceptListener(s, wsURL)
	s.mu.Unlock()
	if wasActive && conn != nil && conn.alive() {
		_ = conn.call("Fetch.disable", map[string]any{}, nil, 3*time.Second)
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
	state.mu.Lock()
	defer state.mu.Unlock()
	stopInterceptListener(state, wsURL) // stop stale listener if one survived
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
		Name:        "intercept",
		Description: "Intercept and mock network requests matching a URL pattern. action='mock' returns a fake response (needs status+body), action='block' fails the request, action='passthrough' observes only. Set remove=true to clear a rule. Rules persist until removed or clear_intercepts is called. Rules are per-tab.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input interceptInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("intercept")
		defer rec.End()

		if targetID := resolveFirstNonEmpty(input.TargetID, input.TabID, input.Target); targetID != "" {
			setCurrentTargetID(targetID)
		}
		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())
		pattern := resolveFirstNonEmpty(input.URLPattern, input.URL, input.Pattern)
		input.URLPattern = pattern
		rec.SetParams(input)
		if pattern == "" {
			rec.SetErrorFromError(fmt.Errorf("intercept requires url_pattern"))
			return nil, nil, fmt.Errorf("intercept requires url_pattern")
		}
		state := getInterceptState(ws)
		state.mu.Lock()
		defer state.mu.Unlock()

		if input.Remove {
			filtered := state.rules[:0]
			for _, rule := range state.rules {
				if rule.URLPattern != pattern {
					filtered = append(filtered, rule)
				}
			}
			state.rules = filtered
			if len(state.rules) == 0 && state.active {
				_ = cdpRequest(ws, "Fetch.disable", map[string]any{}, nil)
				state.active = false
				stopInterceptListener(state, ws)
			}
			result := map[string]any{"removed": pattern, "remaining_rules": len(state.rules), "action_id": string(rec.ID)}
			rec.SetResult(result)
			return nil, result, nil
		}

		action := strings.TrimSpace(input.Action)
		if action == "" {
			if input.Status > 0 {
				action = "mock"
			} else {
				action = "passthrough"
			}
		}

		rule := interceptRule{
			URLPattern: pattern,
			Method:     strings.ToUpper(strings.TrimSpace(input.Method)),
			Status:     input.Status,
			Body:       input.Body,
			Headers:    input.Headers,
			Action:     action,
		}
		state.rules = append(state.rules, rule)

		// Enable Fetch domain if not already active
		if !state.active {
			if err := cdpRequest(ws, "Fetch.enable", map[string]any{
				"patterns": []map[string]any{
					{"urlPattern": "*", "requestStage": "Request"},
				},
			}, nil); err != nil {
				rec.SetErrorFromError(fmt.Errorf("failed to enable Fetch interception: %w", err))
				return nil, nil, fmt.Errorf("failed to enable Fetch interception: %w", err)
			}
			state.active = true
			startInterceptListener(state, ws)
		}

		result := map[string]any{
			"rule_added":  true,
			"url_pattern": pattern,
			"method":      rule.Method,
			"action":      action,
			"status":      input.Status,
			"total_rules": len(state.rules),
			"action_id":   string(rec.ID),
		}
		rec.SetResult(result)
		return nil, result, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "list_intercepts",
		Description: "List all active network interception rules for the current tab.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input tabScopeInput) (*mcp.CallToolResult, any, error) {
		if targetID := resolveFirstNonEmpty(input.TargetID, input.TabID, input.Target); targetID != "" {
			setCurrentTargetID(targetID)
		}
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		state := getInterceptState(ws)
		state.mu.Lock()
		defer state.mu.Unlock()
		rules := make([]map[string]any, len(state.rules))
		for i, rule := range state.rules {
			rules[i] = map[string]any{
				"url_pattern": rule.URLPattern,
				"method":      rule.Method,
				"action":      rule.Action,
				"status":      rule.Status,
			}
		}
		return nil, map[string]any{"rules": rules, "count": len(rules)}, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "clear_intercepts",
		Description: "Remove all network interception rules for the current tab and disable request interception.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input tabScopeInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("clear_intercepts")
		defer rec.End()

		if targetID := resolveFirstNonEmpty(input.TargetID, input.TabID, input.Target); targetID != "" {
			setCurrentTargetID(targetID)
		}
		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())
		rec.SetParams(input)
		state := getInterceptState(ws)
		state.mu.Lock()
		count := len(state.rules)
		state.rules = state.rules[:0]
		wasActive := state.active
		state.active = false
		state.mu.Unlock()

		if wasActive {
			_ = cdpRequest(ws, "Fetch.disable", map[string]any{}, nil)
		}
		stopInterceptListener(state, ws)
		result := map[string]any{"cleared": count, "action_id": string(rec.ID)}
		rec.SetResult(result)
		return nil, result, nil
	})
}

// startInterceptListener dials (or reuses) the pooled conn and arms the
// listener. Called with state.mu held.
func startInterceptListener(state *interceptState, wsURL string) {
	if state.sub != nil {
		return // already running
	}
	conn, err := getConn(wsURL)
	if err != nil {
		logWarn("intercept: failed to get connection for listener: %v", err)
		return
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

// stopInterceptListener is called with state.mu held.
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

	state.mu.Lock()
	matched := (*interceptRule)(nil)
	for i := range state.rules {
		rule := &state.rules[i]
		if !strings.Contains(params.Request.URL, rule.URLPattern) {
			continue
		}
		if rule.Method != "" && rule.Method != strings.ToUpper(params.Request.Method) {
			continue
		}
		matched = rule
		break
	}
	state.mu.Unlock()

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
		if err := cdpRequest(wsURL, "Fetch.fulfillRequest", map[string]any{
			"requestId":       params.RequestID,
			"responseCode":    matched.Status,
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
