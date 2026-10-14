package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// First-class session API (argus#19/#12/#13). A session groups tabs under a
// stable session_id keyed by owner (TMUX_PANE). Modes:
//   shared   — tabs in the user's existing window (zero UI cost, default)
//   attached — dedicated window in the shared Chrome instance (createBrowserContext
//              + newWindow), minimized immediately so it never pops on screen;
//              reveal/hide toggles visibility for supervision. Never activates
//              the Chrome app — the user's screen is not stolen.
//   isolated — separate headless Chrome process + temp profile + own CDP port;
//              this argus process repoints to it for the session's lifetime.
//   fork     — attached-mode window seeded with the source tab's cookies
//              (login-state clone per design "git for browsing" two-tier model).
//
// Session metadata persists to ~/.config/argus/sessions.json so multiple argus
// processes (one per MCP client/pane) share the same view of the shared Chrome.

type argusSession struct {
	ID         string   `json:"id"`
	Mode       string   `json:"mode"`
	Owner      string   `json:"owner"`
	ContextID  string   `json:"browser_context_id,omitempty"`
	Tabs       []string `json:"tabs"`
	WindowID   int      `json:"window_id,omitempty"`
	HomeURL    string   `json:"url"`
	CreatedAt  int64    `json:"created_at"`
	CDPPort    int      `json:"cdp_port,omitempty"`
	PrevPort   int      `json:"prev_port,omitempty"`
	PID        int      `json:"pid,omitempty"`
	Profile    string   `json:"profile_dir,omitempty"`
	Revealed   bool     `json:"revealed,omitempty"`
	ForkedFrom string   `json:"forked_from,omitempty"`
}

var sessionStoreMu sync.Mutex

func sessionsPath() string {
	return filepath.Join(homeDir(), ".config", "argus", "sessions.json")
}

func loadSessions() map[string]*argusSession {
	out := map[string]*argusSession{}
	data, err := os.ReadFile(sessionsPath())
	if err == nil {
		_ = json.Unmarshal(data, &out)
	}
	return out
}

func saveSessions(m map[string]*argusSession) {
	_ = os.MkdirAll(filepath.Dir(sessionsPath()), 0o755)
	data, _ := json.MarshalIndent(m, "", "  ")
	tmp := sessionsPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err == nil {
		_ = os.Rename(tmp, sessionsPath())
	}
}

func withSessions(fn func(map[string]*argusSession)) {
	sessionStoreMu.Lock()
	defer sessionStoreMu.Unlock()
	m := loadSessions()
	fn(m)
	saveSessions(m)
}

// --- browser-level CDP helpers ---

func browserWSURL() (string, error) {
	v, err := cdpVersion()
	if err != nil {
		return "", err
	}
	return v.WebSocketDebuggerURL, nil
}

func cdpCreateBrowserContext() (string, error) {
	ws, err := browserWSURL()
	if err != nil {
		return "", err
	}
	var r struct {
		BrowserContextID string `json:"browserContextId"`
	}
	err = cdpRequest(ws, "Target.createBrowserContext", map[string]any{}, &r)
	return r.BrowserContextID, err
}

func cdpDisposeBrowserContext(id string) error {
	if id == "" {
		return nil
	}
	ws, err := browserWSURL()
	if err != nil {
		return err
	}
	return cdpRequest(ws, "Target.disposeBrowserContext", map[string]any{"browserContextId": id}, nil)
}

func cdpCreateWindowTarget(url, contextID string) (string, error) {
	ws, err := browserWSURL()
	if err != nil {
		return "", err
	}
	params := map[string]any{"url": url, "newWindow": true}
	if contextID != "" {
		params["browserContextId"] = contextID
	}
	var r struct {
		TargetID string `json:"targetId"`
	}
	if err := cdpRequest(ws, "Target.createTarget", params, &r); err != nil {
		return "", err
	}
	beaconRegisterTab(r.TargetID, url)
	return r.TargetID, nil
}

// cdpWindowForTarget returns the windowId hosting a target.
func cdpWindowForTarget(targetID string) (int, error) {
	ws, err := browserWSURL()
	if err != nil {
		return 0, err
	}
	var r struct {
		WindowID int `json:"windowId"`
	}
	err = cdpRequest(ws, "Browser.getWindowForTarget", map[string]any{"targetId": targetID}, &r)
	return r.WindowID, err
}

// cdpSetWindowState sets windowState without activating the app:
// "minimized" hides it from the screen, "normal" restores it in place.
func cdpSetWindowState(windowID int, state string) error {
	ws, err := browserWSURL()
	if err != nil {
		return err
	}
	return cdpRequest(ws, "Browser.setWindowBounds", map[string]any{
		"windowId": windowID,
		"bounds":   map[string]any{"windowState": state},
	}, nil)
}

// cdpBrowserContextOf returns the browserContextId hosting a target, or ""
// for the default context.
func cdpBrowserContextOf(targetID string) (string, error) {
	ws, err := browserWSURL()
	if err != nil {
		return "", err
	}
	var r struct {
		TargetInfos []struct {
			TargetID         string `json:"targetId"`
			BrowserContextID string `json:"browserContextId"`
		} `json:"targetInfos"`
	}
	if err := cdpRequest(ws, "Target.getTargets", map[string]any{}, &r); err != nil {
		return "", err
	}
	for _, t := range r.TargetInfos {
		if t.TargetID == targetID {
			return t.BrowserContextID, nil
		}
	}
	return "", fmt.Errorf("target %s not found", targetID)
}

// cdpStorageGetCookies reads all cookies visible to a browser context
// (empty contextID = default context).
func cdpStorageGetCookies(contextID string) ([]map[string]any, error) {
	ws, err := browserWSURL()
	if err != nil {
		return nil, err
	}
	params := map[string]any{}
	if contextID != "" {
		params["browserContextId"] = contextID
	}
	var r struct {
		Cookies []map[string]any `json:"cookies"`
	}
	err = cdpRequest(ws, "Storage.getCookies", params, &r)
	return r.Cookies, err
}

func cdpStorageSetCookies(contextID string, cookies []map[string]any) error {
	ws, err := browserWSURL()
	if err != nil {
		return err
	}
	params := map[string]any{"cookies": cookies}
	if contextID != "" {
		params["browserContextId"] = contextID
	}
	return cdpRequest(ws, "Storage.setCookies", params, nil)
}

// --- session operations ---

func sessionOwner() string {
	if sessionPaneVal != "" {
		return sessionPaneVal
	}
	return fmt.Sprintf("pid-%d", os.Getpid())
}

// sessionStart creates a session in the requested mode and returns it.
// url defaults to about:blank; fork_from clones the source tab's login state.
func sessionStart(mode, url, forkFrom string) (*argusSession, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "shared"
	}
	if url == "" {
		url = "about:blank"
	}
	s := &argusSession{
		ID:        fmt.Sprintf("ses-%s-%d", strings.TrimPrefix(sessionOwner(), "%"), time.Now().UnixMilli()%100000),
		Mode:      mode,
		Owner:     sessionOwner(),
		HomeURL:   url,
		CreatedAt: time.Now().Unix(),
	}

	switch mode {
	case "shared":
		ws, err := browserWSURL()
		if err != nil {
			return nil, err
		}
		tabID, err := cdpCreateTarget(ws, url, !wantFocus())
		if err != nil {
			return nil, err
		}
		s.Tabs = []string{tabID}

	case "attached", "fork":
		ctxID, err := cdpCreateBrowserContext()
		if err != nil {
			return nil, fmt.Errorf("create browser context: %w", err)
		}
		s.ContextID = ctxID
		if forkFrom != "" {
			srcCtx, err := cdpBrowserContextOf(forkFrom)
			if err != nil {
				cdpDisposeBrowserContext(ctxID)
				return nil, err
			}
			cookies, err := cdpStorageGetCookies(srcCtx)
			if err != nil {
				cdpDisposeBrowserContext(ctxID)
				return nil, fmt.Errorf("read source cookies: %w", err)
			}
			if len(cookies) > 0 {
				if err := cdpStorageSetCookies(ctxID, cookies); err != nil {
					cdpDisposeBrowserContext(ctxID)
					return nil, fmt.Errorf("seed forked context: %w", err)
				}
			}
			s.ForkedFrom = forkFrom
			if url == "about:blank" {
				if src, err := resolveTarget(forkFrom); err == nil && src.URL != "" {
					url = src.URL
					s.HomeURL = url
				}
			}
		}
		tabID, err := cdpCreateWindowTarget(url, ctxID)
		if err != nil {
			cdpDisposeBrowserContext(ctxID)
			return nil, err
		}
		s.Tabs = []string{tabID}
		if winID, err := cdpWindowForTarget(tabID); err == nil {
			s.WindowID = winID
			// Hide immediately — the window must never pop onto the
			// user's screen. Minimize restores position on reveal.
			_ = cdpSetWindowState(winID, "minimized")
		}

	case "isolated":
		port, err := freeCDPPort()
		if err != nil {
			return nil, err
		}
		profile, err := os.MkdirTemp("", "argus-isolated-*")
		if err != nil {
			return nil, err
		}
		pid, err := launchIsolatedChrome(port, profile)
		if err != nil {
			_ = os.RemoveAll(profile)
			return nil, err
		}
		s.CDPPort, s.PID, s.Profile = port, pid, profile
		s.PrevPort = cdpPort
		// Repoint this argus process at the isolated browser.
		setConfig("", port, nil)
		if url != "about:blank" {
			ws, err := browserWSURL()
			if err == nil {
				tabID, err := cdpCreateTarget(ws, url, true)
				if err == nil {
					s.Tabs = []string{tabID}
				}
			}
		}

	default:
		return nil, fmt.Errorf("unknown mode %q (want shared|attached|isolated)", mode)
	}

	withSessions(func(m map[string]*argusSession) { m[s.ID] = s })
	beaconRegisterSession(s)
	return s, nil
}

func freeCDPPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// launchIsolatedChrome starts a headless Chrome with a temp profile on the
// given port and waits for CDP readiness. Returns the process PID.
func launchIsolatedChrome(port int, profile string) (int, error) {
	appPath, err := findChromeBinary()
	if err != nil {
		return 0, err
	}
	bin := appPath
	if strings.HasSuffix(appPath, ".app") {
		bin = ""
		for _, name := range []string{"Google Chrome", "Google Chrome for Testing", "Chromium"} {
			p := filepath.Join(appPath, "Contents", "MacOS", name)
			if fileExists(p) {
				bin = p
				break
			}
		}
	}
	if bin == "" {
		return 0, fmt.Errorf("chrome binary not found in %s", appPath)
	}
	cmd := exec.Command(bin,
		"--user-data-dir="+profile,
		"--remote-debugging-port="+strconv.Itoa(port),
		"--headless=new",
		"--no-first-run", "--disable-default-apps", "--disable-infobars",
		"--hide-crash-restore-bubble", "--no-default-browser-check",
		"about:blank",
	)
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	go cmd.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var v browserCDPVersionInfo
		if err := cdpHTTPGet(fmt.Sprintf("http://127.0.0.1:%d/json/version", port), &v); err == nil {
			return cmd.Process.Pid, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	return 0, fmt.Errorf("isolated chrome on port %d did not expose CDP in time", port)
}

func cdpHTTPGet(url string, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

// sessionStop tears down a session: closes its tabs, disposes its context,
// kills isolated Chrome, and removes the record.
func sessionStop(id string) error {
	var s *argusSession
	withSessions(func(m map[string]*argusSession) {
		s = m[id]
		delete(m, id)
	})
	if s == nil {
		return fmt.Errorf("session %s not found", id)
	}
	for _, tabID := range s.Tabs {
		_ = cdpCloseTarget(tabID)
	}
	if s.Mode == "isolated" {
		if s.PID != 0 {
			if p, err := os.FindProcess(s.PID); err == nil {
				_ = p.Kill()
			}
		}
		if s.Profile != "" {
			_ = os.RemoveAll(s.Profile)
		}
		if s.PrevPort != 0 {
			setConfig("", s.PrevPort, nil)
		}
	} else {
		_ = cdpDisposeBrowserContext(s.ContextID)
	}
	return nil
}

// sessionReveal restores a hidden attached/fork window in place — no app
// activation, no screen stealing.
func sessionSetVisible(id string, visible bool) error {
	m := loadSessions()
	s := m[id]
	if s == nil {
		return fmt.Errorf("session %s not found", id)
	}
	if s.WindowID == 0 {
		return fmt.Errorf("session %s has no dedicated window (mode=%s)", id, s.Mode)
	}
	state := "minimized"
	if visible {
		state = "normal"
	}
	if err := cdpSetWindowState(s.WindowID, state); err != nil {
		return err
	}
	s.Revealed = visible
	withSessions(func(mm map[string]*argusSession) {
		if cur := mm[id]; cur != nil {
			cur.Revealed = visible
		}
	})
	return nil
}

// sessionResolveTab maps "session:<id>" (used anywhere a target_id is
// accepted) to the session's first live tab.
func sessionResolveTab(ref string) (string, bool) {
	if !strings.HasPrefix(ref, "session:") {
		return ref, false
	}
	s := loadSessions()[strings.TrimPrefix(ref, "session:")]
	if s == nil {
		return ref, true
	}
	for _, tab := range s.Tabs {
		if tab != "" {
			return tab, true
		}
	}
	return ref, true
}

func beaconRegisterSession(s *argusSession) {
	meta, _ := json.Marshal(map[string]any{
		"mode": s.Mode, "url": s.HomeURL, "tabs": s.Tabs, "context": s.ContextID,
	})
	beaconCall("register", "session", s.ID, "--pane", sessionPaneVal, "--producer", "argus", "--data", string(meta))
}

// --- MCP tool ---

type sessionInput struct {
	Action    string `json:"action" jsonschema:"start|stop|list|reveal|hide|status"`
	SessionID string `json:"session_id,omitempty"`
	Mode      string `json:"mode,omitempty" jsonschema:"shared|attached|isolated|fork"`
	URL       string `json:"url,omitempty"`
	ForkFrom  string `json:"fork_from,omitempty" jsonschema:"source tab_id for fork mode"`
}

func registerSessionTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "session",
		Description: "Manage browsing sessions: start{mode}, stop, list, reveal/hide an attached window, status. fork_from clones a tab's login state into an isolated window.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sessionInput) (*mcp.CallToolResult, any, error) {
		switch strings.ToLower(strings.TrimSpace(in.Action)) {
		case "start":
			s, err := sessionStart(in.Mode, in.URL, in.ForkFrom)
			if err != nil {
				return nil, nil, err
			}
			if len(s.Tabs) > 0 {
				setCurrentTargetID(s.Tabs[0])
			}
			return nil, map[string]any{
				"session_id": s.ID, "mode": s.Mode, "tab_id": firstOf(s.Tabs),
				"target": "session:" + s.ID, "window_id": s.WindowID,
				"revealed": s.Revealed, "url": s.HomeURL,
			}, nil

		case "stop":
			if in.SessionID == "" {
				return nil, nil, fmt.Errorf("session_id required")
			}
			if err := sessionStop(in.SessionID); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"stopped": in.SessionID}, nil

		case "list":
			m := loadSessions()
			live := map[string]bool{}
			if targets, err := cdpTargets(); err == nil {
				for _, t := range targets {
					live[t.ID] = true
				}
			}
			out := []map[string]any{}
			for _, s := range m {
				liveTabs := []string{}
				for _, t := range s.Tabs {
					if live[t] {
						liveTabs = append(liveTabs, t)
					}
				}
				out = append(out, map[string]any{
					"session_id": s.ID, "mode": s.Mode, "owner": s.Owner,
					"tabs": liveTabs, "revealed": s.Revealed, "url": s.HomeURL,
					"cdp_port": s.CDPPort, "forked_from": s.ForkedFrom,
				})
			}
			return nil, map[string]any{"sessions": out, "count": len(out)}, nil

		case "reveal":
			if err := sessionSetVisible(in.SessionID, true); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"revealed": in.SessionID}, nil

		case "hide":
			if err := sessionSetVisible(in.SessionID, false); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"hidden": in.SessionID}, nil

		case "status":
			s := loadSessions()[in.SessionID]
			if s == nil {
				return nil, nil, fmt.Errorf("session %s not found", in.SessionID)
			}
			return nil, s, nil
		}
		return nil, nil, fmt.Errorf("unknown action %q (want start|stop|list|reveal|hide|status)", in.Action)
	})
}

func firstOf(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}
