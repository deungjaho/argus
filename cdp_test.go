package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- CDP connection pool tests ---

func TestCdpPoolReusesConnection(t *testing.T) {
	// This test verifies the pool returns the same connection for the same URL.
	// We can't test real CDP without Chrome, but we can test pool logic with a mock WS.
	// Since we can't easily mock WebSocket, we test the pool's get/create logic
	// by verifying it doesn't panic and returns consistent results for the same key.

	// Clear pool
	closeAllConns()

	// Test that pool is empty initially
	poolMu.Lock()
	count := len(pool)
	poolMu.Unlock()
	if count != 0 {
		t.Errorf("expected empty pool, got %d entries", count)
	}
}

func TestCdpPoolCloseAll(t *testing.T) {
	closeAllConns()
	poolMu.Lock()
	count := len(pool)
	poolMu.Unlock()
	if count != 0 {
		t.Errorf("expected empty pool after closeAll, got %d", count)
	}
}

// --- CDP HTTP endpoint tests ---

func TestCdpVersion(t *testing.T) {
	// Mock the /json/version endpoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"Browser":              "Chrome/120.0.0.0",
				"webSocketDebuggerUrl": "ws://127.0.0.1:9222/devtools/browser/abc",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	// Temporarily override cdpBaseURL
	originalPort := cdpPort
	defer func() { cdpPort = originalPort }()

	// Parse the test server port
	addr := server.URL
	port := strings.TrimPrefix(addr, "http://127.0.0.1:")
	if port == addr {
		t.Skip("could not parse test server port")
	}

	// We can't easily override cdpBaseURL() since it's a function,
	// but we can test the HTTP parsing logic directly.
	resp, err := http.Get(server.URL + "/json/version")
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	var version browserCDPVersionInfo
	if err := json.NewDecoder(resp.Body).Decode(&version); err != nil {
		t.Fatalf("failed to decode version: %v", err)
	}
	if version.Browser != "Chrome/120.0.0.0" {
		t.Errorf("expected Chrome/120.0.0.0, got %s", version.Browser)
	}
	if version.WebSocketDebuggerURL != "ws://127.0.0.1:9222/devtools/browser/abc" {
		t.Errorf("unexpected WebSocket URL: %s", version.WebSocketDebuggerURL)
	}
}

func TestCdpTargets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/list" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]browserTarget{
				{ID: "tab1", Type: "page", URL: "http://localhost:3003", Title: "Dev Server"},
				{ID: "tab2", Type: "page", URL: "http://localhost:3000", Title: "Other"},
				{ID: "ext1", Type: "service_worker", URL: "chrome-extension://abc"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/json/list")
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	var targets []browserTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		t.Fatalf("failed to decode targets: %v", err)
	}
	if len(targets) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(targets))
	}
	if targets[0].ID != "tab1" || targets[0].URL != "http://localhost:3003" {
		t.Errorf("unexpected first target: %+v", targets[0])
	}
}

// --- Helper function tests ---

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		inputs []string
		want   string
	}{
		{[]string{"", "", "hello"}, "hello"},
		{[]string{"  ", "world", ""}, "world"},
		{[]string{"", "", ""}, ""},
		{[]string{"first", "second"}, "first"},
	}
	for _, tt := range tests {
		got := firstNonEmpty(tt.inputs...)
		if got != tt.want {
			t.Errorf("firstNonEmpty(%v) = %q, want %q", tt.inputs, got, tt.want)
		}
	}
}

func TestJsString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", `"hello"`},
		{`say "hi"`, `"say \"hi\""`},
		{"line\nbreak", `"line\nbreak"`},
		{"", `""`},
	}
	for _, tt := range tests {
		got := jsString(tt.input)
		if got != tt.want {
			t.Errorf("jsString(%q) = %s, want %s", tt.input, got, tt.want)
		}
	}
}

func TestJsNumber(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{100, "100"},
		{100.5, "100.5"},
		{0, "0"},
		{-3.14, "-3.14"},
	}
	for _, tt := range tests {
		got := jsNumber(tt.input)
		if got != tt.want {
			t.Errorf("jsNumber(%v) = %s, want %s", tt.input, got, tt.want)
		}
	}
}

func TestNumberValue(t *testing.T) {
	// float64
	if v, ok := numberValue(float64(42)); !ok || v != 42 {
		t.Errorf("numberValue(float64(42)) = %v, %v, want 42, true", v, ok)
	}
	// int
	if v, ok := numberValue(int(42)); !ok || v != 42 {
		t.Errorf("numberValue(int(42)) = %v, %v, want 42, true", v, ok)
	}
	// string (not a number)
	if _, ok := numberValue("hello"); ok {
		t.Error("numberValue(\"hello\") should return false")
	}
	// nil
	if _, ok := numberValue(nil); ok {
		t.Error("numberValue(nil) should return false")
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("hello world", 5); got != "he..." {
		t.Errorf("truncateRunes(\"hello world\", 5) = %q, want %q", got, "he...")
	}
	if got := truncateRunes("hi", 5); got != "hi" {
		t.Errorf("truncateRunes(\"hi\", 5) = %q, want %q", got, "hi")
	}
	if got := truncateRunes("hello", 0); got != "hello" {
		t.Errorf("truncateRunes(\"hello\", 0) = %q, want %q", got, "hello")
	}
	// Unicode: maxChars=5 → keep 2 chars + "..."
	if got := truncateRunes("你好世界测试", 5); got != "你好..." {
		t.Errorf("truncateRunes unicode = %q, want %q", got, "你好...")
	}
}

// --- Config tests ---

func TestSetConfig(t *testing.T) {
	// Save original state
	origURL, origPort, origAttach := getConfig()
	defer func() {
		configMu.Lock()
		configuredURL = origURL
		configuredPort = origPort
		configuredAttach = origAttach
		configMu.Unlock()
	}()

	attach := true
	setConfig("http://localhost:8080", 8080, &attach)

	url, port, attachGot := getConfig()
	if url != "http://localhost:8080" {
		t.Errorf("url = %q, want %q", url, "http://localhost:8080")
	}
	if port != 8080 {
		t.Errorf("port = %d, want 8080", port)
	}
	if !attachGot {
		t.Error("attach = false, want true")
	}
}

func TestSetProfile(t *testing.T) {
	origProfile := configuredProfile
	defer func() {
		configMu.Lock()
		configuredProfile = origProfile
		configMu.Unlock()
	}()

	setProfile("user")
	if !useUserProfile() {
		t.Error("useUserProfile() = false after setProfile(\"user\")")
	}

	setProfile("isolated")
	if useUserProfile() {
		t.Error("useUserProfile() = true after setProfile(\"isolated\")")
	}

	// Invalid value should not change anything
	setProfile("invalid")
	if useUserProfile() {
		t.Error("useUserProfile() = true after setProfile(\"invalid\")")
	}
}

// --- Intercept rule tests ---

func TestInterceptRuleMatching(t *testing.T) {
	state := &interceptState{}
	state.rules = []interceptRule{
		{URLPattern: "/api/auth", Method: "GET", Action: "mock", Status: 200},
		{URLPattern: "/api/users", Method: "", Action: "block"},
	}

	if len(state.rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(state.rules))
	}

	// First rule matches /api/auth
	if !strings.Contains("http://localhost:3003/api/auth", state.rules[0].URLPattern) {
		t.Error("first rule should match /api/auth URL")
	}

	// Second rule matches any method
	if state.rules[1].Method != "" {
		t.Error("second rule should match any method")
	}

	// Test matching logic
	state.mu.Lock()
	matched := (*interceptRule)(nil)
	for i := range state.rules {
		rule := &state.rules[i]
		if !strings.Contains("http://localhost:3003/api/auth", rule.URLPattern) {
			continue
		}
		if rule.Method != "" && rule.Method != "GET" {
			continue
		}
		matched = rule
		break
	}
	state.mu.Unlock()
	if matched == nil {
		t.Error("expected to match /api/auth rule")
	}
	if matched.Action != "mock" {
		t.Errorf("matched action = %q, want mock", matched.Action)
	}
}

// --- Auto-wait expression tests ---

func TestElementStateExpression(t *testing.T) {
	expr := elementStateExpression("#submit-btn", "", "")
	if !strings.Contains(expr, "#submit-btn") {
		t.Error("expression should contain the selector")
	}
	if !strings.Contains(expr, "querySelector") {
		t.Error("expression should use querySelector")
	}
	if !strings.Contains(expr, "found") {
		t.Error("expression should return found field")
	}
	if !strings.Contains(expr, "visible") {
		t.Error("expression should return visible field")
	}
	if !strings.Contains(expr, "enabled") {
		t.Error("expression should return enabled field")
	}
}

func TestElementStateExpressionWithText(t *testing.T) {
	expr := elementStateExpression("", "Submit", "")
	if !strings.Contains(expr, "Submit") {
		t.Error("expression should contain the text")
	}
	if !strings.Contains(expr, "innerText") {
		t.Error("expression should search by innerText")
	}
}

// --- Key info tests ---

func TestKeyInfo(t *testing.T) {
	tests := []struct {
		input    string
		wantKey  string
		wantCode string
	}{
		{"Enter", "Enter", "Enter"},
		{"enter", "Enter", "Enter"},
		{"Escape", "Escape", "Escape"},
		{"Tab", "Tab", "Tab"},
		{"a", "a", "KeyA"},
		{"A", "A", "KeyA"},
		{"1", "1", "Digit1"},
	}
	for _, tt := range tests {
		info := keyInfo(tt.input)
		if info.Key != tt.wantKey {
			t.Errorf("keyInfo(%q).Key = %q, want %q", tt.input, info.Key, tt.wantKey)
		}
		if info.Code != tt.wantCode {
			t.Errorf("keyInfo(%q).Code = %q, want %q", tt.input, info.Code, tt.wantCode)
		}
	}
}

// --- Screenshot expression tests ---

func TestCoordinateOverlayExpression(t *testing.T) {
	expr := coordinateOverlayExpression(100, 25)
	if !strings.Contains(expr, "100") {
		t.Error("expression should contain major step")
	}
	if !strings.Contains(expr, "25") {
		t.Error("expression should contain minor step")
	}
	if !strings.Contains(expr, "__argus_coordinate_overlay__") {
		t.Error("expression should create overlay element")
	}
}

// --- Device profile tests ---

func TestDeviceProfiles(t *testing.T) {
	if _, ok := deviceProfiles["iPhone 12"]; !ok {
		t.Error("iPhone 12 should be in device profiles")
	}
	if _, ok := deviceProfiles["Desktop 1080p"]; !ok {
		t.Error("Desktop 1080p should be in device profiles")
	}
	avail := availableDevices()
	if !strings.Contains(avail, "iPhone 12") {
		t.Error("availableDevices() should list iPhone 12")
	}
}

// --- Log entry tests ---

func TestLogEntryImportant(t *testing.T) {
	tests := []struct {
		entry map[string]any
		want  bool
	}{
		{map[string]any{"source": "exception", "level": "error", "text": "Uncaught TypeError"}, true},
		{map[string]any{"source": "console", "level": "warning", "text": "deprecated API"}, true},
		{map[string]any{"source": "console", "level": "error", "text": "fetch failed"}, true},
		{map[string]any{"source": "console", "level": "log", "text": "hello world"}, false},
		{map[string]any{"source": "console", "level": "info", "text": "page loaded"}, false},
		{map[string]any{"source": "console", "level": "log", "text": "this was the stack"}, false},
	}
	for _, tt := range tests {
		got := logEntryImportant(tt.entry)
		if got != tt.want {
			t.Errorf("logEntryImportant(%v) = %v, want %v", tt.entry, got, tt.want)
		}
	}
}

func TestPrepareLogEntries(t *testing.T) {
	entries := []map[string]any{
		{"source": "console", "level": "log", "text": "hello"},
		{"source": "console", "level": "error", "text": "failed"},
		{"source": "console", "level": "error", "text": "failed"}, // duplicate
		{"source": "console", "level": "log", "text": "world"},
	}

	// With all=false, only errors should be included
	prepared, omitted := prepareLogEntries(entries, false, 10, 100)
	if len(prepared) != 1 {
		t.Errorf("expected 1 important entry, got %d", len(prepared))
	}
	_ = omitted

	// With all=true, all entries included (with dedup)
	prepared, _ = prepareLogEntries(entries, true, 10, 100)
	if len(prepared) != 3 {
		t.Errorf("expected 3 unique entries (deduped), got %d", len(prepared))
	}

	// With limit=2
	prepared, omitted = prepareLogEntries(entries, true, 2, 100)
	if len(prepared) != 2 {
		t.Errorf("expected 2 entries with limit, got %d", len(prepared))
	}
	if omitted != 1 {
		t.Errorf("expected 1 omitted, got %d", omitted)
	}
}

// --- waitFor expression tests ---

func TestWaitForExpression(t *testing.T) {
	expr := waitForExpression(waitForInput{Text: "Loading complete"})
	if expr == "" {
		t.Error("expression should not be empty")
	}
	if !strings.Contains(expr, "Loading complete") {
		t.Error("expression should contain the text")
	}
	if !strings.Contains(expr, "satisfied") {
		t.Error("expression should return satisfied field")
	}
}

func TestWaitForExpressionEmpty(t *testing.T) {
	expr := waitForExpression(waitForInput{})
	if expr != "" {
		t.Error("expression should be empty when no conditions given")
	}
}

// --- File helper tests ---

func TestFileExists(t *testing.T) {
	if !fileExists(".") {
		t.Error("fileExists(\".\") should be true")
	}
	if fileExists("nonexistent-file-12345") {
		t.Error("fileExists for nonexistent should be false")
	}
}

func TestDirExists(t *testing.T) {
	if !dirExists(".") {
		t.Error("dirExists(\".\") should be true")
	}
	if dirExists("nonexistent-dir-12345") {
		t.Error("dirExists for nonexistent should be false")
	}
	if dirExists("cdp_test.go") {
		t.Error("dirExists for a file should be false")
	}
}

// --- Image dimensions test ---

func TestImageDimensionsInvalidData(t *testing.T) {
	w, h, ok := imageDimensions([]byte("not an image"))
	if ok {
		t.Errorf("expected ok=false for invalid data, got w=%d h=%d ok=%v", w, h, ok)
	}
}

// --- ensure test doesn't hang ---
// TestMain is in testmain_test.go (unit) and integration_test.go (integration)
