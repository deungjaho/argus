package main

import (
	"sort"
	"strings"
	"testing"
	"time"
)

func TestTimelineEntrySorting(t *testing.T) {
	entries := []timelineEntry{
		{Seq: 0, Kind: "event", Timestamp: "2026-07-28T12:00:02.000000000Z"},
		{Seq: 0, Kind: "action", Timestamp: "2026-07-28T12:00:00.000000000Z"},
		{Seq: 0, Kind: "event", Timestamp: "2026-07-28T12:00:01.000000000Z"},
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp < entries[j].Timestamp
	})

	if entries[0].Kind != "action" {
		t.Errorf("first entry should be action (earliest), got %s", entries[0].Kind)
	}
	if entries[1].Kind != "event" {
		t.Errorf("second entry should be event, got %s", entries[1].Kind)
	}
	if entries[2].Kind != "event" {
		t.Errorf("third entry should be event (latest), got %s", entries[2].Kind)
	}
}

func TestTimelineEntryRFC3339NanoFormat(t *testing.T) {
	ts := time.Date(2026, 7, 28, 12, 0, 0, 123456789, time.UTC)
	formatted := ts.Format(time.RFC3339Nano)
	// Should include date, time, and nanoseconds with timezone
	if len(formatted) < 20 {
		t.Errorf("RFC3339Nano format too short: %s", formatted)
	}
	// Should contain the date
	if !strings.Contains(formatted, "2026-07-28") {
		t.Errorf("RFC3339Nano should contain date: %s", formatted)
	}
	// Should contain timezone
	if !strings.Contains(formatted, "Z") && !strings.Contains(formatted, "+") {
		t.Errorf("RFC3339Nano should contain timezone: %s", formatted)
	}
}

func TestTimelineTimeSortingPrecision(t *testing.T) {
	// In string dictionary comparison, "2026-07-28T12:00:00.1Z" (len 24)
	// has 'Z' at pos 23, while "2026-07-28T12:00:00.100000001Z" (len 32)
	// has '0' at pos 23. Since '0' < 'Z', naive string comparison would put
	// 12:00:00.100000001 BEFORE 12:00:00.1, inverting causality.
	t1 := "2026-07-28T12:00:00.1Z"
	t2 := "2026-07-28T12:00:00.100000001Z"

	if !(t2 < t1) {
		t.Fatalf("expected naive string comparison to be inverted ('0' < 'Z')")
	}

	entries := []timelineEntry{
		{Seq: 0, Kind: "second", Timestamp: t2},
		{Seq: 0, Kind: "first", Timestamp: t1},
	}

	sort.Slice(entries, func(i, j int) bool {
		ti := parseTimelineTime(entries[i].Timestamp)
		tj := parseTimelineTime(entries[j].Timestamp)
		if !ti.IsZero() && !tj.IsZero() {
			if !ti.Equal(tj) {
				return ti.Before(tj)
			}
		}
		return entries[i].Timestamp < entries[j].Timestamp
	})

	if entries[0].Kind != "first" {
		t.Errorf("expected entries[0] to be 'first', got %s", entries[0].Kind)
	}
	if entries[1].Kind != "second" {
		t.Errorf("expected entries[1] to be 'second', got %s", entries[1].Kind)
	}
}
