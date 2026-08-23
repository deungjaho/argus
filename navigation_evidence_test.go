package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestNavigationEvidenceJSONFitsIn16KB verifies that a maximally-filled
// NavigationEvidence serializes to well under the 16KB event data limit.
func TestNavigationEvidenceJSONFitsIn16KB(t *testing.T) {
	e := NavigationEvidence{
		FrameID:        "frame." + strings.Repeat("A", 64),
		LoaderID:       "loader." + strings.Repeat("B", 64),
		ParentFrameID:  "parent." + strings.Repeat("C", 64),
		URL:            "https://example.com/" + strings.Repeat("d", 500),
		NavigationType: "Navigation",
		LifecycleEvent: "DOMContentLoaded",
		Reason:         "remove",
		MimeType:       "text/html",
		Phase:          "frameNavigated",
	}
	data := mustMarshalJSON(e)
	if len(data) > maxEventDataBytes {
		t.Errorf("NavigationEvidence JSON is %d bytes, exceeds %d limit", len(data), maxEventDataBytes)
	}
}

// TestNavigationEvidenceOmitsEmptyFields verifies that omitempty tags
// work correctly: fields that are zero-valued should not appear in JSON.
func TestNavigationEvidenceOmitsEmptyFields(t *testing.T) {
	e := NavigationEvidence{
		FrameID: "frame1",
		Phase:   "frameStartedLoading",
	}
	data := mustMarshalJSON(e)
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if _, ok := m["loader_id"]; ok {
		t.Error("loader_id should be omitted when empty")
	}
	if _, ok := m["url"]; ok {
		t.Error("url should be omitted when empty")
	}
	if _, ok := m["navigation_type"]; ok {
		t.Error("navigation_type should be omitted when empty")
	}
	if _, ok := m["lifecycle_event"]; ok {
		t.Error("lifecycle_event should be omitted when empty")
	}
	if _, ok := m["reason"]; ok {
		t.Error("reason should be omitted when empty")
	}
	if _, ok := m["parent_frame_id"]; ok {
		t.Error("parent_frame_id should be omitted when empty")
	}
	if _, ok := m["mime_type"]; ok {
		t.Error("mime_type should be omitted when empty")
	}
	// frame_id and phase should always be present
	if m["frame_id"] != "frame1" {
		t.Errorf("frame_id = %v, want frame1", m["frame_id"])
	}
	if m["phase"] != "frameStartedLoading" {
		t.Errorf("phase = %v, want frameStartedLoading", m["phase"])
	}
}

// TestNavigationEvidenceFromMap verifies round-trip conversion from a
// map[string]any (as produced by JSON round-trip) back to the typed
// struct.
func TestNavigationEvidenceFromMap(t *testing.T) {
	original := NavigationEvidence{
		FrameID:        "frame.abc",
		LoaderID:       "loader.xyz",
		ParentFrameID:  "frame.parent",
		URL:            "https://example.com/page",
		NavigationType: "SameDocument",
		LifecycleEvent: "load",
		Reason:         "swap",
		MimeType:       "text/html",
		Phase:          "frameNavigated",
	}
	// Marshal to JSON then unmarshal to map (simulating round-trip)
	raw, _ := json.Marshal(original)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("failed to unmarshal to map: %v", err)
	}
	got := navigationEvidenceFromMap(m)
	if got.FrameID != original.FrameID {
		t.Errorf("FrameID = %q, want %q", got.FrameID, original.FrameID)
	}
	if got.LoaderID != original.LoaderID {
		t.Errorf("LoaderID = %q, want %q", got.LoaderID, original.LoaderID)
	}
	if got.ParentFrameID != original.ParentFrameID {
		t.Errorf("ParentFrameID = %q, want %q", got.ParentFrameID, original.ParentFrameID)
	}
	if got.URL != original.URL {
		t.Errorf("URL = %q, want %q", got.URL, original.URL)
	}
	if got.NavigationType != original.NavigationType {
		t.Errorf("NavigationType = %q, want %q", got.NavigationType, original.NavigationType)
	}
	if got.LifecycleEvent != original.LifecycleEvent {
		t.Errorf("LifecycleEvent = %q, want %q", got.LifecycleEvent, original.LifecycleEvent)
	}
	if got.Reason != original.Reason {
		t.Errorf("Reason = %q, want %q", got.Reason, original.Reason)
	}
	if got.MimeType != original.MimeType {
		t.Errorf("MimeType = %q, want %q", got.MimeType, original.MimeType)
	}
	if got.Phase != original.Phase {
		t.Errorf("Phase = %q, want %q", got.Phase, original.Phase)
	}
}

// TestNavigationEvidenceToMap verifies that navigationEvidenceToMap
// includes only non-empty fields and always includes phase.
func TestNavigationEvidenceToMap(t *testing.T) {
	e := NavigationEvidence{
		FrameID: "frame1",
		Phase:   "frameDetached",
		Reason:  "remove",
	}
	m := navigationEvidenceToMap(e)
	if m["phase"] != "frameDetached" {
		t.Errorf("phase = %v, want frameDetached", m["phase"])
	}
	if m["frame_id"] != "frame1" {
		t.Errorf("frame_id = %v, want frame1", m["frame_id"])
	}
	if m["reason"] != "remove" {
		t.Errorf("reason = %v, want remove", m["reason"])
	}
	// Empty fields should not be present
	if _, ok := m["loader_id"]; ok {
		t.Error("loader_id should not be present when empty")
	}
	if _, ok := m["url"]; ok {
		t.Error("url should not be present when empty")
	}
}

// TestNavigationEvidenceToMapAllFields verifies all fields are included
// when non-empty.
func TestNavigationEvidenceToMapAllFields(t *testing.T) {
	e := NavigationEvidence{
		FrameID:        "f1",
		LoaderID:       "l1",
		ParentFrameID:  "p1",
		URL:            "https://example.com",
		NavigationType: "Navigation",
		LifecycleEvent: "networkIdle",
		Reason:         "swap",
		MimeType:       "text/html",
		Phase:          "lifecycleEvent",
	}
	m := navigationEvidenceToMap(e)
	expected := map[string]any{
		"frame_id":        "f1",
		"loader_id":       "l1",
		"parent_frame_id": "p1",
		"url":             "https://example.com",
		"navigation_type": "Navigation",
		"lifecycle_event": "networkIdle",
		"reason":          "swap",
		"mime_type":       "text/html",
		"phase":           "lifecycleEvent",
	}
	for k, want := range expected {
		if m[k] != want {
			t.Errorf("m[%q] = %v, want %v", k, m[k], want)
		}
	}
}

// TestNavigationEvidenceRecordedWithEpoch verifies that a navigation
// event recorded via RecordEventWithAction carries the connection epoch
// on the EventRecord (B1 foundation), matching the NetworkEvidence pattern.
func TestNavigationEvidenceRecordedWithEpoch(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	epoch := ConnectionEpoch("epoch_0007")
	evidence := NavigationEvidence{
		FrameID:        "frame.epoch",
		LoaderID:       "loader.epoch",
		URL:            "https://example.com/epoch-test",
		NavigationType: "Navigation",
		Phase:          "frameNavigated",
	}
	rec.RecordEventWithAction("navigation", "tab1", "", epoch, evidence)

	events := rec.QueryEvents(EventFilter{eventType: "navigation"})
	if len(events) != 1 {
		t.Fatalf("expected 1 navigation event, got %d", len(events))
	}
	if events[0].Epoch != epoch {
		t.Errorf("navigation event epoch = %q, want %q", events[0].Epoch, epoch)
	}
	// Verify the evidence data is preserved as the typed struct
	ev, ok := events[0].Data.(NavigationEvidence)
	if !ok {
		t.Fatalf("event data is %T, not NavigationEvidence", events[0].Data)
	}
	if ev.FrameID != evidence.FrameID {
		t.Errorf("FrameID = %q, want %q", ev.FrameID, evidence.FrameID)
	}
	if ev.LoaderID != evidence.LoaderID {
		t.Errorf("LoaderID = %q, want %q", ev.LoaderID, evidence.LoaderID)
	}
	if ev.NavigationType != evidence.NavigationType {
		t.Errorf("NavigationType = %q, want %q", ev.NavigationType, evidence.NavigationType)
	}
}

// TestTruncateCDPID verifies that truncateCDPID bounds CDP identifiers
// and preserves valid UTF-8.
func TestTruncateCDPID(t *testing.T) {
	short := "frame.abc123"
	if got := truncateCDPID(short); got != short {
		t.Errorf("truncateCDPID(%q) = %q, want %q", short, got, short)
	}
	long := strings.Repeat("x", maxCDPIDBytes+100)
	got := truncateCDPID(long)
	if len(got) > maxCDPIDBytes {
		t.Errorf("truncateCDPID result is %d bytes, exceeds %d", len(got), maxCDPIDBytes)
	}
	if !strings.Contains(got, "[truncated]") {
		t.Errorf("truncateCDPID should mark truncation")
	}
}

// TestTruncateNavType verifies that truncateNavType bounds navigation
// type and lifecycle event name strings.
func TestTruncateNavType(t *testing.T) {
	short := "DOMContentLoaded"
	if got := truncateNavType(short); got != short {
		t.Errorf("truncateNavType(%q) = %q, want %q", short, got, short)
	}
	long := strings.Repeat("x", maxNavTypeBytes+50)
	got := truncateNavType(long)
	if len(got) > maxNavTypeBytes {
		t.Errorf("truncateNavType result is %d bytes, exceeds %d", len(got), maxNavTypeBytes)
	}
	if !strings.Contains(got, "[truncated]") {
		t.Errorf("truncateNavType should mark truncation")
	}
}

// TestRedactNavigationEvidence verifies that redactData handles
// NavigationEvidence by converting to map and redacting recursively.
// The URL field should pass through redactString (which checks for
// sensitive patterns).
func TestRedactNavigationEvidence(t *testing.T) {
	e := NavigationEvidence{
		FrameID: "frame1",
		URL:     "https://example.com/page",
		Phase:   "frameNavigated",
	}
	result := redactData(e)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("redactData(NavigationEvidence) returned %T, want map[string]any", result)
	}
	if m["frame_id"] != "frame1" {
		t.Errorf("frame_id = %v, want frame1", m["frame_id"])
	}
	if m["phase"] != "frameNavigated" {
		t.Errorf("phase = %v, want frameNavigated", m["phase"])
	}
}
