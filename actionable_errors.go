package main

import (
	"fmt"
	"strings"
)

// ActionableError is a structured error that carries an error type, a
// human-readable message, and a list of suggested fixes (hints). It is
// returned by Chrome launch and navigation tools so agents can branch on
// the error type and act on the hints instead of parsing free-text.
type ActionableError struct {
	ErrorType string   `json:"error_type"`      // e.g. "chrome_launch_failed", "navigation_dns_error"
	Message   string   `json:"message"`         // human-readable detail
	URL       string   `json:"url,omitempty"`   // target URL, for navigation errors
	Hints     []string `json:"hints"`           // suggested fixes
	Cause     string   `json:"cause,omitempty"` // root cause, if different from message
}

func (e *ActionableError) Error() string {
	if e.URL != "" {
		return fmt.Sprintf("%s: %s (url=%s)", e.ErrorType, e.Message, e.URL)
	}
	return fmt.Sprintf("%s: %s", e.ErrorType, e.Message)
}

// --- Chrome launch errors ---

// chromeLaunchHints returns the standard hint list for a Chrome launch
// failure. The searchedPaths are included so the agent can see what was
// checked; they are bounded to avoid leaking excessive environment detail.
func chromeLaunchHints(searchedPaths []string) []string {
	hints := []string{
		"Install Chrome/Chromium",
		"Set ARGUS_CHROME_PATH to the Chrome binary path",
		fmt.Sprintf("Check that port %d is free (or set ARGUS_CDP_PORT)", cdpPort),
		"Try ARGUS_CHROME_HEADLESS=1 for headless servers",
	}
	if len(searchedPaths) > 0 {
		bounded := searchedPaths
		if len(bounded) > 8 {
			bounded = bounded[:8]
		}
		hints = append(hints, "Searched paths: "+strings.Join(bounded, ", "))
	}
	return hints
}

// newChromeLaunchError builds an ActionableError for a Chrome launch failure.
func newChromeLaunchError(cause string, searchedPaths []string) *ActionableError {
	return &ActionableError{
		ErrorType: "chrome_launch_failed",
		Message:   "Chrome/Chromium could not be launched",
		Hints:     chromeLaunchHints(searchedPaths),
		Cause:     cause,
	}
}

// --- Navigation errors ---

// navigationErrorType classifies a navigation failure from its error text.
// Returns the error type and the matching hint list.
func navigationErrorType(errText string) (string, []string) {
	lower := strings.ToLower(errText)
	switch {
	case strings.Contains(lower, "dns") || strings.Contains(lower, "name resolution") || strings.Contains(lower, "err_name_not_resolved"):
		return "navigation_dns_error", []string{
			"Check the URL spelling",
			"Verify network connectivity (DNS may be unavailable)",
		}
	case strings.Contains(lower, "ssl") || strings.Contains(lower, "certificate") || strings.Contains(lower, "err_cert"):
		return "navigation_ssl_error", []string{
			"Check the site's certificate",
			"Try http:// if testing a non-production site",
		}
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out"):
		return "navigation_timeout", []string{
			"Check if the site is slow or unresponsive",
			"Increase the navigation timeout",
			"Verify the site is not blocking automated browsers",
		}
	case strings.Contains(lower, "connection refused") || strings.Contains(lower, "err_connection_refused"):
		return "navigation_connection_refused", []string{
			"Check if the server is running on the target port",
			"Verify firewall rules are not blocking the connection",
		}
	default:
		return "navigation_failed", []string{
			"Check the URL and network connectivity",
			"Use snapshot or status to inspect the current page state",
		}
	}
}

// newNavigationError builds an ActionableError for a navigation failure.
func newNavigationError(url, errText string) *ActionableError {
	et, hints := navigationErrorType(errText)
	return &ActionableError{
		ErrorType: et,
		Message:   "Navigation to " + url + " failed",
		URL:       url,
		Hints:     hints,
		Cause:     errText,
	}
}
