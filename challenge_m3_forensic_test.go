package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// 1. Stress test MockRoute Pattern Matching with edge cases
func TestAdversarial_MockRoutePatterns(t *testing.T) {
	cases := []struct {
		pattern  string
		url      string
		expected bool
	}{
		// URLs with query params, fragments, ports
		{"*/api/v1/users*", "https://sub.domain.co.uk:8443/api/v1/users?token=xyz#hash", true},
		{"*/search\\?*", "https://engine.com/search?q=argus", true},
		{"*://*.internal.net/*", "https://auth.internal.net/oauth/token", true},
		{"*://*.internal.net/*", "https://external.net/oauth/token", false},

		// Regex with flags and anchors
		{"/^https?:\\/\\/api\\.example\\.com\\/data\\?id=[0-9]+$/", "https://api.example.com/data?id=4920", true},
		{"/^https?:\\/\\/api\\.example\\.com\\/data\\?id=[0-9]+$/", "https://api.example.com/data?id=abc", false},

		// Multiple extension globs
		{"*.min.json", "https://domain.com/v1/app.min.json", true},
		{"*.min.json", "https://domain.com/v1/app.json", false},

		// Substring match with special regex chars
		{"/api/v1/foo[bar]", "https://example.com/api/v1/foo[bar]", true},
		{"/api/v1/foo[bar]", "https://example.com/api/v1/foob", false},
	}

	for _, tc := range cases {
		re, _, err := compileRoutePattern(tc.pattern)
		if err != nil {
			t.Fatalf("pattern %q failed to compile: %v", tc.pattern, err)
		}
		got := re.MatchString(tc.url)
		if got != tc.expected {
			t.Errorf("pattern %q on URL %q: got %v, want %v (compiled: %s)", tc.pattern, tc.url, got, tc.expected, re.String())
		}
	}
}

// 2. Stress test MockRoute Rule State and Matching Logic
func TestAdversarial_MockRouteRulesState(t *testing.T) {
	state := &interceptState{}

	// Add rule with JSON object body
	m, isRegex, err := compileRoutePattern("*/api/v2/config*")
	if err != nil || isRegex {
		t.Fatalf("unexpected compile result: err=%v, isRegex=%v", err, isRegex)
	}

	rule := interceptRule{
		URLPattern:  "*/api/v2/config*",
		Method:      "GET",
		Status:      201,
		Body:        `{"enabled":true,"items":[1,2,3]}`,
		Headers:     map[string]string{"X-Custom-Header": "ArgusTest", "Content-Type": "application/json"},
		ContentType: "application/json",
		Action:      "mock",
		matcher:     m,
	}

	state.mu.Lock()
	state.rules = append(state.rules, rule)
	state.mu.Unlock()

	// Verify state and rule properties
	state.mu.RLock()
	if len(state.rules) != 1 {
		t.Fatalf("expected 1 rule in state, got %d", len(state.rules))
	}
	r := state.rules[0]
	state.mu.RUnlock()

	if r.Status != 201 {
		t.Errorf("expected status 201, got %d", r.Status)
	}
	if r.Headers["X-Custom-Header"] != "ArgusTest" {
		t.Errorf("missing custom header: %+v", r.Headers)
	}
	if r.Headers["Content-Type"] != "application/json" {
		t.Errorf("missing Content-Type header in rule: %+v", r.Headers)
	}
	if !strings.Contains(r.Body, `"enabled":true`) {
		t.Errorf("unexpected rule body: %s", r.Body)
	}

	// Test rule matching against mock CDP requests
	testMatch := func(url, method string) bool {
		state.mu.RLock()
		defer state.mu.RUnlock()
		for _, r := range state.rules {
			if r.matcher != nil && r.matcher.MatchString(url) {
				if r.Method == "" || strings.EqualFold(r.Method, method) {
					return true
				}
			}
		}
		return false
	}

	if !testMatch("https://example.com/api/v2/config?env=prod", "GET") {
		t.Errorf("expected route to match GET config")
	}
	if testMatch("https://example.com/api/v2/config?env=prod", "POST") {
		t.Errorf("expected route NOT to match POST config")
	}
	if testMatch("https://example.com/api/v1/other", "GET") {
		t.Errorf("expected route NOT to match different path")
	}
}

// 3. Stress test Dialog Manager Anti-Deadlock and Concurrency
func TestAdversarial_DialogManagerStress(t *testing.T) {
	dialogMgr.Reset()
	defer dialogMgr.Reset()

	const targetCount = 10
	const opCount = 50

	var wg sync.WaitGroup
	for tIdx := 0; tIdx < targetCount; tIdx++ {
		targetID := fmt.Sprintf("target_stress_%d", tIdx)
		wsURL := "ws://127.0.0.1:9222/devtools/page/" + targetID
		state := dialogMgr.GetOrCreate(targetID, wsURL)

		wg.Add(3)
		// Goroutine 1: Rapid dialog opening and closing
		go func(s *TargetDialogState, id string) {
			defer wg.Done()
			for i := 0; i < opCount; i++ {
				s.SetPending(&DialogInfo{
					Type:     "alert",
					Message:  fmt.Sprintf("Alert %s-%d", id, i),
					OpenedAt: time.Now(),
				})
				time.Sleep(100 * time.Microsecond)
				s.MarkClosed(true, "")
			}
		}(state, targetID)

		// Goroutine 2: Rapid policy updates
		go func(s *TargetDialogState) {
			defer wg.Done()
			policies := []DialogPolicy{DialogPolicyDismiss, DialogPolicyAccept, DialogPolicyNone}
			for i := 0; i < opCount; i++ {
				s.SetPolicy(policies[i%len(policies)], fmt.Sprintf("prompt_%d", i))
			}
		}(state)

		// Goroutine 3: Rapid reads & HasPending checks
		go func(id string) {
			defer wg.Done()
			for i := 0; i < opCount; i++ {
				_ = dialogMgr.HasPendingDialog(id)
				_ = dialogMgr.GetPendingDialog(id)
			}
		}(targetID)
	}

	wg.Wait()
}

// 4. Stress test Dev Overlay File Location Parser with difficult formats
func TestAdversarial_DevOverlayFileParsing(t *testing.T) {
	cases := []struct {
		input    string
		wantFile string
		wantLine int
		wantCol  int
	}{
		{"at handleClick (src/components/Modal.tsx:55:12)", "src/components/Modal.tsx", 55, 12},
		{"Error in /var/www/project/src/index.js:10:5", "/var/www/project/src/index.js", 10, 5},
		{"webpack-internal:///./node_modules/vue/dist/vue.runtime.esm.js:62:3", "./node_modules/vue/dist/vue.runtime.esm.js", 62, 3},
		{"File: /Users/dev/app/src/main.ts:89", "/Users/dev/app/src/main.ts", 89, 0},
		{"Unknown error with no file info", "Unknown error with no file info", 0, 0},
	}

	for _, tc := range cases {
		file, line, col := parseFileLocation(tc.input)
		if file != tc.wantFile || line != tc.wantLine || col != tc.wantCol {
			t.Errorf("parseFileLocation(%q) = (%q, %d, %d), want (%q, %d, %d)",
				tc.input, file, line, col, tc.wantFile, tc.wantLine, tc.wantCol)
		}
	}
}

// 5. Stress test Screenshot Disk Storage Mode and Path Sanitization
func TestAdversarial_ScreenshotDiskModeIntegrity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "argus-adv-screenshot-*")
	if err != nil {
		t.Fatalf("temp dir error: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testPNG := createTestPNG(320, 240)
	targetFile := filepath.Join(tmpDir, "nested", "sub", "capture.png")

	// Verify resolveScreenshotPath creates or validates paths
	resolvedPath, err := resolveScreenshotPath(targetFile, "png")
	if err != nil {
		t.Fatalf("resolveScreenshotPath failed: %v", err)
	}
	if !strings.HasSuffix(resolvedPath, ".png") {
		t.Errorf("expected .png suffix, got %s", resolvedPath)
	}

	// Verify writeScreenshot writes to disk
	imgData, mimeType, err := writeScreenshot(testPNG, resolvedPath, "png", 90, 0, false)
	if err != nil {
		t.Fatalf("writeScreenshot failed: %v", err)
	}
	if mimeType != "image/png" {
		t.Errorf("expected image/png, got %s", mimeType)
	}
	if len(imgData) == 0 {
		t.Fatal("empty imgData returned")
	}

	// Verify file is actually on disk
	fi, err := os.Stat(resolvedPath)
	if err != nil {
		t.Fatalf("file not found on disk at %s: %v", resolvedPath, err)
	}
	if fi.Size() == 0 {
		t.Errorf("file size on disk is 0 bytes")
	}
}
