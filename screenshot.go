package main

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func defaultScreenshotPath() (string, error) {
	return filepath.Join(os.TempDir(), "argus-screenshot-"+time.Now().Format("20060102-150405.000000000")+".jpg"), nil
}

func defaultScreenshotFilename(ext string) string {
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return "argus-screenshot-" + time.Now().Format("20060102-150405.000000000") + ext
}

// resolveScreenshotFormat normalizes format, defaulting to jpeg unless png is requested or path ends in .png.
func resolveScreenshotFormat(format, outPath string) string {
	f := strings.ToLower(strings.TrimSpace(format))
	if f == "png" || f == "image/png" {
		return "png"
	}
	if f == "jpeg" || f == "jpg" || f == "image/jpeg" || f == "image/jpg" {
		return "jpeg"
	}
	if strings.HasSuffix(strings.ToLower(outPath), ".png") {
		return "png"
	}
	return "jpeg"
}

// resolveScreenshotPath returns a cleaned path, expanding ~/ and defaulting to temp if empty.
// If the path is or points to a directory, it generates a timestamped filename inside it.
func resolveScreenshotPath(rawPath string, format string) (string, error) {
	rawPath = strings.TrimSpace(rawPath)
	resolvedFmt := resolveScreenshotFormat(format, rawPath)
	ext := ".jpg"
	if resolvedFmt == "png" {
		ext = ".png"
	}
	if rawPath == "" {
		return filepath.Join(os.TempDir(), defaultScreenshotFilename(ext)), nil
	}
	if rawPath == "~" || strings.HasPrefix(rawPath, "~/") || strings.HasPrefix(rawPath, `~\`) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if rawPath == "~" {
				rawPath = home
			} else {
				rawPath = filepath.Join(home, rawPath[2:])
			}
		}
	}
	clean := filepath.Clean(rawPath)
	if strings.HasSuffix(rawPath, "/") || strings.HasSuffix(rawPath, string(filepath.Separator)) {
		clean = filepath.Join(clean, defaultScreenshotFilename(ext))
	} else if fi, err := os.Stat(clean); err == nil && fi.IsDir() {
		clean = filepath.Join(clean, defaultScreenshotFilename(ext))
	}
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}
	return clean, nil
}

// cleanupScreenshots removes argus screenshot temp files older than 24h.
// Screenshots are debugging artifacts; without GC they accumulate forever.
func cleanupScreenshots() {
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), "argus-screenshot-*"))
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, p := range matches {
		if strings.HasSuffix(p, ".jpg") || strings.HasSuffix(p, ".png") || strings.HasSuffix(p, ".jpeg") {
			if info, err := os.Stat(p); err == nil && info.ModTime().Before(cutoff) {
				_ = os.Remove(p)
			}
		}
	}
}

func imageDimensions(data []byte) (int, int, bool) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, false
	}
	return config.Width, config.Height, true
}

// encodeScreenshotJPEG decodes the PNG screenshot from CDP and re-encodes
// it as a compressed JPEG, downscaling beyond maxSize. Cross-platform.
func encodeScreenshotJPEG(rawPNG []byte) ([]byte, error) {
	return encodeScreenshotJPEGWithOptions(rawPNG, 0, 0, false)
}

func encodeScreenshotJPEGWithParams(rawPNG []byte, customQuality int, customMaxSize int) ([]byte, error) {
	return encodeScreenshotJPEGWithOptions(rawPNG, customQuality, customMaxSize, false)
}

func encodeScreenshotJPEGWithOptions(rawPNG []byte, customQuality int, customMaxWidth int, fullPage bool) ([]byte, error) {
	maxSize := 1400
	if customMaxWidth > 0 {
		maxSize = customMaxWidth
	} else if value := strings.TrimSpace(os.Getenv("ARGUS_SCREENSHOT_MAX")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			maxSize = parsed
		}
	}
	quality := 60
	if customQuality > 100 {
		quality = 100
	} else if customQuality > 0 {
		quality = customQuality
	} else if value := strings.TrimSpace(os.Getenv("ARGUS_SCREENSHOT_QUALITY")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			quality = parsed
		}
	}
	img, _, err := image.Decode(bytes.NewReader(rawPNG))
	if err != nil {
		return nil, fmt.Errorf("failed to decode screenshot: %w", err)
	}
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if fullPage {
		if width > maxSize {
			img = downscaleImage(img, maxSize, 0)
		}
	} else {
		if width > maxSize || height > maxSize {
			img = downscaleImage(img, maxSize, maxSize)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("failed to encode JPEG: %w", err)
	}
	return buf.Bytes(), nil
}

func encodeScreenshotPNG(rawPNG []byte, customMaxSize int) ([]byte, error) {
	return encodeScreenshotPNGWithOptions(rawPNG, customMaxSize, false)
}

func encodeScreenshotPNGWithOptions(rawPNG []byte, customMaxWidth int, fullPage bool) ([]byte, error) {
	maxSize := 1400
	if customMaxWidth > 0 {
		maxSize = customMaxWidth
	} else if value := strings.TrimSpace(os.Getenv("ARGUS_SCREENSHOT_MAX")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			maxSize = parsed
		}
	}
	img, _, err := image.Decode(bytes.NewReader(rawPNG))
	if err != nil {
		return nil, fmt.Errorf("failed to decode screenshot: %w", err)
	}
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	needsScale := false
	if fullPage {
		needsScale = width > maxSize
	} else {
		needsScale = width > maxSize || height > maxSize
	}
	if needsScale {
		if fullPage {
			img = downscaleImage(img, maxSize, 0)
		} else {
			img = downscaleImage(img, maxSize, maxSize)
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return nil, fmt.Errorf("failed to encode PNG: %w", err)
		}
		return buf.Bytes(), nil
	}
	return rawPNG, nil
}

// writeScreenshot encodes rawPNG and writes it to outPath.
func writeScreenshot(rawPNG []byte, outPath string, format string, quality int, maxWidth int, fullPage ...bool) ([]byte, string, error) {
	if strings.TrimSpace(outPath) == "" {
		return nil, "", fmt.Errorf("output path is required")
	}
	isFullPage := false
	if len(fullPage) > 0 && fullPage[0] {
		isFullPage = true
	}
	resolvedFormat := resolveScreenshotFormat(format, outPath)
	var data []byte
	var mimeType string
	var err error

	if resolvedFormat == "png" {
		data, err = encodeScreenshotPNGWithOptions(rawPNG, maxWidth, isFullPage)
		mimeType = "image/png"
	} else {
		data, err = encodeScreenshotJPEGWithOptions(rawPNG, quality, maxWidth, isFullPage)
		mimeType = "image/jpeg"
	}
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return nil, "", fmt.Errorf("failed to create directory for screenshot: %w", err)
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return nil, "", fmt.Errorf("failed to write screenshot to %s: %w", outPath, err)
	}
	return data, mimeType, nil
}

// writeCompressedScreenshot encodes the screenshot as JPEG to outPath.
func writeCompressedScreenshot(rawPNG []byte, outPath string) error {
	_, _, err := writeScreenshot(rawPNG, outPath, "", 0, 0, false)
	return err
}

// downscale resizes an image so that neither dimension exceeds maxSize, preserving aspect ratio.
func downscale(img image.Image, maxSize int) image.Image {
	return downscaleImage(img, maxSize, maxSize)
}

// downscaleImage resizes an image constraining maxWidth (if > 0) and maxHeight (if > 0),
// preserving aspect ratio. Uses nearest-neighbor (fast, sufficient for debugging screenshots).
func downscaleImage(img image.Image, maxWidth, maxHeight int) image.Image {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if width <= 0 || height <= 0 {
		return img
	}
	scale := 1.0
	if maxWidth > 0 && width > maxWidth {
		scale = float64(maxWidth) / float64(width)
	}
	if maxHeight > 0 && height > maxHeight {
		hScale := float64(maxHeight) / float64(height)
		if hScale < scale {
			scale = hScale
		}
	}
	if scale >= 1.0 {
		return img
	}
	newWidth := int(float64(width) * scale)
	newHeight := int(float64(height) * scale)
	if newWidth < 1 {
		newWidth = 1
	}
	if newHeight < 1 {
		newHeight = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	for y := 0; y < newHeight; y++ {
		srcY := bounds.Min.Y + int(float64(y)/scale)
		if srcY >= bounds.Max.Y {
			srcY = bounds.Max.Y - 1
		}
		for x := 0; x < newWidth; x++ {
			srcX := bounds.Min.X + int(float64(x)/scale)
			if srcX >= bounds.Max.X {
				srcX = bounds.Max.X - 1
			}
			dst.Set(x, y, img.At(srcX, srcY))
		}
	}
	return dst
}

// resolveElementClip locates an element via CSS selector or a11y ref and returns its clip box.
func resolveElementClip(ws, selector string) (*ScreenshotClip, map[string]any, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil, nil, fmt.Errorf("selector is empty")
	}

	// 1. If it's an a11y node reference
	if a11yNode, isA11y, err := resolveA11yNode(ws, selector); err == nil && isA11y {
		if a11yNode.BackendDOMNodeID <= 0 {
			return nil, nil, fmt.Errorf("a11y node %q has invalid backendDOMNodeId: %d", a11yNode.NodeID, a11yNode.BackendDOMNodeID)
		}
		_ = cdpScrollIntoViewIfNeeded(ws, a11yNode.BackendDOMNodeID)
		model, bErr := cdpGetBoxModel(ws, a11yNode.BackendDOMNodeID)
		if bErr != nil {
			return nil, nil, fmt.Errorf("failed to get box model for a11y node %q: %w", a11yNode.NodeID, bErr)
		}
		box := model.BoundingBox()
		if box == nil {
			return nil, nil, fmt.Errorf("invalid box model for a11y node %q", a11yNode.NodeID)
		}
		w, _ := numberValue(box["width"])
		h, _ := numberValue(box["height"])
		x, _ := numberValue(box["x"])
		y, _ := numberValue(box["y"])
		if w <= 0 || h <= 0 {
			return nil, nil, fmt.Errorf("element %q has zero dimensions (width: %.1f, height: %.1f); cannot capture screenshot", selector, w, h)
		}
		// Query page scroll offsets to convert to page coordinates for captureBeyondViewport
		scrollVal, _ := cdpEvaluate(ws, `(() => ({scrollX: window.scrollX, scrollY: window.scrollY}))()`, true)
		scrollX, scrollY := 0.0, 0.0
		if sm, ok := scrollVal.(map[string]any); ok {
			scrollX, _ = numberValue(sm["scrollX"])
			scrollY, _ = numberValue(sm["scrollY"])
		}
		clip := &ScreenshotClip{
			X:      x + scrollX,
			Y:      y + scrollY,
			Width:  w,
			Height: h,
			Scale:  1.0,
		}
		return clip, box, nil
	}

	// 2. CSS selector resolution via page evaluation
	queryJS := fmt.Sprintf(`(() => {
const sel = %s;
let el = null;
try {
  el = document.querySelector(sel);
} catch (e) {
  el = document.getElementById(sel) ||
       document.querySelector('[data-testid=' + JSON.stringify(sel) + ']') ||
       document.querySelector('[data-test-id=' + JSON.stringify(sel) + ']');
}
if (!el) return {found: false};
el.scrollIntoView({block: 'center', inline: 'center', behavior: 'instant'});
const rect = el.getBoundingClientRect();
return {
  found: true,
  x: rect.x + window.scrollX,
  y: rect.y + window.scrollY,
  width: rect.width,
  height: rect.height,
  tag: el.localName
};
})()`, jsString(selector))

	val, err := cdpEvaluate(ws, queryJS, true)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to evaluate selector %q: %w", selector, err)
	}
	info, ok := val.(map[string]any)
	if !ok || info == nil {
		return nil, nil, fmt.Errorf("element not found: %s", selector)
	}
	found, _ := info["found"].(bool)
	if !found {
		return nil, nil, fmt.Errorf("element not found: %s", selector)
	}
	w, _ := numberValue(info["width"])
	h, _ := numberValue(info["height"])
	x, _ := numberValue(info["x"])
	y, _ := numberValue(info["y"])
	if w <= 0 || h <= 0 {
		return nil, nil, fmt.Errorf("element %q has zero dimensions (width: %.1f, height: %.1f); cannot capture screenshot", selector, w, h)
	}

	clip := &ScreenshotClip{
		X:      x,
		Y:      y,
		Width:  w,
		Height: h,
		Scale:  1.0,
	}
	return clip, info, nil
}
