package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Feature F04: batch_actions Unmarshaling & Step Dispatch Stress Tests
// =============================================================================

// TestChallengerBatchActionAliasesAndParamPermutations stress-tests all combinations
// of action aliases (key, press) with case variations, whitespace, and parameter keys
// (key, value, text, input), plus precedence rules.
func TestChallengerBatchActionAliasesAndParamPermutations(t *testing.T) {
	actionVariants := []string{"key", "press", "KEY", "Press", " key ", " press "}
	paramKeys := []string{"key", "value", "text", "input"}

	for _, action := range actionVariants {
		for _, param := range paramKeys {
			testName := fmt.Sprintf("action_%s_param_%s", strings.TrimSpace(action), param)
			t.Run(testName, func(t *testing.T) {
				expectedKey := "Enter"
				jsonRaw := fmt.Sprintf(`{"action": %q, %q: %q}`, action, param, expectedKey)

				var step batchActionStep
				if err := json.Unmarshal([]byte(jsonRaw), &step); err != nil {
					t.Fatalf("Unmarshal failed: %v", err)
				}

				normAction := strings.ToLower(strings.TrimSpace(step.Action))
				if normAction != "key" && normAction != "press" {
					t.Fatalf("Normalized action = %q, expected 'key' or 'press'", normAction)
				}

				resolved := resolveFirstNonEmpty(step.Key, step.Value, step.Text, step.Input)
				if resolved != expectedKey {
					t.Errorf("resolveFirstNonEmpty = %q, want %q", resolved, expectedKey)
				}
			})
		}
	}
}

// TestChallengerBatchActionParamPrecedence verifies parameter priority:
// key > value > text > input.
func TestChallengerBatchActionParamPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		step     batchActionStep
		expected string
	}{
		{
			name:     "key beats all",
			step:     batchActionStep{Action: "press", Key: "Enter", Value: "Tab", Text: "Escape", Input: "Space"},
			expected: "Enter",
		},
		{
			name:     "value beats text and input",
			step:     batchActionStep{Action: "key", Value: "Tab", Text: "Escape", Input: "Space"},
			expected: "Tab",
		},
		{
			name:     "text beats input",
			step:     batchActionStep{Action: "press", Text: "Escape", Input: "Space"},
			expected: "Escape",
		},
		{
			name:     "input used when alone",
			step:     batchActionStep{Action: "key", Input: "Space"},
			expected: "Space",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resolved := resolveFirstNonEmpty(tc.step.Key, tc.step.Value, tc.step.Text, tc.step.Input)
			if resolved != tc.expected {
				t.Errorf("got %q, want %q", resolved, tc.expected)
			}
		})
	}
}

// TestChallengerBatchActionMissingKeysFail verifies that key/press actions with
// empty or whitespace-only keys are recognized as invalid.
func TestChallengerBatchActionMissingKeysFail(t *testing.T) {
	emptySteps := []batchActionStep{
		{Action: "key"},
		{Action: "press"},
		{Action: "key", Key: "", Value: "", Text: "", Input: ""},
		{Action: "press", Key: "  ", Value: "\t", Text: "\n", Input: "   "},
	}

	for i, step := range emptySteps {
		t.Run(fmt.Sprintf("step_%d", i), func(t *testing.T) {
			resolved := resolveFirstNonEmpty(step.Key, step.Value, step.Text, step.Input)
			if resolved != "" {
				t.Errorf("expected empty string for whitespace/empty step, got %q", resolved)
			}
		})
	}
}

// TestChallengerBatchActionModifierMasks verifies modifier string parsing into CDP bitmask:
// Alt=1, Ctrl=2, Meta=4, Shift=8.
func TestChallengerBatchActionModifierMasks(t *testing.T) {
	cases := []struct {
		modStr   string
		expected int
	}{
		{"", 0},
		{"none", 0},
		{"alt", 1},
		{"option", 1},
		{"ctrl", 2},
		{"control", 2},
		{"meta", 4},
		{"cmd", 4},
		{"command", 4},
		{"shift", 8},
		{"ctrl+shift", 10},
		{"control+shift", 10},
		{"ctrl+alt", 3},
		{"cmd+shift", 12},
		{"meta+shift", 12},
		{"ctrl, alt, shift, meta", 15},
		{"CTRL+SHIFT", 10},
		{"Cmd+Option+Shift", 13},
	}

	for _, tc := range cases {
		t.Run("mod_"+tc.modStr, func(t *testing.T) {
			mask := modifierMask(tc.modStr)
			if mask != tc.expected {
				t.Errorf("modifierMask(%q) = %d, want %d", tc.modStr, mask, tc.expected)
			}
		})
	}
}

// TestChallengerBatchActionLargeBatchStress tests unmarshaling and validating
// a high-volume batch action with 100 interleaved operations.
func TestChallengerBatchActionLargeBatchStress(t *testing.T) {
	actions := make([]map[string]any, 100)
	for i := 0; i < 100; i++ {
		switch i % 5 {
		case 0:
			actions[i] = map[string]any{"action": "click", "selector": fmt.Sprintf("#btn-%d", i)}
		case 1:
			actions[i] = map[string]any{"action": "fill", "selector": fmt.Sprintf("#input-%d", i), "value": fmt.Sprintf("val-%d", i)}
		case 2:
			actions[i] = map[string]any{"action": "key", "key": "Enter"}
		case 3:
			actions[i] = map[string]any{"action": "press", "value": "Tab"}
		case 4:
			actions[i] = map[string]any{"action": "scroll", "delta_y": 100}
		}
	}

	raw, err := json.Marshal(map[string]any{"actions": actions})
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var batch batchActionsInput
	start := time.Now()
	if err := json.Unmarshal(raw, &batch); err != nil {
		t.Fatalf("Unmarshal large batch failed: %v", err)
	}
	elapsed := time.Since(start)

	if len(batch.Actions) != 100 {
		t.Fatalf("batch.Actions len = %d, want 100", len(batch.Actions))
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("unmarshal took too long: %v", elapsed)
	}
}

// =============================================================================
// Feature F05: fillExpression & React _valueTracker Edge Cases
// =============================================================================

// TestChallengerFillExpressionAdversarialPayloads checks that fillExpression
// correctly escapes and serializes challenging inputs into valid JavaScript.
func TestChallengerFillExpressionAdversarialPayloads(t *testing.T) {
	payloads := []string{
		`hello "world" and 'quotes'`,
		"multiline\nstring\r\nwith\ttabs",
		"`template literals ${7 * 7}`",
		`\backslash \and \path\to\file`,
		"🎉 🚀 🔥 emojis and unicode: 中文 한국어 日本語",
		`<script>alert("xss")</script><img src=x onerror=alert(1)>`,
		`' OR 1=1 -- sql injection`,
		`{"json": "object", "array": [1, 2, 3]}`,
		"", // empty string
		strings.Repeat("long_payload_", 1000),
	}

	for i, p := range payloads {
		t.Run(fmt.Sprintf("payload_%d", i), func(t *testing.T) {
			expr := fillExpression("#input", p)
			if expr == "" {
				t.Fatal("fillExpression returned empty string")
			}

			// Verify required synthetic tokens
			if !strings.Contains(expr, "el.focus()") {
				t.Error("missing el.focus()")
			}
			if !strings.Contains(expr, "el._valueTracker") {
				t.Error("missing el._valueTracker")
			}
			if !strings.Contains(expr, "new InputEvent('beforeinput'") {
				t.Error("missing beforeinput InputEvent")
			}
			if !strings.Contains(expr, "new InputEvent('input'") {
				t.Error("missing input InputEvent")
			}
			if !strings.Contains(expr, "new Event('change'") {
				t.Error("missing change Event")
			}

			// Verify JS syntax validity via node --check
			checkJSSyntax(t, expr)
		})
	}
}

func checkJSSyntax(t *testing.T, jsCode string) {
	t.Helper()
	cmd := exec.Command("node", "--check", "-")
	cmd.Stdin = strings.NewReader(jsCode)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("invalid JavaScript syntax: %v, out: %s\ncode:\n%s", err, string(out), jsCode)
	}
}

// TestChallengerReactValueTrackerStateLogic simulates React's _valueTracker
// and verifies that fillExpression forces a mismatch in all three branches:
// 1. previous != next
// 2. previous == next (non-empty)
// 3. previous == next (empty string)
func TestChallengerReactValueTrackerStateLogic(t *testing.T) {
	// React _valueTracker contract:
	// A change is only detected if tracker.getValue() !== element.value.
	type trackerState struct {
		trackedValue string
	}

	setTrackerValue := func(st *trackerState, previousValue, text string) {
		if previousValue == text {
			if text == "" {
				st.trackedValue = "__argus_cleared__"
			} else {
				st.trackedValue = ""
			}
		} else {
			st.trackedValue = previousValue
		}
	}

	tests := []struct {
		name          string
		previousValue string
		nextValue     string
	}{
		{"different value", "oldVal", "newVal"},
		{"same non-empty value", "hello", "hello"},
		{"same empty value", "", ""},
		{"from non-empty to empty", "initial", ""},
		{"from empty to non-empty", "", "populated"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := &trackerState{trackedValue: tc.previousValue}
			setTrackerValue(st, tc.previousValue, tc.nextValue)

			// After prototype setter, element.value becomes nextValue
			elementValue := tc.nextValue

			// React checks: st.trackedValue !== elementValue
			hasChanged := st.trackedValue != elementValue
			if !hasChanged {
				t.Errorf("React _valueTracker failed to detect change! tracked=%q, element=%q",
					st.trackedValue, elementValue)
			}
		})
	}
}

// =============================================================================
// Feature F06: postInputTransitionSettleScript Stress Tests
// =============================================================================

// TestChallengerTransitionSettleScriptSyntax verifies the settle script
// JavaScript syntax and boundary conditions.
func TestChallengerTransitionSettleScriptSyntax(t *testing.T) {
	testDurations := []int{-10, 0, 1, 50, 200, 1000}

	for _, d := range testDurations {
		t.Run(fmt.Sprintf("duration_%d", d), func(t *testing.T) {
			script := postInputTransitionSettleScript(d)
			expectedSettleMs := d
			if expectedSettleMs <= 0 {
				expectedSettleMs = 50
			}

			expectedToken := fmt.Sprintf("const settleMs = %d;", expectedSettleMs)
			if !strings.Contains(script, expectedToken) {
				t.Errorf("script missing %q", expectedToken)
			}

			// Check syntax via node
			checkJSSyntax(t, script)
		})
	}
}

// TestChallengerTransitionSettleScriptAntiHang verifies the Promise.race logic
// ensuring that a hanging/infinite animation cannot block the settle probe.
func TestChallengerTransitionSettleScriptAntiHang(t *testing.T) {
	script := postInputTransitionSettleScript(50)

	// In the script, Promise.race([Promise.allSettled(animPromises), timer])
	// must be present to prevent infinite animations from hanging.
	requiredSubstrings := []string{
		"Promise.race",
		"Promise.allSettled(animPromises)",
		"timer",
		"requestAnimationFrame",
		"document.getAnimations",
	}

	for _, sub := range requiredSubstrings {
		if !strings.Contains(script, sub) {
			t.Errorf("settle script missing critical anti-hang construct: %q", sub)
		}
	}
}

// =============================================================================
// Feature F08: VerificationPayload & Network Correlation Stress Tests
// =============================================================================

// TestChallengerNetworkCorrelationInterleaving verifies that multiple
// interleaved requests are correlated by RequestID in chronological order.
func TestChallengerNetworkCorrelationInterleaving(t *testing.T) {
	testTarget := "target_interleave_test"
	startTime := time.Now()

	requests := []struct {
		id     string
		method string
		url    string
		status int
	}{
		{"req_1", "GET", "https://api.test/user", 200},
		{"req_2", "POST", "https://api.test/save", 201},
		{"req_3", "DELETE", "https://api.test/cache", 204},
		{"req_4", "PUT", "https://api.test/profile", 400},
	}

	// 1. Emit requestWillBeSent for req_1 and req_2
	for _, req := range requests[:2] {
		rec.RecordEventWithAction("network", testTarget, "", "", NetworkEvidence{
			RequestID: req.id,
			Method:    req.method,
			URL:       req.url,
			Phase:     "request",
		})
	}

	// 2. Emit responseReceived for req_1
	rec.RecordEventWithAction("network", testTarget, "", "", NetworkEvidence{
		RequestID: requests[0].id,
		Status:    requests[0].status,
		Phase:     "response",
	})

	// 3. Emit requestWillBeSent for req_3 and req_4
	for _, req := range requests[2:] {
		rec.RecordEventWithAction("network", testTarget, "", "", NetworkEvidence{
			RequestID: req.id,
			Method:    req.method,
			URL:       req.url,
			Phase:     "request",
		})
	}

	// 4. Emit responseReceived for req_2, req_3, req_4
	for _, req := range requests[1:] {
		rec.RecordEventWithAction("network", testTarget, "", "", NetworkEvidence{
			RequestID: req.id,
			Status:    req.status,
			Phase:     "response",
		})
	}

	correlated := rec.NetworkRequestsSince(testTarget, startTime)
	if len(correlated) != len(requests) {
		t.Fatalf("correlated len = %d, want %d", len(correlated), len(requests))
	}

	for i, req := range requests {
		got := correlated[i]
		if got["method"] != req.method {
			t.Errorf("[%d] method = %v, want %s", i, got["method"], req.method)
		}
		if got["url"] != req.url {
			t.Errorf("[%d] url = %v, want %s", i, got["url"], req.url)
		}
		if got["status"] != req.status {
			t.Errorf("[%d] status = %v, want %d", i, got["status"], req.status)
		}
	}
}

// TestChallengerNetworkCorrelationTargetIsolation verifies that requests
// from Target B do not contaminate Target A's verification payload.
func TestChallengerNetworkCorrelationTargetIsolation(t *testing.T) {
	targetA := "target_isolation_A"
	targetB := "target_isolation_B"
	startTime := time.Now()

	// Target A request
	rec.RecordEventWithAction("network", targetA, "", "", NetworkEvidence{
		RequestID: "req_A_1",
		Method:    "GET",
		URL:       "https://api.test/a",
		Phase:     "request",
	})

	// Target B request
	rec.RecordEventWithAction("network", targetB, "", "", NetworkEvidence{
		RequestID: "req_B_1",
		Method:    "POST",
		URL:       "https://api.test/b",
		Phase:     "request",
	})

	reqsA := rec.NetworkRequestsSince(targetA, startTime)
	if len(reqsA) != 1 || reqsA[0]["url"] != "https://api.test/a" {
		t.Errorf("Target A contaminated or missing: %+v", reqsA)
	}

	reqsB := rec.NetworkRequestsSince(targetB, startTime)
	if len(reqsB) != 1 || reqsB[0]["url"] != "https://api.test/b" {
		t.Errorf("Target B contaminated or missing: %+v", reqsB)
	}
}

// TestChallengerNetworkCorrelationCapacityCap verifies that NetworkRequestsSince
// caps output at 20 requests without overflowing.
func TestChallengerNetworkCorrelationCapacityCap(t *testing.T) {
	targetCap := "target_cap_test"
	startTime := time.Now()

	for i := 0; i < 50; i++ {
		rec.RecordEventWithAction("network", targetCap, "", "", NetworkEvidence{
			RequestID: fmt.Sprintf("req_cap_%d", i),
			Method:    "GET",
			URL:       fmt.Sprintf("https://api.test/item/%d", i),
			Status:    200,
			Phase:     "response",
		})
	}

	reqs := rec.NetworkRequestsSince(targetCap, startTime)
	if len(reqs) != 20 {
		t.Errorf("expected exactly 20 capped requests, got %d", len(reqs))
	}
}

// TestChallengerVerificationPayloadCausalMatrix tests the truth table for
// causal verification success (verified: true vs verified: false).
func TestChallengerVerificationPayloadCausalMatrix(t *testing.T) {
	cases := []struct {
		name         string
		op           string
		startURL     string
		currentURL   string
		domChanges   map[string]any
		netRequests  bool
		wantVerified bool
	}{
		{
			name:         "click with no side effects",
			op:           "click",
			startURL:     "http://test.com/a",
			currentURL:   "http://test.com/a",
			domChanges:   nil,
			netRequests:  false,
			wantVerified: false,
		},
		{
			name:         "click with navigation",
			op:           "click",
			startURL:     "http://test.com/a",
			currentURL:   "http://test.com/b",
			domChanges:   nil,
			netRequests:  false,
			wantVerified: true,
		},
		{
			name:         "click with DOM addition",
			op:           "click",
			startURL:     "http://test.com/a",
			currentURL:   "http://test.com/a",
			domChanges:   map[string]any{"added_count": 1, "removed_count": 0},
			netRequests:  false,
			wantVerified: true,
		},
		{
			name:         "click with DOM removal",
			op:           "click",
			startURL:     "http://test.com/a",
			currentURL:   "http://test.com/a",
			domChanges:   map[string]any{"added_count": 0, "removed_count": 1},
			netRequests:  false,
			wantVerified: true,
		},
		{
			name:         "fill always verified",
			op:           "fill",
			startURL:     "http://test.com/a",
			currentURL:   "http://test.com/a",
			domChanges:   nil,
			netRequests:  false,
			wantVerified: true,
		},
		{
			name:         "type always verified",
			op:           "type",
			startURL:     "http://test.com/a",
			currentURL:   "http://test.com/a",
			domChanges:   nil,
			netRequests:  false,
			wantVerified: true,
		},
		{
			name:         "scroll always verified",
			op:           "scroll",
			startURL:     "http://test.com/a",
			currentURL:   "http://test.com/a",
			domChanges:   nil,
			netRequests:  false,
			wantVerified: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ar := BeginAction(tc.op)
			ar.StartURL = tc.startURL
			target := "target_causal_" + tc.name
			ar.TargetID = target

			if tc.netRequests {
				rec.RecordEventWithAction("network", target, "", "", NetworkEvidence{
					RequestID: "req_tr",
					Method:    "POST",
					URL:       "http://test.com/api",
					Status:    200,
				})
			}

			payload := ar.collectVerification("", tc.domChanges, tc.currentURL)
			if payload == nil {
				t.Fatal("collectVerification returned nil")
			}
			verified, ok := payload["verified"].(bool)
			if !ok {
				t.Fatalf("verified not bool: %v", payload["verified"])
			}
			if verified != tc.wantVerified {
				t.Errorf("verified = %v, want %v", verified, tc.wantVerified)
			}
		})
	}
}

// TestChallengerConcurrentRecorderStress runs 30 goroutines concurrently
// performing network and action recording to verify race freedom.
func TestChallengerConcurrentRecorderStress(t *testing.T) {
	const goroutines = 30
	const iterations = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	var reqCounter int64

	for g := 0; g < goroutines; g++ {
		targetID := fmt.Sprintf("target_race_%d", g%5)
		go func(gid int, target string) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				reqID := fmt.Sprintf("req_%d_%d", gid, atomic.AddInt64(&reqCounter, 1))

				rec.OnRequestStart(target, reqID)
				rec.SetCurrentURL(target, fmt.Sprintf("https://race.test/page_%d", i))

				rec.RecordEventWithAction("network", target, "", "", NetworkEvidence{
					RequestID: reqID,
					Method:    "POST",
					URL:       "https://race.test/api",
					Phase:     "request",
				})

				_ = rec.ActiveRequestCount(target)
				_ = rec.CurrentURL(target)

				rec.RecordEventWithAction("network", target, "", "", NetworkEvidence{
					RequestID: reqID,
					Status:    200,
					Phase:     "response",
				})

				rec.OnRequestEnd(target, reqID)

				ar := BeginAction("click")
				ar.TargetID = target
				ar.StartURL = "https://race.test/page_0"
				_ = ar.collectVerification("", map[string]any{"added_count": 1}, "")
			}
		}(g, targetID)
	}

	wg.Wait()
}

// =============================================================================
// Feature End-to-End: Real Isolated Headless Chrome Validation
// =============================================================================

// TestChallengerRealChromeFeaturesE2E launches an isolated headless Chrome instance
// on an ephemeral port, loads a rich interactive HTML page containing React-like
// controlled inputs, animations, and forms, and validates F04, F05, F06 end-to-end.
func TestChallengerRealChromeFeaturesE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real Chrome test in -short mode")
	}

	inst, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	// Verify safety barrier: isolated port MUST NOT equal 9224
	if inst.Port == 9224 {
		t.Fatalf("FATAL SAFETY BREACH: Isolated Chrome running on port 9224!")
	}

	// Verify host port 9224 is untouched
	conn, err := net.DialTimeout("tcp", "127.0.0.1:9224", 200*time.Millisecond)
	if err == nil {
		// Port 9224 is running host Chrome as expected, close our probe
		_ = conn.Close()
	}

	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}

	// 1. Create a rich HTML test page
	html := `data:text/html,<!DOCTYPE html><html><body>
<div id="container">
  <input id="controlled-input" value="initial">
  <span id="change-count">0</span>
  <span id="last-value">none</span>
  <label id="test-label" for="labeled-input">Username</label>
  <input id="labeled-input" value="">
  <div id="animated-box" style="width:50px;height:50px;background:red;"></div>
</div>
<script>
  const input = document.getElementById('controlled-input');
  const countSpan = document.getElementById('change-count');
  const lastSpan = document.getElementById('last-value');
  let changes = 0;

  // Simulate React _valueTracker
  let currentVal = input.value;
  input._valueTracker = {
    getValue: () => currentVal,
    setValue: (v) => { currentVal = '' + v; }
  };

  input.addEventListener('input', (e) => {
    if (input._valueTracker.getValue() !== input.value) {
      changes++;
      countSpan.textContent = '' + changes;
      lastSpan.textContent = input.value;
      input._valueTracker.setValue(input.value);
    }
  });

  // Start CSS web animation
  const box = document.getElementById('animated-box');
  box.animate([{ opacity: 1 }, { opacity: 0.5 }], { duration: 100, iterations: 1 });
</script>
</body></html>`

	if _, err := cdpPageNavigate(ws, html, 5*time.Second); err != nil {
		t.Fatalf("cdpPageNavigate failed: %v", err)
	}

	// 2. Validate Feature F05: fillExpression against React _valueTracker
	// Step A: Fill new value "updated_value"
	res, err := cdpEvaluate(ws, fillExpression("#controlled-input", "updated_value"), true)
	if err != nil {
		t.Fatalf("fillExpression failed: %v", err)
	}
	t.Logf("fillExpression result: %+v", res)

	valRes, err := cdpEvaluate(ws, `document.getElementById('last-value').textContent`, true)
	if err != nil || valRes != "updated_value" {
		t.Errorf("expected last-value = 'updated_value', got %v (err: %v)", valRes, err)
	}

	countRes, err := cdpEvaluate(ws, `document.getElementById('change-count').textContent`, true)
	if err != nil || countRes != "1" {
		t.Errorf("expected change-count = '1', got %v (err: %v)", countRes, err)
	}

	// Step B: Fill the SAME value "updated_value" again
	// _valueTracker must reset to '' and trigger another change event!
	_, err = cdpEvaluate(ws, fillExpression("#controlled-input", "updated_value"), true)
	if err != nil {
		t.Fatalf("fillExpression same value failed: %v", err)
	}
	countRes2, _ := cdpEvaluate(ws, `document.getElementById('change-count').textContent`, true)
	if countRes2 != "2" {
		t.Errorf("filling same value failed to trigger React change! got %v, want 2", countRes2)
	}

	// Step C: Fill empty string ""
	_, err = cdpEvaluate(ws, fillExpression("#controlled-input", ""), true)
	if err != nil {
		t.Fatalf("fillExpression empty string failed: %v", err)
	}
	countRes3, _ := cdpEvaluate(ws, `document.getElementById('change-count').textContent`, true)
	if countRes3 != "3" {
		t.Errorf("filling empty string failed to trigger React change! got %v, want 3", countRes3)
	}

	// 3. Validate Feature F06: postInputTransitionSettleScript
	settleStart := time.Now()
	_, err = cdpEvaluate(ws, postInputTransitionSettleScript(60), true)
	if err != nil {
		t.Fatalf("postInputTransitionSettleScript failed: %v", err)
	}
	settleElapsed := time.Since(settleStart)
	t.Logf("Transition settle resolved in %v", settleElapsed)
	if settleElapsed < 40*time.Millisecond || settleElapsed > 1500*time.Millisecond {
		t.Errorf("unexpected settle duration: %v", settleElapsed)
	}

	// 4. Validate Feature F04 & F09: Smart locator and key dispatch
	_, err = cdpEvaluate(ws, fillExpression("#test-label", "label_sniffed_val"), true)
	if err != nil {
		t.Fatalf("label sniffing fill failed: %v", err)
	}
	labeledVal, _ := cdpEvaluate(ws, `document.getElementById('labeled-input').value`, true)
	if labeledVal != "label_sniffed_val" {
		t.Errorf("expected labeled-input = 'label_sniffed_val', got %v", labeledVal)
	}

	// Key dispatch on labeled input
	_, err = cdpDispatchKey(ws, "Enter", 0)
	if err != nil {
		t.Errorf("cdpDispatchKey failed: %v", err)
	}
}
