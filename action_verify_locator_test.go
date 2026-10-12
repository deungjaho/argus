package main

import (
	"strings"
	"testing"
	"time"
)

// TestVerificationPayloadStructure verifies the VerificationPayload structure
// conforms to PROJECT.md Section 4.2.
func TestVerificationPayloadStructure(t *testing.T) {
	ar := BeginAction("click")
	ar.StartURL = "https://example.com/start"

	domChanges := map[string]any{
		"added_count":   2,
		"removed_count": 0,
		"status_text":   "Welcome dev",
	}

	payload := ar.collectVerification("", domChanges, "https://example.com/dashboard")
	if payload == nil {
		t.Fatal("collectVerification returned nil")
	}

	navigated, ok := payload["navigated"].(bool)
	if !ok || !navigated {
		t.Errorf("navigated = %v, want true", payload["navigated"])
	}

	curURL, ok := payload["current_url"].(string)
	if !ok || curURL != "https://example.com/dashboard" {
		t.Errorf("current_url = %q, want https://example.com/dashboard", curURL)
	}

	dc, ok := payload["dom_changes"].(map[string]any)
	if !ok || dc["added_count"] != 2 || dc["status_text"] != "Welcome dev" {
		t.Errorf("dom_changes invalid: %+v", dc)
	}

	verified, ok := payload["verified"].(bool)
	if !ok || !verified {
		t.Errorf("verified = %v, want true", payload["verified"])
	}
}

// TestBeginActionBaselineCapture verifies BeginAction accurately captures
// target ID, cached URL, and active request count.
func TestBeginActionBaselineCapture(t *testing.T) {
	testTarget := "target_verify_test"
	rec.SetCurrentURL(testTarget, "https://example.com/login")
	rec.OnRequestStart(testTarget, "req_1")

	setCurrentTargetID(testTarget)
	defer clearCurrentTargetID()

	ar := BeginAction("click")
	if ar.TargetID != testTarget {
		t.Errorf("TargetID = %q, want %q", ar.TargetID, testTarget)
	}
	if ar.StartURL != "https://example.com/login" {
		t.Errorf("StartURL = %q, want https://example.com/login", ar.StartURL)
	}
	if ar.StartRequestCount != 1 {
		t.Errorf("StartRequestCount = %d, want 1", ar.StartRequestCount)
	}

	rec.OnRequestEnd(testTarget, "req_1")
	if count := rec.ActiveRequestCount(testTarget); count != 0 {
		t.Errorf("ActiveRequestCount = %d after request end, want 0", count)
	}
}

// TestNetworkRequestsSinceCorrelation verifies requests in the flight recorder
// are correlated across request/response phases in chronological order.
func TestNetworkRequestsSinceCorrelation(t *testing.T) {
	testTarget := "target_net_test"
	startTime := time.Now()

	// Simulate Network.requestWillBeSent
	rec.RecordEventWithAction("network", testTarget, "", "", NetworkEvidence{
		RequestID: "req_post_01",
		Method:    "POST",
		URL:       "https://example.com/api/submit",
		Phase:     "request",
	})

	// Simulate Network.responseReceived
	rec.RecordEventWithAction("network", testTarget, "", "", NetworkEvidence{
		RequestID: "req_post_01",
		Method:    "POST",
		URL:       "https://example.com/api/submit",
		Status:    200,
		Phase:     "response",
	})

	requests := rec.NetworkRequestsSince(testTarget, startTime)
	if len(requests) != 1 {
		t.Fatalf("NetworkRequestsSince returned %d requests, want 1", len(requests))
	}

	req := requests[0]
	if req["method"] != "POST" {
		t.Errorf("method = %v, want POST", req["method"])
	}
	if req["url"] != "https://example.com/api/submit" {
		t.Errorf("url = %v, want https://example.com/api/submit", req["url"])
	}
	if req["status"] != 200 {
		t.Errorf("status = %v, want 200", req["status"])
	}
}

// TestCausalVerificationLogic verifies causal truth values for different actions.
func TestCausalVerificationLogic(t *testing.T) {
	// Scenario A: Click with no mutations, no navigations, no network requests -> verified: false
	arClick := BeginAction("click")
	arClick.StartURL = "https://example.com/page"
	vClick := arClick.collectVerification("", nil, "https://example.com/page")
	if vClick["verified"] != false {
		t.Errorf("Click with zero side effects should have verified=false, got %v", vClick["verified"])
	}

	// Scenario B: Click that added a modal toast -> verified: true
	vClickToast := arClick.collectVerification("", map[string]any{"added_count": 1, "status_text": "Saved"}, "https://example.com/page")
	if vClickToast["verified"] != true {
		t.Errorf("Click with DOM addition should have verified=true, got %v", vClickToast["verified"])
	}

	// Scenario C: Fill action -> always verified: true
	arFill := BeginAction("fill")
	vFill := arFill.collectVerification("", nil, "")
	if vFill["verified"] != true {
		t.Errorf("Fill action should always have verified=true, got %v", vFill["verified"])
	}
}

// TestSmartLocatorFillExpressionSniffing verifies fillExpression contains
// container and label sniffing logic.
func TestSmartLocatorFillExpressionSniffing(t *testing.T) {
	expr := fillExpression("label.user-label", "alice")
	if !strings.Contains(expr, "el.control") {
		t.Errorf("fillExpression missing el.control label sniffing")
	}
	if !strings.Contains(expr, "el.htmlFor") {
		t.Errorf("fillExpression missing el.htmlFor label sniffing")
	}
	if !strings.Contains(expr, "input:not([type=\"hidden\"])") {
		t.Errorf("fillExpression missing inner input query selector sniffing")
	}
}

// TestSmartLocatorFocusExpressionSniffing verifies focusExpression contains
// container and label sniffing logic.
func TestSmartLocatorFocusExpressionSniffing(t *testing.T) {
	expr := focusExpression(".el-input", false)
	if !strings.Contains(expr, "el.control") {
		t.Errorf("focusExpression missing el.control label sniffing")
	}
	if !strings.Contains(expr, "input:not([type=\"hidden\"])") {
		t.Errorf("focusExpression missing inner input query selector sniffing")
	}
}

// TestSmartLocatorElementStateExpressionDelegation verifies elementStateExpression
// detects transparent/hidden inputs and delegates to visible wrapper containers.
func TestSmartLocatorElementStateExpressionDelegation(t *testing.T) {
	expr := elementStateExpression("input[type=checkbox]", "", "")
	if !strings.Contains(expr, "ant-checkbox-wrapper") {
		t.Errorf("elementStateExpression missing ant-checkbox-wrapper container delegation")
	}
	if !strings.Contains(expr, "el-checkbox") {
		t.Errorf("elementStateExpression missing el-checkbox container delegation")
	}
	if !strings.Contains(expr, "el.closest") {
		t.Errorf("elementStateExpression missing el.closest delegation")
	}
	if !strings.Contains(expr, "el.labels") {
		t.Errorf("elementStateExpression missing el.labels delegation")
	}
}
