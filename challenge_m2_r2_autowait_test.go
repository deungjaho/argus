package main

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestChallengerAutowaitNoWaitOnA11yVsCSS verifies the execution paths and timing
// of autoWaitForElement with opts.NoWait=true across a11y references and CSS selectors.
func TestChallengerAutowaitNoWaitOnA11yVsCSS(t *testing.T) {
	testTarget := "target_challenger_nowait_matrix"
	defer clearA11yCache(testTarget)

	var evalCallCount int32
	var boxModelCallCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{}
		ws, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()

		for {
			var req map[string]any
			if err := ws.ReadJSON(&req); err != nil {
				return
			}
			id, _ := req["id"].(float64)
			method, _ := req["method"].(string)
			params, _ := req["params"].(map[string]any)

			var result any = map[string]any{}

			switch method {
			case "Runtime.evaluate":
				atomic.AddInt32(&evalCallCount, 1)
				expr, _ := params["expression"].(string)
				if strings.Contains(expr, "#submit-btn") {
					result = map[string]any{
						"result": map[string]any{
							"value": map[string]any{
								"found":   true,
								"visible": true,
								"enabled": true,
								"x":       120.0,
								"y":       80.0,
								"rect":    map[string]any{"x": 100.0, "y": 60.0, "width": 40.0, "height": 40.0},
							},
						},
					}
				} else if strings.Contains(expr, "#not-found") {
					result = map[string]any{
						"result": map[string]any{
							"value": map[string]any{
								"found": false,
							},
						},
					}
				} else {
					result = map[string]any{
						"result": map[string]any{
							"value": map[string]any{"found": false},
						},
					}
				}

			case "DOM.getBoxModel":
				atomic.AddInt32(&boxModelCallCount, 1)
				backendID, _ := params["backendNodeId"].(float64)
				switch int(backendID) {
				case 901: // Positive dimensions: 60x40, center at (150, 200)
					result = map[string]any{
						"model": map[string]any{
							"width":   60,
							"height":  40,
							"content": []float64{120, 180, 180, 180, 180, 220, 120, 220},
						},
					}
				case 902: // Collapsed: 0x0
					result = map[string]any{
						"model": map[string]any{
							"width":   0,
							"height":  0,
							"content": []float64{100, 100, 100, 100, 100, 100, 100, 100},
						},
					}
				default:
					result = map[string]any{
						"model": map[string]any{
							"width":   0,
							"height":  0,
							"content": []float64{0, 0, 0, 0, 0, 0, 0, 0},
						},
					}
				}
			case "DOM.scrollIntoViewIfNeeded":
				result = map[string]any{}
			}

			resp := map[string]any{
				"id":     int(id),
				"result": result,
			}
			if err := ws.WriteJSON(resp); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/page/" + testTarget

	// Seed cache with:
	// ax:801 (backend: 901) -> valid positive box model
	// ax:802 (backend: 902) -> collapsed 0x0 box model
	storeA11yTree(testTarget, []map[string]any{
		{
			"nodeId":           "801",
			"backendDOMNodeId": 901,
			"role":             map[string]any{"value": "button"},
			"name":             map[string]any{"value": "Save"},
		},
		{
			"nodeId":           "802",
			"backendDOMNodeId": 902,
			"role":             map[string]any{"value": "dialog"},
			"name":             map[string]any{"value": "Collapsed Modal"},
		},
	})

	ctx := context.Background()

	// 1. A11y ref "ax:801" with NoWait=true (valid positive box model)
	t.Run("A11yPrefixedNoWaitPositive", func(t *testing.T) {
		atomic.StoreInt32(&evalCallCount, 0)
		atomic.StoreInt32(&boxModelCallCount, 0)

		start := time.Now()
		x, y, info, err := autoWaitForElement(ctx, wsURL, "ax:801", "", autoWaitOptions{NoWait: true})
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if elapsed > 200*time.Millisecond {
			t.Errorf("NoWait=true took %v, expected < 200ms", elapsed)
		}
		if math.Abs(x-150.0) > 1e-6 || math.Abs(y-200.0) > 1e-6 {
			t.Errorf("got center (%f, %f), want (150, 200)", x, y)
		}
		if info == nil || info["visible"] != true || info["backend_dom_node_id"] != 901 {
			t.Errorf("unexpected info: %+v", info)
		}
		if atomic.LoadInt32(&evalCallCount) != 0 {
			t.Errorf("Runtime.evaluate was invoked %d times, expected 0 for a11y ref", atomic.LoadInt32(&evalCallCount))
		}
		if atomic.LoadInt32(&boxModelCallCount) != 1 {
			t.Errorf("DOM.getBoxModel called %d times, expected 1", atomic.LoadInt32(&boxModelCallCount))
		}
	})

	// 2. Numeric bare a11y ref "801" with NoWait=true
	t.Run("BareNumericNoWaitPositive", func(t *testing.T) {
		atomic.StoreInt32(&evalCallCount, 0)
		atomic.StoreInt32(&boxModelCallCount, 0)

		start := time.Now()
		x, y, info, err := autoWaitForElement(ctx, wsURL, "801", "", autoWaitOptions{NoWait: true})
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if elapsed > 200*time.Millisecond {
			t.Errorf("NoWait=true took %v, expected < 200ms", elapsed)
		}
		if math.Abs(x-150.0) > 1e-6 || math.Abs(y-200.0) > 1e-6 {
			t.Errorf("got center (%f, %f), want (150, 200)", x, y)
		}
		if info == nil || info["visible"] != true {
			t.Errorf("unexpected info: %+v", info)
		}
		if atomic.LoadInt32(&evalCallCount) != 0 {
			t.Errorf("Runtime.evaluate was invoked %d times, expected 0 for bare numeric a11y ref", atomic.LoadInt32(&evalCallCount))
		}
	})

	// 3. Direct backend reference "backend:901" with NoWait=true
	t.Run("BackendRefNoWaitPositive", func(t *testing.T) {
		atomic.StoreInt32(&evalCallCount, 0)

		start := time.Now()
		x, y, _, err := autoWaitForElement(ctx, wsURL, "backend:901", "", autoWaitOptions{NoWait: true})
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if elapsed > 200*time.Millisecond {
			t.Errorf("NoWait=true took %v, expected < 200ms", elapsed)
		}
		if math.Abs(x-150.0) > 1e-6 || math.Abs(y-200.0) > 1e-6 {
			t.Errorf("got center (%f, %f), want (150, 200)", x, y)
		}
		if atomic.LoadInt32(&evalCallCount) != 0 {
			t.Errorf("Runtime.evaluate was invoked %d times, expected 0 for backend ref", atomic.LoadInt32(&evalCallCount))
		}
	})

	// 4. A11y ref "ax:802" with NoWait=true on 0x0 collapsed element
	t.Run("A11yPrefixedNoWaitCollapsed", func(t *testing.T) {
		atomic.StoreInt32(&boxModelCallCount, 0)

		start := time.Now()
		_, _, _, err := autoWaitForElement(ctx, wsURL, "ax:802", "", autoWaitOptions{NoWait: true})
		elapsed := time.Since(start)

		if err == nil {
			t.Fatalf("expected error for 0x0 collapsed element with NoWait=true, got nil")
		}
		if elapsed > 200*time.Millisecond {
			t.Errorf("NoWait=true took %v, expected immediate return < 200ms", elapsed)
		}
		if !strings.Contains(err.Error(), "not ready within") {
			t.Errorf("expected 'not ready within' error, got %v", err)
		}
		if atomic.LoadInt32(&boxModelCallCount) != 1 {
			t.Errorf("expected exactly 1 box model probe before returning error, got %d", atomic.LoadInt32(&boxModelCallCount))
		}
	})

	// 5. Standard CSS selector "#submit-btn" with NoWait=true
	t.Run("CSSSelectorNoWaitPositive", func(t *testing.T) {
		atomic.StoreInt32(&evalCallCount, 0)
		atomic.StoreInt32(&boxModelCallCount, 0)

		start := time.Now()
		x, y, info, err := autoWaitForElement(ctx, wsURL, "#submit-btn", "", autoWaitOptions{NoWait: true})
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if elapsed > 200*time.Millisecond {
			t.Errorf("NoWait=true took %v, expected < 200ms", elapsed)
		}
		if x != 120.0 || y != 80.0 {
			t.Errorf("got coordinates (%f, %f), want (120, 80)", x, y)
		}
		if info == nil || info["found"] != true {
			t.Errorf("unexpected info: %+v", info)
		}
		if atomic.LoadInt32(&evalCallCount) != 1 {
			t.Errorf("Runtime.evaluate called %d times, want 1", atomic.LoadInt32(&evalCallCount))
		}
		if atomic.LoadInt32(&boxModelCallCount) != 0 {
			t.Errorf("DOM.getBoxModel called %d times, want 0 for CSS selector", atomic.LoadInt32(&boxModelCallCount))
		}
	})

	// 6. Standard CSS selector "#not-found" with NoWait=true
	t.Run("CSSSelectorNoWaitNotFound", func(t *testing.T) {
		atomic.StoreInt32(&evalCallCount, 0)

		start := time.Now()
		_, _, _, err := autoWaitForElement(ctx, wsURL, "#not-found", "", autoWaitOptions{NoWait: true})
		elapsed := time.Since(start)

		if err == nil {
			t.Fatalf("expected error for not found element, got nil")
		}
		if elapsed > 200*time.Millisecond {
			t.Errorf("NoWait=true took %v, expected immediate return < 200ms", elapsed)
		}
		if !strings.Contains(err.Error(), "element not found") {
			t.Errorf("expected 'element not found' error, got %v", err)
		}
	})
}

// TestChallengerA11yDimensionBoundaries comprehensively stress-tests autoWaitForA11yElement
// across all dimension boundaries: zero, negative, asymmetric (width=0, height>0 and vice versa),
// and minimal positive (1x1).
func TestChallengerA11yDimensionBoundaries(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		width       int
		height      int
		content     []float64
		shouldError bool
	}{
		{"BothZero", 0, 0, []float64{0, 0, 0, 0, 0, 0, 0, 0}, true},
		{"ZeroWidthPositiveHeight", 0, 50, []float64{10, 0, 10, 0, 10, 50, 10, 50}, true},
		{"PositiveWidthZeroHeight", 50, 0, []float64{0, 10, 50, 10, 50, 10, 0, 10}, true},
		{"NegativeWidthPositiveHeight", -10, 50, []float64{0, 0, 0, 0, 0, 0, 0, 0}, true},
		{"PositiveWidthNegativeHeight", 50, -10, []float64{0, 0, 0, 0, 0, 0, 0, 0}, true},
		{"MinimalPositive1x1", 1, 1, []float64{10, 10, 11, 10, 11, 11, 10, 11}, false},
		{"StandardPositive100x50", 100, 50, []float64{0, 0, 100, 0, 100, 50, 0, 50}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				u := websocket.Upgrader{}
				ws, err := u.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer ws.Close()
				for {
					var req map[string]any
					if err := ws.ReadJSON(&req); err != nil {
						return
					}
					id, _ := req["id"].(float64)
					method, _ := req["method"].(string)
					var result any = map[string]any{}
					if method == "DOM.getBoxModel" {
						result = map[string]any{
							"model": map[string]any{
								"width":   tc.width,
								"height":  tc.height,
								"content": tc.content,
							},
						}
					}
					resp := map[string]any{"id": int(id), "result": result}
					if err := ws.WriteJSON(resp); err != nil {
						return
					}
				}
			}))
			defer server.Close()

			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/page/test_boundaries"
			node := A11yNodeRef{NodeID: "node_bound", BackendDOMNodeID: 401}

			_, _, info, err := autoWaitForA11yElement(ctx, wsURL, node, autoWaitOptions{NoWait: true})
			if tc.shouldError {
				if err == nil {
					t.Fatalf("%s: expected error, got success", tc.name)
				}
			} else {
				if err != nil {
					t.Fatalf("%s: expected success, got error: %v", tc.name, err)
				}
				if info == nil || info["visible"] != true {
					t.Errorf("%s: info visible = false, want true", tc.name)
				}
			}
		})
	}
}

// TestChallengerA11yExpandingAnimation tests a dynamic expanding element (e.g. accordion/modal)
// that starts at 0x0 and expands to 100x50 after multiple polls.
// Verifies that:
// 1. With NoWait=true, it immediately fails on first probe without polling.
// 2. With NoWait=false, it polls through the 0x0 collapsed phase and succeeds when expanded.
func TestChallengerA11yExpandingAnimation(t *testing.T) {
	var probeCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{}
		ws, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			var req map[string]any
			if err := ws.ReadJSON(&req); err != nil {
				return
			}
			id, _ := req["id"].(float64)
			method, _ := req["method"].(string)
			var result any = map[string]any{}
			if method == "DOM.getBoxModel" {
				cnt := atomic.AddInt32(&probeCount, 1)
				if cnt < 3 {
					// Collapsed 0x0
					result = map[string]any{
						"model": map[string]any{
							"width":   0,
							"height":  0,
							"content": []float64{50, 50, 50, 50, 50, 50, 50, 50},
						},
					}
				} else {
					// Expanded to 100x50 at (50, 50)
					result = map[string]any{
						"model": map[string]any{
							"width":   100,
							"height":  50,
							"content": []float64{0, 0, 100, 0, 100, 50, 0, 50},
						},
					}
				}
			}
			resp := map[string]any{"id": int(id), "result": result}
			if err := ws.WriteJSON(resp); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/page/test_expanding"
	node := A11yNodeRef{NodeID: "node_expand", BackendDOMNodeID: 777}
	ctx := context.Background()

	// 1. NoWait: true should fail immediately on first probe
	t.Run("NoWaitRejectsCollapsedImmediately", func(t *testing.T) {
		atomic.StoreInt32(&probeCount, 0)
		start := time.Now()
		_, _, _, err := autoWaitForA11yElement(ctx, wsURL, node, autoWaitOptions{NoWait: true})
		elapsed := time.Since(start)

		if err == nil {
			t.Fatalf("expected error for collapsed node with NoWait=true, got nil")
		}
		if elapsed > 200*time.Millisecond {
			t.Errorf("took %v, expected < 200ms", elapsed)
		}
		if atomic.LoadInt32(&probeCount) != 1 {
			t.Errorf("probeCount = %d, want 1", atomic.LoadInt32(&probeCount))
		}
	})

	// 2. NoWait: false should poll until probeCount reaches 3 and succeeds
	t.Run("PollingSucceedsUponExpansion", func(t *testing.T) {
		atomic.StoreInt32(&probeCount, 0)
		start := time.Now()
		cx, cy, info, err := autoWaitForA11yElement(ctx, wsURL, node, autoWaitOptions{
			TimeoutMs: 1000,
			PollMs:    20,
			NoWait:    false,
		})
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("expected expansion to succeed, got error: %v", err)
		}
		if atomic.LoadInt32(&probeCount) < 3 {
			t.Errorf("probeCount = %d, want >= 3", atomic.LoadInt32(&probeCount))
		}
		if math.Abs(cx-50.0) > 1e-6 || math.Abs(cy-25.0) > 1e-6 {
			t.Errorf("got center (%f, %f), want (50, 25)", cx, cy)
		}
		if info == nil || info["visible"] != true {
			t.Errorf("unexpected info: %+v", info)
		}
		t.Logf("Polled %d times across %v to detect expansion", atomic.LoadInt32(&probeCount), elapsed)
	})
}

// TestChallengerA11yContextCancellation verifies autoWaitForA11yElement respects context
// cancellation and terminates without waiting for timeout.
func TestChallengerA11yContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{}
		ws, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			var req map[string]any
			if err := ws.ReadJSON(&req); err != nil {
				return
			}
			id, _ := req["id"].(float64)
			// Return collapsed 0x0 forever
			resp := map[string]any{
				"id": int(id),
				"result": map[string]any{
					"model": map[string]any{
						"width":   0,
						"height":  0,
						"content": []float64{0, 0, 0, 0, 0, 0, 0, 0},
					},
				},
			}
			_ = ws.WriteJSON(resp)
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/page/test_cancel"
	node := A11yNodeRef{NodeID: "node_cancel", BackendDOMNodeID: 888}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, _, err := autoWaitForA11yElement(ctx, wsURL, node, autoWaitOptions{
		TimeoutMs: 5000,
		PollMs:    30,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected cancellation error, got nil")
	}
	if !strings.Contains(err.Error(), "auto-wait cancelled") {
		t.Errorf("expected 'auto-wait cancelled' error, got %v", err)
	}
	if elapsed > 1*time.Second {
		t.Errorf("elapsed %v, expected < 1s following context cancellation", elapsed)
	}
}
