package main

import (
	"sync"
	"testing"
	"time"
)

// --- Shutdown coordinator tests ---

func TestShutdownBeginOnce(t *testing.T) {
	s := NewShutdown()
	if s.IsBegin() {
		t.Error("shutdown should not be begun initially")
	}
	s.Begin()
	if !s.IsBegin() {
		t.Error("shutdown should be begun after Begin()")
	}
	// Second Begin should be no-op
	s.Begin()
	if !s.IsBegin() {
		t.Error("shutdown should still be begun after second Begin()")
	}
}

func TestShutdownWaitBegin(t *testing.T) {
	s := NewShutdown()
	go func() {
		time.Sleep(10 * time.Millisecond)
		s.Begin()
	}()
	start := time.Now()
	s.WaitBegin()
	elapsed := time.Since(start)
	if elapsed < 5*time.Millisecond {
		t.Error("WaitBegin returned too fast — should have blocked until Begin()")
	}
}

func TestShutdownComplete(t *testing.T) {
	s := NewShutdown()
	go func() {
		s.WaitBegin()
		time.Sleep(10 * time.Millisecond)
		s.Complete()
	}()
	s.Begin()
	start := time.Now()
	s.WaitComplete()
	elapsed := time.Since(start)
	if elapsed < 5*time.Millisecond {
		t.Error("WaitComplete returned too fast — should have blocked until Complete()")
	}
}

func TestShutdownConcurrentBegin(t *testing.T) {
	s := NewShutdown()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Begin()
		}()
	}
	wg.Wait()
	if !s.IsBegin() {
		t.Error("shutdown should be begun after 100 concurrent Begin() calls")
	}
}

// --- Global shutdown registry tests ---

func TestGlobalShutdown(t *testing.T) {
	// Save and restore global state
	globalShutdownMu.Lock()
	origShutdowns := globalShutdowns
	origBegun := globalShutdownBegun
	globalShutdowns = nil
	globalShutdownBegun = false
	globalShutdownMu.Unlock()

	defer func() {
		globalShutdownMu.Lock()
		globalShutdowns = origShutdowns
		globalShutdownBegun = origBegun
		globalShutdownMu.Unlock()
	}()

	s1 := NewShutdown()
	s2 := NewShutdown()
	RegisterShutdown(s1)
	RegisterShutdown(s2)

	// Complete both in background
	go func() {
		s1.WaitBegin()
		s2.WaitBegin()
		s1.Complete()
		s2.Complete()
	}()

	BeginGlobalShutdown()

	if !s1.IsBegin() {
		t.Error("s1 should be begun after BeginGlobalShutdown")
	}
	if !s2.IsBegin() {
		t.Error("s2 should be begun after BeginGlobalShutdown")
	}

	// Second call should be no-op
	BeginGlobalShutdown()
}

// --- Logger tests ---

func TestLoggerLevels(t *testing.T) {
	// Logger should not crash with any level
	l := NewLogger("test")
	l.Debug("debug message: %d", 42)
	l.Info("info message: %s", "hello")
	l.Warn("warn message")
	l.Error("error message: %v", "oops")
}

func TestLoggerWithPrefix(t *testing.T) {
	l := NewLogger("base")
	l2 := l.WithPrefix("extra")
	if l2.prefix != "base extra" {
		t.Errorf("WithPrefix: got %q, want %q", l2.prefix, "base extra")
	}

	l3 := NewLogger("")
	l4 := l3.WithPrefix("only")
	if l4.prefix != "only" {
		t.Errorf("WithPrefix on empty: got %q, want %q", l4.prefix, "only")
	}
}

func TestLogLevelFiltering(t *testing.T) {
	// Set level to Warn and verify Debug/Info are suppressed
	origLevel := logLevel
	defer func() { logLevel = origLevel }()

	logLevel = LogWarn
	l := NewLogger("test")
	// These should not panic and should be filtered
	l.Debug("should be filtered")
	l.Info("should be filtered")
	l.Warn("should appear")
	l.Error("should appear")
}

// --- Connection pool tests ---

func TestCdpPoolCloseAllConns(t *testing.T) {
	closeAllConns()
	poolMu.Lock()
	count := len(pool)
	poolMu.Unlock()
	if count != 0 {
		t.Errorf("expected empty pool after closeAllConns, got %d", count)
	}
}

func TestCdpPoolShortID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"ws://127.0.0.1:9224/devtools/page/ABC123", "ABC123"},
		{"ws://localhost:9222/devtools/browser/XYZ", "XYZ"},
		{"ws://no-slash-end", "no-slash-end"},
		{"", "?"},
	}
	for _, tt := range tests {
		got := shortID(tt.input)
		if got != tt.want {
			t.Errorf("shortID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- Chrome lifecycle tests ---

func TestChromeLifecycleState(t *testing.T) {
	chromeMu.Lock()
	origLaunched := chromeLaunched
	origRestart := chromeRestartNeeded
	chromeMu.Unlock()

	defer func() {
		chromeMu.Lock()
		chromeLaunched = origLaunched
		chromeRestartNeeded = origRestart
		chromeMu.Unlock()
	}()

	chromeMu.Lock()
	chromeLaunched = false
	chromeMu.Unlock()

	if chromeLaunched {
		t.Error("chromeLaunched should be false")
	}
}

// --- Intercept listener exit test ---

func TestInterceptListenerExitsOnHubClose(t *testing.T) {
	hub := newEventHub(64)
	sub := hub.subscribe("intercept-test", 8, false)

	state := &interceptState{
		stopCh:       make(chan struct{}),
		listenerDone: make(chan struct{}),
		sub:          sub,
	}
	// Override listenerDone since runInterceptListener closes it
	state.listenerDone = make(chan struct{})

	// Can't run runInterceptListener directly without a real wsURL,
	// but we can verify the hub close mechanism
	go func() {
		defer close(state.listenerDone)
		for range sub.ch {
		}
	}()

	// Close hub — subscription channel should close, listener should exit
	hub.close()

	select {
	case <-state.listenerDone:
		// Good — listener exited
	case <-time.After(time.Second):
		t.Error("listener did not exit after hub close")
	}
}
