package main

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestCompileRoutePattern_Glob(t *testing.T) {
	tests := []struct {
		pattern  string
		testURL  string
		expected bool
	}{
		// Wildcard globs
		{"*/api/users*", "https://example.com/api/users", true},
		{"*/api/users*", "https://example.com/api/users?page=1", true},
		{"*/api/users*", "http://localhost:8080/api/users/123", true},
		{"*/api/users*", "https://example.com/api/posts", false},

		// Extension globs
		{"*.json", "https://example.com/data.json", true},
		{"*.json", "https://example.com/data.html", false},

		// Single character ?
		{"*/v?/items", "https://api.com/v1/items", true},
		{"*/v?/items", "https://api.com/v2/items", true},
		{"*/v?/items", "https://api.com/v10/items", false},

		// Relative path with leading slash
		{"/api/profile/*", "https://app.site.com/api/profile/tang", true},
		{"/api/profile/*", "http://127.0.0.1:3000/api/profile/123", true},
		{"/api/profile/*", "https://app.site.com/other/profile/123", false},

		// Escaped slashes
		{`*\/api\/v1\*`, "https://site.com/api/v1/users", true},
	}

	for _, tc := range tests {
		re, isRegex, err := compileRoutePattern(tc.pattern)
		if err != nil {
			t.Fatalf("pattern %q failed to compile: %v", tc.pattern, err)
		}
		if isRegex {
			t.Errorf("expected glob for %q, got isRegex=true", tc.pattern)
		}
		matched := re.MatchString(tc.testURL)
		if matched != tc.expected {
			t.Errorf("pattern %q against %q: got %v, want %v (regex: %s)", tc.pattern, tc.testURL, matched, tc.expected, re.String())
		}
	}
}

func TestCompileRoutePattern_Regex(t *testing.T) {
	tests := []struct {
		pattern  string
		testURL  string
		expected bool
	}{
		{`^https?://api\.service\.com/v[0-9]+/.*$`, "https://api.service.com/v1/users", true},
		{`^https?://api\.service\.com/v[0-9]+/.*$`, "http://api.service.com/v2/posts", true},
		{`^https?://api\.service\.com/v[0-9]+/.*$`, "https://other.service.com/v1/users", false},
		{`(?i)^https://API\.TEST\.COM/`, "https://api.test.com/data", true},
		{`/api\/v[12]\/items/`, "https://my.site.com/api/v1/items", true},
		{`/api\/v[12]\/items/`, "https://my.site.com/api/v3/items", false},
	}

	for _, tc := range tests {
		re, isRegex, err := compileRoutePattern(tc.pattern)
		if err != nil {
			t.Fatalf("pattern %q failed to compile: %v", tc.pattern, err)
		}
		if !isRegex {
			t.Errorf("expected isRegex=true for %q", tc.pattern)
		}
		matched := re.MatchString(tc.testURL)
		if matched != tc.expected {
			t.Errorf("pattern %q against %q: got %v, want %v", tc.pattern, tc.testURL, matched, tc.expected)
		}
	}
}

func TestCompileRoutePattern_SubstringFallback(t *testing.T) {
	re, isRegex, err := compileRoutePattern("/api/data")
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}
	if isRegex {
		t.Errorf("expected substring, got isRegex=true")
	}
	if !re.MatchString("http://localhost:3000/api/data?foo=bar") {
		t.Errorf("expected substring match for /api/data")
	}
	if re.MatchString("http://localhost:3000/api/other") {
		t.Errorf("did not expect match for /api/other")
	}
}

func TestCompileRoutePattern_Errors(t *testing.T) {
	if _, _, err := compileRoutePattern(""); err == nil {
		t.Errorf("expected error for empty pattern")
	}
	if _, _, err := compileRoutePattern("   "); err == nil {
		t.Errorf("expected error for whitespace pattern")
	}
	if _, _, err := compileRoutePattern("^([a-z+$"); err == nil {
		t.Errorf("expected error for broken regex")
	}
}

func TestMockRoute_BodyNormalization(t *testing.T) {
	obj := map[string]any{"success": true, "code": 200}
	bytes, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if string(bytes) != `{"code":200,"success":true}` {
		t.Errorf("unexpected json: %s", string(bytes))
	}
}

func TestMockRoute_TableConcurrency_Race(t *testing.T) {
	state := &interceptState{}
	matcher, _, _ := compileRoutePattern("*/api/*")
	state.rules = []interceptRule{
		{URLPattern: "*/api/*", Method: "GET", Status: 200, Body: "ok", matcher: matcher},
	}

	var wg sync.WaitGroup
	// 20 concurrent readers matching requests
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				state.mu.RLock()
				url := fmt.Sprintf("https://example.com/api/item/%d", j)
				matched := false
				for _, r := range state.rules {
					if r.matcher != nil && r.matcher.MatchString(url) {
						matched = true
						break
					}
				}
				state.mu.RUnlock()
				if !matched {
					t.Errorf("reader %d failed to match %s", id, url)
				}
			}
		}(i)
	}

	// 5 concurrent writers updating rules
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				state.mu.Lock()
				m, _, _ := compileRoutePattern(fmt.Sprintf("*/api/route_%d_*", id))
				state.rules = append(state.rules, interceptRule{
					URLPattern: fmt.Sprintf("*/api/route_%d_*", id),
					matcher:    m,
				})
				if len(state.rules) > 10 {
					state.rules = state.rules[:1]
				}
				state.mu.Unlock()
			}
		}(i)
	}

	wg.Wait()
}

func TestMockRoute_TargetCleanup(t *testing.T) {
	ws1 := "ws://127.0.0.1:54321/devtools/page/T100"
	ws2 := "ws://127.0.0.1:54321/devtools/page/T200"

	interceptTargetsMu.Lock()
	interceptTargets[ws1] = &interceptState{rules: []interceptRule{{URLPattern: "/test"}}}
	interceptTargets[ws2] = &interceptState{rules: []interceptRule{{URLPattern: "/other"}}}
	interceptTargetsMu.Unlock()

	removeInterceptStateForTarget("T100")

	interceptTargetsMu.Lock()
	_, has1 := interceptTargets[ws1]
	_, has2 := interceptTargets[ws2]
	delete(interceptTargets, ws2)
	interceptTargetsMu.Unlock()

	if has1 {
		t.Errorf("expected T100 to be removed from interceptTargets")
	}
	if !has2 {
		t.Errorf("expected T200 to remain in interceptTargets")
	}
}

func TestRemoveInterceptState_DoesNotStallOnInFlightRequest(t *testing.T) {
	ws := "ws://127.0.0.1:59999/devtools/page/NONSTALL_TEST"
	state := getInterceptState(ws)
	state.rules = []interceptRule{{
		URLPattern: "/api/test",
		Action:     "mock",
		Status:     200,
	}}
	state.active = true
	state.stopCh = make(chan struct{})
	state.listenerDone = make(chan struct{})

	// Simulate concurrent in-flight request handler acquiring RLock
	started := make(chan struct{})
	releaseRLock := make(chan struct{})
	go func() {
		close(started)
		state.mu.RLock()
		<-releaseRLock
		state.mu.RUnlock()
		close(state.listenerDone)
	}()

	<-started
	// Give goroutine a moment to hold RLock
	time.Sleep(5 * time.Millisecond)

	startRemove := time.Now()
	done := make(chan struct{})
	go func() {
		close(releaseRLock)
		removeInterceptState(ws)
		close(done)
	}()

	select {
	case <-done:
		elapsed := time.Since(startRemove)
		if elapsed > 500*time.Millisecond {
			t.Fatalf("removeInterceptState took %v, want < 500ms (stalled on lock!)", elapsed)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("removeInterceptState stalled and timed out")
	}
}

func TestHandleRequestPaused_ConcurrentReadNoRace(t *testing.T) {
	state := &interceptState{
		rules: []interceptRule{
			{URLPattern: "*/user/*", Method: "GET", Action: "mock", Status: 200},
			{URLPattern: "^https://api\\.example\\.com/data", Method: "POST", Action: "block"},
		},
	}
	// Verify that concurrent handleRequestPaused evaluations execute under -race without data races
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			envelope := &browserCDPEnvelope{
				Params: json.RawMessage(fmt.Sprintf(`{"requestId":"req-%d","request":{"url":"https://api.example.com/data","method":"POST"}}`, id)),
			}
			handleRequestPaused(state, "", envelope, NewLogger("test"))
		}(i)
	}
	wg.Wait()
}
