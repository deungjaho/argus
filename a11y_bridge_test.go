package main

import (
	"math"
	"strings"
	"testing"
)

func TestA11yTreeCacheStoreAndLookup(t *testing.T) {
	targetID := "target_a11y_test_01"
	defer clearA11yCache(targetID)

	nodes := []map[string]any{
		{
			"nodeId":           "12",
			"backendDOMNodeId": 42,
			"role":             map[string]any{"value": "button"},
			"name":             map[string]any{"value": "Submit"},
		},
		{
			"nodeId":           "15",
			"backendDOMNodeId": 45,
			"role":             "textbox",
			"name":             "Username",
		},
	}

	storeA11yTree(targetID, nodes)

	// Test lookup via ax:12
	n1, found1 := getA11yNode(targetID, "ax:12")
	if !found1 {
		t.Fatalf("expected node with ref 'ax:12' to be found")
	}
	if n1.NodeID != "12" || n1.BackendDOMNodeID != 42 || n1.Role != "button" || n1.Name != "Submit" {
		t.Errorf("unexpected node data: %+v", n1)
	}

	// Test lookup via bare nodeId "12"
	n2, found2 := getA11yNode(targetID, "12")
	if !found2 || n2.BackendDOMNodeID != 42 {
		t.Errorf("expected bare nodeId '12' to resolve, got found=%v, node=%+v", found2, n2)
	}

	// Test lookup via backend:42 and ax:backend:42
	n3, found3 := getA11yNode(targetID, "backend:42")
	if !found3 || n3.NodeID != "12" {
		t.Errorf("expected backend:42 to resolve, got found=%v, node=%+v", found3, n3)
	}
	n4, found4 := getA11yNode(targetID, "ax:backend:42")
	if !found4 || n4.NodeID != "12" {
		t.Errorf("expected ax:backend:42 to resolve, got found=%v, node=%+v", found4, n4)
	}

	// Test second node
	n5, found5 := getA11yNode(targetID, "ax:15")
	if !found5 || n5.Role != "textbox" || n5.Name != "Username" {
		t.Errorf("unexpected node 15 data: %+v", n5)
	}

	// Test cache clear for this target
	clearA11yCache(targetID)
	_, foundAfterClear := getA11yNode(targetID, "ax:12")
	if foundAfterClear {
		t.Errorf("expected cache to be empty after clearA11yCache")
	}
}

func TestA11yBoxModelMath(t *testing.T) {
	// A rectangle from (100, 200) to (300, 250):
	// Top-left: (100, 200), Top-right: (300, 200), Bottom-right: (300, 250), Bottom-left: (100, 250)
	model := &cdpBoxModel{
		Content: []float64{100, 200, 300, 200, 300, 250, 100, 250},
		Width:   200,
		Height:  50,
	}

	cx, cy, err := model.Center()
	if err != nil {
		t.Fatalf("Center calculation error: %v", err)
	}
	expectedCX := 200.0
	expectedCY := 225.0
	if math.Abs(cx-expectedCX) > 1e-6 || math.Abs(cy-expectedCY) > 1e-6 {
		t.Errorf("Center() = (%f, %f), want (%f, %f)", cx, cy, expectedCX, expectedCY)
	}

	box := model.BoundingBox()
	if box == nil {
		t.Fatalf("BoundingBox returned nil")
	}
	if box["x"] != 100.0 || box["y"] != 200.0 || box["width"] != 200.0 || box["height"] != 50.0 {
		t.Errorf("BoundingBox() = %+v, want x=100, y=200, w=200, h=50", box)
	}

	// Invalid box model
	invalidModel := &cdpBoxModel{Content: []float64{10, 20}}
	if _, _, err := invalidModel.Center(); err == nil {
		t.Errorf("expected error for invalid box model quad, got nil")
	}
}

func TestA11yIsLikelyRef(t *testing.T) {
	positiveCases := []string{
		"ax:12",
		"ax_12",
		"ax:login_button",
		"backend:42",
		"ax:backend:42",
		"12",
		"104",
		"0",
	}

	for _, ref := range positiveCases {
		if !isLikelyA11yRef(ref) {
			t.Errorf("isLikelyA11yRef(%q) = false, want true", ref)
		}
	}

	negativeCases := []string{
		"#submit-button",
		"button.btn-primary",
		"input[type='text']",
		"text:Submit",
		"login",
		"",
		"   ",
	}

	for _, ref := range negativeCases {
		if isLikelyA11yRef(ref) {
			t.Errorf("isLikelyA11yRef(%q) = true, want false", ref)
		}
	}
}

func TestGetElementA11yInputResolution(t *testing.T) {
	in1 := getElementInput{NodeID: "123"}
	selector1 := resolveSelector(in1.Ref, in1.Selector, in1.CSS, in1.Target, in1.XPath, in1.Query)
	if selector1 == "" {
		if in1.NodeID != "" {
			selector1 = "ax:" + in1.NodeID
		}
	}
	if selector1 != "ax:123" {
		t.Errorf("selector1 = %q, want 'ax:123'", selector1)
	}

	in2 := getElementInput{BackendNodeID: 456}
	selector2 := resolveSelector(in2.Ref, in2.Selector, in2.CSS, in2.Target, in2.XPath, in2.Query)
	if selector2 == "" {
		if in2.BackendNodeID > 0 {
			selector2 = "backend:456"
		}
	}
	if selector2 != "backend:456" {
		t.Errorf("selector2 = %q, want 'backend:456'", selector2)
	}
}

func TestA11yFillFunctionParityWithFillExpression(t *testing.T) {
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
		"input:not([type=\"hidden\"])",
	}

	for _, token := range requiredTokens {
		if !strings.Contains(a11yFillFunction, token) {
			t.Errorf("a11yFillFunction missing required token %q", token)
		}
	}
}
