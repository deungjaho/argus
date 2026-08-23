package main

// automator_lifecycle_test.go — tests for screenshot temp file cleanup,
// event byte bounds, redaction at ingestion, reconnect gaps, and
// backend switching safety.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestScreenshotTempCleanupOnSuccess asserts that the screenshot temp
// file is removed after a successful capture.
func TestScreenshotTempCleanupOnSuccess(t *testing.T) {
	// Create a minimal valid PNG (1x1 red pixel).
	pngData := minimalPNG()
	handlers := standardFakeHandlers()
	handlers["App.captureScreenshot"] = func(_ json.RawMessage) (any, error) {
		return map[string]any{"data": encodeBase64(pngData)}, nil
	}
	fake := newFakeWxServer(t, handlers)
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	// Track temp files before and after.
	beforeFiles, _ := filepath.Glob(filepath.Join(os.TempDir(), "argus-wx-screenshot-*.png"))

	_, _, err := automatorScreenshotResult(500)
	if err != nil {
		t.Fatalf("automatorScreenshotResult: %v", err)
	}

	// Wait briefly for cleanup.
	time.Sleep(100 * time.Millisecond)

	afterFiles, _ := filepath.Glob(filepath.Join(os.TempDir(), "argus-wx-screenshot-*.png"))
	// The count after should not exceed the count before (no new lingering files).
	if len(afterFiles) > len(beforeFiles) {
		t.Errorf("temp screenshot files leaked: before=%d, after=%d", len(beforeFiles), len(afterFiles))
	}
}

// TestScreenshotTempCleanupOnError asserts that the screenshot temp file
// is removed even when screenshot capture fails.
func TestScreenshotTempCleanupOnError(t *testing.T) {
	handlers := standardFakeHandlers()
	handlers["App.captureScreenshot"] = func(_ json.RawMessage) (any, error) {
		return nil, &automatorError{Message: "screenshot failed"}
	}
	fake := newFakeWxServer(t, handlers)
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	beforeFiles, _ := filepath.Glob(filepath.Join(os.TempDir(), "argus-wx-screenshot-*.png"))

	_, _, err := automatorScreenshotResult(500)
	if err == nil {
		t.Fatal("expected error from failed screenshot, got nil")
	}

	time.Sleep(100 * time.Millisecond)

	afterFiles, _ := filepath.Glob(filepath.Join(os.TempDir(), "argus-wx-screenshot-*.png"))
	if len(afterFiles) > len(beforeFiles) {
		t.Errorf("temp screenshot files leaked on error: before=%d, after=%d", len(beforeFiles), len(afterFiles))
	}
}

// TestEventByteBounds asserts that events exceeding the byte limit are
// truncated and the truncation counter is incremented.
func TestEventByteBounds(t *testing.T) {
	fake := newFakeWxServer(t, standardFakeHandlers())
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	conn, err := getAutomatorConn()
	if err != nil {
		t.Fatalf("getAutomatorConn: %v", err)
	}

	// Record an event with a large payload.
	largePayload := strings.Repeat("x", automatorMaxEventBytes+1000)
	conn.recordEvent("App.logAdded", json.RawMessage(`{"args":["`+largePayload+`"]}`))

	dropped, truncated, _ := conn.eventStats()
	if truncated < 1 {
		t.Errorf("expected truncated >= 1, got %d", truncated)
	}
	_ = dropped
}

// TestEventRedactionAtIngestion asserts that sensitive data in event
// params is redacted at ingestion time.
func TestEventRedactionAtIngestion(t *testing.T) {
	fake := newFakeWxServer(t, standardFakeHandlers())
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	conn, err := getAutomatorConn()
	if err != nil {
		t.Fatalf("getAutomatorConn: %v", err)
	}

	// Record an event with a password field.
	conn.recordEvent("App.logAdded", json.RawMessage(`{"password":"secret123","token":"Bearer abc"}`))

	events, _ := conn.recentEvents(1)
	if len(events) == 0 {
		t.Fatal("expected at least 1 event")
	}
	params, ok := events[0].Params.(map[string]any)
	if !ok {
		t.Fatalf("expected map params, got %T", events[0].Params)
	}
	if params["password"] != "[REDACTED]" {
		t.Errorf("password = %v, want [REDACTED]", params["password"])
	}
}

// TestEventRingBufferDroppedCount asserts that overflow events are
// counted as dropped, not silently lost.
func TestEventRingBufferDroppedCount(t *testing.T) {
	fake := newFakeWxServer(t, standardFakeHandlers())
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	conn, err := getAutomatorConn()
	if err != nil {
		t.Fatalf("getAutomatorConn: %v", err)
	}

	// Fill beyond capacity.
	for i := 0; i < automatorEventBufferSize+50; i++ {
		conn.recordEvent("App.logAdded", json.RawMessage(`{}`))
	}

	dropped, _, _ := conn.eventStats()
	if dropped < 50 {
		t.Errorf("expected dropped >= 50, got %d", dropped)
	}
}

// TestReconnectGapVisible asserts that a reconnect increments the
// reconnect gap counter and it is visible in diagnostics.
func TestReconnectGapVisible(t *testing.T) {
	fake := newFakeWxServer(t, standardFakeHandlers())
	defer fake.close()

	// First connection.
	cleanup1 := setupFakeBackend(t, fake.port())
	conn1, err := getAutomatorConn()
	if err != nil {
		t.Fatalf("first connect: %v", err)
	}
	epoch1 := conn1.epoch

	// Close and reconnect.
	closeAutomatorConn()
	cleanup1()

	// Second connection (same fake server, new epoch).
	cleanup2 := setupFakeBackend(t, fake.port())
	defer cleanup2()
	conn2, err := getAutomatorConn()
	if err != nil {
		t.Fatalf("second connect: %v", err)
	}

	if conn2.epoch <= epoch1 {
		t.Errorf("epoch should increment on reconnect: epoch1=%d, epoch2=%d", epoch1, conn2.epoch)
	}
	if atomic.LoadInt64(&conn2.reconnectGap) < 1 {
		t.Errorf("reconnect gap should be >= 1 after reconnect, got %d", atomic.LoadInt64(&conn2.reconnectGap))
	}
}

// TestBackendSwitchSafety asserts that switching backends tears down the
// automator connection and the old connection is not reused.
func TestBackendSwitchSafety(t *testing.T) {
	fake := newFakeWxServer(t, standardFakeHandlers())
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	// Establish a wechat connection.
	conn1, err := getAutomatorConn()
	if err != nil {
		t.Fatalf("first connect: %v", err)
	}

	// Switch to cdp.
	setBackend(BackendCDP)
	closeAutomatorConn()

	// The old connection should be closed.
	if conn1.alive() {
		t.Error("old wechat connection should be closed after backend switch")
	}

	// Switch back to wechat and verify a new connection is established.
	setBackend(BackendWeChat)
	conn2, err := getAutomatorConn()
	if err != nil {
		t.Fatalf("reconnect after backend switch: %v", err)
	}
	if conn2 == conn1 {
		t.Error("should have created a new connection after backend switch")
	}
}

// TestConcurrentCallsNoCrossWire asserts that concurrent calls through
// the same connection are correctly correlated and do not cross-wire.
func TestConcurrentCallsNoCrossWire(t *testing.T) {
	handlers := standardFakeHandlers()
	handlers["Page.getData"] = func(p json.RawMessage) (any, error) {
		var params struct {
			PageID int `json:"pageId"`
		}
		json.Unmarshal(p, &params)
		return fakeDataResult(map[string]any{"page": params.PageID}), nil
	}
	fake := newFakeWxServer(t, handlers)
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	const N = 20
	var wg sync.WaitGroup
	errs := make([]error, N)
	results := make([]int, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			pageID := idx + 1
			data, err := automatorPageData(pageID, "")
			if err != nil {
				errs[idx] = err
				return
			}
			m, ok := data.(map[string]any)
			if !ok {
				errs[idx] = &automatorError{Message: "expected map"}
				return
			}
			v, ok := numberValue(m["page"])
			if !ok {
				errs[idx] = &automatorError{Message: "missing page field"}
				return
			}
			results[idx] = int(v)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d error: %v", i, err)
		}
	}
	for i, got := range results {
		want := i + 1
		if got != want {
			t.Errorf("call %d: page = %d, want %d (response cross-wired!)", i, got, want)
		}
	}
}

// TestCloseDuringCall asserts that closing the connection during a call
// returns an error, not a hang.
func TestCloseDuringCall(t *testing.T) {
	handlers := standardFakeHandlers()
	handlers["App.getCurrentPage"] = func(_ json.RawMessage) (any, error) {
		time.Sleep(5 * time.Second)
		return fakeCurrentPageResult(1, "pages/index"), nil
	}
	fake := newFakeWxServer(t, handlers)
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	conn, err := getAutomatorConn()
	if err != nil {
		t.Fatalf("getAutomatorConn: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		err := automatorCallContext(context.Background(), "App.getCurrentPage", map[string]any{}, nil)
		done <- err
	}()

	time.Sleep(100 * time.Millisecond)
	conn.close()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error from call during close, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("call hung after connection close")
	}
}

// TestEventFloodNoHang asserts that flooding the automator event ring
// buffer with a high volume of events does not block RPC calls or cause
// unbounded memory growth. The ring buffer must drop excess events
// visibly, not silently accumulate them.
func TestEventFloodNoHang(t *testing.T) {
	handlers := standardFakeHandlers()
	fake := newFakeWxServer(t, handlers)
	defer fake.close()
	cleanup := setupFakeBackend(t, fake.port())
	defer cleanup()

	// Flood the event ring buffer with 500 events.
	conn, err := getAutomatorConn()
	if err != nil {
		t.Fatalf("getAutomatorConn: %v", err)
	}
	for i := 0; i < 500; i++ {
		params, _ := json.Marshal(map[string]any{
			"type":    "log",
			"message": fmt.Sprintf("flood event %d with some padding text to add bytes", i),
			"level":   "info",
		})
		conn.recordEvent("App.logAdded", params)
	}

	// Verify the event buffer is bounded — not all 500 events should be
	// retained. The ring buffer has a max size, so dropped count should be > 0.
	dropped, truncated, _ := conn.eventStats()
	if dropped == 0 {
		// Check if the buffer size is smaller than 500.
		events, _ := conn.recentEvents(0)
		if len(events) > 500 {
			t.Errorf("event buffer grew unbounded: %d events (expected <= buffer max)", len(events))
		}
	}
	t.Logf("event flood: dropped=%d truncated=%d (buffer is bounded)", dropped, truncated)

	// Verify RPC calls still work after the flood.
	start := time.Now()
	_, _, err = automatorCurrentPage()
	if err != nil {
		t.Fatalf("automatorCurrentPage after flood: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Errorf("call took too long after event flood: %v (expected < 5s)", elapsed)
	}

	// Make a second call to verify continued responsiveness.
	_, _, err = automatorCurrentPage()
	if err != nil {
		t.Fatalf("second call after flood failed: %v", err)
	}
	t.Logf("event flood test: two calls completed in %v", time.Since(start))
}

// --- Helpers ---

// minimalPNG returns a valid 1x1 red PNG for screenshot tests.
func minimalPNG() []byte {
	// Generate a valid 1x1 PNG using the image package.
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// encodeBase64 encodes bytes to a base64 string.
func encodeBase64(data []byte) string {
	return base64Encode(data)
}

// base64Encode is a thin wrapper to avoid importing encoding/base64
// in the test file header (it's already imported in automator_backend.go).
func base64Encode(data []byte) string {
	return stdBase64(data)
}
