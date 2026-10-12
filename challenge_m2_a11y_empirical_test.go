package main

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestChallengerQuadCentroidMath rigorously stress-tests cdpBoxModel.Center() and
// BoundingBox() on rotated, skewed, perspective trapezoid, zero-dimension, and degenerate quads.
func TestChallengerQuadCentroidMath(t *testing.T) {
	// 1. Standard axis-aligned rectangle
	t.Run("StandardRectangle", func(t *testing.T) {
		m := &cdpBoxModel{
			Content: []float64{100, 200, 300, 200, 300, 250, 100, 250},
			Width:   200,
			Height:  50,
		}
		cx, cy, err := m.Center()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if math.Abs(cx-200.0) > 1e-6 || math.Abs(cy-225.0) > 1e-6 {
			t.Errorf("Center() = (%f, %f), want (200, 225)", cx, cy)
		}
		box := m.BoundingBox()
		if box["x"] != 100.0 || box["y"] != 200.0 || box["width"] != 200.0 || box["height"] != 50.0 {
			t.Errorf("BoundingBox() = %+v, want x=100, y=200, w=200, h=50", box)
		}
	})

	// 2. Rotated rectangles (angles: 15, 30, 45, 60, 90, 135, 180, 270 degrees) around center (150, 150)
	t.Run("RotatedRectangles", func(t *testing.T) {
		centerRefX, centerRefY := 150.0, 150.0
		halfW, halfH := 50.0, 25.0
		anglesDeg := []float64{0, 15, 30, 45, 60, 90, 120, 135, 180, 270, 315, 360}

		for _, deg := range anglesDeg {
			rad := deg * math.Pi / 180.0
			cosA := math.Cos(rad)
			sinA := math.Sin(rad)

			// 4 corners of unrotated box relative to center:
			// Top-Left: (-halfW, -halfH), Top-Right: (halfW, -halfH),
			// Bottom-Right: (halfW, halfH), Bottom-Left: (-halfW, halfH)
			corners := [][2]float64{
				{-halfW, -halfH},
				{halfW, -halfH},
				{halfW, halfH},
				{-halfW, halfH},
			}

			content := make([]float64, 8)
			for i, c := range corners {
				rx := c[0]*cosA - c[1]*sinA + centerRefX
				ry := c[0]*sinA + c[1]*cosA + centerRefY
				content[i*2] = rx
				content[i*2+1] = ry
			}

			m := &cdpBoxModel{
				Content: content,
				Width:   int(halfW * 2),
				Height:  int(halfH * 2),
			}

			cx, cy, err := m.Center()
			if err != nil {
				t.Fatalf("deg=%f: unexpected error: %v", deg, err)
			}
			if math.Abs(cx-centerRefX) > 1e-6 || math.Abs(cy-centerRefY) > 1e-6 {
				t.Errorf("deg=%f: Center() = (%f, %f), want (%f, %f)", deg, cx, cy, centerRefX, centerRefY)
			}

			box := m.BoundingBox()
			if box == nil {
				t.Fatalf("deg=%f: BoundingBox returned nil", deg)
			}
			minX := box["x"].(float64)
			maxX := box["right"].(float64)
			minY := box["y"].(float64)
			maxY := box["bottom"].(float64)

			// All 4 rotated vertices must be inside the bounding box
			for i := 0; i < 4; i++ {
				vx := content[i*2]
				vy := content[i*2+1]
				if vx < minX-1e-6 || vx > maxX+1e-6 || vy < minY-1e-6 || vy > maxY+1e-6 {
					t.Errorf("deg=%f: vertex (%f, %f) outside bounding box %+v", deg, vx, vy, box)
				}
			}
		}
	})

	// 3. Skewed / affine transformed parallelogram
	t.Run("SkewedParallelogram", func(t *testing.T) {
		m := &cdpBoxModel{
			Content: []float64{0, 0, 100, 20, 120, 100, 20, 80},
		}
		cx, cy, err := m.Center()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expectedCX := (0.0 + 100.0 + 120.0 + 20.0) / 4.0 // 60.0
		expectedCY := (0.0 + 20.0 + 100.0 + 80.0) / 4.0  // 50.0
		if math.Abs(cx-expectedCX) > 1e-6 || math.Abs(cy-expectedCY) > 1e-6 {
			t.Errorf("Center() = (%f, %f), want (%f, %f)", cx, cy, expectedCX, expectedCY)
		}
	})

	// 4. Convex trapezoid (perspective 3D projection)
	t.Run("PerspectiveTrapezoid", func(t *testing.T) {
		// Top width = 100 (50 to 150 at y=0), bottom width = 200 (0 to 200 at y=100)
		m := &cdpBoxModel{
			Content: []float64{50, 0, 150, 0, 200, 100, 0, 100},
		}
		cx, cy, err := m.Center()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if math.Abs(cx-100.0) > 1e-6 || math.Abs(cy-50.0) > 1e-6 {
			t.Errorf("Center() = (%f, %f), want (100, 50)", cx, cy)
		}
		// At y=50, trapezoid horizontal span is x in [25, 175]. Centroid cx=100 is strictly inside.
		if cx <= 25.0 || cx >= 175.0 {
			t.Errorf("Center cx=%f outside trapezoid interior at y=50", cx)
		}
	})

	// 5. Zero-dimension nodes (width=0, height=0, point 0x0)
	t.Run("ZeroDimensionNodes", func(t *testing.T) {
		// 0 width line
		mZeroW := &cdpBoxModel{Content: []float64{100, 50, 100, 50, 100, 150, 100, 150}}
		cx, cy, err := mZeroW.Center()
		if err != nil || cx != 100.0 || cy != 100.0 {
			t.Errorf("ZeroWidth Center() = (%f, %f, %v), want (100, 100, nil)", cx, cy, err)
		}
		bZeroW := mZeroW.BoundingBox()
		if bZeroW["width"] != 0.0 || bZeroW["height"] != 100.0 {
			t.Errorf("ZeroWidth BoundingBox() = %+v, want width=0, height=100", bZeroW)
		}

		// 0 height line
		mZeroH := &cdpBoxModel{Content: []float64{50, 100, 150, 100, 150, 100, 50, 100}}
		cx, cy, err = mZeroH.Center()
		if err != nil || cx != 100.0 || cy != 100.0 {
			t.Errorf("ZeroHeight Center() = (%f, %f, %v), want (100, 100, nil)", cx, cy, err)
		}
		bZeroH := mZeroH.BoundingBox()
		if bZeroH["width"] != 100.0 || bZeroH["height"] != 0.0 {
			t.Errorf("ZeroHeight BoundingBox() = %+v, want width=100, height=0", bZeroH)
		}

		// Point (0x0)
		mPoint := &cdpBoxModel{Content: []float64{100, 100, 100, 100, 100, 100, 100, 100}}
		cx, cy, err = mPoint.Center()
		if err != nil || cx != 100.0 || cy != 100.0 {
			t.Errorf("Point Center() = (%f, %f, %v), want (100, 100, nil)", cx, cy, err)
		}
		bPoint := mPoint.BoundingBox()
		if bPoint["width"] != 0.0 || bPoint["height"] != 0.0 {
			t.Errorf("Point BoundingBox() = %+v, want width=0, height=0", bPoint)
		}
	})

	// 6. Degenerate and invalid quads
	t.Run("DegenerateQuads", func(t *testing.T) {
		nilModel := &cdpBoxModel{Content: nil}
		if _, _, err := nilModel.Center(); err == nil {
			t.Errorf("nil Content Center() expected error, got nil")
		}
		if b := nilModel.BoundingBox(); b != nil {
			t.Errorf("nil Content BoundingBox() expected nil, got %+v", b)
		}

		emptyModel := &cdpBoxModel{Content: []float64{}}
		if _, _, err := emptyModel.Center(); err == nil {
			t.Errorf("empty Content Center() expected error, got nil")
		}

		shortModel := &cdpBoxModel{Content: []float64{10, 20, 30, 40, 50, 60, 70}} // 7 floats
		if _, _, err := shortModel.Center(); err == nil {
			t.Errorf("7-float Content Center() expected error, got nil")
		}

		negativeModel := &cdpBoxModel{Content: []float64{-100, -200, -50, -200, -50, -100, -100, -100}}
		cx, cy, err := negativeModel.Center()
		if err != nil || cx != -75.0 || cy != -150.0 {
			t.Errorf("Negative Content Center() = (%f, %f, %v), want (-75, -150, nil)", cx, cy, err)
		}
	})

	// 7. Zero / negative backendDOMNodeId rejection in autoWaitForA11yElement
	t.Run("InvalidBackendDOMNodeIDRejection", func(t *testing.T) {
		ctx := context.Background()

		// Case 1: BackendDOMNodeID == 0
		node0 := A11yNodeRef{NodeID: "ax_node_0", BackendDOMNodeID: 0}
		_, _, _, err0 := autoWaitForA11yElement(ctx, "ws://fake", node0, autoWaitOptions{NoWait: true})
		if err0 == nil {
			t.Errorf("autoWaitForA11yElement with BackendDOMNodeID=0 should error, got nil")
		}

		// Case 2: BackendDOMNodeID == -1
		nodeNeg := A11yNodeRef{NodeID: "ax_node_neg", BackendDOMNodeID: -1}
		_, _, _, errNeg := autoWaitForA11yElement(ctx, "ws://fake", nodeNeg, autoWaitOptions{NoWait: true})
		if errNeg == nil {
			t.Errorf("autoWaitForA11yElement with BackendDOMNodeID=-1 should error, got nil")
		}
	})
}

// TestChallengerResolveA11yNodeRefs stress-tests resolveA11yNode across all ref variants,
// standard CSS selectors, zero-dimension backend node IDs, and fallthrough semantics.
func TestChallengerResolveA11yNodeRefs(t *testing.T) {
	testTarget := "target_challenger_ax_01"
	defer clearA11yCache(testTarget)

	testWS := "ws://127.0.0.1:54321/devtools/page/" + testTarget

	tree := []map[string]any{
		{
			"nodeId":           "101",
			"backendDOMNodeId": 501,
			"role":             map[string]any{"value": "button"},
			"name":             map[string]any{"value": "Confirm"},
		},
		{
			"nodeId":           "102",
			"backendDOMNodeId": 0, // node without DOM backing
			"role":             "generic",
			"name":             "Virtual Group",
		},
		{
			"nodeId":           "103",
			"backendDOMNodeId": -10, // negative DOM node ID
			"role":             "text",
			"name":             "Static Text",
		},
	}
	storeA11yTree(testTarget, tree)

	// Subtest 1: Valid cached node lookups
	t.Run("ValidCachedNodeLookups", func(t *testing.T) {
		validQueries := []string{"ax:101", "101", "backend:501", "ax:backend:501"}
		for _, q := range validQueries {
			node, isA11y, err := resolveA11yNode(testWS, q)
			if err != nil {
				t.Errorf("query %q: unexpected error: %v", q, err)
			}
			if !isA11y {
				t.Errorf("query %q: isA11y = false, want true", q)
			}
			if node.BackendDOMNodeID != 501 {
				t.Errorf("query %q: BackendDOMNodeID = %d, want 501", q, node.BackendDOMNodeID)
			}
		}
	})

	// Subtest 2: Direct un-cached backend ID references
	t.Run("DirectUncachedBackendLookups", func(t *testing.T) {
		node, isA11y, err := resolveA11yNode(testWS, "backend:999")
		if err != nil || !isA11y || node.BackendDOMNodeID != 999 {
			t.Errorf("backend:999 resolved to %+v, isA11y=%v, err=%v", node, isA11y, err)
		}

		nodeAx, isA11yAx, errAx := resolveA11yNode(testWS, "ax:backend:888")
		if errAx != nil || !isA11yAx || nodeAx.BackendDOMNodeID != 888 {
			t.Errorf("ax:backend:888 resolved to %+v, isA11y=%v, err=%v", nodeAx, isA11yAx, errAx)
		}
	})

	// Subtest 3: Zero or negative backendDOMNodeId in cached a11y tree must error informatively
	t.Run("ZeroOrNegativeBackendDOMNodeRejection", func(t *testing.T) {
		// Node 102 has backendDOMNodeId = 0
		_, isA11y0, err0 := resolveA11yNode(testWS, "ax:102")
		if !isA11y0 {
			t.Errorf("ax:102 isA11y = false, want true")
		}
		if err0 == nil {
			t.Errorf("ax:102 expected error for backendDOMNodeId 0, got nil")
		}

		_, isA11yBare0, errBare0 := resolveA11yNode(testWS, "102")
		if !isA11yBare0 {
			t.Errorf("bare 102 isA11y = false, want true")
		}
		if errBare0 == nil {
			t.Errorf("bare 102 expected error for backendDOMNodeId 0, got nil")
		}

		// Node 103 has backendDOMNodeId = -10
		_, isA11yNeg, errNeg := resolveA11yNode(testWS, "ax:103")
		if !isA11yNeg {
			t.Errorf("ax:103 isA11y = false, want true")
		}
		if errNeg == nil {
			t.Errorf("ax:103 expected error for negative backendDOMNodeId, got nil")
		}
	})

	// Subtest 4: Non-positive direct backend: references (backend:0, backend:-5)
	t.Run("NonPositiveDirectBackendRefs", func(t *testing.T) {
		// backend:0 is not a valid positive backend ID; should fall through cleanly without panic
		_, isA11y, err := resolveA11yNode(testWS, "backend:0")
		if isA11y {
			t.Errorf("backend:0 should not resolve as valid positive a11y node, got isA11y=true, err=%v", err)
		}

		_, isA11yNeg, errNeg := resolveA11yNode(testWS, "backend:-5")
		if isA11yNeg {
			t.Errorf("backend:-5 should not resolve as valid positive a11y node, got isA11y=true, err=%v", errNeg)
		}
	})

	// Subtest 5: Standard CSS selectors MUST fall through cleanly with (isA11y=false, err=nil)
	t.Run("StandardCSSSelectorsCleanFallthrough", func(t *testing.T) {
		cssSelectors := []string{
			"#submit-button",
			".btn-primary",
			"button[type='submit']",
			"div > span:first-child",
			"input#user_name",
			"header.nav-header a.link",
			"[data-testid='login-form']",
			"form > fieldset:nth-of-type(2)",
			"body",
			"html",
		}

		for _, sel := range cssSelectors {
			node, isA11y, err := resolveA11yNode(testWS, sel)
			if err != nil {
				t.Errorf("CSS selector %q produced unexpected error: %v", sel, err)
			}
			if isA11y {
				t.Errorf("CSS selector %q incorrectly classified as a11y node: %+v", sel, node)
			}
		}
	})

	// Subtest 6: Empty strings and whitespace
	t.Run("EmptyAndWhitespaceSelectors", func(t *testing.T) {
		emptyCases := []string{"", "   ", "\t", "\n"}
		for _, q := range emptyCases {
			_, isA11y, err := resolveA11yNode(testWS, q)
			if err != nil || isA11y {
				t.Errorf("query %q: isA11y=%v, err=%v, want false, nil", q, isA11y, err)
			}
		}
	})

	// Subtest 7: Target isolation
	t.Run("TargetCacheIsolation", func(t *testing.T) {
		otherTarget := "target_challenger_ax_02"
		otherWS := "ws://127.0.0.1:54321/devtools/page/" + otherTarget

		// otherTarget has no stored cache; ax:101 must not leak from testTarget
		_, found := getA11yNode(otherTarget, "ax:101")
		if found {
			t.Errorf("target isolation breach: otherTarget found ax:101 from testTarget")
		}

		// resolveA11yNode on otherWS with dummy WS returns not found
		_, isA11y, err := resolveA11yNode(otherWS, "ax:101")
		if !isA11y || err == nil {
			t.Errorf("otherTarget ax:101 expected not found error, got isA11y=%v, err=%v", isA11y, err)
		}
	})
}

// TestChallengerA11yCacheConcurrency stress-tests the thread-safety of a11yCache
// with concurrent reads, writes, and clears.
func TestChallengerA11yCacheConcurrency(t *testing.T) {
	var wg sync.WaitGroup
	workers := 24
	targets := []string{"target_c1", "target_c2", "target_c3", "target_c4"}

	defer func() {
		for _, tgt := range targets {
			clearA11yCache(tgt)
		}
	}()

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			targetID := targets[workerID%len(targets)]

			// Concurrent stores
			nodes := []map[string]any{
				{
					"nodeId":           strconv.Itoa(workerID * 10),
					"backendDOMNodeId": workerID*10 + 1,
					"role":             "button",
					"name":             fmt.Sprintf("Btn_%d", workerID),
				},
				{
					"nodeId":           strconv.Itoa(workerID*10 + 2),
					"backendDOMNodeId": workerID*10 + 3,
					"role":             "textbox",
					"name":             fmt.Sprintf("Input_%d", workerID),
				},
			}
			storeA11yTree(targetID, nodes)

			// Concurrent reads
			_, _ = getA11yNode(targetID, fmt.Sprintf("ax:%d", workerID*10))
			_, _ = getA11yNode(targetID, strconv.Itoa(workerID*10))
			_, _ = getA11yNode(targetID, fmt.Sprintf("backend:%d", workerID*10+1))

			// Occasional clears
			if workerID%5 == 0 {
				clearA11yCache(targetID)
			}
		}(i)
	}

	wg.Wait()
}

// TestChallengerSmartLocatorJSSyntaxValidity empirically verifies with Node.js
// that all generated JavaScript expressions are strictly syntax-error-free.
func TestChallengerSmartLocatorJSSyntaxValidity(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node executable not found in PATH; skipping JS syntax validation")
	}

	testSelectors := []string{
		"input[type='text']",
		"#user-input",
		".ant-checkbox-wrapper",
		".el-checkbox",
		"[data-testid=\"custom-id\"]",
		"label.user-label",
		"button[aria-label='Submit \\'Form\\'']",
	}

	for _, sel := range testSelectors {
		exprs := []struct {
			name string
			js   string
		}{
			{"focusExpression", focusExpression(sel, true)},
			{"fillExpression", fillExpression(sel, "test-value")},
			{"elementStateExpression", elementStateExpression(sel, "text", "mark123")},
		}

		for _, ex := range exprs {
			cmd := exec.Command(nodePath, "--check", "-")
			cmd.Stdin = strings.NewReader(ex.js)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("JS Syntax Error in %s with selector %q:\nOutput: %s\nCode:\n%s", ex.name, sel, string(output), ex.js)
			}
		}
	}
}

// TestChallengerSmartLocatorE2EIsolatedChrome launches an isolated headless Chrome
// (strictly on ephemeral port, NEVER host 9224) and empirically verifies:
// 1. Transparent opacity: 0 checkbox delegation (.ant-checkbox-wrapper, .el-checkbox, label[for])
// 2. Container sniffing with multiple inputs (hidden csrf + first visible + second visible)
// 3. Nested label hierarchies
// 4. Fallback on invalid selectors
func TestChallengerSmartLocatorE2EIsolatedChrome(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping isolated Chrome E2E test in -short mode; run without -short to execute against isolated Chrome")
	}

	inst, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	// Red line assertion
	if inst.Port == 9224 {
		t.Fatalf("CRITICAL SAFETY VIOLATION: Isolated Chrome running on host port 9224!")
	}

	// Construct comprehensive test HTML DOM
	testHTML := `<!DOCTYPE html><html><head><style>
.ant-checkbox-wrapper { display: inline-flex; align-items: center; cursor: pointer; }
.ant-checkbox-input { opacity: 0; position: absolute; width: 0; height: 0; }
.el-checkbox { display: inline-block; position: relative; }
.el-checkbox__original { opacity: 0; outline: none; position: absolute; }
.card-container { padding: 10px; border: 1px solid #ccc; }
.rotated-btn { transform: rotate(45deg); width: 80px; height: 30px; }
</style></head><body>
<!-- 1. Ant Design Checkbox -->
<label class="ant-checkbox-wrapper" id="ant-box">
  <span class="ant-checkbox">
    <input type="checkbox" class="ant-checkbox-input" id="ant-input" />
    <span class="ant-checkbox-inner">Box1</span>
  </span>
  <span>Agree Ant</span>
</label>

<!-- 2. Element UI Checkbox -->
<label class="el-checkbox" id="el-box">
  <span class="el-checkbox__input">
    <input type="checkbox" class="el-checkbox__original" id="el-input" />
  </span>
  <span class="el-checkbox__label">Agree Element</span>
</label>

<!-- 3. Detached Label with for attribute and transparent input -->
<label for="detached-input" id="detached-label">Agree Detached</label>
<input type="checkbox" id="detached-input" style="opacity: 0; width: 0; height: 0;" />

<!-- 4. Container with multiple inputs (hidden csrf, first input, second input) -->
<div class="card-container" id="multi-card">
  <input type="hidden" name="csrf" value="token_xyz" />
  <input type="text" name="first_name" id="first-name" value="" placeholder="First Name" />
  <input type="text" name="last_name" id="last-name" value="" placeholder="Last Name" />
</div>

<!-- 5. Nested Label -->
<label id="nested-outer" for="nested-target">
  <span id="nested-span">
    <label id="nested-inner">Nested Label Text</label>
  </span>
</label>
<input type="text" id="nested-target" value="" />

<!-- 6. Rotated element -->
<button class="rotated-btn" id="rot-btn">Click Me</button>
</body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(testHTML))
	}))
	defer ts.Close()

	ws, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}

	// Navigate isolated Chrome page to HTTP test server
	if _, err := cdpPageNavigate(ws, ts.URL, 5*time.Second); err != nil {
		t.Fatalf("failed to navigate: %v", err)
	}

	// Test 1: Transparent Ant checkbox delegation
	t.Run("AntDesignCheckboxDelegation", func(t *testing.T) {
		res, err := cdpEvaluate(ws, elementStateExpression("#ant-input", "", ""), false)
		if err != nil {
			t.Fatalf("elementStateExpression failed: %v", err)
		}
		info, ok := res.(map[string]any)
		if !ok {
			t.Fatalf("expected map, got %T: %+v", res, res)
		}
		if found, _ := info["found"].(bool); !found {
			t.Errorf("expected ant checkbox to be found, got %+v", info)
		}
		if visible, _ := info["visible"].(bool); !visible {
			t.Errorf("expected transparent ant checkbox to delegate to visible wrapper, got visible=false: %+v", info)
		}
		tag, _ := info["tag"].(string)
		if tag != "label" {
			t.Errorf("expected tag to be delegated wrapper 'label', got %q", tag)
		}
	})

	// Test 2: Transparent Element UI checkbox delegation
	t.Run("ElementUICheckboxDelegation", func(t *testing.T) {
		res, err := cdpEvaluate(ws, elementStateExpression("#el-input", "", ""), false)
		if err != nil {
			t.Fatalf("elementStateExpression failed: %v", err)
		}
		info, ok := res.(map[string]any)
		if !ok || !info["found"].(bool) || !info["visible"].(bool) {
			t.Errorf("expected el-checkbox to delegate to visible wrapper, got %+v", info)
		}
	})

	// Test 3: Detached label for transparent input delegation
	t.Run("DetachedLabelDelegation", func(t *testing.T) {
		res, err := cdpEvaluate(ws, elementStateExpression("#detached-input", "", ""), false)
		if err != nil {
			t.Fatalf("elementStateExpression failed: %v", err)
		}
		info, ok := res.(map[string]any)
		if !ok || !info["found"].(bool) || !info["visible"].(bool) {
			t.Errorf("expected detached input to delegate to visible label[for], got %+v", info)
		}
	})

	// Test 4: Container sniffing with multiple inputs (hidden csrf + first + second)
	t.Run("ContainerSniffingMultipleInputs", func(t *testing.T) {
		// Fill container #multi-card with text "Alice"
		fillRes, err := cdpEvaluate(ws, fillExpression("#multi-card", "Alice"), false)
		if err != nil {
			t.Fatalf("fillExpression failed on container: %v", err)
		}
		if fillMap, ok := fillRes.(map[string]any); !ok || !fillMap["filled"].(bool) {
			t.Errorf("fillExpression result invalid: %+v", fillRes)
		}

		// Verify first_name received "Alice", and hidden csrf was untouched
		checkScript := `(() => ({
			csrf: document.querySelector('[name=csrf]').value,
			firstName: document.querySelector('#first-name').value,
			lastName: document.querySelector('#last-name').value
		}))()`
		valRes, err := cdpEvaluate(ws, checkScript, false)
		if err != nil {
			t.Fatalf("checkScript failed: %v", err)
		}
		vals := valRes.(map[string]any)
		if vals["csrf"] != "token_xyz" {
			t.Errorf("csrf token modified: %v", vals["csrf"])
		}
		if vals["firstName"] != "Alice" {
			t.Errorf("firstName = %q, want 'Alice'", vals["firstName"])
		}
		if vals["lastName"] != "" {
			t.Errorf("lastName = %q, want ''", vals["lastName"])
		}
	})

	// Test 5: Nested label sniffing
	t.Run("NestedLabelSniffing", func(t *testing.T) {
		// Fill outer label #nested-outer with text "Bob"
		_, err := cdpEvaluate(ws, fillExpression("#nested-outer", "Bob"), false)
		if err != nil {
			t.Fatalf("fillExpression on nested label failed: %v", err)
		}
		checkVal, _ := cdpEvaluate(ws, `document.querySelector('#nested-target').value`, false)
		if checkVal != "Bob" {
			t.Errorf("nested target input value = %v, want 'Bob'", checkVal)
		}
	})

	// Test 6: Fallback on non-existent selector
	t.Run("NonExistentSelectorFallback", func(t *testing.T) {
		res, err := cdpEvaluate(ws, elementStateExpression("#non-existent-elem-999", "", ""), false)
		if err != nil {
			t.Fatalf("elementStateExpression on non-existent failed: %v", err)
		}
		info := res.(map[string]any)
		if found, _ := info["found"].(bool); found {
			t.Errorf("non-existent element reported found=true: %+v", info)
		}
	})
}

// TestAutoWaitForElementA11yNoWaitDelegation verifies Finding 1:
// autoWaitForElement with an a11y reference and NoWait: true delegates
// to autoWaitForA11yElement instead of invoking resolveElementPoint.
func TestAutoWaitForElementA11yNoWaitDelegation(t *testing.T) {
	testTarget := "target_nowait_delegation_01"
	defer clearA11yCache(testTarget)

	// Spin up mock CDP WebSocket server
	mock := newMockCDPServerWithTarget(t, testTarget)
	defer mock.close()

	// Seed cache with a11y node 701 -> backendDOMNodeId 901
	storeA11yTree(testTarget, []map[string]any{
		{
			"nodeId":           "701",
			"backendDOMNodeId": 901,
			"role":             map[string]any{"value": "button"},
			"name":             map[string]any{"value": "Submit"},
		},
	})

	ctx := context.Background()
	opts := autoWaitOptions{NoWait: true}

	// Case 1: ref with "ax:701" and NoWait: true
	// Must NOT fail with syntax error in querySelector.
	// Since mock server returns empty responses for getBoxModel,
	// it will report not ready error, confirming autoWaitForA11yElement was reached.
	_, _, _, err := autoWaitForElement(ctx, mock.wsURL, "ax:701", "", opts)
	if err == nil {
		t.Errorf("expected not ready error from mock, got nil")
	}
	if strings.Contains(err.Error(), "SyntaxError") || strings.Contains(err.Error(), "not a valid selector") {
		t.Fatalf("Finding 1 regression: autoWaitForElement threw querySelector syntax error: %v", err)
	}

	// Case 2: Standard CSS selector with NoWait: true continues to reach resolveElementPoint
	_, _, _, errCSS := autoWaitForElement(ctx, mock.wsURL, "#standard-id", "", opts)
	if errCSS == nil {
		t.Errorf("expected element not found from mock CDP evaluate, got nil")
	}
}

// TestAutoWaitForA11yElementZeroDimensionRejection verifies Finding 3:
// Collapsed nodes with Width=0 or Height=0 are rejected and not marked visible.
func TestAutoWaitForA11yElementZeroDimensionRejection(t *testing.T) {
	// 1. Invalid backendDOMNodeId
	nodeInvalid := A11yNodeRef{NodeID: "ax_invalid", BackendDOMNodeID: 0}
	ctx := context.Background()
	_, _, _, errInvalid := autoWaitForA11yElement(ctx, "ws://fake", nodeInvalid, autoWaitOptions{NoWait: true})
	if errInvalid == nil {
		t.Errorf("BackendDOMNodeID=0 should error immediately")
	}

	// 2. Mock server returning collapsed box model (Width=0, Height=0)
	mockZero := newMockCDPServerWithBoxModel(t, "target_zero_dim", 0, 0, []float64{10, 20, 10, 20, 10, 20, 10, 20})
	defer mockZero.close()

	nodeZero := A11yNodeRef{NodeID: "ax_zero", BackendDOMNodeID: 1001}
	_, _, _, errZero := autoWaitForA11yElement(ctx, mockZero.wsURL, nodeZero, autoWaitOptions{NoWait: true})
	if errZero == nil {
		t.Errorf("expected error for zero-dimension element, got nil")
	}
	if !strings.Contains(errZero.Error(), "not ready within") {
		t.Errorf("expected 'not ready within' error, got %v", errZero)
	}

	// 3. Mock server returning non-collapsed box model (Width=20, Height=20)
	mockValid := newMockCDPServerWithBoxModel(t, "target_valid_dim", 20, 20, []float64{10, 20, 30, 20, 30, 40, 10, 40})
	defer mockValid.close()

	nodeValid := A11yNodeRef{NodeID: "ax_valid", BackendDOMNodeID: 1002}
	cx, cy, info, errValid := autoWaitForA11yElement(ctx, mockValid.wsURL, nodeValid, autoWaitOptions{NoWait: true})
	if errValid != nil {
		t.Fatalf("expected valid element to succeed, got error: %v", errValid)
	}
	if info == nil || info["visible"] != true {
		t.Errorf("expected visible=true, got info: %+v", info)
	}
	if math.Abs(cx-20.0) > 1e-6 || math.Abs(cy-30.0) > 1e-6 {
		t.Errorf("Center() = (%f, %f), want (20, 30)", cx, cy)
	}
}

func newMockCDPServerWithBoxModel(t *testing.T, targetID string, width, height int, content []float64) *mockCDPServer {
	m := &mockCDPServer{
		t:        t,
		upgrader: websocket.Upgrader{},
	}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/devtools/") {
			http.NotFound(w, r)
			return
		}
		ws, err := m.upgrader.Upgrade(w, r, nil)
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
						"width":   width,
						"height":  height,
						"content": content,
					},
				}
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
	m.wsURL = "ws" + strings.TrimPrefix(m.server.URL, "http") + "/devtools/page/" + targetID
	return m
}

// TestRemoteObjectReleaseOnResolvedNodes verifies Finding 4:
// Runtime.releaseObject is invoked when RemoteObjects are resolved for a11y nodes.
func TestRemoteObjectReleaseOnResolvedNodes(t *testing.T) {
	var releasedObjects []string
	var mu sync.Mutex

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
			case "DOM.resolveNode":
				result = map[string]any{
					"object": map[string]any{
						"objectId": "mock_obj_999",
					},
				}
			case "Runtime.callFunctionOn":
				result = map[string]any{
					"result": map[string]any{
						"value": map[string]any{"ok": true},
					},
				}
			case "Runtime.releaseObject":
				mu.Lock()
				if objID, ok := params["objectId"].(string); ok {
					releasedObjects = append(releasedObjects, objID)
				}
				mu.Unlock()
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

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/page/test_release_target"

	// Call cdpResolveNode and defer releaseObject
	func() {
		objID, err := cdpResolveNode(wsURL, 501)
		if err != nil {
			t.Fatalf("cdpResolveNode failed: %v", err)
		}
		defer cdpRequest(wsURL, "Runtime.releaseObject", map[string]any{"objectId": objID}, nil)
		_, _ = cdpCallFunctionOn(wsURL, objID, "function() { return true; }", nil, true)
	}()

	mu.Lock()
	defer mu.Unlock()
	if len(releasedObjects) != 1 || releasedObjects[0] != "mock_obj_999" {
		t.Errorf("expected mock_obj_999 to be released, got %v", releasedObjects)
	}
}
