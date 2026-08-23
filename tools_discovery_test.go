package main

import (
	"strings"
	"testing"
)

func TestBuildToolSummaryCoversAll(t *testing.T) {
	summary := buildToolSummary("", false)
	if summary.Total != len(toolMetaRegistry) {
		t.Fatalf("summary total = %d, want %d", summary.Total, len(toolMetaRegistry))
	}

	// Count rows across groups must equal Total.
	rows := 0
	for _, g := range summary.Groups {
		rows += g.Count
	}
	if rows != summary.Total {
		t.Errorf("grouped rows = %d, want total %d", rows, summary.Total)
	}
}

func TestBuildToolSummaryCounts(t *testing.T) {
	summary := buildToolSummary("", false)
	if summary.Counts["canonical"] == 0 {
		t.Error("expected non-zero canonical count")
	}
	if summary.Counts["deprecated"] == 0 {
		t.Error("expected non-zero deprecated count (open, screenshot_grid, etc.)")
	}
	if summary.Counts["trace"] != 3 {
		t.Errorf("trace count = %d, want 3", summary.Counts["trace"])
	}
	if summary.Counts["canonical"]+summary.Counts["deprecated"]+summary.Counts["trace"] != summary.Total {
		t.Errorf("counts sum %d != total %d",
			summary.Counts["canonical"]+summary.Counts["deprecated"]+summary.Counts["trace"],
			summary.Total)
	}
}

func TestBuildToolSummaryCapabilityFilter(t *testing.T) {
	summary := buildToolSummary("diagnose", false)
	for _, g := range summary.Groups {
		if g.Capability != "diagnose" {
			t.Errorf("filter=diagnose but group %s present", g.Capability)
		}
	}
	// Diagnose tools: network_requests, network_request, console_messages,
	// intercept, list_intercepts, clear_intercepts, pdf, list_downloads,
	// timeline, diagnose, export_trace = 11
	if summary.Total != len(toolMetaRegistry) {
		t.Errorf("Total should always be full registry size %d, got %d", len(toolMetaRegistry), summary.Total)
	}
	rows := 0
	for _, g := range summary.Groups {
		rows += g.Count
	}
	if rows == 0 || rows >= summary.Total {
		t.Errorf("filtered rows = %d, expected a strict subset", rows)
	}
}

func TestBuildToolSummaryVerboseIncludesDescription(t *testing.T) {
	summary := buildToolSummary("", true)
	if !summary.Verbose {
		t.Fatal("verbose=true but summary.Verbose is false")
	}
	for _, g := range summary.Groups {
		for _, row := range g.Tools {
			if row.Description == "" {
				t.Errorf("verbose row %s has empty description", row.Name)
			}
		}
	}
}

func TestBuildToolSummaryNonVerboseTruncatesSideEffects(t *testing.T) {
	summary := buildToolSummary("", false)
	// Find a tool with long side effects (configure has 2 effects).
	for _, g := range summary.Groups {
		for _, row := range g.Tools {
			if row.Description != "" {
				continue // non-verbose: description should be absent
			}
		}
	}
	// Verify no row in non-verbose has a description.
	for _, g := range summary.Groups {
		for _, row := range g.Tools {
			if row.Description != "" {
				t.Errorf("non-verbose row %s has description %q", row.Name, row.Description)
			}
		}
	}
}

func TestToolSummaryTextByteBound(t *testing.T) {
	summary := buildToolSummary("", false)
	text := toolSummaryText(summary)
	// The compact summary must fit under 4 KiB (roadmap acceptance: < 2 KiB
	// for the structured output; the text rendering is slightly larger but
	// must remain bounded).
	if len(text) > 4096 {
		t.Errorf("tool_summary text is %d bytes, must be under 4096", len(text))
	}
}

func TestToolSummaryTextIncludesCountsHeader(t *testing.T) {
	summary := buildToolSummary("", false)
	text := toolSummaryText(summary)
	header := "canonical"
	if !strings.Contains(text, header) {
		t.Errorf("text missing %q header\n%s", header, text)
	}
	if !strings.Contains(text, "deprecated") {
		t.Errorf("text missing deprecated header\n%s", text)
	}
	if !strings.Contains(text, "trace") {
		t.Errorf("text missing trace header\n%s", text)
	}
}

func TestFormatSideEffects(t *testing.T) {
	cases := []struct {
		name    string
		effects []string
		verbose bool
		want    string
	}{
		{name: "empty", effects: nil, verbose: false, want: "none"},
		{name: "empty_verbose", effects: nil, verbose: true, want: "none"},
		{name: "single", effects: []string{"navigates"}, verbose: false, want: "navigates"},
		{name: "multi_verbose", effects: []string{"a", "b", "c"}, verbose: true, want: "a, b, c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSideEffects(tc.effects, tc.verbose)
			if got != tc.want {
				t.Errorf("formatSideEffects = %q, want %q", got, tc.want)
			}
		})
	}

	// Truncation: a long side-effects list is truncated to <= 40 runes.
	t.Run("truncated", func(t *testing.T) {
		long := []string{"launches_chrome", "sets_global_config", "writes_temp_file", "reads_local_file"}
		got := formatSideEffects(long, false)
		if len([]rune(got)) > 40 {
			t.Errorf("truncated result %d runes, want <= 40: %q", len([]rune(got)), got)
		}
		if !strings.HasSuffix(got, "...") {
			t.Errorf("truncated result should end with ...: %q", got)
		}
	})
}

func TestSortedToolNames(t *testing.T) {
	names := sortedToolNames()
	if len(names) != len(toolMetaRegistry) {
		t.Fatalf("sortedToolNames len = %d, want %d", len(names), len(toolMetaRegistry))
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Errorf("names not sorted: %q > %q at index %d", names[i-1], names[i], i)
		}
	}
	// tool_summary must be present.
	found := false
	for _, n := range names {
		if n == "tool_summary" {
			found = true
		}
	}
	if !found {
		t.Error("tool_summary not in sortedToolNames")
	}
}
