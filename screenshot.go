package main

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func defaultScreenshotPath() (string, error) {
	return filepath.Join(os.TempDir(), "argus-screenshot-"+time.Now().Format("20060102-150405.000000000")+".jpg"), nil
}

// cleanupScreenshots removes argus screenshot temp files older than 24h.
// Screenshots are debugging artifacts; without GC they accumulate forever.
func cleanupScreenshots() {
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), "argus-screenshot-*.jpg"))
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, p := range matches {
		if info, err := os.Stat(p); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(p)
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

// writeCompressedScreenshot decodes the PNG screenshot from CDP and re-encodes
// as a compressed JPEG, resizing if larger than maxSize. Cross-platform
// (replaces the original sips-based implementation that was macOS-only).
func writeCompressedScreenshot(rawPNG []byte, outPath string) error {
	if strings.TrimSpace(outPath) == "" {
		return fmt.Errorf("output path is required")
	}
	maxSize := 1400
	quality := 60
	if value := strings.TrimSpace(os.Getenv("ARGUS_SCREENSHOT_MAX")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			maxSize = parsed
		}
	}
	if value := strings.TrimSpace(os.Getenv("ARGUS_SCREENSHOT_QUALITY")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			quality = parsed
		}
	}
	// Decode PNG
	img, _, err := image.Decode(bytes.NewReader(rawPNG))
	if err != nil {
		return fmt.Errorf("failed to decode screenshot: %w", err)
	}
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	// Resize if needed (simple nearest-neighbor downscale)
	if width > maxSize || height > maxSize {
		img = downscale(img, maxSize)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	file, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := jpeg.Encode(file, img, &jpeg.Options{Quality: quality}); err != nil {
		return fmt.Errorf("failed to encode JPEG: %w", err)
	}
	return nil
}

// downscale resizes an image so that neither dimension exceeds maxSize,
// preserving aspect ratio. Uses nearest-neighbor (fast, sufficient for
// debugging screenshots).
func downscale(img image.Image, maxSize int) image.Image {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	scale := 1.0
	if width > maxSize {
		scale = float64(maxSize) / float64(width)
	}
	if height > maxSize {
		hScale := float64(maxSize) / float64(height)
		if hScale < scale {
			scale = hScale
		}
	}
	if scale >= 1.0 {
		return img
	}
	newWidth := int(float64(width) * scale)
	newHeight := int(float64(height) * scale)
	dst := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	for y := 0; y < newHeight; y++ {
		srcY := bounds.Min.Y + int(float64(y)/scale)
		for x := 0; x < newWidth; x++ {
			srcX := bounds.Min.X + int(float64(x)/scale)
			dst.Set(x, y, img.At(srcX, srcY))
		}
	}
	return dst
}
