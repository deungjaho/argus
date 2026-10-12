//go:build integration

package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// S1: High-Concurrency Stress Flooding
// Spawns 10 concurrent goroutines executing simultaneous tool calls against the isolated Chrome instance.
// Verifies CDP client thread-safety, connection multiplexing, zero data races under -race, and high throughput.
func TestE2E_Tier5_S1_ConcurrentWorkerFlooding(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	// Baseline navigation
	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/controlled-react")})
	assertE2ESuccess(t, navRes, "navigate to controlled-react fixture")

	const workerCount = 10
	const iterationsPerWorker = 5

	var wg sync.WaitGroup
	var errorCount int64

	startBarrier := make(chan struct{})

	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			<-startBarrier

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			for i := 0; i < iterationsPerWorker; i++ {
				// Alternating tools: eval, get_element, status
				switch i % 3 {
				case 0:
					res, err := callMCPToolRaw(ctx, cs, "eval", map[string]any{
						"expression": "document.title",
					})
					if err != nil || res.IsError {
						atomic.AddInt64(&errorCount, 1)
					}
				case 1:
					res, err := callMCPToolRaw(ctx, cs, "get_element", map[string]any{
						"selector": "#react-username",
					})
					if err != nil || res.IsError {
						atomic.AddInt64(&errorCount, 1)
					}
				case 2:
					res, err := callMCPToolRaw(ctx, cs, "status", map[string]any{})
					if err != nil || res.IsError {
						atomic.AddInt64(&errorCount, 1)
					}
				}
			}
		}(w)
	}

	// Release all workers simultaneously
	close(startBarrier)
	wg.Wait()

	if errorCount > 0 {
		t.Fatalf("concurrent worker flooding encountered %d failed tool calls", errorCount)
	}
}

// S2: Sudden Mid-Flight Tab Destruction
// Opens a new tab, triggers operations, and rapidly closes the target tab.
// Verifies no deadlocks, panics, or hung goroutines, and confirms remaining tabs remain fully operational.
func TestE2E_Tier5_S2_MidFlightTabDestruction(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	// Tab 1: base tab
	navRes1 := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/complex-app")})
	assertE2ESuccess(t, navRes1, "tab 1 navigate")

	// Query Tab 1 ID
	listRes1 := callMCPTool(t, cs, "tab", map[string]any{"action": "list"})
	assertE2ESuccess(t, listRes1, "list tabs")
	tabs1, _ := extractToolResultJSON(t, listRes1)["tabs"].([]any)
	if len(tabs1) == 0 {
		t.Fatalf("expected at least 1 tab")
	}
	t0, _ := tabs1[0].(map[string]any)
	tab1ID, _ := t0["id"].(string)

	// Create Tab 2
	newTabRes := callMCPTool(t, cs, "tab", map[string]any{
		"action": "new",
		"url":    fixtureURL(server.URL, "/fixtures/dialogs"),
	})
	assertE2ESuccess(t, newTabRes, "create tab 2")
	newTabData := extractToolResultJSON(t, newTabRes)
	tab2ID, _ := newTabData["tab_id"].(string)
	if tab2ID == "" {
		tab2ID, _ = newTabData["id"].(string)
	}
	if tab2ID == "" {
		t.Fatalf("expected valid tab_id for tab 2")
	}

	// Immediately close Tab 2
	closeRes := callMCPTool(t, cs, "tab", map[string]any{
		"action": "close",
		"tab_id": tab2ID,
	})
	assertE2ESuccess(t, closeRes, "close tab 2 immediately")

	// Allow Chrome's asynchronous target destruction to settle
	time.Sleep(150 * time.Millisecond)

	// Attempt tool call on Tab 2 after closure: must return clean error without panic
	staleRes := callMCPTool(t, cs, "tab", map[string]any{
		"action": "select",
		"tab_id": tab2ID,
	})
	assertE2EError(t, staleRes, "selecting closed tab must fail cleanly")

	// Re-select Tab 1
	select1Res := callMCPTool(t, cs, "tab", map[string]any{
		"action": "select",
		"tab_id": tab1ID,
	})
	assertE2ESuccess(t, select1Res, "re-select tab 1")

	// Verify Tab 1 is still completely healthy and responsive
	evalRes := callMCPTool(t, cs, "eval", map[string]any{
		"expression": "document.location.pathname",
	})
	assertE2ESuccess(t, evalRes, "eval on tab 1 after tab 2 destruction")
}

// S3: Extreme DOM Scale Stress
// Injects 5,000 DOM nodes via eval and tests element queries, DOM snapshots, and accessibility trees.
// Verifies memory stability, parser limits, and bounded execution time without browser crash.
func TestE2E_Tier5_S3_ExtremeDOMScaleStress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/controlled-react")})
	assertE2ESuccess(t, navRes, "navigate to controlled-react")

	// Inject 5,000 DOM nodes into the page
	injectScript := `(function() {
		const container = document.createElement('div');
		container.id = 'stress-scale-root';
		for (let i = 0; i < 5000; i++) {
			const item = document.createElement('span');
			item.id = 'stress-node-' + i;
			item.className = 'stress-item';
			item.textContent = 'Node item ' + i;
			container.appendChild(item);
		}
		document.body.appendChild(container);
		return 'injected_5000_nodes';
	})()`

	startInject := time.Now()
	evalRes := callMCPTool(t, cs, "eval", map[string]any{"expression": injectScript})
	assertE2ESuccess(t, evalRes, "inject 5000 nodes")
	t.Logf("injected 5000 DOM nodes in %v", time.Since(startInject))

	// Step 1: Query element at the tail of the 5,000 node list
	startElem := time.Now()
	elemRes := callMCPTool(t, cs, "get_element", map[string]any{"selector": "#stress-node-4999"})
	assertE2ESuccess(t, elemRes, "get_element on 5000th node")
	t.Logf("retrieved deep node #stress-node-4999 in %v", time.Since(startElem))
	elemData := extractToolResultJSON(t, elemRes)
	outerHTML := getElementHTML(elemData)
	if !strings.Contains(outerHTML, "stress-node-4999") {
		t.Fatalf("expected node 4999 in outer_html, got: %s", outerHTML)
	}

	// Step 2: DOM snapshot under massive tree
	startSnap := time.Now()
	snapRes := callMCPTool(t, cs, "snapshot", map[string]any{})
	assertE2ESuccess(t, snapRes, "snapshot on massive tree")
	t.Logf("snapshot of 5000 nodes taken in %v", time.Since(startSnap))

	// Step 3: Accessibility snapshot under massive tree
	startA11y := time.Now()
	a11yRes := callMCPTool(t, cs, "a11y_snapshot", map[string]any{})
	assertE2ESuccess(t, a11yRes, "a11y_snapshot on massive tree")
	t.Logf("a11y_snapshot on massive tree taken in %v", time.Since(startA11y))
}

// S4: Barrier Penetration & Input Fuzzing
// Submits malformed and boundary inputs across consolidated tools.
// Verifies graceful error responses, parameter validation, and zero server panics.
func TestE2E_Tier5_S4_BarrierPenetrationFuzzing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)
	cs := setupMCPServer(t)

	navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/controlled-react")})
	assertE2ESuccess(t, navRes, "navigate to controlled-react")

	// Fuzz 1: batch_actions with empty actions array must return validation error
	batchEmptyRes := callMCPTool(t, cs, "batch_actions", map[string]any{"actions": []any{}})
	assertE2EError(t, batchEmptyRes, "batch_actions with empty array must return error cleanly")
	if !strings.Contains(extractToolResultText(t, batchEmptyRes), "no actions provided in batch") {
		t.Errorf("expected 'no actions provided in batch', got: %s", extractToolResultText(t, batchEmptyRes))
	}

	// Fuzz 2: batch_actions with unsupported action type
	batchBadKeyRes := callMCPTool(t, cs, "batch_actions", map[string]any{
		"actions": []map[string]any{
			{"action": "unsupported_action_type_fuzz_9999", "target": "#react-username"},
		},
	})
	assertE2EError(t, batchBadKeyRes, "batch_actions with unsupported action type must return error cleanly")

	// Fuzz 3: mock_route with unclosed regex pattern
	mockBadRegexRes := callMCPTool(t, cs, "mock_route", map[string]any{
		"pattern": "^[a-z(unclosed_regex_syntax",
		"action":  "mock",
		"body":    "data",
	})
	assertE2EError(t, mockBadRegexRes, "mock_route with unclosed regex must return error cleanly")

	// Fuzz 4: storage with invalid action
	storeBadActRes := callMCPTool(t, cs, "storage", map[string]any{
		"action": "invalid_action_fuzz",
		"type":   "local",
	})
	assertE2EError(t, storeBadActRes, "storage with invalid action must return error cleanly")

	// Fuzz 5: storage with query on nonexistent key (must succeed with empty value, not crash)
	storeMissRes := callMCPTool(t, cs, "storage", map[string]any{
		"action": "get",
		"type":   "local",
		"key":    "non_existent_key_fuzz_12345",
	})
	assertE2ESuccess(t, storeMissRes, "storage get missing key must return cleanly")

	// Fuzz 6: tab with non-existent tab ID
	tabBadIDRes := callMCPTool(t, cs, "tab", map[string]any{
		"action": "select",
		"tab_id": "DEADBEEF_NON_EXISTENT_TAB_ID",
	})
	assertE2EError(t, tabBadIDRes, "tab select non-existent ID must return error cleanly")

	// Fuzz 7: get_element with negative coordinates
	elemNegCoordsRes := callMCPTool(t, cs, "get_element", map[string]any{
		"x": -9999,
		"y": -9999,
	})
	assertE2ESuccess(t, elemNegCoordsRes, "get_element with negative coordinates returns found:false or empty without panic")
}

// S5: Rapid Lifecycle Churn
// Rapidly loops setup, tool execution, and teardown across sequential MCP server instances.
// Verifies no connection leakage, socket exhaustion, or orphaned background routines.
func TestE2E_Tier5_S5_RapidLifecycleChurn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	_, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	server := startE2EFixtureServer(t)

	const churnIterations = 10

	for i := 0; i < churnIterations; i++ {
		cs := setupMCPServer(t)

		navRes := callMCPTool(t, cs, "navigate", map[string]any{"url": fixtureURL(server.URL, "/fixtures/complex-app")})
		assertE2ESuccess(t, navRes, "churn navigate")

		evalRes := callMCPTool(t, cs, "eval", map[string]any{"expression": "1 + 1"})
		assertE2ESuccess(t, evalRes, "churn eval")
		evalData := extractToolResultJSON(t, evalRes)
		if fmt.Sprint(evalData["result"]) != "2" {
			t.Fatalf("expected result '2', got %v", evalData["result"])
		}

		_ = cs.Close()
	}
}
