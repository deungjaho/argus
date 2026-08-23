package main

import (
	"strings"
	"testing"
)

func TestActionableErrorError(t *testing.T) {
	e := &ActionableError{
		ErrorType: "chrome_launch_failed",
		Message:   "Chrome/Chromium could not be launched",
		Hints:     []string{"Install Chrome/Chromium"},
	}
	s := e.Error()
	if !strings.Contains(s, "chrome_launch_failed") {
		t.Errorf("Error() missing error_type: %q", s)
	}
	if !strings.Contains(s, "Chrome/Chromium could not be launched") {
		t.Errorf("Error() missing message: %q", s)
	}

	eURL := &ActionableError{
		ErrorType: "navigation_dns_error",
		Message:   "Navigation to http://bad.example failed",
		URL:       "http://bad.example",
	}
	s = eURL.Error()
	if !strings.Contains(s, "url=http://bad.example") {
		t.Errorf("Error() missing url: %q", s)
	}
}

func TestNewChromeLaunchError(t *testing.T) {
	e := newChromeLaunchError("Chrome not found", []string{"/usr/bin/chromium", "/usr/bin/google-chrome"})
	if e.ErrorType != "chrome_launch_failed" {
		t.Errorf("ErrorType = %q, want chrome_launch_failed", e.ErrorType)
	}
	if e.Cause != "Chrome not found" {
		t.Errorf("Cause = %q, want Chrome not found", e.Cause)
	}
	// Must include the standard hints.
	wantHints := []string{
		"Install Chrome/Chromium",
		"Set ARGUS_CHROME_PATH to the Chrome binary path",
		"Try ARGUS_CHROME_HEADLESS=1 for headless servers",
	}
	for _, want := range wantHints {
		found := false
		for _, h := range e.Hints {
			if strings.Contains(h, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("hints missing %q in %v", want, e.Hints)
		}
	}
	// Must include searched paths.
	foundPaths := false
	for _, h := range e.Hints {
		if strings.Contains(h, "Searched paths") {
			foundPaths = true
			if !strings.Contains(h, "/usr/bin/chromium") {
				t.Errorf("searched paths hint missing path: %q", h)
			}
		}
	}
	if !foundPaths {
		t.Error("hints missing searched paths")
	}
}

func TestNewChromeLaunchErrorBoundedPaths(t *testing.T) {
	paths := make([]string, 20)
	for i := range paths {
		paths[i] = "/path/to/chrome"
	}
	e := newChromeLaunchError("fail", paths)
	for _, h := range e.Hints {
		if strings.Contains(h, "Searched paths") {
			// Should be bounded to 8 entries.
			if strings.Count(h, "/path/to/chrome") > 8 {
				t.Errorf("searched paths not bounded: %q", h)
			}
		}
	}
}

func TestNavigationErrorType(t *testing.T) {
	cases := []struct {
		name     string
		errText  string
		wantType string
		wantHint string
	}{
		{name: "dns", errText: "ERR_NAME_NOT_RESOLVED: DNS lookup failed", wantType: "navigation_dns_error", wantHint: "Check the URL spelling"},
		{name: "dns_lower", errText: "dns name resolution error", wantType: "navigation_dns_error", wantHint: "Verify network connectivity"},
		{name: "ssl", errText: "ERR_CERT_AUTHORITY_INVALID: SSL certificate error", wantType: "navigation_ssl_error", wantHint: "certificate"},
		{name: "ssl_lower", errText: "ssl handshake failed", wantType: "navigation_ssl_error", wantHint: "http://"},
		{name: "timeout", errText: "navigation timed out after 30s", wantType: "navigation_timeout", wantHint: "Increase the navigation timeout"},
		{name: "timeout_lower", errText: "request timeout", wantType: "navigation_timeout", wantHint: "site is slow"},
		{name: "refused", errText: "ERR_CONNECTION_REFUSED: connection refused", wantType: "navigation_connection_refused", wantHint: "server is running"},
		{name: "generic", errText: "something went wrong", wantType: "navigation_failed", wantHint: "network connectivity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			et, hints := navigationErrorType(tc.errText)
			if et != tc.wantType {
				t.Errorf("error type = %q, want %q", et, tc.wantType)
			}
			found := false
			for _, h := range hints {
				if strings.Contains(h, tc.wantHint) {
					found = true
				}
			}
			if !found {
				t.Errorf("hints %v missing %q", hints, tc.wantHint)
			}
		})
	}
}

func TestNewNavigationError(t *testing.T) {
	e := newNavigationError("http://bad.example", "ERR_NAME_NOT_RESOLVED: dns failed")
	if e.ErrorType != "navigation_dns_error" {
		t.Errorf("ErrorType = %q, want navigation_dns_error", e.ErrorType)
	}
	if e.URL != "http://bad.example" {
		t.Errorf("URL = %q, want http://bad.example", e.URL)
	}
	if e.Cause != "ERR_NAME_NOT_RESOLVED: dns failed" {
		t.Errorf("Cause = %q, want original error text", e.Cause)
	}
	if len(e.Hints) == 0 {
		t.Error("expected non-empty hints")
	}
}

func TestChromeSearchedPaths(t *testing.T) {
	paths := chromeSearchedPaths()
	if len(paths) == 0 {
		t.Fatal("chromeSearchedPaths returned empty list")
	}
	// Should contain at least one path with "chrome" in it (case-insensitive).
	found := false
	for _, p := range paths {
		if strings.Contains(strings.ToLower(p), "chrome") || strings.Contains(strings.ToLower(p), "chromium") {
			found = true
		}
	}
	if !found {
		t.Errorf("chromeSearchedPaths %v contains no chrome/chromium path", paths)
	}
}

func TestChromeLaunchHintsIncludePort(t *testing.T) {
	hints := chromeLaunchHints(nil)
	foundPort := false
	for _, h := range hints {
		if strings.Contains(h, "port") {
			foundPort = true
		}
	}
	if !foundPort {
		t.Errorf("chromeLaunchHints missing port hint: %v", hints)
	}
}
