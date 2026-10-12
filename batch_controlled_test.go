package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBatchActionStepKeyAliasUnmarshal(t *testing.T) {
	tests := []struct {
		name       string
		jsonRaw    string
		wantAction string
		wantKey    string
	}{
		{
			name:       "action key with key param",
			jsonRaw:    `{"action": "key", "key": "Enter"}`,
			wantAction: "key",
			wantKey:    "Enter",
		},
		{
			name:       "action press with key param",
			jsonRaw:    `{"action": "press", "key": "Tab"}`,
			wantAction: "press",
			wantKey:    "Tab",
		},
		{
			name:       "action key with text param alias",
			jsonRaw:    `{"action": "key", "text": "Escape"}`,
			wantAction: "key",
			wantKey:    "Escape",
		},
		{
			name:       "action press with value param alias",
			jsonRaw:    `{"action": "press", "value": "Backspace"}`,
			wantAction: "press",
			wantKey:    "Backspace",
		},
		{
			name:       "action press with input param alias",
			jsonRaw:    `{"action": "press", "input": "ArrowDown"}`,
			wantAction: "press",
			wantKey:    "ArrowDown",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var step batchActionStep
			if err := json.Unmarshal([]byte(tc.jsonRaw), &step); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if step.Action != tc.wantAction {
				t.Errorf("Action = %q, want %q", step.Action, tc.wantAction)
			}
			resolvedKey := resolveFirstNonEmpty(step.Key, step.Value, step.Text, step.Input)
			if resolvedKey != tc.wantKey {
				t.Errorf("resolvedKey = %q, want %q", resolvedKey, tc.wantKey)
			}
		})
	}
}

func TestFillInputAndBatchStepSettleFields(t *testing.T) {
	rawFill := `{"selector": "#user", "value": "alice", "settle_ms": 100, "wait_transition": true}`
	var fillIn fillInput
	if err := json.Unmarshal([]byte(rawFill), &fillIn); err != nil {
		t.Fatalf("unmarshal fillInput failed: %v", err)
	}
	if fillIn.SettleMs != 100 {
		t.Errorf("SettleMs = %d, want 100", fillIn.SettleMs)
	}
	if !fillIn.WaitTransition {
		t.Errorf("WaitTransition = %v, want true", fillIn.WaitTransition)
	}

	rawStep := `{"action": "fill", "target": "#user", "value": "bob", "settle_ms": 75, "wait_transition": true}`
	var step batchActionStep
	if err := json.Unmarshal([]byte(rawStep), &step); err != nil {
		t.Fatalf("unmarshal batchActionStep failed: %v", err)
	}
	if step.SettleMs != 75 {
		t.Errorf("step.SettleMs = %d, want 75", step.SettleMs)
	}
	if !step.WaitTransition {
		t.Errorf("step.WaitTransition = %v, want true", step.WaitTransition)
	}
}

func TestFillExpressionSyntheticSequence(t *testing.T) {
	expr := fillExpression("#test-input", "hello world")

	requiredTokens := []string{
		"el.focus()",
		"el._valueTracker",
		"_valueTracker.setValue",
		"new InputEvent('beforeinput'",
		"inputType: 'insertText'",
		"Object.getOwnPropertyDescriptor(proto, 'value')",
		"desc.set.call(el, text)",
		"new InputEvent('input'",
		"new Event('change'",
		"el.tagName === 'LABEL'",
	}

	for _, token := range requiredTokens {
		if !strings.Contains(expr, token) {
			t.Errorf("fillExpression missing required token %q", token)
		}
	}
}

func TestPostInputTransitionSettleScript(t *testing.T) {
	script := postInputTransitionSettleScript(60)

	requiredTokens := []string{
		"const settleMs = 60;",
		"requestAnimationFrame(() => {",
		"document.getAnimations",
		"Promise.allSettled",
		"Promise.resolve().then(resolve)",
		"setTimeout(() => {",
	}

	for _, token := range requiredTokens {
		if !strings.Contains(script, token) {
			t.Errorf("postInputTransitionSettleScript missing required token %q", token)
		}
	}
}
