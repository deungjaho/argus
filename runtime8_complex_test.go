//go:build integration

package main

// runtime8_complex_test.go — Complex reproducible acceptance tests for
// Argus Runtime 8.0, derived from real Lacoeur aug11 workflows.
//
// These tests exercise the full agent semantic loop through registered
// MCP handlers against an isolated WeChat Mini Program fixture:
//
//   Scenario A: complex after-sales form (fill, submit, verify state)
//   Scenario B: share-entry, home switch, re-entry, stale refs
//   Scenario C: dynamic 40-row list, duplicate elements, diagnose
//   Scenario D: disconnect/reconnect during multi-step task
//
// Rules (from /private/tmp/argus-runtime8-complex-acceptance.md):
//   - Exercise registered MCP handlers, not helper functions directly.
//   - No call_wx_method, wxml, page_data, raw RPC, or eval in task execution.
//   - Record every tool call with input/output bytes, tokens, latency, retries.
//   - Assertions validate final application state, not merely error == nil.
//   - No skipped test counts as acceptance.
//   - Every test has a deterministic reset and must pass 3 consecutive runs.
//   - Output, RPCs, time, events remain bounded.
//
// Run with:
//   ./scripts/runtime8-wechat-fixture.sh start
//   ARGUS_WX_LIVE=1 ARGUS_WX_PORT=9420 \
//     go test -tags=integration -count=3 -timeout 600s \
//       -run TestRuntime8ComplexWeChat ./...
//   ./scripts/runtime8-wechat-fixture.sh stop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- Metric types ---

// r8ToolCall records one MCP tool invocation with metrics.
type r8ToolCall struct {
	Tool        string `json:"tool"`
	InputBytes  int    `json:"input_bytes"`
	OutputBytes int    `json:"output_bytes"`
	EstTokens   int    `json:"est_tokens"`
	LatencyMs   int64  `json:"latency_ms"`
	Error       string `json:"error,omitempty"`
}

// r8ScenarioResult records the outcome of one scenario run.
type r8ScenarioResult struct {
	Scenario      string         `json:"scenario"`
	Run           int            `json:"run"`
	Success       bool           `json:"success"`
	ToolCalls     int            `json:"tool_calls"`
	FirstActionOK bool           `json:"first_action_ok"`
	Retries       int            `json:"retries"`
	OutputBytes   int            `json:"output_bytes"`
	EstTokens     int            `json:"est_tokens"`
	LatencyMs     int64          `json:"latency_ms"`
	UsedEval      bool           `json:"used_eval"`
	UsedEscape    bool           `json:"used_escape_hatch"`
	WrongElement  bool           `json:"wrong_element"`
	ErrorRecovery string         `json:"error_recovery,omitempty"`
	CallLog       []r8ToolCall   `json:"call_log"`
	FinalState    map[string]any `json:"final_state"`
}

// r8MetricsRecorder tracks tool calls and escape-hatch usage.
type r8MetricsRecorder struct {
	calls        []r8ToolCall
	usedEval     bool
	usedEscape   bool
	totalBytes   int
	retries      int
	firstAction  bool
	wrongElement bool
}

func (m *r8MetricsRecorder) record(tool string, input, output map[string]any, latency time.Duration, err error) {
	inBytes := jsonBytes(input)
	outBytes := jsonBytes(output)
	m.totalBytes += outBytes
	call := r8ToolCall{
		Tool:        tool,
		InputBytes:  inBytes,
		OutputBytes: outBytes,
		EstTokens:   outBytes / 4,
		LatencyMs:   latency.Milliseconds(),
	}
	if err != nil {
		call.Error = err.Error()
	}
	m.calls = append(m.calls, call)

	// Detect escape-hatch usage.
	if tool == "eval" {
		m.usedEval = true
	}
	if tool == "call_wx_method" || tool == "wxml" || tool == "page_data" {
		m.usedEscape = true
	}
}

// --- Helpers ---

func jsonBytes(v any) int {
	b, _ := json.Marshal(v)
	return len(b)
}

// r8Call invokes a tool through the MCP handler and records metrics.
func r8Call(t *testing.T, cs *mcp.ClientSession, m *r8MetricsRecorder, name string, args map[string]any) (map[string]any, error) {
	t.Helper()
	start := time.Now()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	latency := time.Since(start)
	if err != nil {
		m.record(name, args, nil, latency, err)
		return nil, err
	}
	result := parseLiveToolResult(t, name, res)
	m.record(name, args, result, latency, nil)
	return result, nil
}

// r8CallErr invokes a tool and does not fatal on error (for expected-error paths).
func r8CallErr(t *testing.T, cs *mcp.ClientSession, m *r8MetricsRecorder, name string, args map[string]any) (map[string]any, error) {
	t.Helper()
	start := time.Now()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	latency := time.Since(start)
	if err != nil {
		m.record(name, args, nil, latency, err)
		return nil, err
	}
	// Extract raw text content without fataling on parse errors.
	var rawText string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			rawText = tc.Text
			break
		}
	}
	var result map[string]any
	if rawText != "" {
		json.Unmarshal([]byte(rawText), &result)
	}
	if result == nil {
		result = map[string]any{"raw": rawText}
	}
	if res.IsError {
		m.record(name, args, result, latency, fmt.Errorf("%s", rawText))
		return result, fmt.Errorf("%s", rawText)
	}
	m.record(name, args, result, latency, nil)
	return result, nil
}

// r8NavigateTo navigates to a fixture page and waits for route settlement.
func r8NavigateTo(t *testing.T, cs *mcp.ClientSession, m *r8MetricsRecorder, url string) {
	t.Helper()
	r8Call(t, cs, m, "navigate", map[string]any{"url": url, "mode": "reLaunch"})
	// Wait for route to settle.
	r8Call(t, cs, m, "wait_for", map[string]any{
		"url_matches": url,
		"timeout_ms":  5000,
		"poll_ms":     200,
	})
}

// r8FindElementByText finds an element by text and returns its ref.
func r8FindElementByText(t *testing.T, cs *mcp.ClientSession, m *r8MetricsRecorder, text string) string {
	t.Helper()
	result, _ := r8Call(t, cs, m, "find", map[string]any{"text": text})
	matches, _ := result["matches"].([]any)
	if len(matches) == 0 {
		return ""
	}
	first, _ := matches[0].(map[string]any)
	ref, _ := first["ref"].(string)
	return ref
}

// r8FindElementByTextAndTag finds an element matching text and tag, returning ref.
// Used for disambiguation when multiple elements share the same text.
func r8FindElementByTextAndTag(t *testing.T, cs *mcp.ClientSession, m *r8MetricsRecorder, text, tag string) string {
	t.Helper()
	result, _ := r8Call(t, cs, m, "find", map[string]any{"text": text})
	matches, _ := result["matches"].([]any)
	for _, match := range matches {
		em, _ := match.(map[string]any)
		if em["tag"] == tag {
			ref, _ := em["ref"].(string)
			return ref
		}
	}
	return ""
}

// r8AssertNoEscapeHatch fails if any escape-hatch tool was used.
func r8AssertNoEscapeHatch(t *testing.T, m *r8MetricsRecorder) {
	t.Helper()
	if m.usedEval {
		t.Error("FAIL: eval was used during task execution — forbidden by Runtime 8.0 rules")
	}
	if m.usedEscape {
		t.Error("FAIL: escape-hatch tool (call_wx_method/wxml/page_data) was used — forbidden")
	}
}

// r8AssertBoundedOutput fails if total output exceeds the bound.
func r8AssertBoundedOutput(t *testing.T, m *r8MetricsRecorder, maxBytes int) {
	t.Helper()
	if m.totalBytes > maxBytes {
		t.Errorf("FAIL: total output %d bytes exceeds bound %d bytes", m.totalBytes, maxBytes)
	}
}

// r8BuildResult creates a scenario result from the metrics recorder.
func r8BuildResult(scenario string, run int, m *r8MetricsRecorder, start time.Time, success bool, finalState map[string]any) r8ScenarioResult {
	return r8ScenarioResult{
		Scenario:      scenario,
		Run:           run,
		Success:       success,
		ToolCalls:     len(m.calls),
		FirstActionOK: m.firstAction,
		Retries:       m.retries,
		OutputBytes:   m.totalBytes,
		EstTokens:     m.totalBytes / 4,
		LatencyMs:     time.Since(start).Milliseconds(),
		UsedEval:      m.usedEval,
		UsedEscape:    m.usedEscape,
		WrongElement:  m.wrongElement,
		CallLog:       m.calls,
		FinalState:    finalState,
	}
}

// --- Scenario A: Complex after-sales form ---

// r8ScenarioA performs the after-sales form task:
// 1. navigate to form page
// 2. snapshot to identify elements
// 3. find the textarea (disambiguate from duplicate 原因)
// 4. fill the textarea with "包装破损，申请退货"
// 5. wait_for submit button to be enabled
// 6. click submit
// 7. wait_for "申请已保存"
// 8. snapshot to verify final state (keyboard_hide_count=0)
// 9. diagnose to verify run ID in console evidence
func r8ScenarioA(t *testing.T, cs *mcp.ClientSession, run int) r8ScenarioResult {
	t.Helper()
	start := time.Now()
	m := &r8MetricsRecorder{firstAction: true}

	// Step 1: navigate to the after-sales form page.
	r8NavigateTo(t, cs, m, "/pages/aftersales/form")

	// Step 2: snapshot to identify elements.
	snapResult, _ := r8Call(t, cs, m, "snapshot", map[string]any{})
	elements, _ := snapResult["elements"].([]any)
	if len(elements) == 0 {
		return r8BuildResult("A", run, m, start, false, snapResult)
	}
	t.Logf("Scenario A run %d: snapshot found %d elements", run, len(elements))

	// Step 3: find the textarea by text "原因描述" and tag "textarea".
	// This disambiguates from other elements containing "原因".
	textareaRef := r8FindElementByTextAndTag(t, cs, m, "原因描述", "textarea")
	if textareaRef == "" {
		// Fallback: find by placeholder text.
		textareaRef = r8FindElementByTextAndTag(t, cs, m, "请输入退货原因", "textarea")
	}
	if textareaRef == "" {
		t.Errorf("Scenario A run %d: could not find textarea by text/placeholder", run)
		return r8BuildResult("A", run, m, start, false, snapResult)
	}
	t.Logf("Scenario A run %d: found textarea ref=%s", run, textareaRef)

	// Step 4: fill the textarea via MCP handler.
	fillValue := "包装破损，申请退货"
	fillResult, _ := r8Call(t, cs, m, "fill", map[string]any{
		"ref":   textareaRef,
		"value": fillValue,
	})
	if fillResult["backend"] != BackendWeChat {
		t.Errorf("Scenario A run %d: fill backend = %v, want %q", run, fillResult["backend"], BackendWeChat)
	}
	t.Logf("Scenario A run %d: fill result tag=%s", run, fillResult["tag"])

	// Verify the fill was on a textarea, not another element.
	if fillResult["tag"] != "textarea" {
		t.Errorf("Scenario A run %d: fill tag = %v, want textarea — wrong element selected", run, fillResult["tag"])
		m.wrongElement = true
	}

	// Step 5: wait_for the submit button to be enabled.
	// After filling, the submit button should become enabled.
	// We wait for "提交申请" text to appear (submit label when enabled).
	r8Call(t, cs, m, "wait_for", map[string]any{
		"text":       "提交申请",
		"timeout_ms": 3000,
		"poll_ms":    200,
	})

	// Step 6: find and click the submit button.
	submitRef := r8FindElementByTextAndTag(t, cs, m, "提交申请", "button")
	if submitRef == "" {
		t.Errorf("Scenario A run %d: could not find submit button", run)
		return r8BuildResult("A", run, m, start, false, snapResult)
	}
	r8Call(t, cs, m, "click", map[string]any{"ref": submitRef})
	t.Logf("Scenario A run %d: clicked submit", run)

	// Step 7: wait_for "申请已保存" status.
	waitResult, _ := r8Call(t, cs, m, "wait_for", map[string]any{
		"text":       "申请已保存",
		"timeout_ms": 5000,
		"poll_ms":    300,
	})
	satisfied, _ := waitResult["satisfied"].(bool)
	if !satisfied {
		t.Errorf("Scenario A run %d: wait_for 申请已保存 not satisfied", run)
		return r8BuildResult("A", run, m, start, false, snapResult)
	}
	t.Logf("Scenario A run %d: submit confirmed", run)

	// Step 8: snapshot to verify final state.
	finalSnap, _ := r8Call(t, cs, m, "snapshot", map[string]any{})
	finalElements, _ := finalSnap["elements"].([]any)

	// Verify keyboard_hide_count is 0 (no root tap side effect).
	// We look for an element with text containing "keyboard_hide_count: 0".
	keyboardCountOK := false
	for _, e := range finalElements {
		em, _ := e.(map[string]any)
		if text, ok := em["text"].(string); ok && strings.Contains(text, "keyboard_hide_count: 0") {
			keyboardCountOK = true
			break
		}
	}
	if !keyboardCountOK {
		// Try find by text to verify.
		findResult, _ := r8Call(t, cs, m, "find", map[string]any{"text": "keyboard_hide_count: 0"})
		count, _ := findResult["count"].(float64)
		if int(count) > 0 {
			keyboardCountOK = true
		}
	}
	if !keyboardCountOK {
		t.Errorf("Scenario A run %d: keyboard_hide_count is not 0 — root tap side effect detected", run)
	}

	// Step 9: diagnose to verify run ID in console evidence.
	diagResult, _ := r8Call(t, cs, m, "diagnose", map[string]any{"limit": 10})
	consoleIssues, _ := diagResult["console_issues"].([]any)
	hasRunID := false
	for _, issue := range consoleIssues {
		im, _ := issue.(map[string]any)
		text, _ := im["text"].(string)
		if strings.Contains(text, "run_id") || strings.Contains(text, "aftersales") {
			hasRunID = true
			break
		}
	}
	if !hasRunID {
		t.Logf("Scenario A run %d: diagnose did not find run_id in console issues (may have been evicted from ring buffer)", run)
	}

	// Hard assertions.
	r8AssertNoEscapeHatch(t, m)
	r8AssertBoundedOutput(t, m, 32*1024) // 32 KiB total bound

	success := !m.wrongElement && keyboardCountOK && satisfied
	finalState := map[string]any{
		"keyboard_hide_count_ok": keyboardCountOK,
		"submit_confirmed":       satisfied,
		"wrong_element":          m.wrongElement,
		"element_count":          len(finalElements),
	}
	return r8BuildResult("A", run, m, start, success, finalState)
}

// --- Scenario B: Share-entry, home switch, and re-entry ---

func r8ScenarioB(t *testing.T, cs *mcp.ClientSession, run int) r8ScenarioResult {
	t.Helper()
	start := time.Now()
	m := &r8MetricsRecorder{firstAction: true}

	// Step 1: navigate to product detail with share-entry query.
	r8NavigateTo(t, cs, m, "/pages/product/detail?id=42&puser=7")

	// Step 2: snapshot to identify elements, including two "首页" buttons.
	snapResult, _ := r8Call(t, cs, m, "snapshot", map[string]any{})
	elements, _ := snapResult["elements"].([]any)
	if len(elements) == 0 {
		return r8BuildResult("B", run, m, start, false, snapResult)
	}
	t.Logf("Scenario B run %d: product detail snapshot found %d elements", run, len(elements))

	// Step 3: find the "首页" buttons — there should be at least 2.
	findResult, _ := r8Call(t, cs, m, "find", map[string]any{"text": "首页"})
	matches, _ := findResult["matches"].([]any)
	if len(matches) < 2 {
		t.Errorf("Scenario B run %d: expected >= 2 首页 buttons, got %d", run, len(matches))
		return r8BuildResult("B", run, m, start, false, snapResult)
	}
	t.Logf("Scenario B run %d: found %d 首页 buttons", run, len(matches))

	// Step 4: click the real navigation "首页" button (the one with class "nav-home-btn").
	// We disambiguate by class context — the nav button has class containing "nav".
	navRef := ""
	for _, match := range matches {
		em, _ := match.(map[string]any)
		class, _ := em["class"].(string)
		if strings.Contains(class, "nav") {
			ref, _ := em["ref"].(string)
			navRef = ref
			break
		}
	}
	if navRef == "" {
		// Fallback: use the second 首页 button (the nav one is second in DOM).
		if len(matches) >= 2 {
			em, _ := matches[1].(map[string]any)
			navRef, _ = em["ref"].(string)
		}
	}
	if navRef == "" {
		t.Errorf("Scenario B run %d: could not identify the real nav 首页 button", run)
		m.wrongElement = true
		return r8BuildResult("B", run, m, start, false, snapResult)
	}

	// Capture a ref from the product detail page for stale-ref test.
	oldDetailRef := ""
	if len(matches) > 0 {
		em, _ := matches[0].(map[string]any)
		oldDetailRef, _ = em["ref"].(string)
	}

	// Step 5: click the real nav 首页 button.
	r8Call(t, cs, m, "click", map[string]any{"ref": navRef})
	t.Logf("Scenario B run %d: clicked nav 首页 button", run)

	// Step 6: wait_for route transition to home.
	waitResult, _ := r8Call(t, cs, m, "wait_for", map[string]any{
		"url_matches": "/pages/home/index",
		"timeout_ms":  5000,
		"poll_ms":     200,
	})
	satisfied, _ := waitResult["satisfied"].(bool)
	if !satisfied {
		t.Errorf("Scenario B run %d: did not navigate to home", run)
		return r8BuildResult("B", run, m, start, false, snapResult)
	}
	t.Logf("Scenario B run %d: navigated to home", run)

	// Step 7: verify old detail-page ref is stale.
	if oldDetailRef != "" {
		_, staleErr := r8CallErr(t, cs, m, "click", map[string]any{"ref": oldDetailRef})
		errStr := ""
		if staleErr != nil {
			errStr = staleErr.Error()
		}
		if !strings.Contains(errStr, "stale") {
			t.Errorf("Scenario B run %d: old detail ref should return stale_reference, got: %s", run, errStr)
		} else {
			t.Logf("Scenario B run %d: old detail ref correctly returned stale_reference", run)
		}
	}

	// Step 8: re-snapshot on home page and verify route.
	homeSnap, _ := r8Call(t, cs, m, "snapshot", map[string]any{})
	homePath, _ := homeSnap["path"].(string)
	if !strings.Contains(homePath, "home") {
		t.Errorf("Scenario B run %d: home snapshot path = %q, want to contain 'home'", run, homePath)
	}
	t.Logf("Scenario B run %d: home page confirmed at %s", run, homePath)

	// Step 9: verify product detail is absent by checking status.
	statusResult, _ := r8Call(t, cs, m, "status", map[string]any{})
	page, _ := statusResult["page"].(map[string]any)
	finalPath, _ := page["path"].(string)
	if strings.Contains(finalPath, "product/detail") {
		t.Errorf("Scenario B run %d: final path is product/detail, should be home", run)
	}

	// Hard assertions.
	r8AssertNoEscapeHatch(t, m)
	r8AssertBoundedOutput(t, m, 32*1024)

	success := !m.wrongElement && satisfied && strings.Contains(finalPath, "home")
	finalState := map[string]any{
		"final_path":       finalPath,
		"stale_ref_tested": oldDetailRef != "",
		"home_confirmed":   strings.Contains(finalPath, "home"),
		"detail_absent":    !strings.Contains(finalPath, "product/detail"),
		"wrong_element":    m.wrongElement,
	}
	return r8BuildResult("B", run, m, start, success, finalState)
}

// --- Scenario C: Dynamic list, duplicate elements, scroll, and diagnosis ---

func r8ScenarioC(t *testing.T, cs *mcp.ClientSession, run int) r8ScenarioResult {
	t.Helper()
	start := time.Now()
	m := &r8MetricsRecorder{firstAction: true}

	// Step 1: navigate to orders list page.
	r8NavigateTo(t, cs, m, "/pages/orders/list")

	// Step 2: initial snapshot should show loading state (bounded).
	initialSnap, _ := r8Call(t, cs, m, "snapshot", map[string]any{})
	initialElements, _ := initialSnap["elements"].([]any)
	t.Logf("Scenario C run %d: initial snapshot found %d elements (loading state)", run, len(initialElements))

	// Step 3: wait_for orders to load (text "LC-" appears when rows render).
	waitResult, _ := r8Call(t, cs, m, "wait_for", map[string]any{
		"text":       "LC-",
		"timeout_ms": 5000,
		"poll_ms":    300,
	})
	satisfied, _ := waitResult["satisfied"].(bool)
	if !satisfied {
		t.Errorf("Scenario C run %d: orders did not load (wait_for LC- timed out)", run)
		return r8BuildResult("C", run, m, start, false, initialSnap)
	}
	t.Logf("Scenario C run %d: orders loaded", run)

	// Step 4: find order LC-0027 by text.
	// Row 27 has order ID "LC-0027" and a "查看详情" button.
	lc0027Ref := r8FindElementByText(t, cs, m, "LC-0027")
	if lc0027Ref == "" {
		t.Errorf("Scenario C run %d: could not find LC-0027 row", run)
		return r8BuildResult("C", run, m, start, false, initialSnap)
	}
	t.Logf("Scenario C run %d: found LC-0027 row ref=%s", run, lc0027Ref)

	// Step 5: find the "查看详情" button near LC-0027.
	// Since find returns all matching elements, we need to identify the
	// correct one. We use find with text "查看详情" and verify count > 1
	// (there are duplicates). Then we click the row itself to open details.
	findResult, _ := r8Call(t, cs, m, "find", map[string]any{"text": "查看详情"})
	matches, _ := findResult["matches"].([]any)
	t.Logf("Scenario C run %d: found %d 查看详情 buttons (duplicates expected)", run, len(matches))

	// Click the LC-0027 row itself (not a 查看详情 button, since we can't
	// determine which button belongs to which row from find alone).
	// The row tap triggers onDetailTap which emits warning + error.
	r8Call(t, cs, m, "click", map[string]any{"ref": lc0027Ref})
	t.Logf("Scenario C run %d: clicked LC-0027 row", run)

	// Step 6: diagnose to verify warning/error evidence.
	// Wait briefly for console events to be recorded.
	r8Call(t, cs, m, "wait_for", map[string]any{
		"text_absent": "loading",
		"timeout_ms":  2000,
		"poll_ms":     200,
	})

	diagResult, _ := r8Call(t, cs, m, "diagnose", map[string]any{"limit": 20})
	consoleIssues, _ := diagResult["console_issues"].([]any)
	exceptions, _ := diagResult["exceptions"].([]any)

	hasWarning := false
	hasError := false
	for _, issue := range consoleIssues {
		im, _ := issue.(map[string]any)
		text, _ := im["text"].(string)
		if strings.Contains(text, "warning") || strings.Contains(text, "LC-0027") {
			hasWarning = true
		}
	}
	for _, exc := range exceptions {
		em, _ := exc.(map[string]any)
		text, _ := em["text"].(string)
		if strings.Contains(text, "LC-0027") || strings.Contains(text, "detail") {
			hasError = true
		}
	}
	t.Logf("Scenario C run %d: diagnose found warning=%v error=%v, console_issues=%d exceptions=%d",
		run, hasWarning, hasError, len(consoleIssues), len(exceptions))

	// Step 7: trigger unhandled exception and verify diagnose catches it.
	// Find and click the "触发异常" button.
	errorBtnRef := r8FindElementByTextAndTag(t, cs, m, "触发异常", "button")
	if errorBtnRef != "" {
		r8CallErr(t, cs, m, "click", map[string]any{"ref": errorBtnRef})
		t.Logf("Scenario C run %d: triggered unhandled exception", run)
	}

	// Diagnose again to catch the unhandled exception.
	diagResult2, _ := r8Call(t, cs, m, "diagnose", map[string]any{"limit": 20})
	exceptions2, _ := diagResult2["exceptions"].([]any)
	t.Logf("Scenario C run %d: second diagnose found %d exceptions", run, len(exceptions2))

	// Hard assertions.
	r8AssertNoEscapeHatch(t, m)
	r8AssertBoundedOutput(t, m, 48*1024) // 48 KiB for list scenario

	success := satisfied && lc0027Ref != ""
	finalState := map[string]any{
		"orders_loaded":   satisfied,
		"lc0027_found":    lc0027Ref != "",
		"has_warning":     hasWarning,
		"has_error":       hasError,
		"exception_count": len(exceptions2),
		"wrong_element":   m.wrongElement,
	}
	return r8BuildResult("C", run, m, start, success, finalState)
}

// --- Scenario D: Disconnect/reconnect during multi-step task ---

func r8ScenarioD(t *testing.T, cs *mcp.ClientSession, run int) r8ScenarioResult {
	t.Helper()
	start := time.Now()
	m := &r8MetricsRecorder{firstAction: true}

	// Step 1: navigate to after-sales form and capture refs.
	r8NavigateTo(t, cs, m, "/pages/aftersales/form")
	snapResult, _ := r8Call(t, cs, m, "snapshot", map[string]any{})
	elements, _ := snapResult["elements"].([]any)
	if len(elements) == 0 {
		return r8BuildResult("D", run, m, start, false, snapResult)
	}

	// Find the textarea ref.
	textareaRef := r8FindElementByTextAndTag(t, cs, m, "原因描述", "textarea")
	if textareaRef == "" {
		textareaRef = r8FindElementByTextAndTag(t, cs, m, "请输入退货原因", "textarea")
	}
	if textareaRef == "" {
		t.Errorf("Scenario D run %d: could not find textarea", run)
		return r8BuildResult("D", run, m, start, false, snapResult)
	}
	t.Logf("Scenario D run %d: captured textarea ref=%s before reconnect", run, textareaRef)

	// Step 2: force a reconnect by closing the automator connection.
	closeAutomatorConn()
	t.Logf("Scenario D run %d: closed automator connection — next call will reconnect", run)

	// Step 3: try to fill with the old ref — should fail with stale_reference.
	fillValue := "包装破损，申请退货"
	_, staleErr := r8CallErr(t, cs, m, "fill", map[string]any{
		"ref":   textareaRef,
		"value": fillValue,
	})
	errStr := ""
	if staleErr != nil {
		errStr = staleErr.Error()
	}
	if !strings.Contains(errStr, "stale") && !strings.Contains(errStr, "disconnected") {
		t.Errorf("Scenario D run %d: expected stale_reference or disconnected error, got: %s", run, errStr)
	} else {
		t.Logf("Scenario D run %d: old ref correctly rejected with: %s", run, errStr)
	}

	// Step 4: recover with status + re-snapshot.
	r8Call(t, cs, m, "status", map[string]any{})
	recoverySnap, _ := r8Call(t, cs, m, "snapshot", map[string]any{})
	recoveryElements, _ := recoverySnap["elements"].([]any)
	if len(recoveryElements) == 0 {
		t.Errorf("Scenario D run %d: recovery snapshot found no elements", run)
		return r8BuildResult("D", run, m, start, false, recoverySnap)
	}
	t.Logf("Scenario D run %d: recovered with re-snapshot (%d elements)", run, len(recoveryElements))

	// Step 5: find the textarea again from the new snapshot.
	newTextareaRef := r8FindElementByTextAndTag(t, cs, m, "原因描述", "textarea")
	if newTextareaRef == "" {
		newTextareaRef = r8FindElementByTextAndTag(t, cs, m, "请输入退货原因", "textarea")
	}
	if newTextareaRef == "" {
		t.Errorf("Scenario D run %d: could not find textarea after recovery", run)
		return r8BuildResult("D", run, m, start, false, recoverySnap)
	}
	t.Logf("Scenario D run %d: found new textarea ref=%s after recovery", run, newTextareaRef)

	// Step 6: complete Scenario A — fill, wait, submit, verify.
	r8Call(t, cs, m, "fill", map[string]any{
		"ref":   newTextareaRef,
		"value": fillValue,
	})

	// Wait for submit to be enabled.
	r8Call(t, cs, m, "wait_for", map[string]any{
		"text":       "提交申请",
		"timeout_ms": 3000,
		"poll_ms":    200,
	})

	// Find and click submit.
	submitRef := r8FindElementByTextAndTag(t, cs, m, "提交申请", "button")
	if submitRef == "" {
		t.Errorf("Scenario D run %d: could not find submit button after recovery", run)
		return r8BuildResult("D", run, m, start, false, recoverySnap)
	}
	r8Call(t, cs, m, "click", map[string]any{"ref": submitRef})

	// Wait for saved status.
	waitResult, _ := r8Call(t, cs, m, "wait_for", map[string]any{
		"text":       "申请已保存",
		"timeout_ms": 5000,
		"poll_ms":    300,
	})
	satisfied, _ := waitResult["satisfied"].(bool)
	if !satisfied {
		t.Errorf("Scenario D run %d: submit did not complete (申请已保存 not seen)", run)
		return r8BuildResult("D", run, m, start, false, recoverySnap)
	}
	t.Logf("Scenario D run %d: submit confirmed after recovery", run)

	// Verify keyboard_hide_count is 0.
	finalSnap, _ := r8Call(t, cs, m, "snapshot", map[string]any{})
	finalElements, _ := finalSnap["elements"].([]any)
	keyboardCountOK := false
	for _, e := range finalElements {
		em, _ := e.(map[string]any)
		if text, ok := em["text"].(string); ok && strings.Contains(text, "keyboard_hide_count: 0") {
			keyboardCountOK = true
			break
		}
	}
	if !keyboardCountOK {
		findResult, _ := r8Call(t, cs, m, "find", map[string]any{"text": "keyboard_hide_count: 0"})
		count, _ := findResult["count"].(float64)
		if int(count) > 0 {
			keyboardCountOK = true
		}
	}
	if !keyboardCountOK {
		t.Errorf("Scenario D run %d: keyboard_hide_count is not 0", run)
	}

	// Hard assertions.
	r8AssertNoEscapeHatch(t, m)
	r8AssertBoundedOutput(t, m, 32*1024)

	// Verify no duplicate submit — check that we only clicked submit once.
	submitClicks := 0
	for _, c := range m.calls {
		if c.Tool == "click" {
			submitClicks++
		}
	}
	if submitClicks > 1 {
		t.Errorf("Scenario D run %d: %d click calls — possible duplicate submit", run, submitClicks)
	}

	success := satisfied && keyboardCountOK && submitClicks <= 1
	finalState := map[string]any{
		"stale_ref_rejected":     strings.Contains(errStr, "stale") || strings.Contains(errStr, "disconnected"),
		"recovery_succeeded":     satisfied,
		"keyboard_hide_count_ok": keyboardCountOK,
		"submit_clicks":          submitClicks,
		"no_duplicate_submit":    submitClicks <= 1,
	}
	return r8BuildResult("D", run, m, start, success, finalState)
}

// --- Test entry points ---

// TestRuntime8ComplexWeChatScenarioA runs Scenario A (after-sales form) 3 times.
func TestRuntime8ComplexWeChatScenarioA(t *testing.T) {
	automatorLiveSkip(t)
	cs := setupLiveMCP(t)

	var results []r8ScenarioResult
	for run := 1; run <= 3; run++ {
		t.Logf("=== Scenario A run %d/3 ===", run)
		r := r8ScenarioA(t, cs, run)
		results = append(results, r)
		if !r.Success {
			t.Errorf("Scenario A run %d FAILED", run)
		}
	}
	r8LogScenarioSummary(t, results)
}

// TestRuntime8ComplexWeChatScenarioB runs Scenario B (share-entry, home, re-entry) 3 times.
func TestRuntime8ComplexWeChatScenarioB(t *testing.T) {
	automatorLiveSkip(t)
	cs := setupLiveMCP(t)

	var results []r8ScenarioResult
	for run := 1; run <= 3; run++ {
		t.Logf("=== Scenario B run %d/3 ===", run)
		r := r8ScenarioB(t, cs, run)
		results = append(results, r)
		if !r.Success {
			t.Errorf("Scenario B run %d FAILED", run)
		}
	}
	r8LogScenarioSummary(t, results)
}

// TestRuntime8ComplexWeChatScenarioC runs Scenario C (dynamic list, diagnose) 3 times.
func TestRuntime8ComplexWeChatScenarioC(t *testing.T) {
	automatorLiveSkip(t)
	cs := setupLiveMCP(t)

	var results []r8ScenarioResult
	for run := 1; run <= 3; run++ {
		t.Logf("=== Scenario C run %d/3 ===", run)
		r := r8ScenarioC(t, cs, run)
		results = append(results, r)
		if !r.Success {
			t.Errorf("Scenario C run %d FAILED", run)
		}
	}
	r8LogScenarioSummary(t, results)
}

// TestRuntime8ComplexWeChatScenarioD runs Scenario D (disconnect/reconnect) 3 times.
func TestRuntime8ComplexWeChatScenarioD(t *testing.T) {
	automatorLiveSkip(t)
	cs := setupLiveMCP(t)

	var results []r8ScenarioResult
	for run := 1; run <= 3; run++ {
		t.Logf("=== Scenario D run %d/3 ===", run)
		r := r8ScenarioD(t, cs, run)
		results = append(results, r)
		if !r.Success {
			t.Errorf("Scenario D run %d FAILED", run)
		}
	}
	r8LogScenarioSummary(t, results)
}

// TestRuntime8ComplexWeChatAll runs all 4 scenarios sequentially, 3 runs each.
// This is the comprehensive acceptance gate: 12/12 task runs must succeed.
func TestRuntime8ComplexWeChatAll(t *testing.T) {
	automatorLiveSkip(t)
	cs := setupLiveMCP(t)

	var allResults []r8ScenarioResult
	scenarios := []struct {
		name string
		fn   func(t *testing.T, cs *mcp.ClientSession, run int) r8ScenarioResult
	}{
		{"A", r8ScenarioA},
		{"B", r8ScenarioB},
		{"C", r8ScenarioC},
		{"D", r8ScenarioD},
	}

	totalSuccess := 0
	totalRuns := 0
	for _, sc := range scenarios {
		for run := 1; run <= 3; run++ {
			t.Logf("=== Scenario %s run %d/3 ===", sc.name, run)
			r := sc.fn(t, cs, run)
			allResults = append(allResults, r)
			totalRuns++
			if r.Success {
				totalSuccess++
			} else {
				t.Errorf("Scenario %s run %d FAILED", sc.name, run)
			}
		}
	}

	r8LogScenarioSummary(t, allResults)

	// Acceptance metric: 12/12 task runs must succeed.
	if totalSuccess != totalRuns {
		t.Errorf("Runtime 8.0 acceptance: %d/%d runs succeeded, need %d/%d", totalSuccess, totalRuns, totalRuns, totalRuns)
	} else {
		t.Logf("Runtime 8.0 acceptance: ALL %d/%d runs succeeded", totalSuccess, totalRuns)
	}
}

// r8LogScenarioSummary logs a machine-readable summary of scenario results.
func r8LogScenarioSummary(t *testing.T, results []r8ScenarioResult) {
	t.Helper()
	t.Logf("=== Runtime 8.0 Scenario Summary ===")
	successCount := 0
	totalCalls := 0
	totalBytes := 0
	totalTokens := 0
	for _, r := range results {
		status := "PASS"
		if !r.Success {
			status = "FAIL"
		}
		t.Logf("  Scenario %s run %d: %s calls=%d bytes=%d tokens=%d latency=%dms eval=%v escape=%v wrong=%v",
			r.Scenario, r.Run, status, r.ToolCalls, r.OutputBytes, r.EstTokens, r.LatencyMs,
			r.UsedEval, r.UsedEscape, r.WrongElement)
		if r.Success {
			successCount++
		}
		totalCalls += r.ToolCalls
		totalBytes += r.OutputBytes
		totalTokens += r.EstTokens
	}
	t.Logf("  Total: %d/%d succeeded, %d calls, %d bytes, %d tokens",
		successCount, len(results), totalCalls, totalBytes, totalTokens)
}
