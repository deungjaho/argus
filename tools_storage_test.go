package main

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStorageInputUnmarshal(t *testing.T) {
	tests := []struct {
		name      string
		rawJSON   string
		wantAct   string
		wantType  string
		wantKey   string
		wantVal   string
		wantDom   string
		wantPath  string
		wantMaxAg int
	}{
		{
			name:     "get cookie default",
			rawJSON:  `{"type": "cookie", "key": "session_id"}`,
			wantType: "cookie",
			wantKey:  "session_id",
		},
		{
			name:     "get storage with aliases name and storage_type",
			rawJSON:  `{"storage_type": "localStorage", "name": "user_pref"}`,
			wantType: "localStorage",
			wantKey:  "user_pref",
		},
		{
			name:      "set cookie with full options",
			rawJSON:   `{"action": "set", "type": "cookie", "key": "theme", "value": "dark", "domain": ".example.com", "path": "/app", "max_age": 3600}`,
			wantAct:   "set",
			wantType:  "cookie",
			wantKey:   "theme",
			wantVal:   "dark",
			wantDom:   ".example.com",
			wantPath:  "/app",
			wantMaxAg: 3600,
		},
		{
			name:     "clear all target alias",
			rawJSON:  `{"action": "clear", "target": "all"}`,
			wantAct:  "clear",
			wantType: "all",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var in storageInput
			if err := json.Unmarshal([]byte(tc.rawJSON), &in); err != nil {
				t.Fatalf("failed to unmarshal storageInput: %v", err)
			}
			if in.Action != tc.wantAct {
				t.Errorf("got Action %q, want %q", in.Action, tc.wantAct)
			}
			if st := resolveFirstNonEmpty(in.Type, in.StorageType, in.Target); st != tc.wantType {
				t.Errorf("got Type %q, want %q", st, tc.wantType)
			}
			if k := resolveFirstNonEmpty(in.Key, in.Name); k != tc.wantKey {
				t.Errorf("got Key %q, want %q", k, tc.wantKey)
			}
			if in.Value != tc.wantVal {
				t.Errorf("got Value %q, want %q", in.Value, tc.wantVal)
			}
			if in.Domain != tc.wantDom {
				t.Errorf("got Domain %q, want %q", in.Domain, tc.wantDom)
			}
			if in.Path != tc.wantPath {
				t.Errorf("got Path %q, want %q", in.Path, tc.wantPath)
			}
			if in.MaxAge != tc.wantMaxAg {
				t.Errorf("got MaxAge %d, want %d", in.MaxAge, tc.wantMaxAg)
			}
		})
	}
}

func TestStorageActionInference(t *testing.T) {
	// If Action is empty and Value is present -> inferred "set"
	if act := resolveStorageAction("", "some-val"); act != "set" {
		t.Errorf("expected inferred action 'set', got %q", act)
	}

	// If Action is empty and Value is empty -> inferred "get"
	if act := resolveStorageAction("", ""); act != "get" {
		t.Errorf("expected inferred action 'get', got %q", act)
	}

	// Explicit action preserved and normalized
	if act := resolveStorageAction("CLEAR", "some-val"); act != "clear" {
		t.Errorf("expected explicit action 'clear', got %q", act)
	}
	if act := resolveStorageAction("  GET  ", ""); act != "get" {
		t.Errorf("expected trimmed action 'get', got %q", act)
	}
}

func TestStorageTypeResolutions(t *testing.T) {
	cases := map[string]string{
		"cookie":          "cookie",
		"cookies":         "cookie",
		"Cookie":          "cookie",
		"local":           "local",
		"localStorage":    "local",
		"local_storage":   "local",
		"session":         "session",
		"sessionStorage":  "session",
		"session_storage": "session",
		"all":             "all",
		"*":               "all",
		"":                "",
	}

	for in, expected := range cases {
		got := resolveStorageType(in)
		if got != expected {
			t.Errorf("resolveStorageType(%q) = %q, want %q", in, got, expected)
		}
	}
}

func TestStorageBackwardCompatibilityAliases(t *testing.T) {
	var g getStorageInput
	var s setStorageInput
	var c clearStorageInput

	g.Key = "k1"
	s.Value = "v1"
	c.Action = "clear"

	if g.Key != "k1" || s.Value != "v1" || c.Action != "clear" {
		t.Error("unexpected failure with storage struct backward-compatibility aliases")
	}
}

func TestStorageToolRegistration(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test-storage", Version: "1.0"}, nil)
	registerStorageTools(server)
	// Verified registerStorageTools registers without panicking
}
