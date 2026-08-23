package main

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// --- Flight Recorder ---
//
// The recorder is a bounded in-memory store of ActionRecords and
// EventRecords. It provides the evidence chain for diagnose and
// export_trace tools.
//
// Design:
//   - Two ring buffers: actions and events, each with fixed capacity.
//   - When full, oldest records are overwritten (not blocked).
//   - Thread-safe via mutex.
//   - Event-to-action attribution by time overlap: an event at time T
//     is attributed to the action whose [StartTime, EndTime] contains T.
//     This avoids the global currentAction causality bug.
//   - For events that arrive during an active action (EndTime is nil),
//     the action is attributed if the event time >= StartTime.
//
// Capacity:
//   - Default: 200 actions, 2000 events.
//   - Configurable via recorder.setCapacity().
//   - Total memory bounded: ~200 * sizeof(ActionRecord) + ~2000 * sizeof(EventRecord).

const (
	defaultActionCapacity = 200
	defaultEventCapacity  = 2000
)

type recorder struct {
	mu              sync.Mutex
	actions         []ActionRecord
	events          []EventRecord
	actHead         int   // write cursor for actions
	evtHead         int   // write cursor for events
	actCount        int64 // total actions ever recorded
	evtCount        int64 // total events ever recorded
	actCap          int
	evtCap          int
	eventsDropped   int64 // events dropped (buffer full or data too large)
	dataTruncated   int64 // events whose Data was truncated to fit maxEventDataBytes
	domainEnableErr int64 // CDP domain enable failures
	reconnectGaps   int64 // connection reconnect gaps detected
	// epochs tracks the current connection epoch per target. A target's
	// epoch increments on every reconnect so CDP IDs from before and
	// after a reconnect cannot be silently joined. The epoch counter is
	// per-target (not global) because each target has its own WebSocket
	// connection that can die and reconnect independently.
	epochs map[string]int
}

var rec = newRecorder(defaultActionCapacity, defaultEventCapacity)

const maxEventDataBytes = 16384 // 16KB max per event Data field

func newRecorder(actionCap, eventCap int) *recorder {
	if actionCap <= 0 {
		actionCap = defaultActionCapacity
	}
	if eventCap <= 0 {
		eventCap = defaultEventCapacity
	}
	return &recorder{
		actions: make([]ActionRecord, actionCap),
		events:  make([]EventRecord, eventCap),
		actCap:  actionCap,
		evtCap:  eventCap,
		epochs:  make(map[string]int),
	}
}

// RecordAction stores a completed action in the ring buffer.
// Called by tool handlers via defer after rec.End().
func (r *recorder) RecordAction(rec *ActionRecord) {
	if rec == nil {
		return
	}
	r.mu.Lock()
	r.actions[r.actHead] = *rec
	r.actHead = (r.actHead + 1) % r.actCap
	r.mu.Unlock()
	atomic.AddInt64(&r.actCount, 1)
}

// RecordEvent stores a browser event in the ring buffer.
// actionID may be empty if the event can't be attributed to a specific action;
// the recorder will attribute by time overlap in queries.
// epoch is the connection epoch the event was observed under; pass "" if
// unknown (the event will have no epoch and cannot be join-checked).
func (r *recorder) RecordEvent(eventType, targetID string, data any) {
	r.RecordEventWithAction(eventType, targetID, "", "", data)
}

// RecordEventWithAction stores a browser event with an explicit action ID
// and connection epoch. If the event data exceeds maxEventDataBytes when
// serialized, it is truncated and a truncation marker is added.
func (r *recorder) RecordEventWithAction(eventType, targetID string, actionID ActionID, epoch ConnectionEpoch, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Truncate large event data to bound memory
	truncated, wasTruncated := truncateEventData(data, maxEventDataBytes)
	if wasTruncated {
		atomic.AddInt64(&r.dataTruncated, 1)
	}

	r.events[r.evtHead] = EventRecord{
		ID:        EventID(formatEventID(atomic.AddInt64(&r.evtCount, 1))),
		Type:      eventType,
		ActionID:  actionID,
		TargetID:  targetID,
		Epoch:     epoch,
		Timestamp: time.Now(),
		Data:      truncated,
	}
	r.evtHead = (r.evtHead + 1) % r.evtCap
}

// truncateEventData serializes data to JSON and truncates if it exceeds maxLen.
// Returns the original data if it fits, or a truncated map with marker.
// Second return value is true if truncation occurred.
func truncateEventData(data any, maxLen int) (any, bool) {
	if data == nil {
		return nil, false
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return map[string]any{"_error": "failed to marshal event data"}, false
	}
	if len(raw) <= maxLen {
		return data, false
	}
	return map[string]any{
		"_truncated":      true,
		"_original_bytes": len(raw),
		"_preview":        string(raw[:maxLen/2]) + "...[truncated]",
	}, true
}

// QueryActions returns actions matching the filter, most recent first.
// If limit <= 0, returns all matching actions (up to capacity).
func (r *recorder) QueryActions(filter ActionFilter) []ActionRecord {
	r.mu.Lock()
	defer r.mu.Unlock()

	result := []ActionRecord{}
	for i := 0; i < r.actCap; i++ {
		idx := (r.actHead - 1 - i + r.actCap) % r.actCap
		a := r.actions[idx]
		if a.ID == "" {
			break // not yet written
		}
		if !filter.match(a) {
			continue
		}
		result = append(result, a)
		if filter.limit > 0 && len(result) >= filter.limit {
			break
		}
	}
	return result
}

// QueryEvents returns events matching the filter, most recent first.
// If limit <= 0, returns all matching events (up to capacity).
//
// Attribution is performed BEFORE filtering: events without an explicit
// ActionID are attributed by time overlap first, then the filter is
// applied. This ensures that filtering by action_id finds events that
// were attributed to that action by time overlap, not just events with
// an explicitly set ActionID.
func (r *recorder) QueryEvents(filter EventFilter) []EventRecord {
	r.mu.Lock()
	defer r.mu.Unlock()

	result := []EventRecord{}
	for i := 0; i < r.evtCap; i++ {
		idx := (r.evtHead - 1 - i + r.evtCap) % r.evtCap
		e := r.events[idx]
		if e.ID == "" {
			break // not yet written
		}
		// Attribute event to action by time if not explicitly set.
		// This must happen BEFORE filter.match so that filtering by
		// action_id includes time-attributed events.
		if e.ActionID == "" {
			e.ActionID = r.attributeActionLocked(e.Timestamp, e.TargetID)
		}
		if !filter.match(e) {
			continue
		}
		result = append(result, e)
		if filter.limit > 0 && len(result) >= filter.limit {
			break
		}
	}
	return result
}

// attributeActionLocked finds the action whose time interval contains t.
// Must be called with r.mu held.
func (r *recorder) attributeActionLocked(t time.Time, targetID string) ActionID {
	var best ActionID
	var bestStart time.Time
	for i := 0; i < r.actCap; i++ {
		a := r.actions[i]
		if a.ID == "" {
			continue
		}
		if targetID != "" && a.TargetID != "" && a.TargetID != targetID {
			continue
		}
		// Check if t is within [StartTime, EndTime]
		if t.Before(a.StartTime) {
			continue
		}
		if a.EndTime != nil && t.After(*a.EndTime) {
			continue
		}
		// Pick the most recently started action that contains t
		if a.StartTime.After(bestStart) || best == "" {
			best = a.ID
			bestStart = a.StartTime
		}
	}
	return best
}

// Stats returns recorder statistics.
func (r *recorder) Stats() RecorderStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RecorderStats{
		ActionsRecorded:   atomic.LoadInt64(&r.actCount),
		EventsRecorded:    atomic.LoadInt64(&r.evtCount),
		ActionCapacity:    r.actCap,
		EventCapacity:     r.evtCap,
		EventsDropped:     atomic.LoadInt64(&r.eventsDropped),
		DataTruncated:     atomic.LoadInt64(&r.dataTruncated),
		DomainEnableError: atomic.LoadInt64(&r.domainEnableErr),
		ReconnectGaps:     atomic.LoadInt64(&r.reconnectGaps),
	}
}

// RecordDomainEnableError increments the domain enable error counter.
// Called when Runtime.enable or Log.enable fails.
func (r *recorder) RecordDomainEnableError() {
	atomic.AddInt64(&r.domainEnableErr, 1)
}

// BeginEpoch mints the initial connection epoch for a target. This is
// called when a target's first WebSocket connection is established.
// If the target already has an epoch (e.g. after a Clear), the existing
// epoch is returned without incrementing. Returns the epoch the target
// is now operating under.
func (r *recorder) BeginEpoch(targetID string) ConnectionEpoch {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.epochs[targetID]; ok {
		return formatEpoch(r.epochs[targetID])
	}
	r.epochs[targetID] = 0
	return formatEpoch(0)
}

// CurrentEpoch returns the current connection epoch for a target, or
// "" if no epoch has been minted (the target has never connected).
func (r *recorder) CurrentEpoch(targetID string) ConnectionEpoch {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.epochs[targetID]
	if !ok {
		return ""
	}
	return formatEpoch(n)
}

// RecordReconnectGap records a reconnect for targetID: increments the
// reconnect gap counter, mints a new epoch, and records a "reconnect"
// event in the timeline carrying the old and new epoch so the boundary
// is visible in evidence. Returns the new epoch the target is now
// operating under. If targetID has no prior epoch (first connection
// misidentified as a reconnect), this is equivalent to BeginEpoch and
// does NOT increment the reconnect counter.
func (r *recorder) RecordReconnectGap(targetID string) ConnectionEpoch {
	r.mu.Lock()
	defer r.mu.Unlock()

	oldNum, hadPrior := r.epochs[targetID]
	newNum := 0
	if hadPrior {
		newNum = oldNum + 1
		r.epochs[targetID] = newNum
		atomic.AddInt64(&r.reconnectGaps, 1)
	} else {
		r.epochs[targetID] = newNum
	}

	oldEpoch := ConnectionEpoch("")
	if hadPrior {
		oldEpoch = formatEpoch(oldNum)
	}
	newEpoch := formatEpoch(newNum)

	// Record the epoch transition as a visible event in the timeline.
	r.events[r.evtHead] = EventRecord{
		ID:        EventID(formatEventID(atomic.AddInt64(&r.evtCount, 1))),
		Type:      "reconnect",
		TargetID:  targetID,
		Epoch:     newEpoch,
		Timestamp: time.Now(),
		Data: map[string]any{
			"old_epoch": string(oldEpoch),
			"new_epoch": string(newEpoch),
		},
	}
	r.evtHead = (r.evtHead + 1) % r.evtCap
	return newEpoch
}

// formatEpoch renders an epoch counter as a typed handle string.
func formatEpoch(n int) ConnectionEpoch {
	return ConnectionEpoch("epoch_" + formatUintPadded(uint64(n), 4))
}

// Clear removes all records from the recorder.
func (r *recorder) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.actions = make([]ActionRecord, r.actCap)
	r.events = make([]EventRecord, r.evtCap)
	r.actHead = 0
	r.evtHead = 0
	r.epochs = make(map[string]int)
	atomic.StoreInt64(&r.actCount, 0)
	atomic.StoreInt64(&r.evtCount, 0)
	atomic.StoreInt64(&r.eventsDropped, 0)
	atomic.StoreInt64(&r.dataTruncated, 0)
	atomic.StoreInt64(&r.domainEnableErr, 0)
	atomic.StoreInt64(&r.reconnectGaps, 0)
}

type RecorderStats struct {
	ActionsRecorded   int64 `json:"actions_recorded"`
	EventsRecorded    int64 `json:"events_recorded"`
	ActionCapacity    int   `json:"action_capacity"`
	EventCapacity     int   `json:"event_capacity"`
	EventsDropped     int64 `json:"events_dropped"`
	DataTruncated     int64 `json:"data_truncated"`
	DomainEnableError int64 `json:"domain_enable_error"`
	ReconnectGaps     int64 `json:"reconnect_gaps"`
}

// --- Filters ---

type ActionFilter struct {
	op       string // filter by operation name, empty = all
	targetID string // filter by target, empty = all
	limit    int
}

func (f ActionFilter) match(a ActionRecord) bool {
	if f.op != "" && a.Op != f.op {
		return false
	}
	if f.targetID != "" && a.TargetID != f.targetID {
		return false
	}
	return true
}

type EventFilter struct {
	eventType string // filter by event type, empty = all
	targetID  string // filter by target, empty = all
	actionID  string // filter by action, empty = all
	limit     int
}

func (f EventFilter) match(e EventRecord) bool {
	if f.eventType != "" && e.Type != f.eventType {
		return false
	}
	if f.targetID != "" && e.TargetID != f.targetID {
		return false
	}
	if f.actionID != "" && string(e.ActionID) != f.actionID {
		return false
	}
	return true
}

func formatEventID(n int64) string {
	return "evt_" + formatUintPadded(uint64(n), 6)
}
