package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ForbiddenCDPPort is the host agent Chrome CDP port (127.0.0.1:9224), strictly off-limits to all tests.
const ForbiddenCDPPort = 9224

// SafeDummyPort is the non-existent port used to neutralize Argus when tests complete.
const SafeDummyPort = 59999

// IsolatedChromeInstance represents a running isolated headless Chrome child process.
type IsolatedChromeInstance struct {
	Port                 int
	ProfileDir           string
	BinaryPath           string
	Cmd                  *exec.Cmd
	WebSocketDebuggerURL string
	BrowserVersion       string
	TargetID             string
}

// BaseURL returns the HTTP root URL for this Chrome instance (e.g., http://127.0.0.1:54321).
func (inst *IsolatedChromeInstance) BaseURL() string {
	assertSafePort(inst.Port)
	return fmt.Sprintf("http://127.0.0.1:%d", inst.Port)
}

// Version fetches browser version info directly from the instance's /json/version endpoint.
func (inst *IsolatedChromeInstance) Version() (browserCDPVersionInfo, error) {
	assertSafePort(inst.Port)
	resp, err := cdpHTTPClient.Get(inst.BaseURL() + "/json/version")
	if err != nil {
		return browserCDPVersionInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return browserCDPVersionInfo{}, fmt.Errorf("version request failed: %s", resp.Status)
	}
	var ver browserCDPVersionInfo
	if err := json.NewDecoder(resp.Body).Decode(&ver); err != nil {
		return browserCDPVersionInfo{}, err
	}
	return ver, nil
}

// Targets fetches current page targets directly from the instance's /json/list endpoint.
func (inst *IsolatedChromeInstance) Targets() ([]browserTarget, error) {
	assertSafePort(inst.Port)
	resp, err := cdpHTTPClient.Get(inst.BaseURL() + "/json/list")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("targets request failed: %s", resp.Status)
	}
	var targets []browserTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, err
	}
	return targets, nil
}

// assertSafePort panics immediately if the port matches the forbidden host Chrome port.
func assertSafePort(port int) {
	if port == ForbiddenCDPPort {
		panic(fmt.Sprintf("CRITICAL SAFETY VIOLATION: Test harness encountered forbidden host port %d! Terminating immediately.", ForbiddenCDPPort))
	}
	if port <= 0 || port > 65535 {
		panic(fmt.Sprintf("CRITICAL ERROR: Invalid TCP port number: %d", port))
	}
}

// StartIsolatedChrome launches an isolated headless Chrome instance and configures Argus to attach to it.
// It returns the instance and an idempotent cleanup function.
// It also registers the cleanup function with t.Cleanup().
// Intended for use in Go tests taking *testing.T.
func StartIsolatedChrome(t *testing.T) (*IsolatedChromeInstance, func()) {
	t.Helper()
	inst, cleanup, err := LaunchIsolatedChrome()
	if err != nil {
		t.Fatalf("StartIsolatedChrome failed: %v", err)
	}
	t.Cleanup(cleanup)
	return inst, cleanup
}

// LaunchIsolatedChrome launches an isolated headless Chrome instance and configures Argus to attach to it.
// Can be used in TestMain or custom test runners outside *testing.T.
func LaunchIsolatedChrome() (*IsolatedChromeInstance, func(), error) {
	return LaunchIsolatedChromeWithContext(context.Background())
}

// LaunchIsolatedChromeWithContext launches an isolated headless Chrome instance bounded by context.
func LaunchIsolatedChromeWithContext(ctx context.Context) (*IsolatedChromeInstance, func(), error) {
	// 1. Allocate dynamic ephemeral free port via kernel TCP socket
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to allocate ephemeral TCP port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return nil, nil, fmt.Errorf("failed to close ephemeral port listener: %w", err)
	}

	// Hard anti-9224 panic barrier
	assertSafePort(port)

	// 2. Create dedicated isolated temporary profile directory
	tmpDir, err := os.MkdirTemp("", fmt.Sprintf("argus-isolated-%d-*", port))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temp profile dir: %w", err)
	}

	// 3. Resolve raw Chrome binary (NEVER the macOS wrapper script and NEVER /usr/bin/open)
	binaryPath, err := resolveRawChromeBinary()
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, nil, fmt.Errorf("failed to locate raw Chrome binary: %w", err)
	}

	// 4. Construct isolated CLI flags
	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--remote-debugging-port=" + strconv.Itoa(port),
		"--user-data-dir=" + tmpDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-default-apps",
		"--disable-background-networking",
		"--disable-sync",
		"--disable-translate",
		"--disable-component-update",
		"--hide-crash-restore-bubble",
		"--disable-session-crashed-bubble",
		"--mute-audio",
		"--window-size=1280,800",
	}
	if runtime.GOOS == "linux" {
		args = append(args, "--no-sandbox", "--disable-dev-shm-usage")
	}
	args = append(args, "about:blank")

	// 5. Execute raw binary as a child process directly (bypassing LaunchServices)
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	cmd.Stdout = nil

	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, nil, fmt.Errorf("failed to start Chrome binary %s: %w", binaryPath, err)
	}

	// 6. Wait for /json/version probe with timeout
	client := &http.Client{Timeout: 500 * time.Millisecond}
	readyURL := fmt.Sprintf("http://127.0.0.1:%d/json/version", port)
	if strings.Contains(readyURL, ":9224") {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.RemoveAll(tmpDir)
		panic("CRITICAL SAFETY VIOLATION: probe URL targets port 9224!")
	}

	deadline := time.Now().Add(15 * time.Second)
	var readyVersion browserCDPVersionInfo
	var lastProbeErr error
	ready := false

	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, readyURL, nil)
		if err == nil {
			resp, err := client.Do(req)
			if err == nil {
				if resp.StatusCode == http.StatusOK {
					if err := json.NewDecoder(resp.Body).Decode(&readyVersion); err == nil && readyVersion.WebSocketDebuggerURL != "" {
						_ = resp.Body.Close()
						ready = true
						break
					}
				}
				_ = resp.Body.Close()
			} else {
				lastProbeErr = err
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !ready {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.RemoveAll(tmpDir)
		return nil, nil, fmt.Errorf("isolated Chrome on port %d failed to expose CDP within 15s (last err: %v)\nChrome stderr:\n%s", port, lastProbeErr, stderrBuf.String())
	}

	// 7. Configure Argus in STRICT ATTACH mode on this isolated port
	attachTrue := true
	setConfig("about:blank", port, &attachTrue)
	cdpPort = port
	configuredPort = port
	os.Setenv("ARGUS_CDP_PORT", strconv.Itoa(port))
	os.Setenv("ARGUS_ATTACH", "true")

	inst := &IsolatedChromeInstance{
		Port:                 port,
		ProfileDir:           tmpDir,
		BinaryPath:           binaryPath,
		Cmd:                  cmd,
		WebSocketDebuggerURL: readyVersion.WebSocketDebuggerURL,
		BrowserVersion:       readyVersion.Browser,
	}

	// 7b. Automatically discover and select the initial page target so Argus tools can resolve target immediately
	targetDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(targetDeadline) {
		select {
		case <-ctx.Done():
			break
		default:
		}
		targets, err := inst.Targets()
		if err == nil {
			for _, tg := range targets {
				if tg.Type == "page" && strings.TrimSpace(tg.ParentID) == "" {
					inst.TargetID = tg.ID
					setCurrentTargetID(tg.ID)
					break
				}
			}
			if inst.TargetID != "" {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}

	if inst.TargetID == "" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.RemoveAll(tmpDir)
		return nil, nil, fmt.Errorf("isolated Chrome on port %d started but no page target appeared within 5s\nChrome stderr:\n%s", port, stderrBuf.String())
	}

	// 8. Construct idempotent cleanup function
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			// Step A: Close all Argus client connections to avoid broken pipe logs
			closeAllConns()
			clearCurrentTargetID()

			// Step B: Send Browser.close over CDP for graceful browser shutdown
			if readyVersion.WebSocketDebuggerURL != "" {
				_ = cdpRequest(readyVersion.WebSocketDebuggerURL, "Browser.close", nil, nil)
			}

			// Step C: Wait for child process exit or force kill after bounded 3s
			done := make(chan error, 1)
			go func() {
				done <- cmd.Wait()
			}()

			select {
			case <-done:
				// Child process exited cleanly
			case <-time.After(3 * time.Second):
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
					<-done
				}
			}

			// Step D: Remove temporary profile directory with retry (up to 25 attempts, 50ms interval)
			for attempt := 0; attempt < 25; attempt++ {
				if err := os.RemoveAll(tmpDir); err == nil || os.IsNotExist(err) {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}

			// Step E: Reset Argus config to safe dummy port (59999) with attach=true
			assertSafePort(SafeDummyPort)
			dummyAttach := true
			setConfig("", SafeDummyPort, &dummyAttach)
			cdpPort = SafeDummyPort
			configuredPort = SafeDummyPort
			os.Setenv("ARGUS_CDP_PORT", strconv.Itoa(SafeDummyPort))
			os.Setenv("ARGUS_ATTACH", "true")
		})
	}

	return inst, cleanup, nil
}

// resolveRawChromeBinary resolves the direct executable path to Chrome.
// It explicitly rejects macOS .app bundles and wrapper shell scripts.
func resolveRawChromeBinary() (string, error) {
	if custom := strings.TrimSpace(os.Getenv("ARGUS_CHROME_BINARY")); custom != "" {
		if fileExists(custom) {
			return custom, nil
		}
		return "", fmt.Errorf("ARGUS_CHROME_BINARY specified (%s) but file not found", custom)
	}

	var candidates []string
	home := homeDir()

	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
		pwMatches, _ := filepath.Glob(filepath.Join(home, "Library", "Caches", "ms-playwright", "chromium-*", "chrome-mac-*", "Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing"))
		candidates = append(candidates, pwMatches...)

	case "linux":
		candidates = []string{
			"/usr/bin/google-chrome-stable",
			"/usr/bin/google-chrome",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
		}
		pwMatches, _ := filepath.Glob(filepath.Join(home, ".cache", "ms-playwright", "chromium-*", "chrome-linux", "chrome"))
		candidates = append(candidates, pwMatches...)
		for _, name := range []string{"google-chrome-stable", "google-chrome", "chromium", "chromium-browser"} {
			if path, err := exec.LookPath(name); err == nil {
				candidates = append(candidates, path)
			}
		}

	case "windows":
		candidates = []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		}
	}

	for _, c := range candidates {
		if fileExists(c) {
			return c, nil
		}
	}

	return "", fmt.Errorf("raw Chrome binary not found for isolated test harness on %s. Searched paths: %v", runtime.GOOS, candidates)
}
