package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type screenshotInput struct {
	Path          string `json:"path,omitempty"`
	FilePath      string `json:"file_path,omitempty"`
	File          string `json:"file,omitempty"`
	Filename      string `json:"filename,omitempty"`
	Output        string `json:"output,omitempty"`
	Format        string `json:"format,omitempty"`
	Type          string `json:"type,omitempty"`
	Quality       int    `json:"quality,omitempty"`
	FullPage      bool   `json:"full_page,omitempty"`
	FullPageCamel bool   `json:"fullPage,omitempty"`
	MaxWidth      int    `json:"max_width,omitempty"`
	Grid          bool   `json:"grid,omitempty"`
	MajorStep     int    `json:"major_step,omitempty"`
	MinorStep     int    `json:"minor_step,omitempty"`
	MaxSizeKB     int    `json:"max_size_kb,omitempty"` // bound screenshot file size (default 500, max 2000)
	Target        string `json:"target,omitempty"`
	TargetID      string `json:"target_id,omitempty"`
	TabID         string `json:"tab_id,omitempty"`
}

type screenshotFullPageInput struct {
	Path          string `json:"path,omitempty"`
	FilePath      string `json:"file_path,omitempty"`
	File          string `json:"file,omitempty"`
	Filename      string `json:"filename,omitempty"`
	Output        string `json:"output,omitempty"`
	Format        string `json:"format,omitempty"`
	Type          string `json:"type,omitempty"`
	Quality       int    `json:"quality,omitempty"`
	FullPage      bool   `json:"full_page,omitempty"`
	FullPageCamel bool   `json:"fullPage,omitempty"`
	MaxWidth      int    `json:"max_width,omitempty"`
	Grid          bool   `json:"grid,omitempty"`
	MajorStep     int    `json:"major_step,omitempty"`
	MinorStep     int    `json:"minor_step,omitempty"`
	MaxSizeKB     int    `json:"max_size_kb,omitempty"`
	Target        string `json:"target,omitempty"`
	TargetID      string `json:"target_id,omitempty"`
	TabID         string `json:"tab_id,omitempty"`
}

func resolveInputPath(paths ...string) string {
	for _, p := range paths {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func resolveInputFormat(format, typ string) string {
	if strings.TrimSpace(format) != "" {
		return strings.TrimSpace(format)
	}
	return strings.TrimSpace(typ)
}

func registerScreenshotTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "screenshot",
		Description: "Capture a compressed JPEG screenshot of the current viewport. Returns image + metadata (viewport dimensions, image dimensions, coordinate system). Use viewport x/y with click. Set grid=true to overlay a coordinate grid (use those x/y values with click or inspect_point). max_size_kb bounds the screenshot file (default 500, max 2000); if exceeded, truncation metadata is returned instead of the image. Optional path saves the screenshot to a file.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input screenshotInput) (*mcp.CallToolResult, any, error) {
		return screenshotResult(input)
	})

	addTool(server, &mcp.Tool{
		Name:        "screenshot_full_page",
		Description: "Capture a full-page screenshot (including content below the fold). Returns image + metadata. Optional path saves the screenshot to a file.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input screenshotFullPageInput) (*mcp.CallToolResult, any, error) {
		if targetID := resolveFirstNonEmpty(input.TargetID, input.TabID, input.Target); targetID != "" {
			setCurrentTargetID(targetID)
		}
		target, err := requireTarget()
		if err != nil {
			return nil, nil, err
		}
		ws := target.WebSocketDebuggerURL
		pngData, err := cdpCaptureScreenshot(ws, true)
		if err != nil {
			return nil, nil, err
		}
		reqPath := resolveInputPath(input.Path, input.FilePath, input.File, input.Filename, input.Output)
		reqFormat := resolveInputFormat(input.Format, input.Type)
		path, err := resolveScreenshotPath(reqPath, reqFormat)
		if err != nil {
			return nil, nil, err
		}
		imgData, mimeType, err := writeScreenshot(pngData, path, reqFormat, input.Quality, input.MaxWidth, true)
		if err != nil {
			return nil, nil, err
		}
		imageWidth, imageHeight, _ := imageDimensions(imgData)
		meta := map[string]any{
			"url":       target.URL,
			"title":     target.Title,
			"path":      path,
			"bytes":     len(imgData),
			"mime_type": mimeType,
			"full_page": true,
			"image": map[string]any{
				"width":  imageWidth,
				"height": imageHeight,
			},
		}
		if input.MaxSizeKB > 0 {
			maxSizeKB := input.MaxSizeKB
			if maxSizeKB > 2000 {
				maxSizeKB = 2000
			}
			maxBytes := maxSizeKB * 1024
			if len(imgData) > maxBytes {
				attachTruncation(meta, TruncationMeta{
					Truncated:  true,
					TotalCount: len(imgData),
					ShownCount: 0,
					Reason:     fmt.Sprintf("screenshot %d bytes exceeds max_size_kb=%d (%d bytes); image omitted from response", len(imgData), maxSizeKB, maxBytes),
				})
				meta["max_size_kb"] = maxSizeKB
				metaText, _ := json.MarshalIndent(meta, "", "  ")
				return &mcp.CallToolResult{Content: []mcp.Content{
					&mcp.TextContent{Text: string(metaText)},
				}}, nil, nil
			}
		}
		metaText, _ := json.MarshalIndent(meta, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: string(metaText)},
			&mcp.ImageContent{Data: imgData, MIMEType: mimeType},
		}}, nil, nil
	})
}

func screenshotResult(input screenshotInput) (*mcp.CallToolResult, any, error) {
	if targetID := resolveFirstNonEmpty(input.TargetID, input.TabID, input.Target); targetID != "" {
		setCurrentTargetID(targetID)
	}
	target, err := requireTarget()
	if err != nil {
		return nil, nil, err
	}
	ws := target.WebSocketDebuggerURL
	if input.Grid {
		majorStep := input.MajorStep
		minorStep := input.MinorStep
		if majorStep <= 0 {
			majorStep = 100
		}
		if minorStep <= 0 {
			minorStep = 25
		}
		if _, err := cdpEvaluate(ws, coordinateOverlayExpression(majorStep, minorStep), true); err != nil {
			return nil, nil, err
		}
		defer func() {
			_, _ = cdpEvaluate(ws, `document.getElementById('__argus_coordinate_overlay__')?.remove(); true`, true)
		}()
	}
	fullPage := input.FullPage || input.FullPageCamel
	viewport, _ := viewportMetrics(ws)
	pngData, err := cdpCaptureScreenshot(ws, fullPage)
	if err != nil {
		return nil, nil, err
	}
	reqPath := resolveInputPath(input.Path, input.FilePath, input.File, input.Filename, input.Output)
	reqFormat := resolveInputFormat(input.Format, input.Type)
	path, err := resolveScreenshotPath(reqPath, reqFormat)
	if err != nil {
		return nil, nil, err
	}
	imgData, mimeType, err := writeScreenshot(pngData, path, reqFormat, input.Quality, input.MaxWidth, fullPage)
	if err != nil {
		return nil, nil, err
	}
	maxSizeKB := input.MaxSizeKB
	if !fullPage {
		// Enforce the max_size_kb bound. Default 500 KB, hard max 2000 KB.
		if maxSizeKB <= 0 {
			maxSizeKB = 500
		}
		if maxSizeKB > 2000 {
			maxSizeKB = 2000
		}
	} else if maxSizeKB > 2000 {
		maxSizeKB = 2000
	}
	imageWidth, imageHeight, _ := imageDimensions(imgData)
	meta := map[string]any{
		"url":       target.URL,
		"title":     target.Title,
		"path":      path,
		"bytes":     len(imgData),
		"mime_type": mimeType,
		"grid":      input.Grid,
		"image": map[string]any{
			"width":  imageWidth,
			"height": imageHeight,
		},
		"viewport": viewport,
		"coordinate_system": map[string]any{
			"origin": "top-left",
			"x":      "viewport CSS pixels increasing right",
			"y":      "viewport CSS pixels increasing down",
			"click":  "Pass x/y directly to click. If using image pixels from a resized screenshot, pass image_x/image_y with screenshot_path.",
		},
	}
	if input.FullPage {
		meta["full_page"] = true
	}
	if scale := imageToViewportScale(viewport, imageWidth, imageHeight); scale != nil {
		meta["image_to_viewport"] = scale
	}
	if input.Grid {
		majorStep := input.MajorStep
		minorStep := input.MinorStep
		if majorStep <= 0 {
			majorStep = 100
		}
		if minorStep <= 0 {
			minorStep = 25
		}
		meta["grid_steps"] = map[string]any{"major": majorStep, "minor": minorStep}
	}

	// If the screenshot exceeds the size bound, return truncation metadata
	// instead of the image payload. The file is kept on disk so the agent
	// can retrieve it via the path if needed, but the image is not embedded
	// in the MCP response to protect the context window.
	if maxSizeKB > 0 {
		maxBytes := maxSizeKB * 1024
		if len(imgData) > maxBytes {
			attachTruncation(meta, TruncationMeta{
				Truncated:  true,
				TotalCount: len(imgData),
				ShownCount: 0,
				Reason:     fmt.Sprintf("screenshot %d bytes exceeds max_size_kb=%d (%d bytes); image omitted from response", len(imgData), maxSizeKB, maxBytes),
			})
			meta["max_size_kb"] = maxSizeKB
			metaText, _ := json.MarshalIndent(meta, "", "  ")
			return &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.TextContent{Text: string(metaText)},
			}}, nil, nil
		}
	}

	metaText, _ := json.MarshalIndent(meta, "", "  ")
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: string(metaText)},
		&mcp.ImageContent{Data: imgData, MIMEType: mimeType},
	}}, nil, nil
}

func coordinateOverlayExpression(majorStep int, minorStep int) string {
	return fmt.Sprintf(`(() => new Promise((resolve) => {
document.getElementById('__argus_coordinate_overlay__')?.remove();
const major = %d;
const minor = %d;
const overlay = document.createElement('div');
overlay.id = '__argus_coordinate_overlay__';
overlay.style.cssText = 'position:fixed;inset:0;z-index:2147483647;pointer-events:none;font:11px ui-monospace,SFMono-Regular,Menlo,monospace;color:#b91c1c;';
const svgNS = 'http://www.w3.org/2000/svg';
const svg = document.createElementNS(svgNS, 'svg');
svg.setAttribute('width', innerWidth);
svg.setAttribute('height', innerHeight);
svg.setAttribute('viewBox', '0 0 ' + innerWidth + ' ' + innerHeight);
svg.style.cssText = 'position:absolute;inset:0;width:100%%;height:100%%;';
const addLine = (x1, y1, x2, y2, color, width) => {
  const line = document.createElementNS(svgNS, 'line');
  line.setAttribute('x1', x1); line.setAttribute('y1', y1); line.setAttribute('x2', x2); line.setAttribute('y2', y2);
  line.setAttribute('stroke', color); line.setAttribute('stroke-width', width); line.setAttribute('vector-effect', 'non-scaling-stroke');
  svg.appendChild(line);
};
const addText = (x, y, text) => {
  const label = document.createElementNS(svgNS, 'text');
  label.setAttribute('x', x); label.setAttribute('y', y); label.setAttribute('fill', '#b91c1c'); label.setAttribute('stroke', 'white'); label.setAttribute('stroke-width', '3'); label.setAttribute('paint-order', 'stroke');
  label.textContent = text;
  svg.appendChild(label);
};
for (let x = 0; x <= innerWidth; x += minor) addLine(x, 0, x, innerHeight, x %% major === 0 ? 'rgba(185,28,28,.55)' : 'rgba(185,28,28,.18)', x %% major === 0 ? 1.5 : 1);
for (let y = 0; y <= innerHeight; y += minor) addLine(0, y, innerWidth, y, y %% major === 0 ? 'rgba(185,28,28,.55)' : 'rgba(185,28,28,.18)', y %% major === 0 ? 1.5 : 1);
for (let x = 0; x <= innerWidth; x += major) addText(x + 3, 13, String(x));
for (let y = major; y <= innerHeight; y += major) addText(3, y - 3, String(y));
overlay.appendChild(svg);
document.documentElement.appendChild(overlay);
requestAnimationFrame(() => resolve(true));
}))()`, majorStep, minorStep)
}
