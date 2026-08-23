package main

import "testing"

func TestAmbiguityFromMapZero(t *testing.T) {
	m := map[string]any{
		"ambiguous":   false,
		"match_count": float64(0),
		"matches":     []any{},
		"hint":        "No elements matched. Try a different text/regex, or use snapshot to inspect the page.",
		"count":       float64(0),
		"total_count": float64(0),
	}
	info, ok := ambiguityFromMap(m)
	if !ok {
		t.Fatal("expected ambiguity fields present")
	}
	if info.Ambiguous {
		t.Error("Ambiguous = true, want false for 0 matches")
	}
	if info.MatchCount != 0 {
		t.Errorf("MatchCount = %d, want 0", info.MatchCount)
	}
	if info.Hint == "" {
		t.Error("expected non-empty hint for 0 matches")
	}
}

func TestAmbiguityFromMapSingle(t *testing.T) {
	m := map[string]any{
		"ambiguous":   false,
		"match_count": float64(1),
		"matches": []any{
			map[string]any{"tag": "button", "id": "submit", "text": "Submit"},
		},
		"count":       float64(1),
		"total_count": float64(1),
	}
	info, ok := ambiguityFromMap(m)
	if !ok {
		t.Fatal("expected ambiguity fields present")
	}
	if info.Ambiguous {
		t.Error("Ambiguous = true, want false for single match")
	}
	if info.MatchCount != 1 {
		t.Errorf("MatchCount = %d, want 1", info.MatchCount)
	}
	if len(info.Matches) != 1 {
		t.Fatalf("Matches len = %d, want 1", len(info.Matches))
	}
	if info.Matches[0].Tag != "button" {
		t.Errorf("match tag = %q, want button", info.Matches[0].Tag)
	}
	if info.Matches[0].ID != "submit" {
		t.Errorf("match id = %q, want submit", info.Matches[0].ID)
	}
}

func TestAmbiguityFromMapMultiple(t *testing.T) {
	matches := []any{}
	for i := 0; i < 3; i++ {
		matches = append(matches, map[string]any{
			"tag":       "button",
			"id":        "",
			"className": "btn",
			"text":      "Click",
			"xPath":     "/html/body/button",
			"ref":       "button",
		})
	}
	m := map[string]any{
		"ambiguous":   true,
		"match_count": float64(3),
		"matches":     matches,
		"hint":        "Selector matched 3 elements. Refine with id, text, or nth index.",
		"count":       float64(3),
		"total_count": float64(3),
	}
	info, ok := ambiguityFromMap(m)
	if !ok {
		t.Fatal("expected ambiguity fields present")
	}
	if !info.Ambiguous {
		t.Error("Ambiguous = false, want true for 3 matches")
	}
	if info.MatchCount != 3 {
		t.Errorf("MatchCount = %d, want 3", info.MatchCount)
	}
	if len(info.Matches) != 3 {
		t.Fatalf("Matches len = %d, want 3", len(info.Matches))
	}
	if info.Matches[0].Class != "btn" {
		t.Errorf("match class = %q, want btn", info.Matches[0].Class)
	}
	if info.Matches[0].XPath != "/html/body/button" {
		t.Errorf("match xpath = %q, want /html/body/button", info.Matches[0].XPath)
	}
}

func TestAmbiguityFromMapCapsAtFive(t *testing.T) {
	matches := []any{}
	for i := 0; i < 10; i++ {
		matches = append(matches, map[string]any{"tag": "div"})
	}
	m := map[string]any{
		"ambiguous":   true,
		"match_count": float64(10),
		"matches":     matches,
	}
	info, ok := ambiguityFromMap(m)
	if !ok {
		t.Fatal("expected ambiguity fields present")
	}
	if len(info.Matches) > 5 {
		t.Errorf("Matches len = %d, must be capped at 5", len(info.Matches))
	}
}

func TestAmbiguityFromMapAbsent(t *testing.T) {
	m := map[string]any{"count": 5}
	_, ok := ambiguityFromMap(m)
	if ok {
		t.Error("expected ok=false when ambiguity fields absent")
	}
}

func TestAmbiguityFromMapNil(t *testing.T) {
	_, ok := ambiguityFromMap(nil)
	if ok {
		t.Error("expected ok=false for nil map")
	}
}

func TestAmbiguityHint(t *testing.T) {
	cases := []struct {
		count int
		want  string
	}{
		{0, "No elements matched. Try a different text/regex, or use snapshot to inspect the page."},
		{1, ""},
		{2, "Selector matched multiple elements. Refine with id, text, or nth index."},
		{100, "Selector matched multiple elements. Refine with id, text, or nth index."},
	}
	for _, tc := range cases {
		got := ambiguityHint(tc.count)
		if got != tc.want {
			t.Errorf("ambiguityHint(%d) = %q, want %q", tc.count, got, tc.want)
		}
	}
}

func TestFindExpressionAmbiguityFields(t *testing.T) {
	// The find JS expression must produce all ambiguity fields.
	expr := findExpression("test", "")
	for _, field := range []string{"ambiguous", "match_count", "matches", "hint", "xPath", "className"} {
		if !contains(expr, field) {
			t.Errorf("find expression missing field %q", field)
		}
	}
}
