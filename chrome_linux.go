//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func findChromeBinary() (string, error) {
	if custom := strings.TrimSpace(os.Getenv("ARGUS_CHROME_BINARY")); custom != "" && fileExists(custom) {
		return custom, nil
	}
	return findChromeLinux()
}

func findChromeLinux() (string, error) {
	candidates := chromeLinuxCandidates()
	home := homeDir()
	globPatterns := []string{
		filepath.Join(home, ".cache", "ms-playwright", "chromium_headless_shell-*", "chrome-linux", "headless_shell"),
		filepath.Join(home, ".cache", "ms-playwright", "chromium-*", "chrome-linux", "chrome"),
	}
	for _, c := range candidates {
		if fileExists(c) {
			return c, nil
		}
	}
	for _, pattern := range globPatterns {
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			if fileExists(m) {
				return m, nil
			}
		}
	}
	return "", fmt.Errorf("Chrome/Chromium not found; install with: sudo pacman -S chromium (or set ARGUS_CHROME_BINARY)")
}

// chromeLinuxCandidates returns the fixed candidate paths checked on Linux.
func chromeLinuxCandidates() []string {
	return []string{
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/opt/google/chrome/chrome",
		"/snap/bin/chromium",
	}
}

// chromeSearchedPaths returns the candidate paths checked by findChromeBinary
// on this platform. Used in actionable launch-error hints.
func chromeSearchedPaths() []string {
	return chromeLinuxCandidates()
}

// launchChrome starts Chrome on Linux by executing the binary directly.
func launchChrome() error {
	binary, err := findChromeLinux()
	if err != nil {
		return err
	}
	userDataDir := profileDir
	if useUserProfile() {
		// Use the user's real Chrome profile — cookies/tokens/logins available
		userDataDir = filepath.Join(homeDir(), ".config", "google-chrome")
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
		"--no-sandbox",
	}
	args = append(args, chromeHeadlessArgs()...)
	args = append(args, "about:blank")
	cmd := exec.Command(binary, args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	// Detach: start Chrome in its own process group so it survives MCP exit
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to launch Chrome: %w", err)
	}
	_ = cmd.Process.Release()
	return nil
}

// Ensure strings is referenced (used in shared code but not directly in this file)
