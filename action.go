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
	return &ActionRecord{
		ID:        id,
		Op:        op,
		StartTime: time.Now(),
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
	ID        ActionID   `json:"id"`
	Op        string     `json:"op"` // tool name: "click", "navigate", etc.
	TargetID  string     `json:"target_id"`
	StartTime time.Time  `json:"start_time"`
	EndTime   *time.Time `json:"end_time,omitempty"`
	Params    any        `json:"params,omitempty"` // sanitized input
	Result    any        `json:"result,omitempty"` // sanitized output
	Error     *ToolError `json:"error,omitempty"`
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
