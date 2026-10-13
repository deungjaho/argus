package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type emulateInput struct {
	Device         string `json:"device,omitempty"`
	Width          int    `json:"width,omitempty"`
	Height         int    `json:"height,omitempty"`
	UserAgent      string `json:"user_agent,omitempty"`
	UserAgentCamel string `json:"userAgent,omitempty"`
	Target         string `json:"target,omitempty"`
	TargetID       string `json:"target_id,omitempty"`
	TabID          string `json:"tab_id,omitempty"`
}

type evalInput struct {
	Script       string `json:"script,omitempty"`
	Expression   string `json:"expression,omitempty"`
	Code         string `json:"code,omitempty"`
	JS           string `json:"js,omitempty"`
	AwaitPromise bool   `json:"await_promise,omitempty"`
	TimeoutMs    int    `json:"timeout_ms,omitempty"` // CDP call timeout (default 10s); raise for long await_promise work
	Timeout      int    `json:"timeout,omitempty"`
	Target       string `json:"target,omitempty"`
	TargetID     string `json:"target_id,omitempty"`
	TabID        string `json:"tab_id,omitempty"`
}

func registerEmulateTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "emulate",
		Description: "Emulate a device (iPhone, iPad, Pixel, etc.) or set a custom viewport size. Provide device for a preset, or width/height for custom dimensions. Optionally override the User-Agent string with user_agent.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input emulateInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("emulate")
		defer rec.End()

		targetID := resolveFirstNonEmpty(input.TargetID, input.TabID, input.Target)
		if targetID != "" {
			setCurrentTargetID(targetID)
		}
		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())
		rec.SetParams(input)
		device := strings.TrimSpace(input.Device)
		ua := resolveFirstNonEmpty(input.UserAgent, input.UserAgentCamel)

		// No-op if nothing to do.
		if device == "" && (input.Width <= 0 || input.Height <= 0) && ua == "" {
			err := fmt.Errorf("emulate requires device, width/height, or user_agent")
			rec.SetErrorFromError(err)
			return nil, nil, err
		}

		result := map[string]any{"action_id": string(rec.ID)}

		// Device preset takes precedence for viewport metrics.
		if device != "" {
			profile, ok := deviceProfiles[device]
			if !ok {
				err := fmt.Errorf("unknown device: %s (available: %s)", device, availableDevices())
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			if err := cdpRequest(ws, "Emulation.setDeviceMetricsOverride", map[string]any{
				"width":             profile.Width,
				"height":            profile.Height,
				"deviceScaleFactor": profile.ScaleFactor,
				"mobile":            profile.Mobile,
				"screenOrientation": map[string]any{"type": "portraitPrimary", "angle": 0},
			}, nil); err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			result["emulated"] = device
			result["width"] = profile.Width
			result["height"] = profile.Height
			// A device preset may carry its own user agent unless an explicit
			// override is supplied.
			if ua == "" {
				ua = profile.UserAgent
			}
		} else if input.Width > 0 && input.Height > 0 {
			// Custom viewport resize.
			if err := cdpRequest(ws, "Emulation.setDeviceMetricsOverride", map[string]any{
				"width":             input.Width,
				"height":            input.Height,
				"deviceScaleFactor": 1,
				"mobile":            false,
			}, nil); err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			result["resized"] = true
			result["width"] = input.Width
			result["height"] = input.Height
		}

		// User-Agent override (from explicit input or device preset).
		if ua != "" {
			if err := cdpRequest(ws, "Network.setUserAgentOverride", map[string]any{"userAgent": ua}, nil); err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
			result["user_agent"] = ua
		}

		rec.SetResult(result)
		return nil, result, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "eval",
		Description: "Evaluate a JavaScript expression on the page and return the result. Accepts script or expression. Set await_promise=true to wait for promises, timeout_ms to raise the 10s call timeout. Top-level const/let persist across calls; scripts that hit 'already been declared' are automatically retried in an isolated scope.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input evalInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("eval")
		defer rec.End()

		targetID := resolveFirstNonEmpty(input.TargetID, input.TabID)
		if targetID == "" && input.Target != "" && !looksLikeURL(input.Target) {
			targetID = input.Target
		}
		if targetID != "" {
			setCurrentTargetID(targetID)
		} else if input.Target != "" && looksLikeURL(input.Target) {
			setConfig(input.Target, 0, nil)
		}
		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())
		rec.SetParams(input)
		script := resolveFirstNonEmpty(input.Script, input.Expression, input.Code, input.JS)
		if script == "" {
			err := fmt.Errorf("eval requires script, expression, or js")
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		timeoutMs := resolveTimeout(input.TimeoutMs, input.Timeout)
		timeout := time.Duration(timeoutMs) * time.Millisecond
		value, err := cdpEvaluateTimeout(ws, script, input.AwaitPromise, timeout)
		if err != nil && strings.Contains(err.Error(), "has already been declared") {
			// const/let from earlier eval calls persist in the global scope.
			// eval() runs the script in its own declarative environment —
			// declarations don't leak, and the completion value is preserved.
			value, err = cdpEvaluateTimeout(ws, "eval("+jsString(script)+")", input.AwaitPromise, timeout)
		}
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		if m, ok := value.(map[string]any); ok {
			if _, exists := m["action_id"]; !exists {
				m["action_id"] = string(rec.ID)
			}
			attachEvidence(rec, ws, m)
			rec.SetResult(m)
			return nil, m, nil
		}
		result := map[string]any{"result": value, "action_id": string(rec.ID)}
		attachEvidence(rec, ws, result)
		rec.SetResult(result)
		return nil, result, nil
	})
}

type deviceProfile struct {
	Width       int
	Height      int
	ScaleFactor float64
	Mobile      bool
	UserAgent   string
}

var deviceProfiles = map[string]deviceProfile{
	"iPhone 12":     {390, 844, 3, true, "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"},
	"iPhone SE":     {375, 667, 2, true, "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"},
	"iPad":          {810, 1080, 2, true, "Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"},
	"Pixel 7":       {412, 915, 2.625, true, "Mozilla/5.0 (Linux; Android 14; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36"},
	"Galaxy S23":    {360, 780, 3, true, "Mozilla/5.0 (Linux; Android 14; SM-S911B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36"},
	"Desktop 1080p": {1920, 1080, 1, false, ""},
	"Desktop 1440p": {2560, 1440, 1, false, ""},
}

func availableDevices() string {
	devices := []string{}
	for name := range deviceProfiles {
		devices = append(devices, name)
	}
	return strings.Join(devices, ", ")
}
