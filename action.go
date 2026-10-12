package main

import (
	"encoding/json"
	"sync/atomic"
	"time"
)

// --- Action tracking ---
//
// Every agent-initiated browser action (click, type, navigate, etc.)
// gets an ActionID. This allows the future recorder to correlate
// events (console messages, network requests, exceptions) with the
// action that triggered them.
//
// Design: explicit propagation, not global state.
//
// BeginAction returns an ActionRecord. The tool handler owns the record
// and passes it to event collectors (collectLogs, collectNetworkRequests)
// so they can attribute events to the correct action.
//
// There is NO global "current action" — that approach has a causality
// bug when actions interleave: the second action to finish restores
// currentAction to the first action's ID, even though the first action
// may have already completed.
//
// Current implementation: in-memory counter, no persistence.
// P1 will add a ring buffer recorder that stores actions + events.

var actionCounter uint64

// nextActionID generates a unique ActionID.
func nextActionID() ActionID {
	n := atomic.AddUint64(&actionCounter, 1)
	return ActionID(formatActionID(n))
}

func formatActionID(n uint64) string {
	return "act_" + formatUintPadded(n, 4)
}

func formatUintPadded(n uint64, width int) string {
	digits := []byte{}
	if n == 0 {
		digits = []byte{'0'}
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	for len(digits) < width {
		digits = append([]byte{'0'}, digits...)
	}
	return string(digits)
}

// BeginAction starts tracking a new agent action.
// Returns an ActionRecord that the caller owns and must pass to
// event collectors. Call record.End() when the action completes.
//
// The ActionRecord is not stored globally — the caller is responsible
// for propagating it. This avoids the causality bug of a single
// global currentAction that breaks under interleaved actions.
func BeginAction(op string) *ActionRecord {
	id := nextActionID()
	targetID := getCurrentTargetID()
	var startURL string
	var startReqs int64
	if targetID != "" {
		startURL = rec.CurrentURL(targetID)
		startReqs = rec.ActiveRequestCount(targetID)
	}
	return &ActionRecord{
		ID:                id,
		Op:                op,
		TargetID:          targetID,
		StartTime:         time.Now(),
		StartURL:          startURL,
		StartRequestCount: startReqs,
	}
}

// End marks the action as completed, records the end time, and
// stores the action in the flight recorder.
func (r *ActionRecord) End() {
	if r == nil {
		return
	}
	now := time.Now()
	r.EndTime = &now
	rec.RecordAction(r)
}

// SetTarget records which target this action was performed on.
func (r *ActionRecord) SetTarget(targetID string) {
	if r == nil {
		return
	}
	r.TargetID = targetID
	if r.StartURL == "" && targetID != "" {
		r.StartURL = rec.CurrentURL(targetID)
	}
	if r.StartRequestCount == 0 && targetID != "" {
		r.StartRequestCount = rec.ActiveRequestCount(targetID)
	}
}

// CaptureInitialState captures the target URL and in-flight request count
// if not already captured at BeginAction.
func (r *ActionRecord) CaptureInitialState(ws string) {
	if r == nil || ws == "" {
		return
	}
	targetID := targetIDFromWSURL(ws)
	if r.TargetID == "" && targetID != "" {
		r.TargetID = targetID
	}
	if r.StartURL == "" && targetID != "" {
		r.StartURL = rec.CurrentURL(targetID)
	}
	if r.StartRequestCount == 0 && targetID != "" {
		r.StartRequestCount = rec.ActiveRequestCount(targetID)
	}
}

// SetError records an error on the action record.
func (r *ActionRecord) SetError(err *ToolError) {
	if r == nil {
		return
	}
	r.Error = err
}

// SetErrorFromError records a Go error on the action record.
// If err is nil, this is a no-op.
func (r *ActionRecord) SetErrorFromError(err error) {
	if r == nil || err == nil {
		return
	}
	r.Error = &ToolError{Message: err.Error()}
}

// attachEvidence appends events observed since ar started (console
// errors/warnings, exceptions, network failures, URL navigations) to
// the tool result map under "evidence". Call before SetResult so the
// action record captures it too. No-op when nothing noteworthy happened.
func attachEvidence(ar *ActionRecord, wsURL string, result map[string]any) {
	if ar == nil || result == nil {
		return
	}
	if ev := rec.EvidenceSince(targetIDFromWSURL(wsURL), ar.StartTime); ev != nil {
		result["evidence"] = ev
	}
}

// VerificationPayload represents the causal verification feedback returned
// alongside page miniSnapshot on interactive actions.
type VerificationPayload struct {
	Navigated       bool             `json:"navigated"`
	CurrentURL      string           `json:"current_url"`
	NetworkRequests []map[string]any `json:"network_requests"`
	DOMChanges      map[string]any   `json:"dom_changes"`
	DialogHandled   map[string]any   `json:"dialog_handled,omitempty"`
	DevOverlay      *DevOverlayError `json:"dev_overlay,omitempty"`
	Verified        bool             `json:"verified"`
}

// collectVerification gathers causal events (URL change, triggered requests,
// DOM mutations) observed on targetID during this action's window.
func (r *ActionRecord) collectVerification(ws string, domChanges map[string]any, currentURL string) map[string]any {
	if r == nil {
		return nil
	}
	targetID := r.TargetID
	if targetID == "" && ws != "" {
		targetID = targetIDFromWSURL(ws)
	}

	// 1. Navigation verification
	navigated := false
	if currentURL == "" && targetID != "" {
		currentURL = rec.CurrentURL(targetID)
	}
	if currentURL != "" && r.StartURL != "" && currentURL != r.StartURL {
		navigated = true
	}
	if !navigated && targetID != "" {
		navEvents := rec.QueryEvents(EventFilter{targetID: targetID, since: r.StartTime, limit: 20})
		for _, e := range navEvents {
			if e.Type == "navigation" {
				navigated = true
				if currentURL == "" {
					if ne, ok := e.Data.(NavigationEvidence); ok && ne.URL != "" {
						currentURL = ne.URL
					}
				}
				break
			}
		}
	}
	if currentURL == "" {
		currentURL = r.StartURL
	}
	if currentURL != "" && targetID != "" {
		rec.SetCurrentURL(targetID, currentURL)
	}

	// 2. Network verification
	netReqs := rec.NetworkRequestsSince(targetID, r.StartTime)
	if netReqs == nil {
		netReqs = []map[string]any{}
	}

	// 3. DOM mutation verification
	if domChanges == nil {
		domChanges = map[string]any{
			"added_count":   0,
			"removed_count": 0,
			"status_text":   "",
		}
	}
	addedCount, _ := numberValue(domChanges["added_count"])
	removedCount, _ := numberValue(domChanges["removed_count"])

	// 4. Dialog handled verification (CDP Dialog Anti-Deadlock)
	var dialogHandled map[string]any
	if targetID != "" {
		dialogEvents := rec.QueryEvents(EventFilter{targetID: targetID, since: r.StartTime, limit: 10})
		for _, e := range dialogEvents {
			if e.Type == "dialog" {
				if m, ok := e.Data.(map[string]any); ok {
					if dialogHandled == nil {
						// Clone newest event data to preserve terminal status while allowing metadata backfill
						dialogHandled = make(map[string]any, len(m)+2)
						for k, v := range m {
							dialogHandled[k] = v
						}
					}
					// Ensure type and message are populated from earlier opening/handled events if missing
					if msg, _ := dialogHandled["message"].(string); msg == "" {
						if prevMsg, _ := m["message"].(string); prevMsg != "" {
							dialogHandled["message"] = prevMsg
						}
					}
					if dt, _ := dialogHandled["type"].(string); dt == "" {
						if prevType, _ := m["type"].(string); prevType != "" {
							dialogHandled["type"] = prevType
						}
					}
					// Stop once we have complete type and message
					msg, _ := dialogHandled["message"].(string)
					dt, _ := dialogHandled["type"].(string)
					if msg != "" && dt != "" {
						break
					}
				}
			}
		}
		if dialogHandled != nil {
			// Fallback: If type or message is still empty, consult TargetDialogState
			if msg, _ := dialogHandled["message"].(string); msg == "" {
				if state := dialogMgr.Get(targetID); state != nil {
					if last := state.Last(); last != nil && last.Message != "" {
						dialogHandled["message"] = last.Message
					}
				}
			}
			if dt, _ := dialogHandled["type"].(string); dt == "" {
				if state := dialogMgr.Get(targetID); state != nil {
					if last := state.Last(); last != nil && last.Type != "" {
						dialogHandled["type"] = last.Type
					}
				}
			}
		} else {
			// If no events found in recorder, check if dialog was handled during this action window
			if state := dialogMgr.Get(targetID); state != nil {
				if last := state.LastHandled(); last != nil && !last.HandledAt.IsZero() && !last.HandledAt.Before(r.StartTime) {
					status := "closed"
					if last.AutoHandled {
						if last.HandledAction == "accept" {
							status = "auto_accepted"
						} else {
							status = "auto_dismissed"
						}
					}
					dialogHandled = map[string]any{
						"type":    last.Type,
						"message": last.Message,
						"status":  status,
					}
				}
			}
			// Or check if a dialog is currently open and pending
			if dialogHandled == nil && dialogMgr.HasPendingDialog(targetID) {
				d := dialogMgr.GetPendingDialog(targetID)
				dialogHandled = map[string]any{
					"type":    d.Type,
					"message": d.Message,
					"status":  "pending",
				}
			}
		}
	}

	// 4b. Dev overlay detection in causal verification (Feature F11)
	var devOverlay *DevOverlayError
	if ws != "" && !dialogMgr.HasPendingDialog(targetID) {
		if o, _ := checkDevOverlay(ws); o != nil && o.Detected {
			devOverlay = o
		}
	}

	// 5. Causal success determination
	verified := navigated || len(netReqs) > 0 || addedCount > 0 || removedCount > 0 || dialogHandled != nil
	if devOverlay != nil {
		verified = false
	}
	if r.Op == "fill" || r.Op == "type" || r.Op == "scroll" {
		verified = true
		if devOverlay != nil {
			verified = false
		}
	}

	v := map[string]any{
		"navigated":        navigated,
		"current_url":      currentURL,
		"network_requests": netReqs,
		"dom_changes":      domChanges,
		"verified":         verified,
	}
	if dialogHandled != nil {
		v["dialog_handled"] = dialogHandled
	}
	if devOverlay != nil {
		v["dev_overlay"] = devOverlay
	}
	return v
}

// attachVerification adds the "verification" object into the tool result map.
func attachVerification(ar *ActionRecord, ws string, result map[string]any, domChanges map[string]any, currentURL string) {
	if ar == nil || result == nil {
		return
	}
	v := ar.collectVerification(ws, domChanges, currentURL)
	result["verification"] = v
}

// settleAndCaptureVerification runs the DOM auto-settle probe, gathers
// post-action miniSnapshot and causal verification payload, and attaches
// "page", "verification", and "evidence" to the result map.
func settleAndCaptureVerification(ar *ActionRecord, ws string, result map[string]any) {
	if ar == nil || result == nil {
		return
	}
	var domChanges map[string]any
	var curURL string
	targetID := targetIDFromWSURL(ws)
	if ws != "" && !dialogMgr.HasPendingDialog(targetID) {
		domChanges, curURL = autoSettleDOM(ws)
		if !dialogMgr.HasPendingDialog(targetID) {
			if page := miniSnapshotOnly(ws); page != nil {
				result["page"] = page
			}
		}
	}
	attachVerification(ar, ws, result, domChanges, curURL)
	attachEvidence(ar, ws, result)
	ar.SetResult(result)
}

// SetParams records a sanitized, truncated summary of the action's
// input parameters. The summary is truncated to maxParamSummaryLen
// bytes to bound recorder memory.
func (r *ActionRecord) SetParams(params any) {
	if r == nil {
		return
	}
	r.Params = truncateSummary(params, maxParamSummaryLen)
}

// SetResult records a sanitized, truncated summary of the action's
// output. The summary is truncated to maxResultSummaryLen bytes.
func (r *ActionRecord) SetResult(result any) {
	if r == nil {
		return
	}
	r.Result = truncateSummary(result, maxResultSummaryLen)
}

const (
	maxParamSummaryLen  = 512 // max bytes for params summary
	maxResultSummaryLen = 512 // max bytes for result summary
)

// truncateSummary serializes v to JSON and truncates to maxLen bytes.
// If the JSON exceeds maxLen, it is truncated and a truncation marker
// is appended.
func truncateSummary(v any, maxLen int) any {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return map[string]any{"_error": "failed to marshal summary"}
	}
	if len(data) <= maxLen {
		// Return the original value if it fits
		return v
	}
	// Truncate and mark
	return string(data[:maxLen]) + "...[truncated]"
}

// --- Action record (for future recorder) ---

type ActionRecord struct {
	ID                ActionID   `json:"id"`
	Op                string     `json:"op"` // tool name: "click", "navigate", etc.
	TargetID          string     `json:"target_id"`
	StartTime         time.Time  `json:"start_time"`
	EndTime           *time.Time `json:"end_time,omitempty"`
	StartURL          string     `json:"start_url,omitempty"`
	StartRequestCount int64      `json:"start_request_count,omitempty"`
	Params            any        `json:"params,omitempty"` // sanitized input
	Result            any        `json:"result,omitempty"` // sanitized output
	Error             *ToolError `json:"error,omitempty"`
}

// --- Event record (for future recorder) ---
//
// Browser events (console, network, exception) will be stored with
// the ActionID that was passed to the collector. This provides
// correct causality even under interleaved actions.

type EventRecord struct {
	ID        EventID         `json:"id"`
	Type      string          `json:"type"`                // "console", "network", "exception", "navigation"
	ActionID  ActionID        `json:"action_id,omitempty"` // explicit, not global
	TargetID  string          `json:"target_id"`
	Epoch     ConnectionEpoch `json:"epoch,omitempty"` // connection generation that produced this event
	Timestamp time.Time       `json:"timestamp"`
	Data      any             `json:"data"`
}
