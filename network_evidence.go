package main

import (
	"net/url"
	"strings"
	"unicode/utf8"
)

// --- Typed network evidence model ---
//
// P2-A: Network events are captured as typed evidence records and fed
// into the existing recorder/timeline. This file defines the typed
// structures, bounds, and redaction for network evidence.
//
// Security constraints (docs/roadmap.md P2-A):
//   - No request/response body capture.
//   - No cookies, authorization headers, complete query secrets, or
//     arbitrary POST data.
//   - Header capture uses an explicit allowlist.
//   - URL, header, error, and aggregate event bytes are bounded before
//     insertion into the recorder.
//   - Redaction is applied at ingestion time (defense in depth), not
//     only at output time.

const (
	// maxURLBytes limits the stored URL length per network event.
	maxURLBytes = 2048
	// maxHeaderValBytes limits each stored header value.
	maxHeaderValBytes = 256
	// maxErrorTextBytes limits the stored error text length.
	maxErrorTextBytes = 512
	// maxRedirectHops limits the number of redirect hops stored per request.
	maxRedirectHops = 10
)

// NetworkEvidence is the typed data stored in EventRecord.Data for
// network events. It is designed to fit within maxEventDataBytes (16KB)
// after JSON serialization.
type NetworkEvidence struct {
	RequestID    string `json:"request_id"`
	LoaderID     string `json:"loader_id,omitempty"`
	FrameID      string `json:"frame_id,omitempty"`
	URL          string `json:"url"`
	Method       string `json:"method,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
	// Status is set on response or failure.
	Status     int    `json:"status,omitempty"`
	StatusText string `json:"status_text,omitempty"`
	MimeType   string `json:"mime_type,omitempty"`
	Failed     bool   `json:"failed,omitempty"`
	ErrorText  string `json:"error_text,omitempty"`
	Canceled   bool   `json:"canceled,omitempty"`
	// RedirectHops is the number of redirects preceding this request.
	// The redirect chain itself is represented as separate events
	// (one per hop), linked by RequestID and LoaderID.
	RedirectHops int `json:"redirect_hops,omitempty"`
	// Headers is an allowlisted subset of request/response headers.
	Headers map[string]string `json:"headers,omitempty"`
	// Phase indicates which CDP event produced this evidence:
	// "request", "response", or "failed".
	Phase string `json:"phase"`
}

// allowedHeaders is the explicit allowlist of header names that may be
// stored in network evidence. All other headers are dropped at ingestion.
var allowedHeaders = map[string]bool{
	"content-type":                 true,
	"content-length":               true,
	"accept":                       true,
	"accept-encoding":              true,
	"cache-control":                true,
	"pragma":                       true,
	"expires":                      true,
	"last-modified":                true,
	"etag":                         true,
	"if-none-match":                true,
	"if-modified-since":            true,
	"location":                     true,
	"referer":                      true,
	"origin":                       true,
	"access-control-allow-origin":  true,
	"access-control-allow-methods": true,
	"access-control-allow-headers": true,
	"x-request-id":                 true,
	"x-powered-by":                 true,
	"server":                       true,
	"date":                         true,
	"vary":                         true,
	"connection":                   true,
	"transfer-encoding":            true,
}

// sanitizeURL parses the URL, removes query parameters and fragment
// (which may contain secrets), then applies a UTF-8-safe byte limit.
// On parse failure, it strips everything after '?' to avoid leaking
// query secrets, then applies the byte limit.
func sanitizeURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		// Parse failed — strip anything after '?' to avoid preserving
		// query secrets, then apply byte limit.
		stripped := rawURL
		if idx := strings.Index(stripped, "?"); idx >= 0 {
			stripped = stripped[:idx]
		}
		return truncateUTF8Safe(stripped, maxURLBytes)
	}
	// Remove query parameters entirely — they may contain tokens,
	// API keys, or other secrets. This is aggressive but safe.
	u.RawQuery = ""
	u.Fragment = ""
	return truncateUTF8Safe(u.String(), maxURLBytes)
}

// truncateUTF8Safe truncates s to at most maxBytes, ensuring the
// result is valid UTF-8 (does not split a multi-byte sequence).
// If truncation occurs, "...[truncated]" is appended.
func truncateUTF8Safe(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Find the largest valid UTF-8 boundary <= maxBytes - len(suffix)
	suffix := "...[truncated]"
	limit := maxBytes - len(suffix)
	if limit <= 0 {
		// Not enough room for suffix; just truncate hard at maxBytes
		limit = maxBytes
		suffix = ""
	}
	// Walk back to a valid UTF-8 boundary
	end := limit
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + suffix
}

// urlTypeHeaders are headers whose values are URLs and must be
// sanitized with sanitizeURL (removes query secrets) before storage.
var urlTypeHeaders = map[string]bool{
	"referer":  true,
	"location": true,
	"origin":   true,
}

// filterHeaders returns only allowlisted headers, with each value
// truncated to maxHeaderValBytes. URL-type headers (Referer, Location,
// Origin) are sanitized as URLs to remove query secrets before storage.
func filterHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	result := make(map[string]string, len(allowedHeaders))
	for k, v := range headers {
		lower := strings.ToLower(k)
		if !allowedHeaders[lower] {
			continue
		}
		// URL-type headers: sanitize as URL to remove query secrets
		if urlTypeHeaders[lower] {
			v = sanitizeURL(v)
		}
		if len(v) > maxHeaderValBytes {
			v = truncateUTF8Safe(v, maxHeaderValBytes)
		}
		result[k] = v
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// truncateErrorText truncates error text to maxErrorTextBytes.
func truncateErrorText(s string) string {
	if len(s) > maxErrorTextBytes {
		return s[:maxErrorTextBytes] + "...[truncated]"
	}
	return s
}
