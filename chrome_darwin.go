//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Note: findChromeBinary is also defined here for darwin.

func findChromeBinary() (string, error) {
	if custom := strings.TrimSpace(os.Getenv("ARGUS_CHROME_BINARY")); custom != "" && fileExists(custom) {
		return custom, nil
	}
	return findChromeDarwin()
}

func findChromeDarwin() (string, error) {
	// Prefer real Google Chrome over Chrome for Testing, since the
	// Playwright-bundled Chrome for Testing can be unstable on some systems.
	candidates := chromeDarwinCandidates()
	home := homeDir()
	matches, _ := filepath.Glob(filepath.Join(home, "Library", "Caches", "ms-playwright", "chromium-*", "chrome-mac-arm64", "Google Chrome for Testing.app"))
	candidates = append(candidates, matches...)
	for _, c := range candidates {
		if !dirExists(c) {
			continue
		}
		// Verify the actual binary exists inside the .app bundle
		binaryPath := filepath.Join(c, "Contents", "MacOS", "Google Chrome")
		if !fileExists(binaryPath) {
			binaryPath = filepath.Join(c, "Contents", "MacOS", "Google Chrome for Testing")
		}
		if fileExists(binaryPath) {
			return c, nil
		}
	}
	return "", fmt.Errorf("Chrome not found; install Google Chrome or Chrome for Testing: npx playwright install chromium")
}

// chromeDarwinCandidates returns the fixed candidate paths checked on macOS.
func chromeDarwinCandidates() []string {
	return []string{
		"/Applications/Google Chrome.app",
		"/Applications/Google Chrome for Testing.app",
	}
}

// chromeSearchedPaths returns the candidate paths checked by findChromeBinary
// on this platform. Used in actionable launch-error hints.
func chromeSearchedPaths() []string {
	return chromeDarwinCandidates()
}

// launchChrome starts Chrome on macOS.
// Uses the binary directly (not `open -a`) so that --user-data-dir and
// --remote-debugging-port are respected even when another Chrome instance
// is already running.
func launchChrome() error {
	appPath, err := findChromeDarwin()
	if err != nil {
		return err
	}
	// Resolve to the actual binary inside the .app bundle
	binaryPath := filepath.Join(appPath, "Contents", "MacOS", "Google Chrome")
	if !fileExists(binaryPath) {
		// Try Chrome for Testing naming
		binaryPath = filepath.Join(appPath, "Contents", "MacOS", "Google Chrome for Testing")
	}
	if !fileExists(binaryPath) {
		return fmt.Errorf("Chrome binary not found in %s", appPath)
	}

	userDataDir := profileDir
	if useUserProfile() {
		userDataDir = filepath.Join(homeDir(), "Library", "Application Support", "Google", "Chrome")
	} else {
		if err := os.MkdirAll(profileDir, 0o755); err != nil {
			return err
		}
		sanitizeProfile()
	}
	args := []string{
		"--user-data-dir=" + userDataDir,
		"--remote-debugging-port=" + strconv.Itoa(cdpPort),
		"--no-first-run",
		"--disable-default-apps",
		"--disable-infobars",
		"--hide-crash-restore-bubble",
		"--disable-session-crashed-bubble",
		"--no-default-browser-check",
		"--new-window",
	}
	args = append(args, chromeHeadlessArgs()...)
	args = append(args, "about:blank")
	cmd := exec.Command(binaryPath, args...)
	// Detach from parent so Chrome survives after Argus exits
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to launch Chrome: %w", err)
	}
	// Release the process so it doesn't become a zombie
	_ = cmd.Process.Release()
	// Send Chrome to background so it doesn't steal focus from the user's
	// active window. Chrome activates itself on launch; this reverts that.
	go func() {
		for range 3 {
			time.Sleep(1 * time.Second)
			_ = exec.Command("osascript", "-e",
				`tell application "System Events" to set frontmost of (first process whose name = "Google Chrome") to false`,
			).Run()
		}
	}()
	return nil
}
