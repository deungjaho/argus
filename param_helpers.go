package main

import (
	"strconv"
	"strings"
)

// resolveFirstNonEmpty returns the first non-whitespace string from the provided arguments.
func resolveFirstNonEmpty(values ...string) string {
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// resolveSelector resolves an element selector/ref from aliases: ref, selector, css, target, xpath, query.
func resolveSelector(ref, selector, css, target, xpath, query string) string {
	return resolveFirstNonEmpty(ref, selector, css, target, xpath, query)
}

// resolveValue resolves a value/text string from aliases: value, text, input.
func resolveValue(value, text, input string) string {
	return resolveFirstNonEmpty(value, text, input)
}

// resolveURL resolves a target URL from url, href, target.
func resolveURL(url, href, target string) string {
	if u := resolveFirstNonEmpty(url, href); u != "" {
		return u
	}
	target = strings.TrimSpace(target)
	if looksLikeURL(target) {
		return target
	}
	return ""
}

// looksLikeURL checks if a string appears to be a web URL or relative path.
func looksLikeURL(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "file://") ||
		strings.HasPrefix(s, "ws://") ||
		strings.HasPrefix(s, "wss://") ||
		strings.HasPrefix(s, "about:") ||
		strings.HasPrefix(s, "data:") ||
		strings.HasPrefix(s, "chrome://") ||
		strings.HasPrefix(s, "localhost") ||
		strings.HasPrefix(s, "127.0.0.1") ||
		strings.HasPrefix(s, "0.0.0.0") ||
		strings.HasPrefix(s, "www.") ||
		strings.HasPrefix(s, "/") ||
		strings.Contains(s, "://") {
		return true
	}
	// Check for domain-like strings (e.g. google.com, example.org/path)
	if strings.Contains(s, ".") && !strings.Contains(s, " ") {
		return true
	}
	return false
}

// normalizeURL ensures a web/file URL has an appropriate scheme.
func normalizeURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	lower := strings.ToLower(u)
	if strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "file://") ||
		strings.HasPrefix(lower, "about:") ||
		strings.HasPrefix(lower, "data:") ||
		strings.HasPrefix(lower, "chrome://") ||
		strings.HasPrefix(lower, "ws://") ||
		strings.HasPrefix(lower, "wss://") ||
		strings.HasPrefix(lower, "javascript:") ||
		strings.Contains(lower, "://") {
		return u
	}
	if strings.HasPrefix(lower, "localhost") ||
		strings.HasPrefix(lower, "127.0.0.1") ||
		strings.HasPrefix(lower, "0.0.0.0") ||
		strings.HasPrefix(lower, "[::1]") {
		return "http://" + u
	}
	if strings.HasPrefix(lower, "/") {
		return "file://" + u
	}
	if strings.Contains(lower, ".") && !strings.Contains(lower, " ") {
		return "https://" + u
	}
	return u
}

// isNumericPort checks if a string is a valid numeric TCP port.
func isNumericPort(s string) (int, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), ":")
	p, err := strconv.Atoi(s)
	if err == nil && p > 0 && p <= 65535 {
		return p, true
	}
	return 0, false
}

// resolveTimeout converts timeout_ms or timeout into milliseconds.
// If timeout is <= 60 and > 0, it is interpreted as seconds and converted to milliseconds.
func resolveTimeout(timeoutMs int, timeout int) int {
	if timeoutMs > 0 {
		return timeoutMs
	}
	if timeout > 0 {
		if timeout <= 60 {
			return timeout * 1000
		}
		return timeout
	}
	return 0
}

// resolveStorageType normalizes common aliases for storage type (e.g. cookies -> cookie).
func resolveStorageType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	switch t {
	case "cookies", "cookie":
		return "cookie"
	case "local", "localstorage", "local_storage":
		return "local"
	case "session", "sessionstorage", "session_storage":
		return "session"
	case "all", "*":
		return "all"
	default:
		return t
	}
}
