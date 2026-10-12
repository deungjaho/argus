package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =========================================================================
// CHALLENGE 1: Dialog Stale Token, Condition Wait & Delayed Arrival Stress
// =========================================================================

// TestChallenger_DialogStaleTokenPrematureWakeupImmunity verifies that pre-buffered
// or spurious tokens in notifyCh do NOT cause serverHandleDialog to wake up early when Pending is nil.
func TestChallenger_DialogStaleTokenPrematureWakeupImmunity(t *testing.T) {
	dialogMgr.Reset()
	targetID := "challenger_stale_token_target"
	wsURL := "ws://test/" + targetID
	state := dialogMgr.GetOrCreate(targetID, wsURL)
	state.SetPolicy(DialogPolicyNone, "")
	setCurrentTargetID(targetID)
	defer clearCurrentTargetID()

	// Inject multiple pre-buffered tokens (buffered channel has capacity 1)
	for i := 0; i < 5; i++ {
		select {
		case state.notifyCh <- struct{}{}:
		default:
		}
	}

	start := time.Now()
	timeoutMs := 150
	_, _, err := serverHandleDialog(context.Background(), handleDialogInput{
		Action:    "accept",
		TimeoutMs: timeoutMs,
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error during serverHandleDialog: %v", err)
	}

	// It must wait for the full timeout and not wake up immediately
	minWait := 120 * time.Millisecond
	if elapsed < minWait {
		t.Fatalf("premature wakeup detected: woke up in %v, expected at least %v", elapsed, minWait)
	}

	// Verify notifyCh remains clean
	select {
	case <-state.notifyCh:
		t.Fatal("expected notifyCh to be empty after waiting on non-existent dialog")
	default:
	}
}

// TestChallenger_DialogDelayedArrivalTimingMatrix stress-tests prompt wakeup across
// delayed dialog arrivals: 50ms, 100ms, and 200ms, with pre-existing stale tokens.
func TestChallenger_DialogDelayedArrivalTimingMatrix(t *testing.T) {
	delays := []time.Duration{
		50 * time.Millisecond,
		100 * time.Millisecond,
		200 * time.Millisecond,
	}

	for _, delay := range delays {
		t.Run(fmt.Sprintf("Delayed_%v", delay), func(t *testing.T) {
			dialogMgr.Reset()
			targetID := fmt.Sprintf("delayed_target_%v", delay.Milliseconds())
			wsURL := "ws://test/" + targetID
			state := dialogMgr.GetOrCreate(targetID, wsURL)
			state.SetPolicy(DialogPolicyNone, "")
			setCurrentTargetID(targetID)
			defer clearCurrentTargetID()

			// Pre-pollute notifyCh with a stale token before wait starts
			select {
			case state.notifyCh <- struct{}{}:
			default:
			}

			wakeupChan := make(chan struct {
				elapsed time.Duration
				res     map[string]any
				err     error
			}, 1)

			go func() {
				start := time.Now()
				_, out, err := serverHandleDialog(context.Background(), handleDialogInput{
					Action:    "accept",
					TimeoutMs: 1500, // Generous timeout
				})
				resMap, _ := out.(map[string]any)
				wakeupChan <- struct {
					elapsed time.Duration
					res     map[string]any
					err     error
				}{
					elapsed: time.Since(start),
					res:     resMap,
					err:     err,
				}
			}()

			// Sleep for the specified delay before firing dialog
			time.Sleep(delay)

			dialogMsg := fmt.Sprintf("Delayed Dialog Message at %v", delay)
			state.SetPending(&DialogInfo{
				Type:     "confirm",
				Message:  dialogMsg,
				OpenedAt: time.Now(),
			})

			select {
			case r := <-wakeupChan:
				if r.err != nil {
					t.Fatalf("unexpected error: %v", r.err)
				}
				// Verify minimum wait
				minExpected := delay - 15*time.Millisecond
				if r.elapsed < minExpected {
					t.Fatalf("woke up too early (%v < %v)", r.elapsed, minExpected)
				}
				// Verify prompt wakeup within reasonable headroom (delay + 300ms for race/scheduling jitter)
				maxExpected := delay + 350*time.Millisecond
				if r.elapsed > maxExpected {
					t.Fatalf("wakeup delayed too long (%v > %v)", r.elapsed, maxExpected)
				}
				// Verify dialog payload
				d, ok := r.res["dialog"].(*DialogInfo)
				if !ok || d == nil || d.Message != dialogMsg {
					t.Fatalf("expected dialog message %q, got %+v", dialogMsg, r.res["dialog"])
				}
				if r.res["handled"] != true || r.res["accepted"] != true {
					t.Fatalf("dialog was not marked handled/accepted: %+v", r.res)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("waiting loop hung and failed to unblock for delay %v", delay)
			}
		})
	}
}

// TestChallenger_DialogConcurrentStressFuzzing stress-tests concurrent SetPending,
// MarkHandled, MarkClosed, DrainNotify, and readers across 16 goroutines.
func TestChallenger_DialogConcurrentStressFuzzing(t *testing.T) {
	dialogMgr.Reset()
	targetID := "challenger_concurrent_fuzz_target"
	wsURL := "ws://test/" + targetID
	state := dialogMgr.GetOrCreate(targetID, wsURL)

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var opCount int64

	// 4 Producers: Rapid SetPending
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			i := 0
			for ctx.Err() == nil && i < 200 {
				info := &DialogInfo{
					Type:          "prompt",
					Message:       fmt.Sprintf("Concurrent msg %d-%d", id, i),
					DefaultPrompt: fmt.Sprintf("val-%d", i),
					OpenedAt:      time.Now(),
				}
				state.SetPending(info)
				atomic.AddInt64(&opCount, 1)
				i++
				time.Sleep(100 * time.Microsecond)
			}
		}(g)
	}

	// 4 Consumers: Rapid MarkHandled
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for ctx.Err() == nil {
				state.MarkHandled(id%2 == 0, fmt.Sprintf("resp-%d", id), false)
				atomic.AddInt64(&opCount, 1)
				time.Sleep(150 * time.Microsecond)
			}
		}(g)
	}

	// 4 Closers: Rapid MarkClosed
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for ctx.Err() == nil {
				state.MarkClosed(id%2 == 1, fmt.Sprintf("closed-%d", id))
				atomic.AddInt64(&opCount, 1)
				time.Sleep(200 * time.Microsecond)
			}
		}(g)
	}

	// 4 Observers: Rapid state readers and DrainNotify
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for ctx.Err() == nil {
				_ = state.Pending()
				_ = state.HasPending()
				_ = state.LastHandled()
				h := state.History()
				if len(h) > 10 {
					t.Errorf("history exceeded maximum bounded size of 10: got %d", len(h))
				}
				if id == 0 {
					state.DrainNotify()
				}
				atomic.AddInt64(&opCount, 1)
				time.Sleep(100 * time.Microsecond)
			}
		}(g)
	}

	wg.Wait()

	totalOps := atomic.LoadInt64(&opCount)
	if totalOps < 100 {
		t.Fatalf("expected high concurrent throughput, only executed %d operations", totalOps)
	}

	// Verify history invariants after stress
	hist := state.History()
	if len(hist) > 10 {
		t.Fatalf("final history size %d exceeds 10", len(hist))
	}
}

// =========================================================================
// CHALLENGE 2: Dev Overlay Precision & Real Error Overlays Stress
// =========================================================================

// TestChallenger_DevOverlay_StandardAlertFalsePositiveImmunity verifies via Node.js
// that a wide variety of standard accessible alerts, form error messages, and framework UI banners
// are NEVER falsely detected as dev crash overlays.
func TestChallenger_DevOverlay_StandardAlertFalsePositiveImmunity(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node executable not found in PATH; skipping node test")
	}

	// Comprehensive test suite of non-crash form alerts and banners
	testCases := []struct {
		Name     string
		Elements string
	}{
		{
			Name: "Bootstrap Alert Danger",
			Elements: `[{
				tag: 'div',
				classes: ['alert', 'alert-danger'],
				attrs: { role: 'alert' },
				innerText: 'Please enter a valid email address.'
			}]`,
		},
		{
			Name: "Ant Design Form Alert Error",
			Elements: `[{
				tag: 'div',
				classes: ['ant-alert', 'ant-alert-error', 'ant-alert-with-description'],
				attrs: { role: 'alert' },
				innerText: 'Validation Failed: Password must be at least 8 characters.'
			}]`,
		},
		{
			Name: "Element UI Error Alert",
			Elements: `[{
				tag: 'div',
				classes: ['el-alert', 'el-alert--error', 'is-light'],
				attrs: { role: 'alert' },
				innerText: 'Operation timed out. Please retry.'
			}]`,
		},
		{
			Name: "Generic Form Field Error Message",
			Elements: `[{
				tag: 'span',
				classes: ['error-message', 'form-error-text', 'text-error'],
				attrs: { role: 'alert' },
				innerText: 'This field is required.'
			}]`,
		},
		{
			Name: "WAI-ARIA Live Region Alert",
			Elements: `[{
				tag: 'div',
				classes: ['live-region-error'],
				attrs: { role: 'alert', 'aria-live': 'assertive' },
				innerText: 'Invalid coupon code applied.'
			}]`,
		},
		{
			Name: "Tailwind CSS Error Banner",
			Elements: `[{
				tag: 'div',
				classes: ['bg-red-100', 'border', 'border-red-400', 'text-red-700', 'rounded'],
				attrs: { role: 'alert' },
				innerText: 'Danger: Credit card payment declined.'
			}]`,
		},
	}

	script := fmt.Sprintf(`
const detectDevOverlayJS = %s;

function evaluateOnElements(elements) {
  const document = {
    querySelector(sel) {
      const selectors = sel.split(',').map(s => s.trim());
      for (const el of elements) {
        for (const s of selectors) {
          if (s === 'vite-error-overlay' && el.tag === 'vite-error-overlay') return el;
          if (s === 'nextjs-portal' && el.tag === 'nextjs-portal') return el;
          if (s === 'div[data-react-error-overlay="true"]' && el.tag === 'div' && el.attrs?.['data-react-error-overlay'] === 'true') return el;
          if (s === '[data-error-boundary="true"]' && el.attrs?.['data-error-boundary'] === 'true') return el;
          if (s === 'div.remix-error-boundary' && el.tag === 'div' && el.classes?.includes('remix-error-boundary')) return el;
          if (s === '#__nuxt-error' && el.id === '__nuxt-error') return el;
          if (s.includes('[role="alert"]') && el.attrs?.role === 'alert') return el;
        }
      }
      return null;
    },
    querySelectorAll(sel) { return []; },
    getElementById(id) { return elements.find(e => e.id === id) || null; }
  };
  const fn = new Function('document', 'return ' + detectDevOverlayJS);
  return fn(document);
}

const testCases = %s;
for (const tc of testCases) {
  const res = evaluateOnElements(tc.elements);
  if (res.detected) {
    console.error("FAIL: " + tc.name + " falsely detected as dev overlay: " + JSON.stringify(res));
    process.exit(1);
  }
}
console.log("ALL_ALERTS_CLEAN");
`, jsString(detectDevOverlayJS), buildJSONTestCases(testCases))

	cmd := exec.Command(nodePath, "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Node false-positive verification failed: %v\nOutput: %s", err, string(out))
	}
}

// TestChallenger_DevOverlay_RealFrameworkOverlaysAccuracy verifies via Node.js
// that all genuine framework overlays (Vite, Next.js, Remix, Nuxt, React ErrorBoundary)
// are accurately detected and structured.
func TestChallenger_DevOverlay_RealFrameworkOverlaysAccuracy(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node executable not found in PATH; skipping node test")
	}

	script := fmt.Sprintf(`
const detectDevOverlayJS = %s;

// 1. Vite Overlay
{
  const viteElement = {
    tag: 'vite-error-overlay',
    shadowRoot: {
      querySelector(sel) {
        if (sel === '.plugin') return { textContent: 'vite:react-swc' };
        if (sel === '.message-body') return { textContent: 'Unexpected token (24:8)' };
        if (sel === '.file') return { textContent: 'src/components/Header.tsx:24:8' };
        if (sel === '.frame') return { textContent: '23 | return (\n24 |   <Header >\n        ^' };
        return null;
      }
    }
  };
  const doc = {
    querySelector(sel) { return sel === 'vite-error-overlay' ? viteElement : null; },
    querySelectorAll() { return []; },
    getElementById() { return null; }
  };
  const res = new Function('document', 'return ' + detectDevOverlayJS)(doc);
  if (!res.detected || res.framework !== 'vite' || !res.title.includes('vite:react-swc') || res.file_raw !== 'src/components/Header.tsx:24:8') {
    console.error("Vite detection failed:", res);
    process.exit(1);
  }
}

// 2. Remix Error Boundary
{
  const remixElement = {
    tag: 'div',
    classes: ['remix-error-boundary'],
    innerText: '500 Internal Server Error in app/routes/admin.tsx:32:15'
  };
  const doc = {
    querySelector(sel) {
      if (sel.includes('div.remix-error-boundary')) return remixElement;
      return null;
    },
    querySelectorAll() { return []; },
    getElementById() { return null; }
  };
  const res = new Function('document', 'return ' + detectDevOverlayJS)(doc);
  if (!res.detected || res.framework !== 'react_error_boundary' || !res.message.includes('500 Internal Server Error')) {
    console.error("Remix detection failed:", res);
    process.exit(2);
  }
}

// 3. Nuxt Error
{
  const nuxtElement = {
    id: '__nuxt-error',
    innerText: 'Page Not Found in pages/users/[id].vue'
  };
  const doc = {
    querySelector(sel) {
      if (sel.includes('#__nuxt-error')) return nuxtElement;
      return null;
    },
    querySelectorAll() { return []; },
    getElementById() { return null; }
  };
  const res = new Function('document', 'return ' + detectDevOverlayJS)(doc);
  if (!res.detected || res.framework !== 'react_error_boundary') {
    console.error("Nuxt detection failed:", res);
    process.exit(3);
  }
}

console.log("ALL_GENUINE_OVERLAYS_DETECTED");
`, jsString(detectDevOverlayJS))

	cmd := exec.Command(nodePath, "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Real framework overlay detection failed: %v\nOutput: %s", err, string(out))
	}
}

// TestChallenger_DevOverlay_ParseFileLocationStress tests edge cases in file location parsing.
func TestChallenger_DevOverlay_ParseFileLocationStress(t *testing.T) {
	cases := []struct {
		raw      string
		wantFile string
		wantLine int
		wantCol  int
	}{
		{"src/components/Modal.tsx:120:4", "src/components/Modal.tsx", 120, 4},
		{"webpack-internal:///./pages/index.tsx:34:12", "./pages/index.tsx", 34, 12},
		{"///Users/dev/project/src/main.ts:89:1", "Users/dev/project/src/main.ts", 89, 1},
		{"//app/routes/root.jsx:10:5", "app/routes/root.jsx", 10, 5},
		{"/lib/parser.js:55", "/lib/parser.js", 55, 0},
		{"(app/routes/index.jsx:12:3)", "app/routes/index.jsx", 12, 3},
		{"no-file-here at all", "no-file-here at all", 0, 0},
		{"", "", 0, 0},
	}

	for _, c := range cases {
		file, line, col := parseFileLocation(c.raw)
		if file != c.wantFile || line != c.wantLine || col != c.wantCol {
			t.Errorf("parseFileLocation(%q) = (%q, %d, %d), want (%q, %d, %d)",
				c.raw, file, line, col, c.wantFile, c.wantLine, c.wantCol)
		}
	}
}

func buildJSONTestCases(cases []struct {
	Name     string
	Elements string
}) string {
	var sb string
	sb = "[\n"
	for i, c := range cases {
		sb += fmt.Sprintf("  { name: %q, elements: %s }", c.Name, c.Elements)
		if i < len(cases)-1 {
			sb += ",\n"
		} else {
			sb += "\n"
		}
	}
	sb += "]"
	_ = json.Marshal
	return sb
}
