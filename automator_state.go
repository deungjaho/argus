package main

// automator_state.go — active backend selection and WeChat automator config.
//
// Argus supports two runtime backends:
//   - cdp:       Chrome DevTools Protocol (H5 / browser target). Default.
//   - wechat:    WeChat DevTools automation (Mini Program target).
//
// The active backend is selected via the `configure` tool (backend field)
// or the ARGUS_BACKEND env var. Shared tools (screenshot, click, fill,
// snapshot, console_messages, eval, navigate) dispatch based on the active
// backend. Backend-specific tools (page_data, call_wx_method, mock_wx_method)
// are only meaningful under the wechat backend and return ErrUnsupported
// under cdp.
//
// Configuration fields for the wechat backend:
//   - wx_project: path to the compiled mp-weixin output directory. This is
//                 informational only — it does not start or validate a
//                 WeChat DevTools instance. Use wx_port to connect to a
//                 running automator endpoint.
//   - wx_port:    the WeChat automator WebSocket port (default 9420).

import (
	"fmt"
	"strings"
	"sync"
)

// Backend names. Stable strings exposed in tool results and diagnostics.
const (
	BackendCDP    = "cdp"
	BackendWeChat = "wechat"
)

var (
	backendMu           sync.RWMutex
	configuredBackend   string // "cdp" (default) or "wechat"
	configuredWxProject string
	configuredWxPort    int
)

func init() {
	// Default backend is cdp. ARGUS_BACKEND can override at startup.
	if v := strings.TrimSpace(envStrDefault("", "ARGUS_BACKEND")); v != "" {
		if err := setBackendValidated(v); err != nil {
			logWarn("ARGUS_BACKEND: %v", err)
		}
	}
	if v := strings.TrimSpace(envStrDefault("", "ARGUS_WX_PROJECT")); v != "" {
		setWxProject(v)
	}
	if p := envIntDefault(0, "ARGUS_WX_PORT"); p > 0 {
		setWxPort(p)
	}
}

// setBackendValidated sets the backend, returning an error for unknown values.
// Invalid values are NOT silently ignored — the caller should surface the
// error to the agent.
func setBackendValidated(name string) error {
	normalized := strings.ToLower(strings.TrimSpace(name))
	switch normalized {
	case BackendWeChat, "wx", "miniprogram", "mp":
		backendMu.Lock()
		configuredBackend = BackendWeChat
		backendMu.Unlock()
		return nil
	case "", BackendCDP, "chrome", "browser":
		backendMu.Lock()
		configuredBackend = BackendCDP
		backendMu.Unlock()
		return nil
	default:
		return fmt.Errorf("unknown backend %q (valid: cdp, wechat)", name)
	}
}

// setBackend is the unvalidated setter (backward compatibility for tests
// that set directly). Prefer setBackendValidated for user-facing paths.
func setBackend(name string) {
	_ = setBackendValidated(name)
}

func setWxProject(path string) {
	backendMu.Lock()
	defer backendMu.Unlock()
	configuredWxProject = strings.TrimSpace(path)
}

func setWxPort(port int) {
	backendMu.Lock()
	defer backendMu.Unlock()
	if port > 0 {
		configuredWxPort = port
	}
}

// activeBackend returns the active backend name. Does NOT hold the RWMutex
// — callers that need a consistent snapshot should use backendInfo or hold
// the lock themselves.
func activeBackend() string {
	backendMu.RLock()
	defer backendMu.RUnlock()
	if configuredBackend == "" {
		return BackendCDP
	}
	return configuredBackend
}

func wxProject() string {
	backendMu.RLock()
	defer backendMu.RUnlock()
	return configuredWxProject
}

func automatorPort() int {
	backendMu.RLock()
	defer backendMu.RUnlock()
	if configuredWxPort > 0 {
		return configuredWxPort
	}
	// Default WeChat automator port observed in the project.
	if configuredBackend == BackendWeChat {
		return 9420
	}
	return 0
}

// backendInfo returns a small map describing the active backend, included
// in tool results and diagnostics so the agent always knows which runtime
// it is talking to. This function reads all fields under a single RLock
// and does NOT call activeBackend() (which would re-acquire the same lock
// and deadlock under a waiting writer).
func backendInfo() map[string]any {
	backendMu.RLock()
	defer backendMu.RUnlock()
	info := map[string]any{}
	if configuredBackend == "" {
		info["backend"] = BackendCDP
	} else {
		info["backend"] = configuredBackend
	}
	if configuredWxProject != "" {
		info["wx_project"] = configuredWxProject
		info["wx_project_role"] = "informational"
	}
	if configuredWxPort > 0 {
		info["wx_port"] = configuredWxPort
	}
	return info
}

// errUnsupportedBackend returns a structured unsupported error when a tool
// is called under the wrong backend. Reads the active backend under the
// lock without nesting.
func errUnsupportedBackend(op, need string) *ToolError {
	backendMu.RLock()
	current := configuredBackend
	if current == "" {
		current = BackendCDP
	}
	backendMu.RUnlock()
	return &ToolError{
		Code:      ErrUnsupported,
		Op:        op,
		Message:   "tool requires backend '" + need + "', active backend is '" + current + "'",
		Retryable: false,
	}
}
