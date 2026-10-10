package main

import (
	"strings"
	"testing"
)

func TestRedactJWT(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	result := redactString(jwt)
	if result != "[REDACTED JWT]" {
		t.Errorf("JWT not redacted: got %q", result)
	}
}

func TestRedactBearer(t *testing.T) {
	result := redactString("Bearer abc123token")
	if result != "Bearer [REDACTED]" {
		t.Errorf("Bearer not redacted: got %q", result)
	}
}

func TestRedactPassword(t *testing.T) {
	result := redactString("password=secret123")
	if result != "password=[REDACTED]" {
		t.Errorf("password= not redacted: got %q", result)
	}
}

func TestRedactAPIKey(t *testing.T) {
	result := redactString("api_key=sk-1234567890")
	if result != "api_key=[REDACTED]" {
		t.Errorf("api_key= not redacted: got %q", result)
	}
}

func TestRedactPrivateKey(t *testing.T) {
	key := "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQ..."
	result := redactString(key)
	if result != "[REDACTED PRIVATE KEY]" {
		t.Errorf("private key not redacted: got %q", result)
	}
}

func TestRedactNonSensitive(t *testing.T) {
	result := redactString("hello world")
	if result != "hello world" {
		t.Errorf("non-sensitive string was modified: got %q", result)
	}
}

func TestRedactShortStringNotMatchedAsJWT(t *testing.T) {
	// "eyJ" is in the string but it's too short to be a JWT
	result := redactString("eyJ short")
	if result != "eyJ short" {
		t.Errorf("short string with eyJ was incorrectly redacted: got %q", result)
	}
}

func TestRedactDataMap(t *testing.T) {
	data := map[string]any{
		"message":  "hello",
		"password": "secret123",
		"token":    "abc123",
		"nested": map[string]any{
			"api_key": "sk-xxx",
			"text":    "safe text",
		},
	}
	result := redactData(data)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("result is not a map")
	}
	if m["password"] != "[REDACTED]" {
		t.Errorf("password not redacted: got %v", m["password"])
	}
	if m["token"] != "[REDACTED]" {
		t.Errorf("token not redacted: got %v", m["token"])
	}
	if m["message"] != "hello" {
		t.Errorf("message was modified: got %v", m["message"])
	}
	nested, ok := m["nested"].(map[string]any)
	if !ok {
		t.Fatal("nested is not a map")
	}
	if nested["api_key"] != "[REDACTED]" {
		t.Errorf("nested api_key not redacted: got %v", nested["api_key"])
	}
	if nested["text"] != "safe text" {
		t.Errorf("nested text was modified: got %v", nested["text"])
	}
}

func TestRedactDataSlice(t *testing.T) {
	data := []any{
		"Bearer token123",
		"safe text",
		map[string]any{"secret": "value"},
	}
	result := redactData(data)
	slice, ok := result.([]any)
	if !ok {
		t.Fatal("result is not a slice")
	}
	if slice[0] != "Bearer [REDACTED]" {
		t.Errorf("Bearer not redacted in slice: got %v", slice[0])
	}
	if slice[1] != "safe text" {
		t.Errorf("safe text was modified in slice: got %v", slice[1])
	}
	m, ok := slice[2].(map[string]any)
	if !ok {
		t.Fatal("slice[2] is not a map")
	}
	if m["secret"] != "[REDACTED]" {
		t.Errorf("secret not redacted in slice map: got %v", m["secret"])
	}
}

func TestIsSensitiveKey(t *testing.T) {
	sensitive := []string{"password", "PASSWORD", "token", "api_key", "Authorization", "cookie", "session_id"}
	for _, key := range sensitive {
		if !isSensitiveKey(key) {
			t.Errorf("isSensitiveKey(%q) = false, want true", key)
		}
	}
	notSensitive := []string{"message", "text", "level", "url", "method", "status"}
	for _, key := range notSensitive {
		if isSensitiveKey(key) {
			t.Errorf("isSensitiveKey(%q) = true, want false", key)
		}
	}
}

func TestRedactPreservesStackTraceAndLineNumbers(t *testing.T) {
	// A real stack trace that mentions authorization and passwords
	rawTrace := "Error: Uncaught exception at /app/src/services/authorizationService.ts:42:15 in checkAuth() - connect failed with password=supersecret123&user=admin and Authorization: Bearer secret_token_xyz"
	redacted := redactString(rawTrace)

	if !strings.Contains(redacted, "/app/src/services/authorizationService.ts:42:15") {
		t.Errorf("stack trace filename/line number was corrupted: %s", redacted)
	}
	if !strings.Contains(redacted, "checkAuth()") {
		t.Errorf("function name was corrupted: %s", redacted)
	}
	if strings.Contains(redacted, "supersecret123") {
		t.Errorf("password was not redacted: %s", redacted)
	}
	if strings.Contains(redacted, "secret_token_xyz") {
		t.Errorf("bearer token was not redacted: %s", redacted)
	}
	if !strings.Contains(redacted, "password=[REDACTED]") {
		t.Errorf("expected password=[REDACTED] in %s", redacted)
	}
	if !strings.Contains(redacted, "Bearer [REDACTED]") {
		t.Errorf("expected Bearer [REDACTED] in %s", redacted)
	}
}

func TestRedactPreservesStackTraceWithPrivateKey(t *testing.T) {
	rawTrace := "Error in ssl.ts:18:22: failed to parse private key:\n-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0m...\n-----END RSA PRIVATE KEY-----\n    at loadCertificates (cert.ts:88:5)\n    at initServer (server.ts:12:3)"
	redacted := redactString(rawTrace)

	if !strings.Contains(redacted, "ssl.ts:18:22") {
		t.Errorf("expected file/line to be preserved before private key, got: %s", redacted)
	}
	if !strings.Contains(redacted, "cert.ts:88:5") {
		t.Errorf("expected stack trace after private key to be preserved, got: %s", redacted)
	}
	if !strings.Contains(redacted, "[REDACTED RSA KEY]") {
		t.Errorf("expected [REDACTED RSA KEY], got: %s", redacted)
	}
	if strings.Contains(redacted, "MIIEowIBAAKCAQEA0m") {
		t.Errorf("private key content was not redacted: %s", redacted)
	}
}
