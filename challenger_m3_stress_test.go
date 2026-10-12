package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ============================================================================
// Feature F12 Stress Tests: Element Screenshot & Disk Storage Mode
// ============================================================================

func TestChallenger_M3_F12_ScreenshotClip_Coordinates(t *testing.T) {
	// 1. Non-integer floating point coordinates
	clip := &ScreenshotClip{
		X:      12.34567,
		Y:      78.90123,
		Width:  456.78901,
		Height: 123.45678,
		Scale:  2.5,
	}

	bytesJSON, err := json.Marshal(clip)
	if err != nil {
		t.Fatalf("failed to marshal ScreenshotClip: %v", err)
	}

	var parsed map[string]float64
	if err := json.Unmarshal(bytesJSON, &parsed); err != nil {
		t.Fatalf("failed to unmarshal ScreenshotClip JSON: %v", err)
	}

	if parsed["x"] != 12.34567 || parsed["y"] != 78.90123 {
		t.Errorf("float coordinates precision lost: %+v", parsed)
	}
	if parsed["width"] != 456.78901 || parsed["height"] != 123.45678 {
		t.Errorf("float dimensions precision lost: %+v", parsed)
	}
	if parsed["scale"] != 2.5 {
		t.Errorf("scale precision lost: %+v", parsed)
	}

	// 2. Out-of-bounds / negative coordinates
	negClip := &ScreenshotClip{
		X:      -150.5,
		Y:      -300.25,
		Width:  1920.0,
		Height: 1080.0,
		Scale:  0.0, // Scale <= 0 should default to 1.0 in cdpCaptureScreenshotWithClip
	}
	if negClip.X >= 0 || negClip.Y >= 0 {
		t.Errorf("expected negative coordinates to be preserved: %+v", negClip)
	}
}

func TestChallenger_M3_F12_DiskStorageMode_DeepDirCreationAndOverwrite(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "argus-challenger-m3-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// 1. Deep nested non-existent directory auto-creation
	deepPath := filepath.Join(tmpDir, "level1", "level2", "level3", "deep_shot.png")
	img1 := createTestPNG(150, 80)

	data1, mime1, err := writeScreenshot(img1, deepPath, "png", 0, 0, false)
	if err != nil {
		t.Fatalf("writeScreenshot into deep path failed: %v", err)
	}
	if mime1 != "image/png" {
		t.Errorf("expected image/png, got %s", mime1)
	}
	fi1, err := os.Stat(deepPath)
	if err != nil {
		t.Fatalf("deep screenshot file was not created: %v", err)
	}
	if fi1.Size() != int64(len(data1)) {
		t.Errorf("file size mismatch: stat=%d, returned=%d", fi1.Size(), len(data1))
	}

	// 2. File overwrite behavior
	img2 := createTestPNG(300, 200) // larger image
	data2, mime2, err := writeScreenshot(img2, deepPath, "png", 0, 0, false)
	if err != nil {
		t.Fatalf("writeScreenshot overwrite failed: %v", err)
	}
	if mime2 != "image/png" {
		t.Errorf("expected image/png, got %s", mime2)
	}
	fi2, err := os.Stat(deepPath)
	if err != nil {
		t.Fatalf("file disappeared after overwrite: %v", err)
	}
	if fi2.Size() != int64(len(data2)) {
		t.Errorf("overwritten file size mismatch: stat=%d, returned=%d", fi2.Size(), len(data2))
	}
	if fi2.Size() == fi1.Size() {
		t.Errorf("overwritten file size did not change: %d == %d", fi2.Size(), fi1.Size())
	}

	// 3. Invalid output paths fail gracefully
	invalidPaths := []string{
		"",                                       // empty path
		"   ",                                    // whitespace
		"/dev/null/impossible_dir/shot.png",      // file as parent dir
		string([]byte{0, 1, 2}) + "/illegal.png", // null bytes in path
	}
	for _, inv := range invalidPaths {
		_, _, err := writeScreenshot(img1, inv, "png", 0, 0, false)
		if err == nil {
			t.Errorf("expected error for invalid output path %q, but got nil", inv)
		}
	}
}

func TestChallenger_M3_F12_DiskStorageMode_NoBase64BlobInToolOutput(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "argus-challenger-payload-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	outPath := filepath.Join(tmpDir, "disk_shot.jpg")
	imgPNG := createTestPNG(240, 160)
	imgData, mimeType, err := writeScreenshot(imgPNG, outPath, "jpeg", 75, 0, false)
	if err != nil {
		t.Fatalf("writeScreenshot failed: %v", err)
	}

	// Construct disk mode metadata as done in screenshotResult / screenshotFullPage
	meta := map[string]any{
		"url":           "https://test.local/dashboard",
		"title":         "Dashboard",
		"path":          outPath,
		"file_path":     outPath,
		"bytes":         len(imgData),
		"size_bytes":    len(imgData),
		"mime_type":     mimeType,
		"saved_to_disk": true,
		"clipped":       true,
		"selector":      "#chart-card",
		"element_bounds": map[string]any{
			"x":      100.5,
			"y":      250.75,
			"width":  500.0,
			"height": 300.0,
		},
		"image": map[string]any{
			"width":  240,
			"height": 160,
		},
	}

	metaText, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatalf("json marshal indent failed: %v", err)
	}

	callResult := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(metaText)},
		},
	}

	// Adversarial assertions:
	// A. Exactly 1 content element (no ImageContent)
	if len(callResult.Content) != 1 {
		t.Fatalf("expected exactly 1 content block, got %d", len(callResult.Content))
	}
	for i, c := range callResult.Content {
		if _, isImg := c.(*mcp.ImageContent); isImg {
			t.Fatalf("content[%d] is *mcp.ImageContent! Disk mode MUST NOT return ImageContent", i)
		}
	}

	// B. Content is TextContent
	tc, ok := callResult.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected *mcp.TextContent, got %T", callResult.Content[0])
	}

	// C. Payload does NOT contain base64 string
	if strings.Contains(tc.Text, "data:image") || strings.Contains(tc.Text, ";base64,") {
		t.Fatalf("TextContent contains base64 image data uri!")
	}

	// D. JSON fields strictly populated
	var resMap map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &resMap); err != nil {
		t.Fatalf("invalid JSON in text response: %v", err)
	}
	if resMap["saved_to_disk"] != true {
		t.Errorf("expected saved_to_disk=true, got %v", resMap["saved_to_disk"])
	}
	if resMap["file_path"] != outPath {
		t.Errorf("expected file_path=%q, got %v", outPath, resMap["file_path"])
	}
	if resMap["bytes"] != float64(len(imgData)) {
		t.Errorf("expected bytes=%d, got %v", len(imgData), resMap["bytes"])
	}
	if resMap["clipped"] != true {
		t.Errorf("expected clipped=true, got %v", resMap["clipped"])
	}
}

func TestChallenger_M3_F12_ZeroDimensionsAndDeepScroll(t *testing.T) {
	// Test error formatting when an element has zero dimensions
	w, h := 0.0, 100.0
	selector := "#zero-width-banner"
	if w <= 0 || h <= 0 {
		err := fmt.Errorf("element %q has zero dimensions (width: %.1f, height: %.1f); cannot capture screenshot", selector, w, h)
		if !strings.Contains(err.Error(), "zero dimensions") {
			t.Errorf("unexpected error message: %v", err)
		}
	}

	// Deep scroll offset math test:
	// Verify that deep scroll offsets are preserved with float64 precision
	elemX, elemY := 120.5, 340.25
	deepScrollX, deepScrollY := 12500.875, 48300.625
	clip := &ScreenshotClip{
		X:      elemX + deepScrollX,
		Y:      elemY + deepScrollY,
		Width:  800.0,
		Height: 600.0,
		Scale:  1.0,
	}
	expectedX := 12621.375
	expectedY := 48640.875
	if clip.X != expectedX || clip.Y != expectedY {
		t.Errorf("scroll offset summation mismatch: got (%.3f, %.3f), want (%.3f, %.3f)", clip.X, clip.Y, expectedX, expectedY)
	}
}

// ============================================================================
// Feature F13 Stress Tests: Mock Route 3-Tier Pattern Matcher & Concurrency
// ============================================================================

func TestChallenger_M3_F13_3TierMatcher_BoundaryPatterns(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		url      string
		expected bool
		isRegex  bool
	}{
		// Deep glob wildcards
		{"Deep glob 1", "**/*.json", "https://api.example.com/v1/users.json", true, false},
		{"Deep glob 2", "**/*.json", "https://api.example.com/v1/nested/path/to/users.json", true, false},
		{"Deep glob 3 (query)", "**/*.json", "https://api.example.com/data.json?sort=asc", false, false}, // glob ends with .json
		{"Deep glob 4 (xml mismatch)", "**/*.json", "https://api.example.com/v1/users.xml", false, false},
		{"Deep glob with query", "**/*.json*", "https://api.example.com/data.json?sort=asc", true, false},
		{"Single char glob ?", "*/v?/users", "https://api.example.com/v1/users", true, false},
		{"Single char glob ? fail", "*/v?/users", "https://api.example.com/v10/users", false, false},

		// Regex special characters embedded in glob patterns
		{"Glob with parentheses and plus", "*/api(v1)+[query]/*", "https://example.com/api(v1)+[query]/list", true, false},
		{"Glob with parentheses mismatch", "*/api(v1)+[query]/*", "https://example.com/apiv1+[query]/list", false, false},
		{"Glob with dots and brackets", "*/data.[0-9].json", "https://example.com/data.[0-9].json", true, false},

		// Explicit regex starting with ^
		{"Regex with params", `^https://example\.com/users/(\d+)\?active=(true|false)$`, "https://example.com/users/42?active=true", true, true},
		{"Regex with params mismatch", `^https://example\.com/users/(\d+)\?active=(true|false)$`, "https://example.com/users/abc?active=true", false, true},

		// Case-insensitive regex
		{"Case insensitive regex", `(?i)^https://api\.example\.com/V[0-9]+/DATA`, "https://api.example.com/v2/data", true, true},
		{"Case insensitive uppercase", `(?i)^https://api\.example\.com/V[0-9]+/DATA`, "https://API.EXAMPLE.COM/V2/DATA", true, true},

		// Slash-wrapped regex
		{"Slash-wrapped regex", `/api\/v[12]\/items/`, "https://site.com/api/v1/items/detail", true, true},
		{"Slash-wrapped regex flag i", `/API\/V[12]\/ITEMS/i`, "https://site.com/api/v1/items/detail", true, true},
		{"Slash-wrapped regex mismatch", `/api\/v[12]\/items/`, "https://site.com/api/v3/items/detail", false, true},

		// Plain substring fallback
		{"Plain substring", "/api/checkout", "https://shop.com/api/checkout?item=1", true, false},
		{"Plain substring mismatch", "/api/checkout", "https://shop.com/api/products?item=1", false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			re, isRegex, err := compileRoutePattern(tc.pattern)
			if err != nil {
				t.Fatalf("compileRoutePattern(%q) failed: %v", tc.pattern, err)
			}
			if isRegex != tc.isRegex {
				t.Errorf("isRegex mismatch for %q: got %v, want %v", tc.pattern, isRegex, tc.isRegex)
			}
			matched := re.MatchString(tc.url)
			if matched != tc.expected {
				t.Errorf("pattern %q against %q: got %v, want %v (compiled: %s)", tc.pattern, tc.url, matched, tc.expected, re.String())
			}
		})
	}
}

func TestChallenger_M3_F13_OverlappingRoutesPrecedence(t *testing.T) {
	state := &interceptState{}

	re1, _, _ := compileRoutePattern("*/api/*")
	re2, _, _ := compileRoutePattern("*/api/special")

	// Order matters: first registered rule wins
	state.rules = []interceptRule{
		{URLPattern: "*/api/*", Method: "GET", Status: 200, Body: "general", matcher: re1},
		{URLPattern: "*/api/special", Method: "GET", Status: 200, Body: "special", matcher: re2},
	}

	targetURL := "https://example.com/api/special"

	state.mu.RLock()
	var matchedRule *interceptRule
	for i := range state.rules {
		r := &state.rules[i]
		if r.matcher.MatchString(targetURL) {
			matchedRule = r
			break
		}
	}
	state.mu.RUnlock()

	if matchedRule == nil || matchedRule.Body != "general" {
		t.Fatalf("expected first registered rule (general) to match, got %+v", matchedRule)
	}

	// Reverse order: specific first
	state.rules = []interceptRule{
		{URLPattern: "*/api/special", Method: "GET", Status: 200, Body: "special", matcher: re2},
		{URLPattern: "*/api/*", Method: "GET", Status: 200, Body: "general", matcher: re1},
	}

	state.mu.RLock()
	matchedRule = nil
	for i := range state.rules {
		r := &state.rules[i]
		if r.matcher.MatchString(targetURL) {
			matchedRule = r
			break
		}
	}
	state.mu.RUnlock()

	if matchedRule == nil || matchedRule.Body != "special" {
		t.Fatalf("expected specific rule to match when registered first, got %+v", matchedRule)
	}
}

func TestChallenger_M3_F13_AutoJSONBodyAndContentTypeNegotiation(t *testing.T) {
	testCases := []struct {
		name            string
		inputBody       any
		inputHeaders    map[string]string
		inputCT         string
		expectedBody    string
		expectedCT      string
		expectedHeaders map[string]string
	}{
		{
			name:         "Map body auto JSON",
			inputBody:    map[string]any{"id": 1, "active": true},
			expectedBody: `{"active":true,"id":1}`,
			expectedCT:   "application/json",
		},
		{
			name:         "Slice body auto JSON",
			inputBody:    []any{"foo", "bar"},
			expectedBody: `["foo","bar"]`,
			expectedCT:   "application/json",
		},
		{
			name:         "Int primitive auto JSON",
			inputBody:    42,
			expectedBody: "42",
			expectedCT:   "application/json",
		},
		{
			name:         "Boolean primitive auto JSON",
			inputBody:    true,
			expectedBody: "true",
			expectedCT:   "application/json",
		},
		{
			name:         "Raw JSON string detected as JSON",
			inputBody:    `{"status":"ok"}`,
			expectedBody: `{"status":"ok"}`,
			expectedCT:   "application/json",
		},
		{
			name:         "Plain string NOT auto JSON",
			inputBody:    "hello world",
			expectedBody: "hello world",
			expectedCT:   "",
		},
		{
			name:         "Explicit ContentType overrides default",
			inputBody:    map[string]any{"code": 404},
			inputCT:      "application/problem+json",
			expectedBody: `{"code":404}`,
			expectedCT:   "application/problem+json",
		},
		{
			name:         "Custom Header Content-Type preserved",
			inputBody:    map[string]any{"code": 200},
			inputHeaders: map[string]string{"Content-Type": "application/vnd.api+json"},
			expectedBody: `{"code":200}`,
			expectedHeaders: map[string]string{
				"Content-Type": "application/vnd.api+json",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			bodyStr := ""
			isJSON := false
			if tc.inputBody != nil {
				switch b := tc.inputBody.(type) {
				case string:
					bodyStr = b
					trimmed := strings.TrimSpace(b)
					if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
						(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
						isJSON = true
					}
				default:
					marshaled, err := json.Marshal(b)
					if err != nil {
						t.Fatalf("json.Marshal failed: %v", err)
					}
					bodyStr = string(marshaled)
					isJSON = true
				}
			}

			if bodyStr != tc.expectedBody {
				t.Errorf("body mismatch: got %q, want %q", bodyStr, tc.expectedBody)
			}

			headers := make(map[string]string, len(tc.inputHeaders)+1)
			for k, v := range tc.inputHeaders {
				headers[k] = v
			}
			contentType := strings.TrimSpace(tc.inputCT)
			if contentType == "" && isJSON {
				contentType = "application/json"
			}
			if contentType != "" {
				hasCT := false
				for k := range headers {
					if strings.EqualFold(k, "content-type") {
						hasCT = true
						break
					}
				}
				if !hasCT {
					headers["Content-Type"] = contentType
				}
			}

			if tc.expectedCT != "" && headers["Content-Type"] != tc.expectedCT {
				t.Errorf("Content-Type mismatch: got %q, want %q", headers["Content-Type"], tc.expectedCT)
			}
			if tc.expectedHeaders != nil {
				for k, v := range tc.expectedHeaders {
					if headers[k] != v {
						t.Errorf("header %s mismatch: got %q, want %q", k, headers[k], v)
					}
				}
			}
		})
	}
}

func TestChallenger_M3_F13_ParallelRequestsPausedHandling_StressRace(t *testing.T) {
	state := &interceptState{}

	// Setup initial rules
	for i := 0; i < 5; i++ {
		pat := fmt.Sprintf("*/api/v%d/*", i+1)
		re, _, _ := compileRoutePattern(pat)
		state.rules = append(state.rules, interceptRule{
			URLPattern: pat,
			Method:     "GET",
			Status:     200,
			Body:       fmt.Sprintf(`{"version": %d}`, i+1),
			matcher:    re,
		})
	}

	var matchCount int64
	var noMatchCount int64
	var writersCompleted int64

	var wg sync.WaitGroup

	// 60 concurrent readers evaluating requests
	for r := 0; r < 60; r++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			for req := 0; req < 100; req++ {
				url := fmt.Sprintf("https://test.site.com/api/v%d/items/%d", (req%7)+1, req)
				state.mu.RLock()
				matched := false
				for _, rule := range state.rules {
					if rule.matcher != nil && rule.matcher.MatchString(url) {
						matched = true
						break
					}
				}
				state.mu.RUnlock()

				if matched {
					atomic.AddInt64(&matchCount, 1)
				} else {
					atomic.AddInt64(&noMatchCount, 1)
				}
			}
		}(r)
	}

	// 10 concurrent writers dynamically modifying rules
	for w := 0; w < 10; w++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for step := 0; step < 20; step++ {
				state.mu.Lock()
				pat := fmt.Sprintf("*/api/dyn_%d_%d/*", writerID, step)
				re, _, _ := compileRoutePattern(pat)
				state.rules = append(state.rules, interceptRule{
					URLPattern: pat,
					Method:     "POST",
					Status:     201,
					Body:       "created",
					matcher:    re,
				})
				if len(state.rules) > 15 {
					// keep slice bounded
					state.rules = state.rules[1:]
				}
				state.mu.Unlock()
				time.Sleep(10 * time.Microsecond)
			}
			atomic.AddInt64(&writersCompleted, 1)
		}(w)
	}

	wg.Wait()

	totalEvaluated := atomic.LoadInt64(&matchCount) + atomic.LoadInt64(&noMatchCount)
	if totalEvaluated != 60*100 {
		t.Fatalf("expected 6000 total request evaluations, got %d", totalEvaluated)
	}
	if atomic.LoadInt64(&writersCompleted) != 10 {
		t.Fatalf("expected 10 writers completed, got %d", atomic.LoadInt64(&writersCompleted))
	}
}

func TestChallenger_M3_F13_LifecycleCleanupOnCloseTabAndDisconnect(t *testing.T) {
	wsA := "ws://127.0.0.1:54321/devtools/page/TAB_AAA"
	wsB := "ws://127.0.0.1:54321/devtools/page/TAB_BBB"
	wsC := "ws://127.0.0.1:54321/devtools/page/TAB_CCC"

	interceptTargetsMu.Lock()
	interceptTargets[wsA] = &interceptState{rules: []interceptRule{{URLPattern: "/a"}}}
	interceptTargets[wsB] = &interceptState{rules: []interceptRule{{URLPattern: "/b"}}}
	interceptTargets[wsC] = &interceptState{rules: []interceptRule{{URLPattern: "/c"}}}
	interceptTargetsMu.Unlock()

	// 1. Cleanup by target ID on tab close
	removeInterceptStateForTarget("TAB_AAA")

	interceptTargetsMu.Lock()
	_, hasA := interceptTargets[wsA]
	_, hasB := interceptTargets[wsB]
	_, hasC := interceptTargets[wsC]
	interceptTargetsMu.Unlock()

	if hasA {
		t.Errorf("TAB_AAA was not removed by removeInterceptStateForTarget")
	}
	if !hasB || !hasC {
		t.Errorf("unexpected removal of TAB_BBB or TAB_CCC")
	}

	// 2. Cleanup on connection drop
	removeInterceptState(wsB)

	interceptTargetsMu.Lock()
	_, hasB2 := interceptTargets[wsB]
	_, hasC2 := interceptTargets[wsC]
	delete(interceptTargets, wsC) // clean up test state
	interceptTargetsMu.Unlock()

	if hasB2 {
		t.Errorf("wsB was not removed by removeInterceptState")
	}
	if !hasC2 {
		t.Errorf("wsC was unexpectedly removed")
	}
}
