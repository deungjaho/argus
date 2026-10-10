package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const argusVersion = "0.1.0"

// enabledTools controls which tools are registered based on --toolsets.
// nil means all tools are enabled (default, backwards compatible).
var enabledTools []string

// toolEnabled returns true if the tool should be registered.
func toolEnabled(name string) bool {
	if enabledTools == nil {
		return true
	}
	for _, n := range enabledTools {
		if n == name {
			return true
		}
	}
	return false
}

func main() {
	fs := flag.NewFlagSet("argus", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		url      string
		port     int
		attach   bool
		profile  string
		debug    bool
		focus    bool
		toolsets string
	)
	fs.StringVar(&url, "url", "", "initial target URL (also set ARGUS_URL env)")
	fs.IntVar(&port, "port", 0, "target port (derives URL as http://localhost:PORT)")
	fs.BoolVar(&attach, "attach", false, "attach to existing CDP endpoint instead of launching Chrome (also set ARGUS_ATTACH env)")
	fs.StringVar(&profile, "profile", "", "profile mode: isolated (default) or user (use real Chrome profile with cookies/tokens)")
	fs.BoolVar(&debug, "debug", false, "enable debug logging to stderr")
	fs.BoolVar(&focus, "focus", false, "bring Chrome to the foreground: activate windows and open tabs in front (default: background only, never covers your screen; also set ARGUS_FOCUS env)")
	fs.StringVar(&toolsets, "toolsets", "", "comma-separated capability sets to expose on top of the always-on default loop (default,core,diagnose,debug,full). 'full' exposes everything including deprecated aliases + escape hatches. Also set ARGUS_TOOLSETS env.")
	var doctor bool
	fs.BoolVar(&doctor, "doctor", false, "run diagnostic checks and exit")
	_ = fs.Parse(os.Args[1:])

	if debug {
		logLevel = LogDebug
	}

	// Apply initial config from flags / env
	if url == "" {
		url = os.Getenv("ARGUS_URL")
	}
	if url == "" && port > 0 {
		url = fmt.Sprintf("http://localhost:%d", port)
	}
	if !attach {
		attach = os.Getenv("ARGUS_ATTACH") == "true" || os.Getenv("ARGUS_ATTACH") == "1"
	}
	if profile == "" {
		profile = os.Getenv("ARGUS_PROFILE")
	}
	if profile == "user" {
		setProfile("user")
	}
	if !focus {
		focus = envBoolDefault(false, "ARGUS_FOCUS")
	}
	setFocus(focus)
	// Toolset filtering: determines which tools are exposed to the MCP client.
	// The "default" loop (configure/status/snapshot/click/fill/navigate/eval/
	// screenshot/find/get_element/wait_for/exists/tool_summary) is always on —
	// it is the minimum surface an agent needs to function. Named sets add
	// on top: "diagnose" for evidence collection, "core" for internal
	// primitives (tab management, storage, intercepts, emulate), "debug" for
	// debugger tools, "full" for everything including deprecated aliases.
	if toolsets == "" {
		toolsets = os.Getenv("ARGUS_TOOLSETS")
	}
	if toolsets == "" {
		toolsets = "default"
	}
	enabledTools = parseToolsets(toolsets)

	if doctor {
		runDoctor(toolsets)
		return
	}

	logInfo("toolsets: %s (%d tools enabled)", toolsets, len(enabledTools))
	go cleanupScreenshots()

	attachPtr := &attach
	if url != "" || port > 0 {
		setConfig(url, port, attachPtr)
	} else if attach {
		setConfig("", 0, attachPtr)
	}

	// Graceful shutdown: all exit paths (signal, stdin EOF, server error)
	// go through the same cleanup: BeginGlobalShutdown + closeAllConns + cancel.
	ctx, cancel := context.WithCancel(context.Background())
	shutdownDone := make(chan struct{})
	var shutdownOnce sync.Once

	doShutdown := func(reason string) {
		logInfo("shutting down: %s", reason)
		BeginGlobalShutdown()
		closeAllConns()
		cancel()
		shutdownOnce.Do(func() { close(shutdownDone) })
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig, ok := <-sigCh
		if !ok {
			return
		}
		doShutdown(fmt.Sprintf("signal %s", sig))
	}()

	server := mcp.NewServer(&mcp.Implementation{Name: "argus", Version: argusVersion}, nil)
	registerAllTools(server)

	// Start background health checker for Chrome
	if !attach {
		startHealthChecker()
	}
	startPpidWatcher(doShutdown)

	logInfo("argus %s starting (cdp port=%d, attach=%v, profile=%s, focus=%v)", argusVersion, cdpPort, attach, profile, focus)
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		logError("server error: %v", err)
		// Server error (includes stdin EOF) — still clean up
		select {
		case <-shutdownDone:
			// Signal already triggered shutdown
		default:
			doShutdown(fmt.Sprintf("server exit: %v", err))
		}
		os.Exit(1)
	}
	// Normal exit (stdin EOF, server.Run returned nil)
	select {
	case <-shutdownDone:
	default:
		doShutdown("server stopped")
	}
	logInfo("argus stopped")
}

// addTool wraps mcp.AddTool with toolset filtering.
// If the tool is not in the enabled toolsets, it is silently skipped.
func addTool[In, Out any](server *mcp.Server, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) {
	if !toolEnabled(tool.Name) {
		return
	}
	mcp.AddTool(server, tool, handler)
}

// noInput is a zero-struct for tools that take no parameters.
type noInput struct{}

// tabScopeInput absorbs optional target, tab_id, or dummy parameters for parameterless tools.
type tabScopeInput struct {
	Target   string `json:"target,omitempty"`
	TargetID string `json:"target_id,omitempty"`
	TabID    string `json:"tab_id,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

func registerAllTools(server *mcp.Server) {
	registerConfigTools(server)
	registerNavigationTools(server)
	registerScreenshotTools(server)
	registerSnapshotTools(server)
	registerInteractionTools(server)
	registerNetworkTools(server)
	registerDialogTools(server)
	registerTabTools(server)
	registerWaitTools(server)
	registerEmulateTools(server)
	registerInspectTools(server)
	registerStorageTools(server)
	registerElementTools(server)
	registerInterceptTools(server)
	registerMiscTools(server)
	registerTraceTools(server)
	registerDiscoveryTools(server)
	registerDiffTools(server)
	registerRecordTools(server)
}

// runDoctor runs diagnostic checks and exits.
func runDoctor(toolsets string) {
	failed := 0

	// Binary self-check.
	fmt.Println("ok      argus")

	// CDP port reachability (check first — if CDP is up, Chrome is running).
	// Check the configured port first, then the well-known defaults.
	reachable := false
	foundPort := 0
	ports := []int{cdpPort}
	for _, p := range []int{9222, 9224} {
		if p != cdpPort {
			ports = append(ports, p)
		}
	}
	for _, p := range ports {
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", p))
		if err == nil {
			resp.Body.Close()
			fmt.Printf("ok      cdp-port-%d\n", p)
			reachable = true
			foundPort = p
			break
		}
	}

	// Chrome binary discovery.
	if bin, err := findChromeBinary(); err == nil && bin != "" {
		fmt.Printf("ok      chrome (%s)\n", bin)
	} else if reachable {
		fmt.Printf("ok      chrome (running via CDP %d, binary not in standard paths)\n", foundPort)
	} else {
		fmt.Println("missing chrome (install Google Chrome or Chromium)")
		failed = 1
	}

	if !reachable {
		fmt.Println("info    no-cdp-running (Chrome not launched yet — normal if not started)")
	}

	// Toolset validity.
	tools := parseToolsets(toolsets)
	if len(tools) > 0 {
		fmt.Printf("ok      toolsets (%s, %d tools)\n", toolsets, len(tools))
	} else {
		fmt.Println("failed  toolsets (no tools enabled)")
		failed = 1
	}

	os.Exit(failed)
}
