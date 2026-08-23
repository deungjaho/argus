package main

import (
	"encoding/json"
	"strings"
)

// --- Sensitive data redaction ---
//
// The flight recorder may capture sensitive data in event payloads:
// - Console messages that log tokens, passwords, or PII
// - Network request bodies containing form data
// - Exception messages that expose internal state
//
// Redaction strategy:
//   - Scan string values in event data for sensitive patterns
//   - Replace matched portions with [REDACTED]
//   - Patterns: JWT tokens, API keys, passwords, bearer tokens,
//     authorization headers, cookie values
//
// Redaction is applied in timeline, diagnose, and export_trace outputs.
// The raw recorder stores unredacted data (for debugging), but all
// MCP-facing outputs are redacted.

// sensitivePatterns are regex-like patterns to detect sensitive data.
// We use simple string matching for performance and predictability.
var sensitivePatterns = []sensitivePattern{
	// JWT tokens: eyJ...three parts separated by dots
	{contains: "eyJ", minLen: 40, replaceWith: "[REDACTED JWT]"},
	// Bearer tokens
	{prefix: "Bearer ", replaceWith: "Bearer [REDACTED]"},
	// Authorization headers
	{contains: "authorization", caseInsensitive: true, replaceWith: "[REDACTED AUTH]"},
	// Password fields
	{prefix: "password=", replaceWith: "password=[REDACTED]"},
	{contains: "\"password\"", caseInsensitive: true, replaceWith: "\"password\":\"[REDACTED]\""},
	// API key patterns
	{prefix: "api_key=", replaceWith: "api_key=[REDACTED]"},
	{prefix: "apikey=", replaceWith: "apikey=[REDACTED]"},
	{contains: "secret=", caseInsensitive: true, replaceWith: "secret=[REDACTED]"},
	// Cookie values (long base64-like strings after "cookie:")
	{prefix: "cookie:", replaceWith: "cookie:[REDACTED]"},
	// Private keys
	{contains: "BEGIN PRIVATE KEY", replaceWith: "[REDACTED PRIVATE KEY]"},
	{contains: "BEGIN RSA PRIVATE KEY", replaceWith: "[REDACTED RSA KEY]"},
}

type sensitivePattern struct {
	contains        string
	prefix          string
	minLen          int
	caseInsensitive bool
	replaceWith     string
}

// redactString checks a string for sensitive patterns and replaces them.
func redactString(s string) string {
	for _, p := range sensitivePatterns {
		if p.minLen > 0 && len(s) < p.minLen {
			continue
		}
		if p.prefix != "" && strings.HasPrefix(s, p.prefix) {
			return p.replaceWith
		}
		if p.contains != "" {
			haystack := s
			needle := p.contains
			if p.caseInsensitive {
				haystack = strings.ToLower(s)
				needle = strings.ToLower(p.contains)
			}
			if strings.Contains(haystack, needle) {
				return p.replaceWith
			}
		}
	}
	return s
}

// redactData recursively redacts sensitive data in a map[string]any.
// It returns a new map with redacted values (does not modify input).
func redactData(data any) any {
	switch v := data.(type) {
	case string:
		return redactString(v)
	case map[string]any:
		result := make(map[string]any, len(v))
		for key, val := range v {
			// Redact sensitive keys entirely
			if isSensitiveKey(key) {
				result[key] = "[REDACTED]"
			} else {
				result[key] = redactData(val)
			}
		}
		return result
	case []any:
		result := make([]any, len(v))
		for i, val := range v {
			result[i] = redactData(val)
		}
		return result
	case []map[string]any:
		result := make([]map[string]any, len(v))
		for i, val := range v {
			redacted := redactData(val)
			if m, ok := redacted.(map[string]any); ok {
				result[i] = m
			} else {
				result[i] = map[string]any{"value": redacted}
			}
		}
		return result
	case NetworkEvidence:
		// Convert to map and redact recursively
		return redactData(networkEvidenceToMap(v))
	case NavigationEvidence:
		// Convert to map and redact recursively
		return redactData(navigationEvidenceToMap(v))
	default:
		// For other struct types, marshal to map and redact
		if data == nil {
			return nil
		}
		m, ok := structToMap(data)
		if !ok {
			return v
		}
		return redactData(m)
	}
}

// networkEvidenceToMap converts a NetworkEvidence to a map[string]any
// for redaction processing.
func networkEvidenceToMap(e NetworkEvidence) map[string]any {
	m := map[string]any{
		"request_id":    e.RequestID,
		"loader_id":     e.LoaderID,
		"frame_id":      e.FrameID,
		"url":           e.URL,
		"method":        e.Method,
		"resource_type": e.ResourceType,
		"phase":         e.Phase,
	}
	if e.Status != 0 {
		m["status"] = e.Status
	}
	if e.StatusText != "" {
		m["status_text"] = e.StatusText
	}
	if e.MimeType != "" {
		m["mime_type"] = e.MimeType
	}
	if e.Failed {
		m["failed"] = e.Failed
	}
	if e.ErrorText != "" {
		m["error_text"] = e.ErrorText
	}
	if e.Canceled {
		m["canceled"] = e.Canceled
	}
	if e.RedirectHops > 0 {
		m["redirect_hops"] = e.RedirectHops
	}
	if e.Headers != nil {
		m["headers"] = e.Headers
	}
	return m
}

// structToMap converts a struct to map[string]any via JSON round-trip.
// Returns false if the value is not a struct or cannot be marshaled.
func structToMap(v any) (map[string]any, bool) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, false
	}
	return m, true
}

// isSensitiveKey returns true if the key name suggests sensitive data.
func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	sensitiveKeys := []string{
		"password", "passwd", "pwd", "secret", "token", "apikey", "api_key",
		"authorization", "auth", "cookie", "session", "credential",
		"private_key", "privatekey", "access_token", "refresh_token",
	}
	for _, sk := range sensitiveKeys {
		if lower == sk || strings.Contains(lower, sk) {
			return true
		}
	}
	return false
}
