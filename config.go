package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Global configuration state. Set by `configure` tool, env vars, or CLI flags.
var (
	configMu sync.RWMutex

	// Runtime-configurable (via configure tool)
	configuredURL     string
	configuredPort    int
	configuredAttach  bool   // attach mode: don't launch Chrome, connect to existing CDP
	configuredProfile string // "isolated" or "user"
	configuredFocus   bool   // bring Chrome windows/tabs to the foreground (default: stay in background)

	// Fixed at startup from env vars
	cdpPort    int
	profileDir string
)

func init() {
	cdpPort = envIntDefault(9224, "ARGUS_CDP_PORT", "AGENT_BROWSER_PORT")
	configuredPort = cdpPort
	profileDir = envStrDefault(filepath.Join(homeDir(), ".config", "argus", "chrome-profile"), "ARGUS_PROFILE_DIR")
	if envBoolDefault(false, "ARGUS_USE_USER_PROFILE") {
		configuredProfile = "user"
	} else {
		configuredProfile = "isolated"
	}
	configuredAttach = envBoolDefault(false, "ARGUS_ATTACH")
}

func cdpBaseURL() string {
	port := cdpPort
	configMu.RLock()
	if configuredPort != 0 {
		port = configuredPort
	}
	configMu.RUnlock()
	assertNotPort9224("cdpBaseURL", port)
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

func setConfig(url string, port int, attach *bool) {
	if url != "" {
		assertNotPort9224(url)
	}
	if port != 0 {
		assertNotPort9224("setConfig", port)
	}
	configMu.Lock()
	defer configMu.Unlock()
	if url != "" {
		configuredURL = url
	}
	if port != 0 {
		configuredPort = port
		cdpPort = port
	}
	if attach != nil {
		configuredAttach = *attach
	}
}

func setProfile(profile string) {
	configMu.Lock()
	defer configMu.Unlock()
	if profile == "user" || profile == "isolated" {
		configuredProfile = profile
	}
}

func useUserProfile() bool {
	configMu.RLock()
	defer configMu.RUnlock()
	return configuredProfile == "user"
}

// setFocus records whether argus may bring Chrome to the foreground.
// Default false: tabs are created in the background and no window
// activation is performed, so the agent never covers the user's screen.
func setFocus(v bool) {
	configMu.Lock()
	defer configMu.Unlock()
	configuredFocus = v
}

// wantFocus reports whether foreground behavior is enabled (--focus /
// ARGUS_FOCUS). When false, createTarget uses background tabs and
// Target.activateTarget calls are skipped.
func wantFocus() bool {
	configMu.RLock()
	defer configMu.RUnlock()
	return configuredFocus
}

func getConfig() (string, int, bool) {
	configMu.RLock()
	defer configMu.RUnlock()
	return configuredURL, configuredPort, configuredAttach
}

// requireTarget finds the current Chrome tab.
// Uses targetID as stable identity, falls back to URL for backward compat.
// In launch mode: launches Chrome if not running, creates tab if none matches.
// In attach mode: just connects to the existing CDP endpoint, no launching.
func requireTarget(targetID ...string) (*browserTarget, error) {
	return resolveTarget(targetID...)
}

func requireTargetWS(targetID ...string) (string, error) {
	target, err := requireTarget(targetID...)
	if err != nil {
		return "", err
	}
	return target.WebSocketDebuggerURL, nil
}

// --- env helpers ---

func envStrDefault(defaultVal string, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return defaultVal
}

func envIntDefault(defaultVal int, names ...string) int {
	for _, name := range names {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultVal
}

func envBoolDefault(defaultVal bool, names ...string) bool {
	for _, name := range names {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			continue
		}
		return value == "true" || value == "1" || value == "yes"
	}
	return defaultVal
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return os.TempDir()
	}
	return home
}
