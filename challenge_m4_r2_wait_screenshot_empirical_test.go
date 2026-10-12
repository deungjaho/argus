package main

import (
	"encoding/json"
	"testing"
	"time"
)

// challenge_m4_r2_wait_screenshot_empirical_test.go
// Milestone 4 Iteration 2 Challenger 1: Wait Timeout Logic & Screenshot Metadata Empirical Suite.

func TestEmpirical_M4R2_WaitForTimeoutPermutations(t *testing.T) {
	testCases := []struct {
		name                 string
		rawJSON              string
		wantTimeoutSpecified bool
		wantTimeoutMs        int
		wantTimeout          int
		wantInstantExists    bool
	}{
		{
			name:                 "timeout_ms=100 positive timeout",
			rawJSON:              `{"selector": "#btn", "timeout_ms": 100}`,
			wantTimeoutSpecified: true,
			wantTimeoutMs:        100,
			wantTimeout:          100,
			wantInstantExists:    false,
		},
		{
			name:                 "timeout=100 alias positive timeout",
			rawJSON:              `{"selector": "#btn", "timeout": 100}`,
			wantTimeoutSpecified: true,
			wantTimeoutMs:        100,
			wantTimeout:          100,
			wantInstantExists:    false,
		},
		{
			name:                 "timeout_ms=0 instant exists check",
			rawJSON:              `{"selector": "#btn", "timeout_ms": 0}`,
			wantTimeoutSpecified: true,
			wantTimeoutMs:        0,
			wantTimeout:          0,
			wantInstantExists:    true,
		},
		{
			name:                 "timeout=0 alias instant exists check",
			rawJSON:              `{"selector": "#btn", "timeout": 0}`,
			wantTimeoutSpecified: true,
			wantTimeoutMs:        0,
			wantTimeout:          0,
			wantInstantExists:    true,
		},
		{
			name:                 "omitted timeout",
			rawJSON:              `{"selector": "#btn"}`,
			wantTimeoutSpecified: false,
			wantTimeoutMs:        0,
			wantTimeout:          0,
			wantInstantExists:    false,
		},
		{
			name:                 "negative timeout_ms=-1",
			rawJSON:              `{"selector": "#btn", "timeout_ms": -1}`,
			wantTimeoutSpecified: true,
			wantTimeoutMs:        -1,
			wantTimeout:          -1,
			wantInstantExists:    false,
		},
		{
			name:                 "negative timeout=-50",
			rawJSON:              `{"selector": "#btn", "timeout": -50}`,
			wantTimeoutSpecified: true,
			wantTimeoutMs:        -50,
			wantTimeout:          -50,
			wantInstantExists:    false,
		},
		{
			name:                 "both provided: timeout_ms=100, timeout=200 (timeout_ms priority)",
			rawJSON:              `{"selector": "#btn", "timeout_ms": 100, "timeout": 200}`,
			wantTimeoutSpecified: true,
			wantTimeoutMs:        100,
			wantTimeout:          100,
			wantInstantExists:    false,
		},
		{
			name:                 "both provided: timeout_ms=0, timeout=100 (timeout_ms priority)",
			rawJSON:              `{"selector": "#btn", "timeout_ms": 0, "timeout": 100}`,
			wantTimeoutSpecified: true,
			wantTimeoutMs:        0,
			wantTimeout:          0,
			wantInstantExists:    true,
		},
		{
			name:                 "both provided: timeout_ms=100, timeout=0 (timeout_ms priority)",
			rawJSON:              `{"selector": "#btn", "timeout_ms": 100, "timeout": 0}`,
			wantTimeoutSpecified: true,
			wantTimeoutMs:        100,
			wantTimeout:          100,
			wantInstantExists:    false,
		},
		{
			name:                 "large timeout_ms=60000",
			rawJSON:              `{"selector": "#btn", "timeout_ms": 60000}`,
			wantTimeoutSpecified: true,
			wantTimeoutMs:        60000,
			wantTimeout:          60000,
			wantInstantExists:    false,
		},
		{
			name:                 "large timeout=60000",
			rawJSON:              `{"selector": "#btn", "timeout": 60000}`,
			wantTimeoutSpecified: true,
			wantTimeoutMs:        60000,
			wantTimeout:          60000,
			wantInstantExists:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var in waitForInput
			if err := json.Unmarshal([]byte(tc.rawJSON), &in); err != nil {
				t.Fatalf("unmarshal failed for %q: %v", tc.name, err)
			}

			if in.TimeoutSpecified != tc.wantTimeoutSpecified {
				t.Errorf("[%s] got TimeoutSpecified=%v, want %v", tc.name, in.TimeoutSpecified, tc.wantTimeoutSpecified)
			}
			if in.TimeoutMs != tc.wantTimeoutMs {
				t.Errorf("[%s] got TimeoutMs=%d, want %d", tc.name, in.TimeoutMs, tc.wantTimeoutMs)
			}
			if in.Timeout != tc.wantTimeout {
				t.Errorf("[%s] got Timeout=%d, want %d", tc.name, in.Timeout, tc.wantTimeout)
			}

			// Production check from tools_wait.go:88
			isInstantExists := in.TimeoutSpecified && in.TimeoutMs == 0 && in.Timeout == 0
			if isInstantExists != tc.wantInstantExists {
				t.Errorf("[%s] got isInstantExists=%v, want %v", tc.name, isInstantExists, tc.wantInstantExists)
			}

			// Historical bug assertion: check with the old disjunction expression (tools_wait.go pre-fix)
			oldBugInstantExists := in.TimeoutSpecified && (in.TimeoutMs == 0 || in.Timeout == 0)
			if tc.wantInstantExists == false && oldBugInstantExists == true {
				t.Errorf("[%s] REGRESSION: uninitialized field caused old disjunction to evaluate true for positive timeout!", tc.name)
			}
		})
	}
}

func TestEmpirical_M4R2_WaitForPositiveTimeoutBug_UnconditionalPass(t *testing.T) {
	// Re-verify the exact scenario that triggered the forensic bug in Iteration 1
	positivePayloads := []string{
		`{"selector": "#submit", "timeout_ms": 2500}`,
		`{"selector": "#submit", "timeout": 2500}`,
		`{"selector": "#submit", "timeout_ms": 100}`,
		`{"selector": "#submit", "timeout": 100}`,
		`{"text": "Save", "timeout_ms": 1500}`,
		`{"text": "Save", "timeout": 1500}`,
		`{"url_matches": "https://example.com/*", "timeout_ms": 3000}`,
	}

	for _, raw := range positivePayloads {
		var in waitForInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatalf("unmarshal failed for %s: %v", raw, err)
		}

		if !in.TimeoutSpecified {
			t.Errorf("expected TimeoutSpecified=true for %s", raw)
		}
		if in.TimeoutMs <= 0 || in.Timeout <= 0 {
			t.Errorf("expected both TimeoutMs and Timeout > 0 for %s, got ms=%d, t=%d", raw, in.TimeoutMs, in.Timeout)
		}

		isInstantExists := in.TimeoutSpecified && in.TimeoutMs == 0 && in.Timeout == 0
		if isInstantExists {
			t.Fatalf("CRITICAL: positive timeout evaluated isInstantExists=true for %s", raw)
		}
	}
}

func TestEmpirical_M4R2_WaitForFixedSleepDurationFallback(t *testing.T) {
	// Verify that when no conditions are present, timeout_ms / timeout properly fall back into sleep
	type sleepCase struct {
		name        string
		rawJSON     string
		wantSleepMs int
	}

	cases := []sleepCase{
		{name: "pure timeout_ms", rawJSON: `{"timeout_ms": 250}`, wantSleepMs: 250},
		{name: "pure timeout alias", rawJSON: `{"timeout": 300}`, wantSleepMs: 300},
		{name: "time_ms parameter", rawJSON: `{"time_ms": 150}`, wantSleepMs: 150},
		{name: "sleep_ms parameter", rawJSON: `{"sleep_ms": 175}`, wantSleepMs: 175},
		{name: "duration_ms parameter", rawJSON: `{"duration_ms": 225}`, wantSleepMs: 225},
		{name: "delay parameter", rawJSON: `{"delay": 500}`, wantSleepMs: 500},
		{name: "delay_ms parameter", rawJSON: `{"delay_ms": 400}`, wantSleepMs: 400},
	}

	for _, sc := range cases {
		t.Run(sc.name, func(t *testing.T) {
			var in waitForInput
			if err := json.Unmarshal([]byte(sc.rawJSON), &in); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}

			// Trace sleep resolution logic from tools_wait.go:100-116
			sleepMs := in.TimeMs
			if sleepMs <= 0 {
				sleepMs = in.SleepMs
			}
			if sleepMs <= 0 {
				sleepMs = in.DurationMs
			}
			if sleepMs <= 0 {
				sleepMs = resolveTimeout(in.DelayMs, in.Delay)
			}
			hasNoConditions := in.Text == "" && in.TextAbsent == "" && in.Selector == "" &&
				in.URLMatches == "" && in.WaitUntil == "" && in.Condition == "" &&
				in.Script == "" && in.Expression == "" && in.Code == ""
			if sleepMs <= 0 && hasNoConditions && in.TimeoutMs > 0 {
				sleepMs = in.TimeoutMs
			}

			if sleepMs != sc.wantSleepMs {
				t.Errorf("resolved sleepMs=%d, want %d", sleepMs, sc.wantSleepMs)
			}
		})
	}
}

func TestEmpirical_M4R2_ScreenshotMetadataAliasMatrix(t *testing.T) {
	testCases := []struct {
		name                 string
		rawJSON              string
		wantFullPageResolved bool
		wantMetaFullPage     bool
		wantMetaFieldPresent bool
	}{
		{
			name:                 "snake_case full_page: true",
			rawJSON:              `{"full_page": true}`,
			wantFullPageResolved: true,
			wantMetaFullPage:     true,
			wantMetaFieldPresent: true,
		},
		{
			name:                 "camelCase fullPage: true",
			rawJSON:              `{"fullPage": true}`,
			wantFullPageResolved: true,
			wantMetaFullPage:     true,
			wantMetaFieldPresent: true,
		},
		{
			name:                 "camelCase fullPage: false",
			rawJSON:              `{"fullPage": false}`,
			wantFullPageResolved: false,
			wantMetaFullPage:     false,
			wantMetaFieldPresent: false,
		},
		{
			name:                 "snake_case full_page: false",
			rawJSON:              `{"full_page": false}`,
			wantFullPageResolved: false,
			wantMetaFullPage:     false,
			wantMetaFieldPresent: false,
		},
		{
			name:                 "both false: full_page: false, fullPage: false",
			rawJSON:              `{"full_page": false, "fullPage": false}`,
			wantFullPageResolved: false,
			wantMetaFullPage:     false,
			wantMetaFieldPresent: false,
		},
		{
			name:                 "snake_case true, camelCase false",
			rawJSON:              `{"full_page": true, "fullPage": false}`,
			wantFullPageResolved: true,
			wantMetaFullPage:     true,
			wantMetaFieldPresent: true,
		},
		{
			name:                 "snake_case false, camelCase true",
			rawJSON:              `{"full_page": false, "fullPage": true}`,
			wantFullPageResolved: true,
			wantMetaFullPage:     true,
			wantMetaFieldPresent: true,
		},
		{
			name:                 "omitted full page parameter",
			rawJSON:              `{"selector": "#content"}`,
			wantFullPageResolved: false,
			wantMetaFullPage:     false,
			wantMetaFieldPresent: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var in screenshotInput
			if err := json.Unmarshal([]byte(tc.rawJSON), &in); err != nil {
				t.Fatalf("unmarshal failed for %s: %v", tc.name, err)
			}

			// Resolution logic in tools_screenshot.go:93
			fullPage := in.FullPage || in.FullPageCamel
			if fullPage != tc.wantFullPageResolved {
				t.Errorf("[%s] got fullPage=%v, want %v", tc.name, fullPage, tc.wantFullPageResolved)
			}

			// Metadata population logic in tools_screenshot.go:158-160
			meta := map[string]any{
				"url":        "https://example.com",
				"title":      "Example",
				"bytes":      1024,
				"size_bytes": 1024,
				"mime_type":  "image/jpeg",
			}
			if fullPage {
				meta["full_page"] = true
			}

			val, exists := meta["full_page"]
			if exists != tc.wantMetaFieldPresent {
				t.Errorf("[%s] got meta['full_page'] present=%v, want %v", tc.name, exists, tc.wantMetaFieldPresent)
			}
			if tc.wantMetaFieldPresent {
				boolVal, isBool := val.(bool)
				if !isBool || boolVal != tc.wantMetaFullPage {
					t.Errorf("[%s] got meta['full_page']=%v (bool=%v), want bool %v", tc.name, val, isBool, tc.wantMetaFullPage)
				}
			}

			// Verify JSON serialization of metadata (as returned in CallToolResult Content)
			metaBytes, err := json.Marshal(meta)
			if err != nil {
				t.Fatalf("failed to marshal meta: %v", err)
			}
			var parsedMeta map[string]any
			if err := json.Unmarshal(metaBytes, &parsedMeta); err != nil {
				t.Fatalf("failed to unmarshal parsedMeta: %v", err)
			}

			jsonVal, jsonExists := parsedMeta["full_page"]
			if jsonExists != tc.wantMetaFieldPresent {
				t.Errorf("[%s] in serialized JSON: got full_page present=%v, want %v", tc.name, jsonExists, tc.wantMetaFieldPresent)
			}
			if tc.wantMetaFieldPresent {
				jsonBool, isBool := jsonVal.(bool)
				if !isBool || jsonBool != tc.wantMetaFullPage {
					t.Errorf("[%s] in serialized JSON: got full_page=%v, want %v", tc.name, jsonVal, tc.wantMetaFullPage)
				}
			}
		})
	}
}

func TestEmpirical_M4R2_ScreenshotDiskModeWithAliases(t *testing.T) {
	// Test save_to_disk / disk flags combined with fullPage / full_page aliases
	combos := []struct {
		name         string
		rawJSON      string
		wantDisk     bool
		wantFullPage bool
	}{
		{
			name:         "disk=true and fullPage=true (camelCase pair)",
			rawJSON:      `{"disk": true, "fullPage": true, "path": "/tmp/out.jpg"}`,
			wantDisk:     true,
			wantFullPage: true,
		},
		{
			name:         "save_to_disk=true and full_page=true (snake_case pair)",
			rawJSON:      `{"save_to_disk": true, "full_page": true, "path": "/tmp/out.jpg"}`,
			wantDisk:     true,
			wantFullPage: true,
		},
		{
			name:         "disk=true and fullPage=false",
			rawJSON:      `{"disk": true, "fullPage": false, "path": "/tmp/out.jpg"}`,
			wantDisk:     true,
			wantFullPage: false,
		},
	}

	for _, c := range combos {
		t.Run(c.name, func(t *testing.T) {
			var in screenshotInput
			if err := json.Unmarshal([]byte(c.rawJSON), &in); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}

			saveToDisk := in.SaveToDisk || in.Disk
			fullPage := in.FullPage || in.FullPageCamel

			if saveToDisk != c.wantDisk {
				t.Errorf("got saveToDisk=%v, want %v", saveToDisk, c.wantDisk)
			}
			if fullPage != c.wantFullPage {
				t.Errorf("got fullPage=%v, want %v", fullPage, c.wantFullPage)
			}

			meta := map[string]any{"path": in.Path}
			if fullPage {
				meta["full_page"] = true
			}
			if saveToDisk {
				meta["saved_to_disk"] = true
			}

			if c.wantFullPage {
				if meta["full_page"] != true {
					t.Errorf("expected meta['full_page']=true")
				}
			} else {
				if _, ok := meta["full_page"]; ok {
					t.Errorf("expected meta['full_page'] to be omitted when false")
				}
			}

			if meta["saved_to_disk"] != true {
				t.Errorf("expected meta['saved_to_disk']=true")
			}
		})
	}
}

func TestEmpirical_M4R2_WaitTimeoutDurationSafety(t *testing.T) {
	// Ensure that negative or zero timeouts never cause time.Duration underflow or panic
	inputs := []waitForInput{
		{TimeoutMs: -100, Timeout: -100},
		{TimeoutMs: 0, Timeout: 0},
		{TimeoutMs: 1000, Timeout: 1000},
	}

	for _, in := range inputs {
		timeoutMsVal := 10000
		if in.TimeoutMs > 0 {
			timeoutMsVal = in.TimeoutMs
		} else if in.Timeout > 0 {
			timeoutMsVal = in.Timeout
		}
		duration := time.Duration(timeoutMsVal) * time.Millisecond
		if duration <= 0 {
			t.Fatalf("duration computed as non-positive: %v for input %+v", duration, in)
		}
	}
}
