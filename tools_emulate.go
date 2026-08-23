package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type resizeInput struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type emulateInput struct {
	Device string `json:"device"`
}

type evalInput struct {
	Script       string `json:"script"`
	AwaitPromise bool   `json:"await_promise,omitempty"`
}

func registerEmulateTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "resize",
		Description: "Resize the browser viewport to specific width and height (in CSS pixels). Useful for testing responsive layouts.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input resizeInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("resize")
		defer rec.End()

		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())
		rec.SetParams(input)
		if input.Width <= 0 || input.Height <= 0 {
			err := fmt.Errorf("resize requires positive width and height")
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		if err := cdpRequest(ws, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width":             input.Width,
			"height":            input.Height,
			"deviceScaleFactor": 1,
			"mobile":            false,
		}, nil); err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		result := map[string]any{"resized": true, "width": input.Width, "height": input.Height, "action_id": string(rec.ID)}
		rec.SetResult(result)
		return nil, result, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "emulate",
		Description: "Emulate a device (iPhone, iPad, Pixel, etc.) by setting viewport size, device scale factor, and user agent.",
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
		if profile.UserAgent != "" {
			if err := cdpRequest(ws, "Network.setUserAgentOverride", map[string]any{"userAgent": profile.UserAgent}, nil); err != nil {
				rec.SetErrorFromError(err)
				return nil, nil, err
			}
		}
		result := map[string]any{"emulated": device, "width": profile.Width, "height": profile.Height, "user_agent": profile.UserAgent, "action_id": string(rec.ID)}
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
