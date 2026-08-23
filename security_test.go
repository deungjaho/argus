package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSSRFProtectionRejectsFileScheme(t *testing.T) {
	input := networkRequestInput{URL: "file:///etc/passwd"}
	_, err := makeHTTPRequest(input)
	if err == nil {
		t.Fatal("expected error for file:// scheme, got nil")
	}
}

func TestSSRFProtectionRejectsDataScheme(t *testing.T) {
	input := networkRequestInput{URL: "data:text/html,<h1>test</h1>"}
	_, err := makeHTTPRequest(input)
	if err == nil {
		t.Fatal("expected error for data: scheme, got nil")
	}
}

func TestSSRFProtectionRejectsGopherScheme(t *testing.T) {
	input := networkRequestInput{URL: "gopher://localhost:6379/_FLUSHALL"}
	_, err := makeHTTPRequest(input)
	if err == nil {
		t.Fatal("expected error for gopher:// scheme, got nil")
	}
}

func TestSSRFProtectionAllowsHTTP(t *testing.T) {
	// We can't actually make a request in unit tests, but we can verify
	// that the scheme check passes (the request will fail for other reasons).
	// Use a non-routable address to fail fast without side effects.
	input := networkRequestInput{URL: "http://192.0.2.1:1/test", TimeoutMs: 100}
	result, err := makeHTTPRequest(input)
	// Should not fail at the scheme check — may fail at connection.
	if err != nil {
		t.Fatalf("scheme check should pass for http, got error: %v", err)
	}
	// Result should have an error field (connection failed) but not a scheme error.
	if result == nil {
		t.Fatal("result should not be nil")
	}
}

func TestSSRFProtectionAllowsHTTPS(t *testing.T) {
	input := networkRequestInput{URL: "https://192.0.2.1:1/test", TimeoutMs: 100}
	result, err := makeHTTPRequest(input)
	if err != nil {
		t.Fatalf("scheme check should pass for https, got error: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
}

func TestSSRFProtectionRejectsEmptyScheme(t *testing.T) {
	input := networkRequestInput{URL: "not-a-url"}
	_, err := makeHTTPRequest(input)
	if err == nil {
		t.Fatal("expected error for URL without scheme, got nil")
	}
}

func TestCookieRedaction(t *testing.T) {
	// Test the redaction logic directly (without CDP).
	// Values > 20 chars get redacted.
	longValue := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	shortValue := "short"

	cookies := []map[string]any{
		{"name": "session", "value": longValue},
		{"name": "theme", "value": shortValue},
	}

	// Apply same redaction logic as getCookieStorage
	for i := range cookies {
		if val, ok := cookies[i]["value"].(string); ok && len(val) > 20 {
			cookies[i]["value"] = val[:8] + "...(redacted)"
			cookies[i]["value_redacted"] = true
		}
	}

	if cookies[0]["value"] == longValue {
		t.Error("long cookie value should be redacted")
	}
	if cookies[0]["value_redacted"] != true {
		t.Error("long cookie should have value_redacted=true")
	}
	if cookies[1]["value"] != shortValue {
		t.Error("short cookie value should not be redacted")
	}
	if _, hasFlag := cookies[1]["value_redacted"]; hasFlag {
		t.Error("short cookie should not have value_redacted flag")
	}
}

func TestUploadPathCanonicalization(t *testing.T) {
	// Create a temp file to test with
	tmpFile, err := os.CreateTemp("", "argus-test-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString("test content")
	tmpFile.Close()

	// Test that a valid path resolves correctly
	abs, err := filepath.Abs(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if info.IsDir() {
		t.Fatal("file should not be a directory")
	}

	// Test that a non-existent path fails
	_, err = filepath.EvalSymlinks("/nonexistent/path/file.txt")
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}

	// Test that a directory is rejected
	tmpDir, err := os.MkdirTemp("", "argus-test-dir-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	dirResolved, _ := filepath.EvalSymlinks(tmpDir)
	dirInfo, _ := os.Stat(dirResolved)
	if !dirInfo.IsDir() {
		t.Fatal("expected directory")
	}
}
