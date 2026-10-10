package main

import (
	"encoding/json"
	"regexp"
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
//   - Target sensitive tokens (JWT, Bearer, passwords, secrets, API keys)
//   - Mask only the sensitive value while preserving surrounding error text,
//     stack traces, filenames, and line numbers.

var (
	reRSAKey        = regexp.MustCompile(`(?s)-----BEGIN RSA PRIVATE KEY-----[\s\S]*?(?:-----END RSA PRIVATE KEY-----|$)`)
	rePrivateKey    = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`)
	reJWT           = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]+\b`)
	reBearer        = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9\-._~+/]+=*`)
	reAuthHeader    = regexp.MustCompile(`(?i)(authorization:\s*)([^\r\n,]+)`)
	rePasswordQuery = regexp.MustCompile(`(?i)(password=)[^&\s;"]+`)
	rePasswordJSON  = regexp.MustCompile(`(?i)("password"\s*:\s*)"[^"]*"`)
	reAPIKey        = regexp.MustCompile(`(?i)(api[_-]?key=)[^&\s;"]+`)
	reSecret        = regexp.MustCompile(`(?i)(secret=)[^&\s;"]+`)
	reCookie        = regexp.MustCompile(`(?i)(cookie:\s*)[^\r\n]+`)
)

// redactString masks sensitive credentials in a string while preserving
// surrounding context, error messages, filenames, and stack traces.
func redactString(s string) string {
	if reRSAKey.MatchString(s) {
		s = reRSAKey.ReplaceAllString(s, "[REDACTED RSA KEY]")
	}
	if rePrivateKey.MatchString(s) {
		s = rePrivateKey.ReplaceAllString(s, "[REDACTED PRIVATE KEY]")
	}
	if reJWT.MatchString(s) {
		s = reJWT.ReplaceAllString(s, "[REDACTED JWT]")
	}
	if reAuthHeader.MatchString(s) {
		s = reAuthHeader.ReplaceAllStringFunc(s, func(m string) string {
			sub := reAuthHeader.FindStringSubmatch(m)
			if len(sub) < 3 {
				return m
			}
			prefix := sub[1]
			val := strings.TrimSpace(sub[2])
			if strings.HasPrefix(strings.ToLower(val), "bearer ") {
				return prefix + "Bearer [REDACTED]"
			}
			if strings.HasPrefix(strings.ToLower(val), "basic ") {
				return prefix + "Basic [REDACTED]"
			}
			return prefix + "[REDACTED]"
		})
	}
	if reBearer.MatchString(s) {
		s = reBearer.ReplaceAllString(s, "Bearer [REDACTED]")
	}
	if rePasswordQuery.MatchString(s) {
		s = rePasswordQuery.ReplaceAllString(s, "${1}[REDACTED]")
	}
	if rePasswordJSON.MatchString(s) {
		s = rePasswordJSON.ReplaceAllString(s, "${1}\"[REDACTED]\"")
	}
	if reAPIKey.MatchString(s) {
		s = reAPIKey.ReplaceAllString(s, "${1}[REDACTED]")
	}
	if reSecret.MatchString(s) {
		s = reSecret.ReplaceAllString(s, "${1}[REDACTED]")
	}
	if reCookie.MatchString(s) {
		s = reCookie.ReplaceAllString(s, "${1}[REDACTED]")
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
