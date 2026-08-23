package main

import (
	"testing"
	"time"
)

// --- B3: exact CDP-ID attribution in the unified timeline ---

// TestExtractCDPIDsFromNavigationEvidence verifies that CDP identifiers
// are extracted from a typed NavigationEvidence event data.
func TestExtractCDPIDsFromNavigationEvidence(t *testing.T) {
	nav := NavigationEvidence{
		FrameID:       "frame.abc",
		LoaderID:      "loader.xyz",
		ParentFrameID: "frame.parent",
		Phase:         "frameNavigated",
	}
	ids := extractCDPIDs(nav)
	if ids["frame_id"] != "frame.abc" {
		t.Errorf("frame_id = %q, want frame.abc", ids["frame_id"])
	}
	if ids["loader_id"] != "loader.xyz" {
		t.Errorf("loader_id = %q, want loader.xyz", ids["loader_id"])
	}
	if ids["parent_frame_id"] != "frame.parent" {
		t.Errorf("parent_frame_id = %q, want frame.parent", ids["parent_frame_id"])
	}
}

// TestExtractCDPIDsFromNetworkEvidence verifies that CDP identifiers
// are extracted from a typed NetworkEvidence event data.
func TestExtractCDPIDsFromNetworkEvidence(t *testing.T) {
	net := NetworkEvidence{
		RequestID: "req.123",
		LoaderID:  "loader.xyz",
		FrameID:   "frame.abc",
		Phase:     "request",
	}
	ids := extractCDPIDs(net)
	if ids["request_id"] != "req.123" {
		t.Errorf("request_id = %q, want req.123", ids["request_id"])
	}
	if ids["loader_id"] != "loader.xyz" {
		t.Errorf("loader_id = %q, want loader.xyz", ids["loader_id"])
	}
	if ids["frame_id"] != "frame.abc" {
		t.Errorf("frame_id = %q, want frame.abc", ids["frame_id"])
	}
}

// TestExtractCDPIDsFromMap verifies extraction from a map[string]any
// (as produced by JSON round-trip or truncation).
func TestExtractCDPIDsFromMap(t *testing.T) {
	m := map[string]any{
		"frame_id":  "frame.map",
		"loader_id": "loader.map",
		"url":       "https://example.com",
		"phase":     "frameNavigated",
	}
	ids := extractCDPIDs(m)
	if ids["frame_id"] != "frame.map" {
		t.Errorf("frame_id = %q, want frame.map", ids["frame_id"])
	}
	if ids["loader_id"] != "loader.map" {
		t.Errorf("loader_id = %q, want loader.map", ids["loader_id"])
	}
	if _, ok := ids["url"]; ok {
		t.Error("url should not be extracted as a CDP ID")
	}
}

// TestExtractCDPIDsFromNonCDPEvent verifies that events without CDP IDs
// (console, exception, log) return an empty map.
func TestExtractCDPIDsFromNonCDPEvent(t *testing.T) {
	ids := extractCDPIDs(map[string]any{"text": "hello", "level": "error"})
	if len(ids) != 0 {
		t.Errorf("expected empty CDP IDs for console event, got %v", ids)
	}
}

// TestBuildTimelineExactCDPIDLink verifies that two events sharing the
// same loaderId within the same target are labelled with
// attribution="exact_cdp_id" and confidence="high".
func TestBuildTimelineExactCDPIDLink(t *testing.T) {
	base := time.Date(2026, 7, 28, 12, 0, 0, 100000000, time.UTC)
	events := []EventRecord{
		{
			ID:        "evt_1",
			Type:      "navigation",
			TargetID:  "tab1",
			Timestamp: base,
			Data: NavigationEvidence{
				FrameID:  "frame.1",
				LoaderID: "loader.shared",
				Phase:    "frameNavigated",
			},
		},
		{
			ID:        "evt_2",
			Type:      "network",
			TargetID:  "tab1",
			Timestamp: base.Add(100 * time.Millisecond),
			Data: NetworkEvidence{
				RequestID: "req.1",
				LoaderID:  "loader.shared",
				FrameID:   "frame.1",
				Phase:     "request",
			},
		},
	}

	entries := buildTimeline(events, nil, 50)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	for _, e := range entries {
		if e.Attribution != "exact_cdp_id" {
			t.Errorf("entry %s attribution = %q, want exact_cdp_id", e.EventID, e.Attribution)
		}
		if e.Confidence != "high" {
			t.Errorf("entry %s confidence = %q, want high", e.EventID, e.Confidence)
		}
		// LinkedBy should be one of the shared CDP IDs (map iteration
		// order is non-deterministic, so accept either loader_id or
		// frame_id).
		if e.LinkedBy != "loader_id:loader.shared" && e.LinkedBy != "frame_id:frame.1" {
			t.Errorf("entry %s linked_by = %q, want loader_id:loader.shared or frame_id:frame.1",
				e.EventID, e.LinkedBy)
		}
	}
}

// TestBuildTimelineNoCDPIDLinkTimeOverlap verifies that events without
// shared CDP IDs keep the time_overlap attribution when they have an
// ActionID.
func TestBuildTimelineNoCDPIDLinkTimeOverlap(t *testing.T) {
	base := time.Date(2026, 7, 28, 12, 0, 0, 100000000, time.UTC)
	events := []EventRecord{
		{
			ID:        "evt_1",
			Type:      "console",
			TargetID:  "tab1",
			ActionID:  "act_1",
			Timestamp: base,
			Data:      map[string]any{"text": "hello"},
		},
		{
			ID:        "evt_2",
			Type:      "exception",
			TargetID:  "tab1",
			ActionID:  "act_1",
			Timestamp: base.Add(50 * time.Millisecond),
			Data:      map[string]any{"text": "error"},
		},
	}

	entries := buildTimeline(events, nil, 50)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Attribution != "time_overlap" {
			t.Errorf("entry %s attribution = %q, want time_overlap", e.EventID, e.Attribution)
		}
		if e.Confidence != "heuristic" {
			t.Errorf("entry %s confidence = %q, want heuristic", e.EventID, e.Confidence)
		}
		if e.LinkedBy != "" {
			t.Errorf("entry %s linked_by should be empty, got %q", e.EventID, e.LinkedBy)
		}
	}
}

// TestBuildTimelineCrossTypeCDPIDLink verifies that a navigation event
// and a network event sharing the same loaderId are labelled
// exact_cdp_id, supporting the chain:
// action -> navigation -> frame/loader -> request -> outcome.
func TestBuildTimelineCrossTypeCDPIDLink(t *testing.T) {
	base := time.Date(2026, 7, 28, 12, 0, 0, 100000000, time.UTC)
	events := []EventRecord{
		{
			ID:        "evt_nav",
			Type:      "navigation",
			TargetID:  "tab1",
			Timestamp: base,
			Data: NavigationEvidence{
				FrameID:        "frame.top",
				LoaderID:       "loader.chain",
				URL:            "https://example.com/page",
				NavigationType: "Navigation",
				Phase:          "frameNavigated",
			},
		},
		{
			ID:        "evt_req",
			Type:      "network",
			TargetID:  "tab1",
			Timestamp: base.Add(10 * time.Millisecond),
			Data: NetworkEvidence{
				RequestID: "req.chain",
				LoaderID:  "loader.chain",
				FrameID:   "frame.top",
				URL:       "https://example.com/page",
				Method:    "GET",
				Phase:     "request",
			},
		},
		{
			ID:        "evt_resp",
			Type:      "network",
			TargetID:  "tab1",
			Timestamp: base.Add(200 * time.Millisecond),
			Data: NetworkEvidence{
				RequestID: "req.chain",
				LoaderID:  "loader.chain",
				Status:    200,
				Phase:     "response",
			},
		},
	}

	entries := buildTimeline(events, nil, 50)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Attribution != "exact_cdp_id" {
			t.Errorf("entry %s (%s) attribution = %q, want exact_cdp_id",
				e.EventID, e.EventType, e.Attribution)
		}
		if e.Confidence != "high" {
			t.Errorf("entry %s confidence = %q, want high", e.EventID, e.Confidence)
		}
	}
}

// TestBuildTimelineDifferentTargetsNotLinked verifies that events from
// different targets carrying the same CDP ID value are NOT linked,
// because CDP IDs are scoped to a target's session.
func TestBuildTimelineDifferentTargetsNotLinked(t *testing.T) {
	base := time.Date(2026, 7, 28, 12, 0, 0, 100000000, time.UTC)
	events := []EventRecord{
		{
			ID:        "evt_1",
			Type:      "navigation",
			TargetID:  "tab1",
			Timestamp: base,
			Data: NavigationEvidence{
				FrameID: "frame.same",
				Phase:   "frameNavigated",
			},
		},
		{
			ID:        "evt_2",
			Type:      "navigation",
			TargetID:  "tab2",
			Timestamp: base.Add(10 * time.Millisecond),
			Data: NavigationEvidence{
				FrameID: "frame.same",
				Phase:   "frameNavigated",
			},
		},
	}

	entries := buildTimeline(events, nil, 50)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Attribution == "exact_cdp_id" {
			t.Errorf("entry %s from target %s should not be linked across targets",
				e.EventID, e.TargetID)
		}
		if e.LinkedBy != "" {
			t.Errorf("entry %s linked_by should be empty, got %q", e.EventID, e.LinkedBy)
		}
	}
}

// TestBuildTimelineSingleCDPIDEventNotLinked verifies that a single
// event carrying a CDP ID (with no other event sharing it) is not
// labelled exact_cdp_id — a link requires at least two entries.
func TestBuildTimelineSingleCDPIDEventNotLinked(t *testing.T) {
	base := time.Date(2026, 7, 28, 12, 0, 0, 100000000, time.UTC)
	events := []EventRecord{
		{
			ID:        "evt_1",
			Type:      "navigation",
			TargetID:  "tab1",
			Timestamp: base,
			Data: NavigationEvidence{
				FrameID:  "frame.solo",
				LoaderID: "loader.solo",
				Phase:    "frameNavigated",
			},
		},
	}

	entries := buildTimeline(events, nil, 50)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Attribution == "exact_cdp_id" {
		t.Error("single event should not be labelled exact_cdp_id")
	}
	if entries[0].LinkedBy != "" {
		t.Errorf("linked_by should be empty, got %q", entries[0].LinkedBy)
	}
	// LinkedIDs should still be populated so consumers can query by CDP ID
	if entries[0].LinkedIDs["loader_id"] != "loader.solo" {
		t.Errorf("linked_ids loader_id = %q, want loader.solo", entries[0].LinkedIDs["loader_id"])
	}
}

// TestBuildTimelineExactCDPIDOverridesTimeOverlap verifies that when an
// event has both a time-overlap action attribution and a shared CDP ID,
// the exact_cdp_id attribution takes precedence (it is the stronger,
// deterministic link). The ActionID field remains populated so the
// consumer can see both relationships.
func TestBuildTimelineExactCDPIDOverridesTimeOverlap(t *testing.T) {
	base := time.Date(2026, 7, 28, 12, 0, 0, 100000000, time.UTC)
	events := []EventRecord{
		{
			ID:        "evt_1",
			Type:      "navigation",
			TargetID:  "tab1",
			ActionID:  "act_1",
			Timestamp: base,
			Data: NavigationEvidence{
				FrameID:  "frame.1",
				LoaderID: "loader.shared",
				Phase:    "frameNavigated",
			},
		},
		{
			ID:        "evt_2",
			Type:      "network",
			TargetID:  "tab1",
			Timestamp: base.Add(10 * time.Millisecond),
			Data: NetworkEvidence{
				RequestID: "req.1",
				LoaderID:  "loader.shared",
				Phase:     "request",
			},
		},
	}

	entries := buildTimeline(events, nil, 50)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Find the navigation entry (sort order may vary with timestamp
	// precision, so locate by event type rather than position).
	var nav *timelineEntry
	for i := range entries {
		if entries[i].EventType == "navigation" {
			nav = &entries[i]
			break
		}
	}
	if nav == nil {
		t.Fatal("navigation entry not found in timeline")
	}
	if nav.Attribution != "exact_cdp_id" {
		t.Errorf("navigation attribution = %q, want exact_cdp_id", nav.Attribution)
	}
	if nav.ActionID != "act_1" {
		t.Errorf("navigation action_id = %q, want act_1 (should still be populated)", nav.ActionID)
	}
}

// TestBuildTimelineActionsKeepExplicitAttribution verifies that action
// entries are not affected by CDP-ID linking — they keep
// attribution="explicit" and confidence="high".
func TestBuildTimelineActionsKeepExplicitAttribution(t *testing.T) {
	base := time.Date(2026, 7, 28, 12, 0, 0, 100000000, time.UTC)
	endTime := base.Add(500 * time.Millisecond)
	actions := []ActionRecord{
		{
			ID:        "act_1",
			Op:        "navigate",
			TargetID:  "tab1",
			StartTime: base,
			EndTime:   &endTime,
		},
	}
	events := []EventRecord{
		{
			ID:        "evt_1",
			Type:      "navigation",
			TargetID:  "tab1",
			Timestamp: base.Add(100 * time.Millisecond),
			Data: NavigationEvidence{
				FrameID:  "frame.1",
				LoaderID: "loader.1",
				Phase:    "frameNavigated",
			},
		},
	}

	entries := buildTimeline(events, actions, 50)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Kind == "action" {
			if e.Attribution != "explicit" {
				t.Errorf("action attribution = %q, want explicit", e.Attribution)
			}
			if e.Confidence != "high" {
				t.Errorf("action confidence = %q, want high", e.Confidence)
			}
		}
	}
}

// TestBuildTimelineSequenceNumbers verifies that sequence numbers are
// assigned in timestamp order after sorting.
func TestBuildTimelineSequenceNumbers(t *testing.T) {
	base := time.Date(2026, 7, 28, 12, 0, 0, 100000000, time.UTC)
	events := []EventRecord{
		{
			ID:        "evt_3",
			Type:      "console",
			TargetID:  "tab1",
			Timestamp: base.Add(2 * time.Second),
			Data:      map[string]any{"text": "c"},
		},
		{
			ID:        "evt_1",
			Type:      "console",
			TargetID:  "tab1",
			Timestamp: base,
			Data:      map[string]any{"text": "a"},
		},
		{
			ID:        "evt_2",
			Type:      "console",
			TargetID:  "tab1",
			Timestamp: base.Add(1 * time.Second),
			Data:      map[string]any{"text": "b"},
		},
	}

	entries := buildTimeline(events, nil, 50)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Seq != 1 || entries[1].Seq != 2 || entries[2].Seq != 3 {
		t.Errorf("seq numbers = %d,%d,%d, want 1,2,3",
			entries[0].Seq, entries[1].Seq, entries[2].Seq)
	}
	if entries[0].EventID != "evt_1" {
		t.Errorf("first entry should be evt_1 (earliest), got %s", entries[0].EventID)
	}
}
