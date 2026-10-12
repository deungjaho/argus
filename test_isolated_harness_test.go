//go:build integration

package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestIsolatedChromeLifecycle(t *testing.T) {
	inst, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	// 1. Verify dynamic port allocation & anti-9224
	if inst.Port == 9224 {
		t.Fatalf("FATAL: isolated Chrome port is 9224!")
	}
	if inst.Port <= 1024 {
		t.Fatalf("unexpected port number: %d", inst.Port)
	}

	// 2. Verify temporary user profile directory
	if inst.ProfileDir == "" {
		t.Fatal("expected non-empty profile dir")
	}
	info, err := os.Stat(inst.ProfileDir)
	if err != nil {
		t.Fatalf("failed to stat profile dir %s: %v", inst.ProfileDir, err)
	}
	if !info.IsDir() {
		t.Fatalf("profile dir %s is not a directory", inst.ProfileDir)
	}

	// 3. Verify process state
	if inst.Cmd == nil || inst.Cmd.Process == nil {
		t.Fatal("expected running Chrome process")
	}

	// 4. Verify CDP connection and target creation
	ver, err := inst.Version()
	if err != nil {
		t.Fatalf("failed to get CDP version: %v", err)
	}
	if ver.WebSocketDebuggerURL == "" {
		t.Fatalf("expected non-empty WebSocketDebuggerURL, got %+v", ver)
	}

	// Verify Argus global config was updated
	cfgURL, cfgPort, cfgAttach := getConfig()
	if cfgPort != inst.Port {
		t.Errorf("Argus configuredPort = %d, want %d", cfgPort, inst.Port)
	}
	if !cfgAttach {
		t.Errorf("Argus configuredAttach = %v, want true", cfgAttach)
	}
	if cfgURL != "about:blank" {
		t.Errorf("Argus configuredURL = %q, want %q", cfgURL, "about:blank")
	}

	// Test navigating / resolving a target through Argus
	target, err := requireTarget()
	if err != nil {
		t.Fatalf("requireTarget failed: %v", err)
	}
	if target.ID == "" {
		t.Fatal("expected non-empty target ID")
	}
	if target.Type != "page" {
		t.Errorf("expected target.Type == 'page', got %q", target.Type)
	}
	if inst.TargetID != "" && target.ID != inst.TargetID {
		t.Errorf("target.ID = %s, want inst.TargetID = %s", target.ID, inst.TargetID)
	}

	// Verify WebSocket endpoint resolution: requireTargetWS() succeeds
	pageWSURL, err := requireTargetWS()
	if err != nil {
		t.Fatalf("requireTargetWS failed: %v", err)
	}
	if !strings.HasPrefix(pageWSURL, "ws://") {
		t.Fatalf("expected ws:// URL from requireTargetWS, got %q", pageWSURL)
	}

	// Verify Active Page Navigation & Event Flow via CDP
	testTitle := "Argus Isolated Lifecycle Verified"
	testHTML := fmt.Sprintf("data:text/html,<html><head><title>%s</title></head><body><h1>Isolation Pass</h1></body></html>", testTitle)
	navResult, err := cdpPageNavigate(pageWSURL, testHTML, 5*time.Second)
	if err != nil {
		t.Fatalf("cdpPageNavigate to data URL failed: %v", err)
	}
	if navResult == nil || navResult["frame_id"] == nil {
		t.Fatalf("expected valid navigation result with frame_id, got: %+v", navResult)
	}

	// Verify JavaScript DOM Execution inside isolated browser
	evalVal, err := cdpEvaluate(pageWSURL, "document.title", false)
	if err != nil {
		t.Fatalf("cdpEvaluate document.title failed: %v", err)
	}
	if evalVal != testTitle {
		t.Fatalf("expected document.title %q, got %v", testTitle, evalVal)
	}

	// Execute cleanup explicitly to verify teardown
	cleanup()

	// 5. Verify cleanup: process termination, temp dir deletion, dummy port reset
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(inst.ProfileDir); !os.IsNotExist(err) {
		t.Errorf("expected profile dir %s to be deleted after cleanup, got err: %v", inst.ProfileDir, err)
	}

	// Verify Argus config was reset to dummy port (59999) with attach=true
	_, resetPort, resetAttach := getConfig()
	if resetPort != 59999 {
		t.Errorf("expected Argus port reset to 59999, got %d", resetPort)
	}
	if !resetAttach {
		t.Errorf("expected Argus attach to remain true after reset, got %v", resetAttach)
	}
}

func TestIsolatedChromeAnti9224Barrier(t *testing.T) {
	// Verify that assertSafePort panics when port is 9224
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected assertSafePort(9224) to panic, but it did not")
		}
		t.Logf("assertSafePort correctly panicked: %v", r)
	}()

	assertSafePort(9224)
}

func TestMultipleIsolatedChromeInstancesConcurrently(t *testing.T) {
	inst1, cleanup1 := StartIsolatedChrome(t)
	defer cleanup1()

	inst2, cleanup2 := StartIsolatedChrome(t)
	defer cleanup2()

	if inst1.Port == inst2.Port {
		t.Fatalf("expected distinct ports for concurrent instances, got %d for both", inst1.Port)
	}
	if inst1.ProfileDir == inst2.ProfileDir {
		t.Fatalf("expected distinct profile dirs for concurrent instances, got %s for both", inst1.ProfileDir)
	}

	ver1, err1 := inst1.Version()
	if err1 != nil {
		t.Fatalf("inst1 Version failed: %v", err1)
	}
	ver2, err2 := inst2.Version()
	if err2 != nil {
		t.Fatalf("inst2 Version failed: %v", err2)
	}

	if ver1.WebSocketDebuggerURL == ver2.WebSocketDebuggerURL {
		t.Fatalf("expected distinct WebSocket debugger URLs, got %s", ver1.WebSocketDebuggerURL)
	}
}
