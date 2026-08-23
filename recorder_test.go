package main

import (
	"testing"
	"time"
)

// helper: swap global recorder, return restore func
func withTestRecorder(actionCap, eventCap int) func() {
	orig := rec
	rec = newRecorder(actionCap, eventCap)
	return func() { rec = orig }
}

func TestRecorderRecordAction(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()
	rec1 := BeginAction("click")
	rec1.SetTarget("tab1")
	rec1.End()

	actions := rec.QueryActions(ActionFilter{})
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	if actions[0].Op != "click" {
		t.Errorf("op = %s, want click", actions[0].Op)
	}
	if actions[0].TargetID != "tab1" {
		t.Errorf("target = %s, want tab1", actions[0].TargetID)
	}
	if actions[0].EndTime == nil {
		t.Error("EndTime should be set")
	}
}

func TestRecorderRingBufferOverflow(t *testing.T) {
	restore := withTestRecorder(5, 5)
	defer restore()
	for i := 0; i < 10; i++ {
		r := BeginAction("op")
		r.End()
	}
	actions := rec.QueryActions(ActionFilter{})
	if len(actions) != 5 {
		t.Fatalf("expected 5 actions (capacity), got %d", len(actions))
	}
	if actions[0].ID == actions[4].ID {
		t.Error("actions should be distinct")
	}
}

func TestRecorderRecordEvent(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()
	rec.RecordEvent("console", "tab1", map[string]any{"text": "hello"})
	rec.RecordEvent("network", "tab1", map[string]any{"url": "http://example.com"})

	events := rec.QueryEvents(EventFilter{})
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != "network" {
		t.Errorf("first event type = %s, want network", events[0].Type)
	}
	if events[1].Type != "console" {
		t.Errorf("second event type = %s, want console", events[1].Type)
	}
}

func TestRecorderEventFilterByType(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()
	rec.RecordEvent("console", "tab1", nil)
	rec.RecordEvent("network", "tab1", nil)
	rec.RecordEvent("exception", "tab1", nil)
	rec.RecordEvent("console", "tab1", nil)

	consoleOnly := rec.QueryEvents(EventFilter{eventType: "console"})
	if len(consoleOnly) != 2 {
		t.Fatalf("expected 2 console events, got %d", len(consoleOnly))
	}
	for _, e := range consoleOnly {
		if e.Type != "console" {
			t.Errorf("got type %s, want console", e.Type)
		}
	}
}

func TestRecorderEventAttributionByTime(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	actionRec := &ActionRecord{
		ID:        "act_test1",
		Op:        "click",
		TargetID:  "tab1",
		StartTime: time.Now().Add(-2 * time.Second),
	}
	time.Sleep(10 * time.Millisecond)
	rec.RecordEvent("console", "tab1", map[string]any{"text": "clicked"})
	time.Sleep(10 * time.Millisecond)
	now := time.Now()
	actionRec.EndTime = &now
	rec.RecordAction(actionRec)

	events := rec.QueryEvents(EventFilter{})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ActionID != "act_test1" {
		t.Errorf("event attributed to %s, want act_test1", events[0].ActionID)
	}
}

func TestRecorderEventAttributionExplicitActionID(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()
	rec.RecordEventWithAction("console", "tab1", "act_explicit", "", nil)

	events := rec.QueryEvents(EventFilter{})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ActionID != "act_explicit" {
		t.Errorf("event ActionID = %s, want act_explicit", events[0].ActionID)
	}
}

// TestRecorderFilterByActionIDFindsTimeAttributedEvents verifies the
// fix for the action_id filter order bug: events attributed by time
// overlap (not explicit ActionID) must be found when filtering by
// action_id. Previously, attribution happened AFTER filtering, so
// time-attributed events were missed.
func TestRecorderFilterByActionIDFindsTimeAttributedEvents(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	// Create an action with explicit time bounds
	actionRec := &ActionRecord{
		ID:        "act_filter_test",
		Op:        "click",
		TargetID:  "tab1",
		StartTime: time.Now().Add(-2 * time.Second),
	}
	time.Sleep(10 * time.Millisecond)
	// Record an event WITHOUT explicit ActionID — will be attributed by time
	rec.RecordEvent("console", "tab1", map[string]any{"text": "during action"})
	time.Sleep(10 * time.Millisecond)
	now := time.Now()
	actionRec.EndTime = &now
	rec.RecordAction(actionRec)

	// Filter by the action's ID — must find the time-attributed event
	events := rec.QueryEvents(EventFilter{actionID: "act_filter_test"})
	if len(events) != 1 {
		t.Fatalf("filtering by action_id should find 1 time-attributed event, got %d", len(events))
	}
	if events[0].ActionID != "act_filter_test" {
		t.Errorf("event ActionID = %s, want act_filter_test", events[0].ActionID)
	}

	// Filter by a non-existent action ID — should find nothing
	events = rec.QueryEvents(EventFilter{actionID: "act_nonexistent"})
	if len(events) != 0 {
		t.Errorf("filtering by non-existent action_id should return 0 events, got %d", len(events))
	}
}

func TestRecorderQueryActionsByOp(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()
	for _, op := range []string{"click", "navigate", "click", "fill", "click"} {
		r := BeginAction(op)
		r.End()
	}

	clicks := rec.QueryActions(ActionFilter{op: "click"})
	if len(clicks) != 3 {
		t.Fatalf("expected 3 click actions, got %d", len(clicks))
	}
}

func TestRecorderQueryActionsLimit(t *testing.T) {
	restore := withTestRecorder(20, 20)
	defer restore()
	for i := 0; i < 10; i++ {
		r := BeginAction("op")
		r.End()
	}

	limited := rec.QueryActions(ActionFilter{limit: 3})
	if len(limited) != 3 {
		t.Fatalf("expected 3 actions with limit, got %d", len(limited))
	}
}

func TestRecorderStats(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()
	for i := 0; i < 3; i++ {
		r := BeginAction("op")
		r.End()
	}
	rec.RecordEvent("console", "tab1", nil)
	rec.RecordEvent("network", "tab1", nil)

	stats := rec.Stats()
	if stats.ActionsRecorded != 3 {
		t.Errorf("actions recorded = %d, want 3", stats.ActionsRecorded)
	}
	if stats.EventsRecorded != 2 {
		t.Errorf("events recorded = %d, want 2", stats.EventsRecorded)
	}
	if stats.EventsDropped != 0 {
		t.Errorf("events dropped = %d, want 0", stats.EventsDropped)
	}
	if stats.DataTruncated != 0 {
		t.Errorf("data truncated = %d, want 0", stats.DataTruncated)
	}
}

func TestRecorderDataTruncation(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	// Record an event with data larger than maxEventDataBytes
	largeData := map[string]any{}
	for i := 0; i < 1000; i++ {
		largeData[formatInt(i)] = "this is a long string that will exceed 16KB when serialized"
	}
	rec.RecordEvent("console", "tab1", largeData)

	events := rec.QueryEvents(EventFilter{})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	data, ok := events[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("event data should be a map, got %T", events[0].Data)
	}
	if _, ok := data["_truncated"]; !ok {
		t.Error("large event data should be truncated with _truncated marker")
	}

	stats := rec.Stats()
	if stats.DataTruncated != 1 {
		t.Errorf("data truncated = %d, want 1", stats.DataTruncated)
	}
}

func TestRecorderDomainEnableError(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	rec.RecordDomainEnableError()
	rec.RecordDomainEnableError()

	stats := rec.Stats()
	if stats.DomainEnableError != 2 {
		t.Errorf("domain enable error = %d, want 2", stats.DomainEnableError)
	}
}

func TestRecorderReconnectGaps(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	// First connection establishes epoch 0 (not a reconnect).
	rec.BeginEpoch("tab1")
	rec.RecordReconnectGap("tab1")

	stats := rec.Stats()
	if stats.ReconnectGaps != 1 {
		t.Errorf("reconnect gaps = %d, want 1", stats.ReconnectGaps)
	}
}

func TestRecorderClear(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()
	r := BeginAction("op")
	r.End()
	rec.RecordEvent("console", "tab1", nil)

	rec.Clear()

	if len(rec.QueryActions(ActionFilter{})) != 0 {
		t.Error("actions not cleared")
	}
	if len(rec.QueryEvents(EventFilter{})) != 0 {
		t.Error("events not cleared")
	}
}

func TestRecorderConcurrentAccess(t *testing.T) {
	restore := withTestRecorder(100, 100)
	defer restore()
	done := make(chan struct{})

	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 50; j++ {
				r := BeginAction("op")
				r.End()
				rec.RecordEvent("console", "tab1", nil)
			}
			done <- struct{}{}
		}()
	}

	go func() {
		for j := 0; j < 100; j++ {
			rec.QueryActions(ActionFilter{})
			rec.QueryEvents(EventFilter{})
			rec.Stats()
		}
		done <- struct{}{}
	}()

	for i := 0; i < 11; i++ {
		<-done
	}
}

// TestRecorderBeginEpochMintsEpochZero verifies that BeginEpoch mints
// epoch_0000 for a target that has never connected, and that calling
// it again for the same target returns the same epoch without
// incrementing.
func TestRecorderBeginEpochMintsEpochZero(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	epoch := rec.BeginEpoch("tab1")
	if epoch != "epoch_0000" {
		t.Fatalf("first BeginEpoch = %q, want epoch_0000", epoch)
	}

	// Calling again returns the same epoch (idempotent).
	epoch2 := rec.BeginEpoch("tab1")
	if epoch2 != "epoch_0000" {
		t.Fatalf("second BeginEpoch = %q, want epoch_0000", epoch2)
	}

	// No reconnect gap should be counted for the first connection.
	stats := rec.Stats()
	if stats.ReconnectGaps != 0 {
		t.Errorf("reconnect gaps = %d, want 0 for first connection", stats.ReconnectGaps)
	}
}

// TestRecorderCurrentEpochUnconnected verifies that CurrentEpoch
// returns "" for a target that has never connected.
func TestRecorderCurrentEpochUnconnected(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	if ep := rec.CurrentEpoch("never-connected"); ep != "" {
		t.Fatalf("CurrentEpoch for unconnected target = %q, want empty", ep)
	}
}

// TestRecorderRecordReconnectGapMintsNewEpoch verifies that
// RecordReconnectGap increments the epoch, increments the reconnect
// counter, and returns the new epoch.
func TestRecorderRecordReconnectGapMintsNewEpoch(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	rec.BeginEpoch("tab1")
	newEpoch := rec.RecordReconnectGap("tab1")

	if newEpoch != "epoch_0001" {
		t.Fatalf("epoch after reconnect = %q, want epoch_0001", newEpoch)
	}
	if rec.CurrentEpoch("tab1") != "epoch_0001" {
		t.Fatalf("CurrentEpoch after reconnect = %q, want epoch_0001", rec.CurrentEpoch("tab1"))
	}

	stats := rec.Stats()
	if stats.ReconnectGaps != 1 {
		t.Errorf("reconnect gaps = %d, want 1", stats.ReconnectGaps)
	}
}

// TestRecorderRecordReconnectGapWithoutPriorEpoch verifies that calling
// RecordReconnectGap on a target with no prior epoch does NOT count as
// a reconnect (it's effectively a first connection).
func TestRecorderRecordReconnectGapWithoutPriorEpoch(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	epoch := rec.RecordReconnectGap("tab-no-prior")
	if epoch != "epoch_0000" {
		t.Fatalf("epoch = %q, want epoch_0000 (first connection)", epoch)
	}
	stats := rec.Stats()
	if stats.ReconnectGaps != 0 {
		t.Errorf("reconnect gaps = %d, want 0 (no prior epoch)", stats.ReconnectGaps)
	}
}

// TestRecorderReconnectGapRecordsTransitionEvent verifies that a
// reconnect produces a visible "reconnect" event in the timeline
// carrying the old and new epoch.
func TestRecorderReconnectGapRecordsTransitionEvent(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	rec.BeginEpoch("tab1")
	rec.RecordReconnectGap("tab1")

	events := rec.QueryEvents(EventFilter{eventType: "reconnect"})
	if len(events) != 1 {
		t.Fatalf("expected 1 reconnect event, got %d", len(events))
	}
	data, ok := events[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("reconnect event data is %T, want map", events[0].Data)
	}
	if data["old_epoch"] != "epoch_0000" {
		t.Errorf("old_epoch = %v, want epoch_0000", data["old_epoch"])
	}
	if data["new_epoch"] != "epoch_0001" {
		t.Errorf("new_epoch = %v, want epoch_0001", data["new_epoch"])
	}
	if events[0].Epoch != "epoch_0001" {
		t.Errorf("reconnect event epoch = %q, want epoch_0001", events[0].Epoch)
	}
}

// TestRecorderEpochsArePerTarget verifies that epochs are tracked
// independently per target — reconnecting one target does not affect
// another target's epoch.
func TestRecorderEpochsArePerTarget(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	rec.BeginEpoch("tab1")
	rec.BeginEpoch("tab2")

	rec.RecordReconnectGap("tab1")

	if rec.CurrentEpoch("tab1") != "epoch_0001" {
		t.Errorf("tab1 epoch = %q, want epoch_0001", rec.CurrentEpoch("tab1"))
	}
	if rec.CurrentEpoch("tab2") != "epoch_0000" {
		t.Errorf("tab2 epoch = %q, want epoch_0000 (unaffected)", rec.CurrentEpoch("tab2"))
	}
}

// TestRecorderEventCarriesEpoch verifies that events recorded with an
// epoch carry that epoch in the EventRecord.
func TestRecorderEventCarriesEpoch(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	epoch := rec.BeginEpoch("tab1")
	rec.RecordEventWithAction("console", "tab1", "act_1", epoch, map[string]any{"text": "hello"})

	events := rec.QueryEvents(EventFilter{})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Epoch != epoch {
		t.Errorf("event epoch = %q, want %q", events[0].Epoch, epoch)
	}
}

// TestRecorderClearResetsEpochs verifies that Clear resets the epoch
// tracking so a subsequent BeginEpoch starts from epoch_0000 again.
func TestRecorderClearResetsEpochs(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	rec.BeginEpoch("tab1")
	rec.RecordReconnectGap("tab1")
	rec.RecordReconnectGap("tab1")
	if rec.CurrentEpoch("tab1") != "epoch_0002" {
		t.Fatalf("epoch before clear = %q, want epoch_0002", rec.CurrentEpoch("tab1"))
	}

	rec.Clear()

	epoch := rec.BeginEpoch("tab1")
	if epoch != "epoch_0000" {
		t.Fatalf("epoch after clear = %q, want epoch_0000", epoch)
	}
}
