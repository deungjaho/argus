package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type emulateInput struct {
	Device    string `json:"device,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
}

type evalInput struct {
	Script       string `json:"script"`
	AwaitPromise bool   `json:"await_promise,omitempty"`
}

func registerEmulateTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "emulate",
		Description: "Emulate a device (iPhone, iPad, Pixel, etc.) or set a custom viewport size. Provide device for a preset, or width/height for custom dimensions. Optionally override the User-Agent string with user_agent.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input emulateInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("emulate")
		defer rec.End()

		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())
		rec.SetParams(input)
		device := strings.TrimSpace(input.Device)
		ua := strings.TrimSpace(input.UserAgent)

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
		Description: "Evaluate a JavaScript expression on the page and return the result. Set await_promise=true to wait for promises.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input evalInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("eval")
		defer rec.End()

		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())
		rec.SetParams(input)
		if strings.TrimSpace(input.Script) == "" {
			err := fmt.Errorf("eval requires script")
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		value, err := cdpEvaluate(ws, input.Script, input.AwaitPromise)
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		if m, ok := value.(map[string]any); ok {
			m["action_id"] = string(rec.ID)
			rec.SetResult(m)
			return nil, m, nil
		}
		result := map[string]any{"result": value, "action_id": string(rec.ID)}
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
