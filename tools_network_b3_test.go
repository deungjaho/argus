package main

import (
	"testing"
)

// --- B3: network_requests navigation context + target scoping ---

// TestQueryNetworkFromRecorderNavigationContext verifies that when
// include_navigation_context is true, each network request entry is
// enriched with a navigation_url field matching its loaderId to a
// navigation event's URL.
func TestQueryNetworkFromRecorderNavigationContext(t *testing.T) {
	restore := withTestRecorder(20, 50)
	defer restore()

	// Record a navigation event with a loaderId and URL
	rec.RecordEvent("navigation", "tab1", NavigationEvidence{
		FrameID:        "frame.1",
		LoaderID:       "loader.nav",
		URL:            "https://example.com/page",
		NavigationType: "Navigation",
		Phase:          "frameNavigated",
	})

	// Record a network request sharing the same loaderId
	rec.RecordEvent("network", "tab1", NetworkEvidence{
		RequestID: "req.1",
		LoaderID:  "loader.nav",
		FrameID:   "frame.1",
		URL:       "https://example.com/page",
		Method:    "GET",
		Phase:     "request",
	})

	// Record a network request with a different loaderId (no navigation)
	rec.RecordEvent("network", "tab1", NetworkEvidence{
		RequestID: "req.2",
		LoaderID:  "loader.other",
		URL:       "https://cdn.example.com/script.js",
		Method:    "GET",
		Phase:     "request",
	})

	entries := queryNetworkFromRecorder(10, true, "tab1", true)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Find the request with loader.nav — it should have navigation_url
	foundNavURL := false
	foundNoNavURL := false
	for _, entry := range entries {
		loaderID, _ := entry["loader_id"].(string)
		if loaderID == "loader.nav" {
			navURL, ok := entry["navigation_url"].(string)
			if !ok {
				t.Error("request with loader.nav should have navigation_url")
			} else if navURL != "https://example.com/page" {
				t.Errorf("navigation_url = %q, want https://example.com/page", navURL)
			}
			foundNavURL = true
		}
		if loaderID == "loader.other" {
			if _, ok := entry["navigation_url"]; ok {
				t.Error("request with loader.other should not have navigation_url")
			}
			foundNoNavURL = true
		}
	}
	if !foundNavURL {
		t.Error("request with loader.nav not found in results")
	}
	if !foundNoNavURL {
		t.Error("request with loader.other not found in results")
	}
}

// TestQueryNetworkFromRecorderNoNavigationContext verifies that when
// include_navigation_context is false (default), network request
// entries do not include the navigation_url field.
func TestQueryNetworkFromRecorderNoNavigationContext(t *testing.T) {
	restore := withTestRecorder(20, 50)
	defer restore()

	rec.RecordEvent("navigation", "tab1", NavigationEvidence{
		FrameID:  "frame.1",
		LoaderID: "loader.nav",
		URL:      "https://example.com/page",
		Phase:    "frameNavigated",
	})
	rec.RecordEvent("network", "tab1", NetworkEvidence{
		RequestID: "req.1",
		LoaderID:  "loader.nav",
		URL:       "https://example.com/page",
		Method:    "GET",
		Phase:     "request",
	})

	entries := queryNetworkFromRecorder(10, true, "tab1", false)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if _, ok := entries[0]["navigation_url"]; ok {
		t.Error("navigation_url should not be present when include_navigation_context is false")
	}
	// loader_id should still be present for manual cross-referencing
	if entries[0]["loader_id"] != "loader.nav" {
		t.Errorf("loader_id = %v, want loader.nav", entries[0]["loader_id"])
	}
}

// TestQueryNetworkFromRecorderNavigationContextTargetScoped verifies
// that navigation context is scoped to the same target — a network
// request in target-A does not get a navigation_url from a navigation
// event in target-B, even if they share the same loaderId value.
func TestQueryNetworkFromRecorderNavigationContextTargetScoped(t *testing.T) {
	restore := withTestRecorder(20, 50)
	defer restore()

	// Navigation in target-A
	rec.RecordEvent("navigation", "target-A", NavigationEvidence{
		FrameID:  "frame.1",
		LoaderID: "loader.shared",
		URL:      "https://a.com/page",
		Phase:    "frameNavigated",
	})
	// Navigation in target-B with same loaderId value
	rec.RecordEvent("navigation", "target-B", NavigationEvidence{
		FrameID:  "frame.1",
		LoaderID: "loader.shared",
		URL:      "https://b.com/page",
		Phase:    "frameNavigated",
	})
	// Network request in target-A
	rec.RecordEvent("network", "target-A", NetworkEvidence{
		RequestID: "req.A",
		LoaderID:  "loader.shared",
		URL:       "https://a.com/api",
		Method:    "GET",
		Phase:     "request",
	})

	entries := queryNetworkFromRecorder(10, true, "target-A", true)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	navURL, ok := entries[0]["navigation_url"].(string)
	if !ok {
		t.Fatal("navigation_url should be present")
	}
	if navURL != "https://a.com/page" {
		t.Errorf("navigation_url = %q, want https://a.com/page (target-A's navigation, not target-B's)", navURL)
	}
}

// TestBuildTimelineTargetScopedExcludesOtherTabs verifies that a
// target-scoped timeline (events queried with a targetID filter) does
// not include events from other tabs, and that CDP-ID linking does not
// cross targets when events from multiple targets are present.
func TestBuildTimelineTargetScopedExcludesOtherTabs(t *testing.T) {
	restore := withTestRecorder(20, 50)
	defer restore()

	// Record navigation events for two targets with the same loaderId
	rec.RecordEventWithAction("navigation", "target-A", "", "epoch_0000", NavigationEvidence{
		FrameID:  "frame.shared",
		LoaderID: "loader.shared",
		URL:      "https://a.com",
		Phase:    "frameNavigated",
	})
	rec.RecordEventWithAction("navigation", "target-B", "", "epoch_0000", NavigationEvidence{
		FrameID:  "frame.shared",
		LoaderID: "loader.shared",
		URL:      "https://b.com",
		Phase:    "frameNavigated",
	})
	rec.RecordEventWithAction("network", "target-A", "", "epoch_0000", NetworkEvidence{
		RequestID: "req.A",
		LoaderID:  "loader.shared",
		URL:       "https://a.com/api",
		Method:    "GET",
		Phase:     "request",
	})

	// Simulate a target-scoped query: only events from target-A
	eventsA := rec.QueryEvents(EventFilter{targetID: "target-A", limit: 50})
	for _, e := range eventsA {
		if e.TargetID != "target-A" {
			t.Fatalf("target-scoped query returned event from %s, want target-A only", e.TargetID)
		}
	}

	// Build timeline from target-A events only — should not link to
	// target-B events (they're not in the input).
	entries := buildTimeline(eventsA, nil, 50)
	for _, e := range entries {
		if e.TargetID != "target-A" {
			t.Errorf("timeline entry target_id = %s, want target-A", e.TargetID)
		}
	}

	// The two target-A events share loaderId, so they should be linked
	exactCount := 0
	for _, e := range entries {
		if e.Attribution == "exact_cdp_id" {
			exactCount++
		}
	}
	if exactCount != 2 {
		t.Errorf("expected 2 exact_cdp_id entries (nav + network sharing loaderId), got %d", exactCount)
	}
}

// TestBuildTimelineMixedTargetsNoCrossTargetLink verifies that when
// buildTimeline receives events from multiple targets with the same
// CDP ID value, they are NOT linked across targets.
func TestBuildTimelineMixedTargetsNoCrossTargetLink(t *testing.T) {
	restore := withTestRecorder(20, 50)
	defer restore()

	rec.RecordEventWithAction("navigation", "target-A", "", "epoch_0000", NavigationEvidence{
		FrameID:  "frame.dup",
		LoaderID: "loader.dup",
		Phase:    "frameNavigated",
	})
	rec.RecordEventWithAction("navigation", "target-B", "", "epoch_0000", NavigationEvidence{
		FrameID:  "frame.dup",
		LoaderID: "loader.dup",
		Phase:    "frameNavigated",
	})

	// Query all events (no target filter)
	allEvents := rec.QueryEvents(EventFilter{limit: 50})
	entries := buildTimeline(allEvents, nil, 50)

	for _, e := range entries {
		if e.Attribution == "exact_cdp_id" {
			t.Errorf("entry from target %s should not be linked across targets (attribution=%s)",
				e.TargetID, e.Attribution)
		}
	}
}
