package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// --- Chrome lifecycle management ---
//
// ensureChromeRunning checks if Chrome is alive and exposing CDP.
// If not, it launches Chrome with exponential backoff retry.
// A background health checker monitors Chrome and marks it for restart on crash.

var (
	chromeMu            sync.Mutex
	chromeLaunched      bool
	chromeRestartNeeded bool
)

// ensureChromeRunning checks if Chrome is exposing CDP; if not, launches it.
// Uses exponential backoff for retries (1s, 2s, 4s, 8s, max 10s).
func ensureChromeRunning() error {
	chromeMu.Lock()
	defer chromeMu.Unlock()

	// Fast path: Chrome is already alive
	if _, err := cdpVersion(); err == nil {
		return nil
	}

	logInfo("Chrome not responding, launching...")

	// Check if we're in attach mode — don't launch Chrome if so
	if configuredAttach {
		return &ActionableError{
			ErrorType: "chrome_not_responding",
			Message:   fmt.Sprintf("attach mode: Chrome not responding on port %d (expected to be already running)", cdpPort),
			Hints: []string{
				"Make sure the external CDP target is running",
				fmt.Sprintf("Verify CDP is exposed at port %d", cdpPort),
				"Check ARGUS_ATTACH / --attach configuration",
			},
		}
	}

	// Launch with retry
	wait := 1 * time.Second
	maxWait := 10 * time.Second
	maxRetries := 3

	var lastLaunchErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			logInfo("retrying Chrome launch (attempt %d/%d) after %s", attempt+1, maxRetries, wait)
			time.Sleep(wait)
			wait = time.Duration(float64(wait) * 2)
			if wait > maxWait {
				wait = maxWait
			}
		}

		if err := launchChrome(); err != nil {
			lastLaunchErr = err
			logWarn("Chrome launch failed: %v", err)
			continue
		}

		// Wait for CDP to become available
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := cdpVersion(); err == nil {
				chromeLaunched = true
				logInfo("Chrome is ready on port %d", cdpPort)
				return nil
			}
			time.Sleep(50 * time.Millisecond)
		}
		logWarn("Chrome launched but CDP not responding after 10s")
	}

	// Return an actionable error with the searched paths so the agent can
	// diagnose the launch failure without reading the source.
	cause := "Chrome did not expose CDP after retries"
	if lastLaunchErr != nil {
		cause = lastLaunchErr.Error()
	}
	return newChromeLaunchError(cause, chromeSearchedPaths())
}

// startHealthChecker runs a background goroutine that periodically checks
// if Chrome is alive. If Chrome crashes, it marks Chrome for restart.
// Registers with the shutdown coordinator for clean exit.
func startHealthChecker() {
	sd := NewShutdown()
	RegisterShutdown(sd)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logError("health checker panic: %v", r)
			}
			sd.Complete()
		}()

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				chromeMu.Lock()
				launched := chromeLaunched
				chromeMu.Unlock()

				if !launched {
					continue
				}

				if _, err := cdpVersion(); err != nil {
					logWarn("Chrome health check failed: %v — will restart on next access", err)
					chromeMu.Lock()
					chromeLaunched = false
					chromeRestartNeeded = true
					chromeMu.Unlock()
					// Close stale connections
					closeAllConns()
				}
			case <-sd.begin:
				return
			}
		}
	}()
}

// findChromeBinary is defined per-platform in chrome_darwin.go, chrome_linux.go, chrome_windows.go.
// Each platform file checks ARGUS_CHROME_BINARY first, then falls back to platform-specific search.

// chromeSearchedPaths returns the platform-specific candidate paths that
// findChromeBinary checks. Used in actionable launch errors so the agent
// can see what was searched without reading the source. Defined per-platform
// in chrome_{platform}.go.

// chromeHeadlessArgs returns extra args to force headless Chrome when
// ARGUS_CHROME_HEADLESS is set to "1" or "true". This is intended for
// integration tests on headless servers; it does not change the default
// user-facing launch behavior.
func chromeHeadlessArgs() []string {
	if v := strings.TrimSpace(strings.ToLower(os.Getenv("ARGUS_CHROME_HEADLESS"))); v == "1" || v == "true" {
		return []string{"--headless=new"}
	}
	return nil
}

// sanitizeProfile clears session-restore state so Chrome doesn't show
// "restore tabs?" bubbles on restart.
func sanitizeProfile() {
	for _, path := range []string{
		filepath.Join(profileDir, "Default", "Current Session"),
		filepath.Join(profileDir, "Default", "Current Tabs"),
		filepath.Join(profileDir, "Default", "Last Session"),
		filepath.Join(profileDir, "Default", "Last Tabs"),
	} {
		_ = os.Remove(path)
	}
	sessionDir := filepath.Join(profileDir, "Default", "Sessions")
	if entries, err := os.ReadDir(sessionDir); err == nil {
		for _, entry := range entries {
			_ = os.Remove(filepath.Join(sessionDir, entry.Name()))
		}
	}
	for _, path := range []string{
		filepath.Join(profileDir, "Default", "Preferences"),
		filepath.Join(profileDir, "Local State"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var root map[string]any
		if err := json.Unmarshal(data, &root); err != nil {
			continue
		}
		profile, ok := root["profile"].(map[string]any)
		if !ok {
			continue
		}
		changed := false
		if profile["exit_type"] != "Normal" {
			profile["exit_type"] = "Normal"
			changed = true
		}
		if profile["exited_cleanly"] != true {
			profile["exited_cleanly"] = true
			changed = true
		}
		if !changed {
			continue
		}
		encoded, err := json.Marshal(root)
		if err != nil {
			continue
		}
		_ = os.WriteFile(path, append(encoded, '\n'), 0o644)
	}
}
