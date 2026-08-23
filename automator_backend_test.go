package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// automator_backend_test.go — unit tests for the WeChat automator backend
// that do not require a running WeChat DevTools instance.
//
// Integration tests against a live automator endpoint are covered separately
// (see the manual verification flow in AGENTS.md).

func TestAutomatorNewIDUnique(t *testing.T) {
	ids := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := automatorNewID()
		if ids[id] {
			t.Fatalf("duplicate id generated: %s", id)
		}
		ids[id] = true
	}
}

func TestAutomatorNewIDFormat(t *testing.T) {
	id := automatorNewID()
	if !strings.HasPrefix(id, "argus-") {
		t.Errorf("id %q should start with 'argus-'", id)
	}
}

func TestSetBackend(t *testing.T) {
	// Save and restore
	backendMu.Lock()
	orig := configuredBackend
	backendMu.Unlock()
	defer func() {
		backendMu.Lock()
		configuredBackend = orig
		backendMu.Unlock()
	}()

	for _, input := range []string{"wechat", "WeChat", "WX", "miniprogram", "MP"} {
		setBackend(input)
		if got := activeBackend(); got != BackendWeChat {
			t.Errorf("setBackend(%q) -> activeBackend=%q, want %q", input, got, BackendWeChat)
		}
	}
	for _, input := range []string{"cdp", "CDP", "chrome", "browser", ""} {
		setBackend(input)
		if got := activeBackend(); got != BackendCDP {
			t.Errorf("setBackend(%q) -> activeBackend=%q, want %q", input, got, BackendCDP)
		}
	}
}

func TestAutomatorPortDefault(t *testing.T) {
	backendMu.Lock()
	origBackend := configuredBackend
	origPort := configuredWxPort
	configuredBackend = BackendWeChat
	configuredWxPort = 0
	backendMu.Unlock()
	defer func() {
		backendMu.Lock()
		configuredBackend = origBackend
		configuredWxPort = origPort
		backendMu.Unlock()
	}()
	if got := automatorPort(); got != 9420 {
		t.Errorf("automatorPort() under wechat with no explicit port = %d, want 9420", got)
	}
}

func TestAutomatorPortCDPZero(t *testing.T) {
	backendMu.Lock()
	origBackend := configuredBackend
	origPort := configuredWxPort
	configuredBackend = BackendCDP
	configuredWxPort = 0
	backendMu.Unlock()
	defer func() {
		backendMu.Lock()
		configuredBackend = origBackend
		configuredWxPort = origPort
		backendMu.Unlock()
	}()
	if got := automatorPort(); got != 0 {
		t.Errorf("automatorPort() under cdp = %d, want 0", got)
	}
}

func TestErrUnsupportedBackend(t *testing.T) {
	backendMu.Lock()
	orig := configuredBackend
	configuredBackend = BackendCDP
	backendMu.Unlock()
	defer func() {
		backendMu.Lock()
		configuredBackend = orig
		backendMu.Unlock()
	}()
	err := errUnsupportedBackend("page_data", BackendWeChat)
	if err.Code != ErrUnsupported {
		t.Errorf("errUnsupportedBackend code = %s, want %s", err.Code, ErrUnsupported)
	}
	if !strings.Contains(err.Message, "wechat") || !strings.Contains(err.Message, "cdp") {
		t.Errorf("errUnsupportedBackend message should mention both backends: %q", err.Message)
	}
	if err.Retryable {
		t.Error("errUnsupportedBackend should not be retryable")
	}
}

func TestAutomatorEventLevel(t *testing.T) {
	cases := map[string]string{
		"App.logAdded":          "log",
		"App.errorAdded":        "error",
		"App.exceptionOccurred": "error",
		"App.warnAdded":         "warn",
		"Page.someWarn":         "warn",
		"Page.someError":        "error",
		"App.callWxMethod":      "log",
	}
	for method, want := range cases {
		if got := automatorEventLevel(method, nil); got != want {
			t.Errorf("automatorEventLevel(%q) = %q, want %q", method, got, want)
		}
	}
}

func TestAutomatorEventTextBounds(t *testing.T) {
	big := strings.Repeat("x", 5000)
	got := automatorEventText(big, 100)
	if len(got) > 103 { // 100 + "..."
		t.Errorf("automatorEventText did not bound: got %d chars", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("automatorEventText should end with '...' when truncated, got %q", got[len(got)-10:])
	}
	small := "hello"
	// automatorEventText JSON-marshals its input, so a string becomes quoted.
	if got := automatorEventText(small, 100); got != `"hello"` {
		t.Errorf("automatorEventText(short) = %q, want %q", got, `"hello"`)
	}
}

func TestAutomatorEventTextJSON(t *testing.T) {
	params := map[string]any{"key": "value", "n": 42}
	got := automatorEventText(params, 1000)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Errorf("automatorEventText should produce valid JSON, got parse error: %v", err)
	}
}

func TestAutomatorEventRecordRingBuffer(t *testing.T) {
	c := &automatorConn{
		events: make([]automatorEventRecord, 0, automatorEventBufferSize),
	}
	// Fill beyond capacity to verify ring behavior and dropped count.
	for i := 0; i < automatorEventBufferSize+50; i++ {
		c.recordEvent("App.logAdded", json.RawMessage(`{}`))
	}
	recent, dropped := c.recentEvents(10)
	if len(recent) != 10 {
		t.Errorf("recentEvents(10) returned %d, want 10", len(recent))
	}
	if dropped != 50 {
		t.Errorf("dropped = %d, want 50", dropped)
	}
	_ = dropped // int64
	// The most recent event should have the highest seq.
	if recent[len(recent)-1].Seq <= recent[0].Seq {
		t.Error("recentEvents should be ordered oldest-first")
	}
}

func TestBackendInfoDefaults(t *testing.T) {
	backendMu.Lock()
	origBackend := configuredBackend
	origProject := configuredWxProject
	origPort := configuredWxPort
	configuredBackend = BackendCDP
	configuredWxProject = ""
	configuredWxPort = 0
	backendMu.Unlock()
	defer func() {
		backendMu.Lock()
		configuredBackend = origBackend
		configuredWxProject = origProject
		configuredWxPort = origPort
		backendMu.Unlock()
	}()
	info := backendInfo()
	if info["backend"] != BackendCDP {
		t.Errorf("backendInfo backend = %v, want %q", info["backend"], BackendCDP)
	}
	if _, ok := info["wx_project"]; ok {
		t.Error("backendInfo should not include wx_project when unset")
	}
}

func TestBackendInfoWeChat(t *testing.T) {
	backendMu.Lock()
	origBackend := configuredBackend
	origProject := configuredWxProject
	origPort := configuredWxPort
	configuredBackend = BackendWeChat
	configuredWxProject = "/tmp/mp-weixin"
	configuredWxPort = 9420
	backendMu.Unlock()
	defer func() {
		backendMu.Lock()
		configuredBackend = origBackend
		configuredWxProject = origProject
		configuredWxPort = origPort
		backendMu.Unlock()
	}()
	info := backendInfo()
	if info["backend"] != BackendWeChat {
		t.Errorf("backendInfo backend = %v, want %q", info["backend"], BackendWeChat)
	}
	if info["wx_project"] != "/tmp/mp-weixin" {
		t.Errorf("backendInfo wx_project = %v, want /tmp/mp-weixin", info["wx_project"])
	}
	if info["wx_port"] != 9420 {
		t.Errorf("backendInfo wx_port = %v, want 9420", info["wx_port"])
	}
}
