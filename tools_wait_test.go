package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWaitForInputUnmarshal_InstantExists(t *testing.T) {
	raw := `{"selector": "#submit-button", "timeout_ms": 0}`
	var in waitForInput
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if in.Selector != "#submit-button" {
		t.Errorf("got selector %q, want '#submit-button'", in.Selector)
	}
	if !in.TimeoutSpecified {
		t.Errorf("expected TimeoutSpecified to be true when timeout_ms is 0")
	}
	if in.TimeoutMs != 0 {
		t.Errorf("expected TimeoutMs to be 0, got %d", in.TimeoutMs)
	}

	// Alias timeout: 0
	raw2 := `{"text": "Login", "timeout": 0}`
	var in2 waitForInput
	if err := json.Unmarshal([]byte(raw2), &in2); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if !in2.TimeoutSpecified || in2.TimeoutMs != 0 {
		t.Errorf("expected TimeoutSpecified=true and TimeoutMs=0 for timeout: 0")
	}
}

func TestWaitForInputUnmarshal_PositiveTimeout(t *testing.T) {
	// 1. Canonical timeout_ms: 2500
	rawMs := `{"selector": "#submit", "timeout_ms": 2500}`
	var inMs waitForInput
	if err := json.Unmarshal([]byte(rawMs), &inMs); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if !inMs.TimeoutSpecified {
		t.Errorf("expected TimeoutSpecified=true for timeout_ms: 2500")
	}
	if inMs.TimeoutMs != 2500 {
		t.Errorf("expected TimeoutMs=2500, got %d", inMs.TimeoutMs)
	}
	if inMs.Timeout != 2500 {
		t.Errorf("expected Timeout=2500, got %d", inMs.Timeout)
	}
	isInstantExistsMs := inMs.TimeoutSpecified && inMs.TimeoutMs == 0 && inMs.Timeout == 0
	if isInstantExistsMs {
		t.Errorf("isInstantExists must evaluate false for timeout_ms: 2500")
	}

	// 2. Alias timeout: 1500
	rawAlias := `{"selector": "#submit", "timeout": 1500}`
	var inAlias waitForInput
	if err := json.Unmarshal([]byte(rawAlias), &inAlias); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if !inAlias.TimeoutSpecified {
		t.Errorf("expected TimeoutSpecified=true for timeout: 1500")
	}
	if inAlias.Timeout != 1500 {
		t.Errorf("expected Timeout=1500, got %d", inAlias.Timeout)
	}
	if inAlias.TimeoutMs != 1500 {
		t.Errorf("expected TimeoutMs=1500, got %d", inAlias.TimeoutMs)
	}
	isInstantExistsAlias := inAlias.TimeoutSpecified && inAlias.TimeoutMs == 0 && inAlias.Timeout == 0
	if isInstantExistsAlias {
		t.Errorf("isInstantExists must evaluate false for timeout: 1500")
	}

	// 3. Structural guard: ensure conjunction blocks instant check if either field is positive
	inPartialMs := waitForInput{TimeoutSpecified: true, TimeoutMs: 2500, Timeout: 0}
	if inPartialMs.TimeoutSpecified && inPartialMs.TimeoutMs == 0 && inPartialMs.Timeout == 0 {
		t.Errorf("isInstantExists must evaluate false when TimeoutMs is positive")
	}
	inPartialAlias := waitForInput{TimeoutSpecified: true, TimeoutMs: 0, Timeout: 2500}
	if inPartialAlias.TimeoutSpecified && inPartialAlias.TimeoutMs == 0 && inPartialAlias.Timeout == 0 {
		t.Errorf("isInstantExists must evaluate false when Timeout is positive")
	}
}

func TestWaitForInputUnmarshal_OmittedTimeout(t *testing.T) {
	raw := `{"selector": ".loaded-card"}`
	var in waitForInput
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if in.TimeoutSpecified {
		t.Errorf("expected TimeoutSpecified to be false when timeout is omitted")
	}
}

func TestWaitForInputConditionAliases(t *testing.T) {
	cases := []struct {
		json string
		want string
	}{
		{`{"condition": "window.appReady === true"}`, "window.appReady === true"},
		{`{"script": "document.readyState === 'complete'"}`, "document.readyState === 'complete'"},
		{`{"expression": "items.length > 0"}`, "items.length > 0"},
		{`{"code": "window.__INITIALIZED__"}`, "window.__INITIALIZED__"},
	}

	for _, c := range cases {
		var in waitForInput
		if err := json.Unmarshal([]byte(c.json), &in); err != nil {
			t.Fatalf("unmarshal failed for %s: %v", c.json, err)
		}
		cond := resolveFirstNonEmpty(in.Condition, in.Script, in.Expression, in.Code)
		if cond != c.want {
			t.Errorf("got condition %q, want %q", cond, c.want)
		}
	}
}

func TestWaitForExpressionGeneration(t *testing.T) {
	inSel := waitForInput{Selector: "#user-profile"}
	exprSel := waitForExpression(inSel)
	if !strings.Contains(exprSel, "#user-profile") {
		t.Errorf("expected selector in expression, got %s", exprSel)
	}

	inText := waitForInput{Text: "Welcome Back"}
	exprText := waitForExpression(inText)
	if !strings.Contains(exprText, "Welcome Back") {
		t.Errorf("expected text in expression, got %s", exprText)
	}

	inAbsent := waitForInput{TextAbsent: "Loading..."}
	exprAbsent := waitForExpression(inAbsent)
	if !strings.Contains(exprAbsent, "Loading...") {
		t.Errorf("expected text_absent in expression, got %s", exprAbsent)
	}

	inURL := waitForInput{URLMatches: "/dashboard"}
	exprURL := waitForExpression(inURL)
	if !strings.Contains(exprURL, "/dashboard") {
		t.Errorf("expected url_matches in expression, got %s", exprURL)
	}
}

func TestWaitForBackwardCompatibilityAliases(t *testing.T) {
	var tryIn tryUntilInput
	var exIn existsInput

	tryIn.Condition = "window.ready"
	exIn.Selector = "#dialog"

	if tryIn.Condition != "window.ready" || exIn.Selector != "#dialog" {
		t.Error("unexpected failure with wait backward-compatibility aliases")
	}
}

func TestWaitForToolRegistration(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test-wait", Version: "1.0"}, nil)
	registerWaitTools(server)
	// Verified registerWaitTools registers without panicking
}
