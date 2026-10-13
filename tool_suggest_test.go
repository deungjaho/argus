package main

import (
	"testing"
)

func TestSuggestTools(t *testing.T) {
	cands := []string{"configure", "status", "navigate", "look", "snapshot", "screenshot", "click", "fill", "eval", "wait_for", "tab", "console_messages", "type"}

	cases := []struct {
		name    string
		wantIn  string
		notWant string
	}{
		{"fill_form", "fill", ""},
		{"open", "navigate", ""},
		{"press_key", "type", ""},
		{"logs", "console_messages", ""},
		{"screenshot_gri", "screenshot", "configure"},
		{"fil", "fill", "snapshot"},
	}
	for _, tc := range cases {
		got := suggestTools(tc.name, cands)
		found := false
		for _, s := range got {
			if s == tc.wantIn {
				found = true
			}
			if tc.notWant != "" && s == tc.notWant {
				t.Errorf("suggestTools(%q) unexpectedly suggested %q: %v", tc.name, tc.notWant, got)
			}
		}
		if !found {
			t.Errorf("suggestTools(%q) = %v, want it to contain %q", tc.name, got, tc.wantIn)
		}
		if len(got) > 3 {
			t.Errorf("suggestTools(%q) returned %d suggestions, want <=3", tc.name, len(got))
		}
	}
}

func TestLevenshtein(t *testing.T) {
	if d := levenshtein("fill", "fill"); d != 0 {
		t.Fatalf("fill vs fill = %d", d)
	}
	if d := levenshtein("fill_form", "fill"); d != 5 {
		t.Fatalf("fill_form vs fill = %d, want 5", d)
	}
	if d := levenshtein("screenshott", "snapshot"); d != 6 {
		t.Fatalf("screenshott vs snapshot = %d, want 6", d)
	}
}
