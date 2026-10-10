//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func findChromeBinary() (string, error) {
	if custom := strings.TrimSpace(os.Getenv("ARGUS_CHROME_BINARY")); custom != "" && fileExists(custom) {
		return custom, nil
	}
	return findChromeWindows()
}

func findChromeWindows() (string, error) {
	candidates := chromeWindowsCandidates()
	for _, c := range candidates {
		if fileExists(c) {
			return c, nil
		}
	}
	return "", fmt.Errorf("Chrome not found; install Chrome or set ARGUS_CHROME_BINARY")
}

// chromeWindowsCandidates returns the fixed candidate paths checked on Windows.
func chromeWindowsCandidates() []string {
	return []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome Application", "chrome.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome Application", "chrome.exe"),
		filepath.Join(os.Getenv("LocalAppData"), "Google", "Chrome Application", "chrome.exe"),
	}
}

// chromeSearchedPaths returns the candidate paths checked by findChromeBinary
// on this platform. Used in actionable launch-error hints.
func chromeSearchedPaths() []string {
	return chromeWindowsCandidates()
}

func launchChrome() error {
	binary, err := findChromeWindows()
	if err != nil {
		return err
	}
	userDataDir := profileDir
	if useUserProfile() {
		userDataDir = filepath.Join(os.Getenv("LocalAppData"), "Google", "Chrome", "User Data")
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
	if !wantFocus() {
		// Keep the window minimized unless --focus requested foreground.
		args = append(args, "--start-minimized")
	}
	args = append(args, "about:blank")
	cmd := exec.Command(binary, args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to launch Chrome: %w", err)
	}
	_ = cmd.Process.Release()
	return nil
}
