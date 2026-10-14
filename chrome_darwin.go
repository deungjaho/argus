//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
		for _, name := range []string{"Google Chrome", "Google Chrome for Testing", "Chromium"} {
			if fileExists(filepath.Join(c, "Contents", "MacOS", name)) {
				return c, nil
			}
		}
	}
	return "", fmt.Errorf("Chrome not found; install Google Chrome or Chrome for Testing: npx playwright install chromium")
}

// chromeDarwinCandidates returns the candidate app bundle paths checked on macOS.
func chromeDarwinCandidates() []string {
	return []string{
		"/Applications/Google Chrome.app",
		"/Applications/Google Chrome for Testing.app",
		"/Applications/Chromium.app",
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
	var binaryPath string
	for _, name := range []string{"Google Chrome", "Google Chrome for Testing", "Chromium"} {
		p := filepath.Join(appPath, "Contents", "MacOS", name)
		if fileExists(p) {
			binaryPath = p
			break
		}
	}
	if binaryPath == "" && !strings.HasSuffix(appPath, ".app") && fileExists(appPath) {
		binaryPath = appPath
	}
	if binaryPath == "" {
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
	}
	args = append(args, chromeHeadlessArgs()...)

	// When headless mode is requested or when running tests, NEVER use LaunchServices / open -a.
	// Launching via open delegates to LaunchServices which pops GUI windows on the user's active screen.
	// Instead, directly execute binaryPath via exec.Command.
	isHeadless := len(chromeHeadlessArgs()) > 0 ||
		strings.TrimSpace(os.Getenv("ARGUS_CHROME_HEADLESS")) == "1" ||
		strings.TrimSpace(os.Getenv("ARGUS_CHROME_HEADLESS")) == "true" ||
		testing.Testing() ||
		os.Getenv("ARGUS_NO_OPEN") == "1"
	for _, a := range args {
		if strings.HasPrefix(a, "--headless") {
			isHeadless = true
			break
		}
	}

	if !isHeadless && strings.HasSuffix(appPath, ".app") {
		openArgs := []string{"-n", "-a", appPath}
		if !wantFocus() {
			openArgs = []string{"-n", "-g", "-j", "-a", appPath}
		}
		openArgs = append(openArgs, "--args")
		openArgs = append(openArgs, args...)
		cmd := exec.Command("/usr/bin/open", openArgs...)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to launch Chrome via open: %w", err)
		}
		return nil
	}

	cmd := exec.Command(binaryPath, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to launch Chrome: %w", err)
	}
	_ = cmd.Process.Release()
	return nil
}
