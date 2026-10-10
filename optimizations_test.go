package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEvalInputTolerance verifies that evalInput accepts both script and expression.
func TestEvalInputTolerance(t *testing.T) {
	// Case 1: Expression provided
	var input1 evalInput
	err := json.Unmarshal([]byte(`{"expression": "document.title"}`), &input1)
	if err != nil {
		t.Fatalf("failed to unmarshal expression: %v", err)
	}
	if input1.Expression != "document.title" {
		t.Errorf("input1.Expression = %q, want 'document.title'", input1.Expression)
	}
	if input1.Script != "" {
		t.Errorf("input1.Script = %q, want empty", input1.Script)
	}

	// Case 2: Script provided
	var input2 evalInput
	err = json.Unmarshal([]byte(`{"script": "1 + 1"}`), &input2)
	if err != nil {
		t.Fatalf("failed to unmarshal script: %v", err)
	}
	if input2.Script != "1 + 1" {
		t.Errorf("input2.Script = %q, want '1 + 1'", input2.Script)
	}

	// Case 3: Both provided (whichever is non-empty, script takes priority or expression used)
	script := strings.TrimSpace(input1.Script)
	if script == "" {
		script = strings.TrimSpace(input1.Expression)
	}
	if script != "document.title" {
		t.Errorf("resolved script = %q, want 'document.title'", script)
	}
}

// TestSnapshotInputTolerance verifies that snapshotInput accepts interactive_only and interactiveOnly.
func TestSnapshotInputTolerance(t *testing.T) {
	// Case 1: snake_case interactive_only
	var input1 snapshotInput
	err := json.Unmarshal([]byte(`{"interactive_only": true, "max_elements": 50}`), &input1)
	if err != nil {
		t.Fatalf("failed to unmarshal interactive_only: %v", err)
	}
	if !input1.InteractiveOnly {
		t.Error("input1.InteractiveOnly = false, want true")
	}
	if input1.MaxElements != 50 {
		t.Errorf("input1.MaxElements = %d, want 50", input1.MaxElements)
	}

	// Case 2: camelCase interactiveOnly
	var input2 snapshotInput
	err = json.Unmarshal([]byte(`{"interactiveOnly": true}`), &input2)
	if err != nil {
		t.Fatalf("failed to unmarshal interactiveOnly: %v", err)
	}
	if !input2.InteractiveOnlyCamel {
		t.Error("input2.InteractiveOnlyCamel = false, want true")
	}

	// Case 3: Combined check
	if !(input1.InteractiveOnly || input1.InteractiveOnlyCamel) {
		t.Error("expected input1 combined check to be true")
	}
	if !(input2.InteractiveOnly || input2.InteractiveOnlyCamel) {
		t.Error("expected input2 combined check to be true")
	}
}

// TestFindExpressionPrioritizationAndLimits verifies that findExpression includes
// interactive element prioritization, 100-character text truncation, and 5-item capping.
func TestFindExpressionPrioritizationAndLimits(t *testing.T) {
	expr := findExpression("Open", "")

	// 1. Text truncation to 100 chars
	if !strings.Contains(expr, "ownText.slice(0, 100)") {
		t.Error("findExpression missing text truncation to 100 chars (ownText.slice(0, 100))")
	}

	// 2. Interactive tags & roles
	if !strings.Contains(expr, "interactiveTags") || !strings.Contains(expr, "'button'") || !strings.Contains(expr, "'textarea'") {
		t.Error("findExpression missing interactive tags priority definition")
	}
	if !strings.Contains(expr, "containerTags") || !strings.Contains(expr, "'div'") {
		t.Error("findExpression missing container tags definition")
	}

	// 3. Priority calculation and sorting
	if !strings.Contains(expr, "getPriority") {
		t.Error("findExpression missing getPriority function")
	}
	if !strings.Contains(expr, "candidates.sort") {
		t.Error("findExpression missing priority sorting")
	}

	// 4. Returned matches capped to max 5 items
	if !strings.Contains(expr, "maxReturned = 5") {
		t.Error("findExpression missing maxReturned = 5 cap")
	}
	if !strings.Contains(expr, "maxMatches = 50") {
		t.Error("findExpression missing maxMatches = 50 candidate bound")
	}
}

// TestAmbiguityFromMapTextTruncationAndCapping verifies that ambiguityFromMap
// truncates text to max 100 runes and caps matches to 5 items.
func TestAmbiguityFromMapTextTruncationAndCapping(t *testing.T) {
	longText := strings.Repeat("A", 150)
	longUnicodeText := strings.Repeat("中", 120)

	matches := []any{
		map[string]any{"tag": "button", "text": longText},
		map[string]any{"tag": "a", "text": longUnicodeText},
		map[string]any{"tag": "input", "text": "normal text"},
		map[string]any{"tag": "div", "text": "container 1"},
		map[string]any{"tag": "div", "text": "container 2"},
		map[string]any{"tag": "div", "text": "container 3"}, // 6th item
	}

	m := map[string]any{
		"ambiguous":   true,
		"match_count": float64(6),
		"matches":     matches,
	}

	info, ok := ambiguityFromMap(m)
	if !ok {
		t.Fatal("ambiguityFromMap returned ok=false")
	}

	// Must cap at 5 matches
	if len(info.Matches) != 5 {
		t.Fatalf("info.Matches len = %d, want 5", len(info.Matches))
	}

	// Match 0: ASCII text truncated to 100
	if len(info.Matches[0].Text) != 100 {
		t.Errorf("match[0].Text len = %d, want 100", len(info.Matches[0].Text))
	}

	// Match 1: Unicode runes truncated to 100 runes
	runes := []rune(info.Matches[1].Text)
	if len(runes) != 100 {
		t.Errorf("match[1].Text rune count = %d, want 100", len(runes))
	}
}

// TestAmbiguityFromMapKeyTolerance verifies that ambiguityFromMap accepts
// alternative key names like "class" and "xpath".
func TestAmbiguityFromMapKeyTolerance(t *testing.T) {
	m := map[string]any{
		"ambiguous":   true,
		"match_count": float64(1),
		"matches": []any{
			map[string]any{
				"tag":   "button",
				"class": "btn-primary",
				"xpath": "//button[@id='submit']",
				"text":  "Submit",
			},
		},
	}

	info, ok := ambiguityFromMap(m)
	if !ok {
		t.Fatal("ambiguityFromMap returned ok=false")
	}
	if len(info.Matches) != 1 {
		t.Fatalf("Matches count = %d, want 1", len(info.Matches))
	}
	if info.Matches[0].Class != "btn-primary" {
		t.Errorf("match[0].Class = %q, want 'btn-primary'", info.Matches[0].Class)
	}
	if info.Matches[0].XPath != "//button[@id='submit']" {
		t.Errorf("match[0].XPath = %q, want '//button[@id=\\'submit\\']'", info.Matches[0].XPath)
	}
}

// TestFindExpressionEnhancedSearchAndBucketing verifies that findExpression includes
// combined text sources (innerText, value, aria-label, placeholder, title) and
// partitioned priority candidate bucketing.
func TestFindExpressionEnhancedSearchAndBucketing(t *testing.T) {
	expr := findExpression("Search", "")

	// 1. Interactive tags includes summary
	if !strings.Contains(expr, "'summary'") {
		t.Error("findExpression missing 'summary' in interactiveTags")
	}

	// 2. Multi-source text combination
	if !strings.Contains(expr, "textParts") || !strings.Contains(expr, "aria-label") || !strings.Contains(expr, "placeholder") || !strings.Contains(expr, "title") {
		t.Error("findExpression missing multi-source textParts combination")
	}

	// 3. Priority bucketing to prevent starvation
	if !strings.Contains(expr, "candidatesP0") || !strings.Contains(expr, "candidatesP1") || !strings.Contains(expr, "candidatesP2") {
		t.Error("findExpression missing candidatesP0/P1/P2 bucketing")
	}
}

// TestSnapshotExpressionZeroTextOptimization verifies that snapshotExpression avoids
// evaluating document.body.innerText when maxText is 0.
func TestSnapshotExpressionZeroTextOptimization(t *testing.T) {
	expr := snapshotExpression(0, 50)
	if !strings.Contains(expr, "maxText > 0 && document.body") {
		t.Error("snapshotExpression missing maxText > 0 guard on document.body.innerText")
	}
}
