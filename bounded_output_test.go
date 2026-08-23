package main

import "testing"

func TestNewTruncationMetaNotTruncated(t *testing.T) {
	meta := newTruncationMeta(5, 5, "test")
	if meta.Truncated {
		t.Error("expected Truncated=false when total==shown")
	}
	if meta.TotalCount != 5 {
		t.Errorf("TotalCount = %d, want 5", meta.TotalCount)
	}
	if meta.ShownCount != 5 {
		t.Errorf("ShownCount = %d, want 5", meta.ShownCount)
	}
}

func TestNewTruncationMetaTruncated(t *testing.T) {
	meta := newTruncationMeta(100, 50, "max_matches")
	if !meta.Truncated {
		t.Error("expected Truncated=true when total>shown")
	}
	if meta.TotalCount != 100 {
		t.Errorf("TotalCount = %d, want 100", meta.TotalCount)
	}
	if meta.ShownCount != 50 {
		t.Errorf("ShownCount = %d, want 50", meta.ShownCount)
	}
	if meta.Reason != "max_matches" {
		t.Errorf("Reason = %q, want max_matches", meta.Reason)
	}
}

func TestAttachTruncation(t *testing.T) {
	m := map[string]any{"count": 5}
	meta := TruncationMeta{Truncated: true, TotalCount: 10, ShownCount: 5, Reason: "max"}
	attachTruncation(m, meta)
	extracted, ok := truncationFromMap(m)
	if !ok {
		t.Fatal("truncation field not found after attach")
	}
	if !extracted.Truncated {
		t.Error("extracted Truncated = false, want true")
	}
	if extracted.TotalCount != 10 {
		t.Errorf("extracted TotalCount = %d, want 10", extracted.TotalCount)
	}
	if extracted.ShownCount != 5 {
		t.Errorf("extracted ShownCount = %d, want 5", extracted.ShownCount)
	}
	if extracted.Reason != "max" {
		t.Errorf("extracted Reason = %q, want max", extracted.Reason)
	}
}

func TestAttachTruncationNilMap(t *testing.T) {
	// Must not panic on nil map.
	attachTruncation(nil, TruncationMeta{Truncated: true})
}

func TestTruncationFromMapAbsent(t *testing.T) {
	m := map[string]any{"count": 5}
	_, ok := truncationFromMap(m)
	if ok {
		t.Error("expected ok=false when truncation field absent")
	}
}

func TestTruncationFromMapNil(t *testing.T) {
	_, ok := truncationFromMap(nil)
	if ok {
		t.Error("expected ok=false for nil map")
	}
}

func TestFindExpressionIncludesBoundAndAmbiguity(t *testing.T) {
	// The find JS expression must include maxMatches=50, total_count,
	// ambiguous, and match_count fields for bounded output + ambiguity.
	expr := findExpression("hello", "")
	if !contains(expr, "maxMatches = 50") {
		t.Error("find expression missing maxMatches=50 bound")
	}
	if !contains(expr, "total_count") {
		t.Error("find expression missing total_count field")
	}
	if !contains(expr, "ambiguous") {
		t.Error("find expression missing ambiguous field")
	}
	if !contains(expr, "match_count") {
		t.Error("find expression missing match_count field")
	}
	if !contains(expr, "xPath") {
		t.Error("find expression missing xPath distinguishing attribute")
	}
}

func TestFindExpressionHintZeroMatches(t *testing.T) {
	expr := findExpression("nonexistent", "")
	if !contains(expr, "No elements matched") {
		t.Error("find expression missing zero-match hint")
	}
}

func TestFindExpressionHintAmbiguous(t *testing.T) {
	expr := findExpression("dup", "")
	if !contains(expr, "Refine with id, text, or nth index") {
		t.Error("find expression missing ambiguity hint")
	}
}

func TestSnapshotExpressionReportsTruncationFlags(t *testing.T) {
	expr := snapshotExpression(4000, 80)
	if !contains(expr, "text_truncated") {
		t.Error("snapshot expression missing text_truncated flag")
	}
	if !contains(expr, "elements_truncated") {
		t.Error("snapshot expression missing elements_truncated flag")
	}
}

func contains(haystack, needle string) bool {
	return indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
