package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustMarshalJSON(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}

func TestSanitizeURLRemovesQueryParams(t *testing.T) {
	raw := "https://example.com/api?token=secret&api_key=abc123&page=1"
	got := sanitizeURL(raw)
	if strings.Contains(got, "token=secret") {
		t.Errorf("sanitizeURL should remove query params: got %s", got)
	}
	if strings.Contains(got, "api_key=abc123") {
		t.Errorf("sanitizeURL should remove query params: got %s", got)
	}
	if !strings.Contains(got, "example.com/api") {
		t.Errorf("sanitizeURL should preserve path: got %s", got)
	}
}

func TestSanitizeURLTruncatesLongURLs(t *testing.T) {
	long := "https://example.com/" + strings.Repeat("a", maxURLBytes+100)
	got := sanitizeURL(long)
	if len(got) > maxURLBytes {
		t.Errorf("sanitizeURL should truncate to at most %d bytes, got %d", maxURLBytes, len(got))
	}
	if !strings.Contains(got, "[truncated]") {
		t.Errorf("sanitizeURL should mark truncation: got %s", got[:50])
	}
}

func TestSanitizeURLPreservesSimpleURL(t *testing.T) {
	raw := "https://example.com/api/users"
	got := sanitizeURL(raw)
	if got != raw {
		t.Errorf("sanitizeURL should preserve simple URL: got %s, want %s", got, raw)
	}
}

func TestSanitizeURLHandlesInvalidURL(t *testing.T) {
	raw := "://not-a-url"
	got := sanitizeURL(raw)
	// Should not panic, should return something
	if got == "" {
		t.Error("sanitizeURL should return non-empty for invalid URL")
	}
}

func TestFilterHeadersAllowlist(t *testing.T) {
	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer secret-token-xyz",
		"Cookie":        "session=abc123",
		"X-Request-ID":  "req-456",
		"Accept":        "text/html",
	}
	got := filterHeaders(headers)
	if got == nil {
		t.Fatal("filterHeaders returned nil for valid headers")
	}
	if _, ok := got["Content-Type"]; !ok {
		t.Error("filterHeaders should keep Content-Type")
	}
	if _, ok := got["Accept"]; !ok {
		t.Error("filterHeaders should keep Accept")
	}
	if _, ok := got["X-Request-ID"]; !ok {
		t.Error("filterHeaders should keep X-Request-ID")
	}
	// Authorization and Cookie should be dropped
	if _, ok := got["Authorization"]; ok {
		t.Error("filterHeaders should drop Authorization")
	}
	if _, ok := got["Cookie"]; ok {
		t.Error("filterHeaders should drop Cookie")
	}
}

func TestFilterHeadersTruncatesValues(t *testing.T) {
	longVal := strings.Repeat("x", maxHeaderValBytes+50)
	headers := map[string]string{
		"Content-Type": longVal,
	}
	got := filterHeaders(headers)
	if got["Content-Type"] == longVal {
		t.Error("filterHeaders should truncate long header values")
	}
	if !strings.Contains(got["Content-Type"], "[truncated]") {
		t.Errorf("filterHeaders should mark truncation: got %s", got["Content-Type"][:50])
	}
}

func TestFilterHeadersNilForEmpty(t *testing.T) {
	got := filterHeaders(nil)
	if got != nil {
		t.Errorf("filterHeaders should return nil for nil input, got %v", got)
	}
}

func TestFilterHeadersNilForAllDropped(t *testing.T) {
	headers := map[string]string{
		"Authorization": "Bearer secret",
		"Cookie":        "session=abc",
	}
	got := filterHeaders(headers)
	if got != nil {
		t.Errorf("filterHeaders should return nil when all headers dropped, got %v", got)
	}
}

func TestTruncateErrorText(t *testing.T) {
	short := "net::ERR_CONNECTION_REFUSED"
	got := truncateErrorText(short)
	if got != short {
		t.Errorf("truncateErrorText should not modify short text: got %s", got)
	}

	long := strings.Repeat("e", maxErrorTextBytes+100)
	got = truncateErrorText(long)
	if len(got) <= maxErrorTextBytes {
		t.Errorf("truncateErrorText should truncate long text")
	}
	if !strings.Contains(got, "[truncated]") {
		t.Errorf("truncateErrorText should mark truncation")
	}
}

func TestNetworkEvidenceJSONFitsIn16KB(t *testing.T) {
	e := NetworkEvidence{
		RequestID:    "req-1234567890",
		LoaderID:     "loader-abc",
		FrameID:      "frame-xyz",
		URL:          "https://example.com/api/v1/users/" + strings.Repeat("a", 500),
		Method:       "POST",
		ResourceType: "XHR",
		Status:       500,
		StatusText:   "Internal Server Error",
		MimeType:     "application/json",
		Headers: map[string]string{
			"Content-Type": "application/json",
			"Accept":       "text/html",
		},
		Phase: "response",
	}
	data := mustMarshalJSON(e)
	if len(data) > maxEventDataBytes {
		t.Errorf("NetworkEvidence JSON is %d bytes, exceeds %d limit", len(data), maxEventDataBytes)
	}
}
