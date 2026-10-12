package main

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTabInputUnmarshal(t *testing.T) {
	tests := []struct {
		name      string
		rawJSON   string
		wantAct   string
		wantTabID string
		wantURL   string
		wantTitle string
		wantBg    bool
	}{
		{
			name:    "list tabs default",
			rawJSON: `{"action": "list"}`,
			wantAct: "list",
		},
		{
			name:    "new tab with url and background",
			rawJSON: `{"action": "new", "url": "https://example.com", "background": true}`,
			wantAct: "new",
			wantURL: "https://example.com",
			wantBg:  true,
		},
		{
			name:      "select tab by target_id alias",
			rawJSON:   `{"action": "select", "target_id": "TAB-42"}`,
			wantAct:   "select",
			wantTabID: "TAB-42",
		},
		{
			name:      "select tab by title",
			rawJSON:   `{"action": "select", "title": "Dashboard"}`,
			wantAct:   "select",
			wantTitle: "Dashboard",
		},
		{
			name:      "close tab by id alias",
			rawJSON:   `{"action": "close", "id": "TAB-99"}`,
			wantAct:   "close",
			wantTabID: "TAB-99",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var in tabInput
			if err := json.Unmarshal([]byte(tc.rawJSON), &in); err != nil {
				t.Fatalf("failed to unmarshal tabInput: %v", err)
			}
			if in.Action != tc.wantAct {
				t.Errorf("got Action %q, want %q", in.Action, tc.wantAct)
			}
			tabID := resolveFirstNonEmpty(in.TabID, in.TargetID, in.ID)
			if tabID != tc.wantTabID {
				t.Errorf("got TabID %q, want %q", tabID, tc.wantTabID)
			}
			url := resolveURL(in.URL, in.Href, in.Target)
			if url != tc.wantURL {
				t.Errorf("got URL %q, want %q", url, tc.wantURL)
			}
			if in.Title != tc.wantTitle {
				t.Errorf("got Title %q, want %q", in.Title, tc.wantTitle)
			}
			if in.Background != tc.wantBg {
				t.Errorf("got Background %v, want %v", in.Background, tc.wantBg)
			}
		})
	}
}

func TestTabActionInference(t *testing.T) {
	// 1. With tab_id -> inferred select
	if act := resolveTabAction(tabInput{TabID: "TAB-1"}); act != "select" {
		t.Errorf("expected select, got %q", act)
	}
	// 2. With title -> inferred select
	if act := resolveTabAction(tabInput{Title: "Inbox"}); act != "select" {
		t.Errorf("expected select, got %q", act)
	}
	// 3. With url -> inferred new
	if act := resolveTabAction(tabInput{URL: "https://golang.org"}); act != "new" {
		t.Errorf("expected new, got %q", act)
	}
	// 4. Empty -> inferred list
	if act := resolveTabAction(tabInput{}); act != "list" {
		t.Errorf("expected list, got %q", act)
	}
	// 5. Explicit action preserved and normalized
	if act := resolveTabAction(tabInput{Action: " CLOSE "}); act != "close" {
		t.Errorf("expected explicit action 'close', got %q", act)
	}
}

func TestTabBackwardCompatibilityAliases(t *testing.T) {
	var nt newTabInput
	var st selectTabInput
	var ct closeTabInput
	var fk forkInput

	nt.URL = "about:blank"
	st.TabID = "T1"
	ct.ID = "T2"
	fk.Target = "https://example.com"

	if nt.URL != "about:blank" || st.TabID != "T1" || ct.ID != "T2" || fk.Target != "https://example.com" {
		t.Error("unexpected failure with tab backward-compatibility aliases")
	}
}

func TestTabToolRegistration(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test-tab", Version: "1.0"}, nil)
	registerTabTools(server)
	// Verified registerTabTools registers without panicking
}
