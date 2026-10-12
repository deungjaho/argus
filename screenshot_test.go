package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func createTestPNG(width, height int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestResolveScreenshotFormat(t *testing.T) {
	tests := []struct {
		format  string
		outPath string
		want    string
	}{
		{"png", "", "png"},
		{"PNG", "", "png"},
		{"image/png", "", "png"},
		{"jpeg", "", "jpeg"},
		{"jpg", "", "jpeg"},
		{"JPEG", "", "jpeg"},
		{"image/jpeg", "", "jpeg"},
		{"image/jpg", "", "jpeg"},
		{"", "test.png", "png"},
		{"", "/path/to/test.PNG", "png"},
		{"", "test.jpg", "jpeg"},
		{"", "test.jpeg", "jpeg"},
		{"", "test.unknown", "jpeg"},
		{"", "", "jpeg"},
		{"webp", "", "jpeg"}, // tolerance fallback
	}

	for _, tt := range tests {
		got := resolveScreenshotFormat(tt.format, tt.outPath)
		if got != tt.want {
			t.Errorf("resolveScreenshotFormat(%q, %q) = %q, want %q", tt.format, tt.outPath, got, tt.want)
		}
	}
}

func TestResolveScreenshotPath(t *testing.T) {
	// 1. Empty path defaults to temp dir with .jpg
	p1, err := resolveScreenshotPath("", "")
	if err != nil {
		t.Fatalf("resolveScreenshotPath empty failed: %v", err)
	}
	if !strings.HasPrefix(p1, os.TempDir()) || !strings.HasSuffix(p1, ".jpg") {
		t.Errorf("expected temp jpg path, got %q", p1)
	}

	// 2. Empty path with png format defaults to temp dir with .png
	p2, err := resolveScreenshotPath("", "png")
	if err != nil {
		t.Fatalf("resolveScreenshotPath empty png failed: %v", err)
	}
	if !strings.HasPrefix(p2, os.TempDir()) || !strings.HasSuffix(p2, ".png") {
		t.Errorf("expected temp png path, got %q", p2)
	}

	// 3. User home dir expansion to specific file
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		p3, err := resolveScreenshotPath("~/my-shot.jpg", "")
		if err != nil {
			t.Fatalf("resolveScreenshotPath home failed: %v", err)
		}
		expected := filepath.Join(home, "my-shot.jpg")
		if p3 != expected {
			t.Errorf("expected %q, got %q", expected, p3)
		}
	}

	// 4. Custom clean path
	custom := filepath.Join(os.TempDir(), "subdir", "shot.png")
	p4, err := resolveScreenshotPath(custom, "")
	if err != nil {
		t.Fatalf("resolveScreenshotPath custom failed: %v", err)
	}
	if p4 != custom {
		t.Errorf("expected %q, got %q", custom, p4)
	}

	// 5. Directory path generates timestamped file inside directory
	tmpDir := t.TempDir()
	p5, err := resolveScreenshotPath(tmpDir, "png")
	if err != nil {
		t.Fatalf("resolveScreenshotPath dir failed: %v", err)
	}
	if !strings.HasPrefix(p5, tmpDir) || !strings.HasSuffix(p5, ".png") {
		t.Errorf("expected file inside dir %q with .png, got %q", tmpDir, p5)
	}

	// 6. Path with trailing separator generates timestamped file
	dirWithSlash := tmpDir + string(filepath.Separator)
	p6, err := resolveScreenshotPath(dirWithSlash, "jpeg")
	if err != nil {
		t.Fatalf("resolveScreenshotPath trailing slash failed: %v", err)
	}
	if !strings.HasPrefix(p6, tmpDir) || !strings.HasSuffix(p6, ".jpg") {
		t.Errorf("expected file inside dir %q with .jpg, got %q", tmpDir, p6)
	}

	// 7. Relative path is converted to absolute path
	p7, err := resolveScreenshotPath("relative-shot.png", "")
	if err != nil {
		t.Fatalf("resolveScreenshotPath relative failed: %v", err)
	}
	if !filepath.IsAbs(p7) {
		t.Errorf("expected absolute path for relative input, got %q", p7)
	}
}

func TestWriteScreenshotJPEG(t *testing.T) {
	rawPNG := createTestPNG(200, 200)
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "nested", "shot.jpg")

	data, mimeType, err := writeScreenshot(rawPNG, outPath, "jpeg", 75, 0)
	if err != nil {
		t.Fatalf("writeScreenshot JPEG failed: %v", err)
	}
	if mimeType != "image/jpeg" {
		t.Errorf("mimeType = %q, want image/jpeg", mimeType)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("output file does not exist: %v", err)
	}
	w, h, ok := imageDimensions(data)
	if !ok || w != 200 || h != 200 {
		t.Errorf("unexpected image dimensions: %dx%d (ok=%v)", w, h, ok)
	}
}

func TestWriteScreenshotPNG(t *testing.T) {
	rawPNG := createTestPNG(300, 150)
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "nested", "shot.png")

	data, mimeType, err := writeScreenshot(rawPNG, outPath, "png", 0, 0)
	if err != nil {
		t.Fatalf("writeScreenshot PNG failed: %v", err)
	}
	if mimeType != "image/png" {
		t.Errorf("mimeType = %q, want image/png", mimeType)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("output file does not exist: %v", err)
	}
	w, h, ok := imageDimensions(data)
	if !ok || w != 300 || h != 150 {
		t.Errorf("unexpected image dimensions: %dx%d (ok=%v)", w, h, ok)
	}
}

func TestDownscalePNGAndJPEG(t *testing.T) {
	rawPNG := createTestPNG(2000, 1000)

	// JPEG with maxSize=500
	jpegData, err := encodeScreenshotJPEGWithParams(rawPNG, 80, 500)
	if err != nil {
		t.Fatalf("encodeScreenshotJPEGWithParams failed: %v", err)
	}
	jw, jh, ok := imageDimensions(jpegData)
	if !ok {
		t.Fatal("failed to decode JPEG dimensions")
	}
	if jw > 500 || jh > 500 {
		t.Errorf("JPEG dimensions %dx%d exceed maxSize 500", jw, jh)
	}

	// PNG with maxSize=400
	pngData, err := encodeScreenshotPNG(rawPNG, 400)
	if err != nil {
		t.Fatalf("encodeScreenshotPNG failed: %v", err)
	}
	pw, ph, ok := imageDimensions(pngData)
	if !ok {
		t.Fatal("failed to decode PNG dimensions")
	}
	if pw > 400 || ph > 400 {
		t.Errorf("PNG dimensions %dx%d exceed maxSize 400", pw, ph)
	}
}

func TestFullPageDownscalePreservesHeight(t *testing.T) {
	// A long webpage: 1200 width x 4000 height.
	// For fullPage, width is within 1400 max_width, so height must NOT be crushed to 1400!
	rawPNG := createTestPNG(1200, 4000)

	// 1. Full page JPEG: height preserved
	jpegFull, err := encodeScreenshotJPEGWithOptions(rawPNG, 80, 1400, true)
	if err != nil {
		t.Fatalf("encodeScreenshotJPEGWithOptions fullPage failed: %v", err)
	}
	w, h, ok := imageDimensions(jpegFull)
	if !ok || w != 1200 || h != 4000 {
		t.Errorf("full-page JPEG dimensions = %dx%d, want 1200x4000", w, h)
	}

	// 2. Viewport JPEG (fullPage=false): bounded by 1400 on both dimensions
	jpegView, err := encodeScreenshotJPEGWithOptions(rawPNG, 80, 1400, false)
	if err != nil {
		t.Fatalf("encodeScreenshotJPEGWithOptions viewport failed: %v", err)
	}
	vw, vh, ok := imageDimensions(jpegView)
	if !ok || vw > 1400 || vh > 1400 {
		t.Errorf("viewport JPEG dimensions = %dx%d exceed 1400", vw, vh)
	}

	// 3. Full page PNG: height preserved
	pngFull, err := encodeScreenshotPNGWithOptions(rawPNG, 1400, true)
	if err != nil {
		t.Fatalf("encodeScreenshotPNGWithOptions fullPage failed: %v", err)
	}
	pw, ph, ok := imageDimensions(pngFull)
	if !ok || pw != 1200 || ph != 4000 {
		t.Errorf("full-page PNG dimensions = %dx%d, want 1200x4000", pw, ph)
	}

	// 4. Full page with retina scaling (2400 x 8000 scaled down to max_width 1200):
	// width becomes 1200, height scales proportionally to 4000 (NOT 1200!)
	rawRetina := createTestPNG(2400, 8000)
	retinaData, err := encodeScreenshotJPEGWithOptions(rawRetina, 80, 1200, true)
	if err != nil {
		t.Fatalf("encode retina fullPage failed: %v", err)
	}
	rw, rh, ok := imageDimensions(retinaData)
	if !ok || rw != 1200 || rh != 4000 {
		t.Errorf("retina full-page dimensions = %dx%d, want 1200x4000", rw, rh)
	}
}

func TestScreenshotToolSchemas(t *testing.T) {
	cs := setupMCPWithToolsets(t, "full")
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	var screenshotTool *mcp.Tool
	for _, tool := range res.Tools {
		if tool.Name == "screenshot" {
			screenshotTool = tool
		}
	}

	if screenshotTool == nil {
		t.Fatal("screenshot tool not found")
	}

	checkToolSchemaProperties := func(tool *mcp.Tool, expectedProps ...string) {
		t.Helper()
		schemaBytes, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal inputSchema failed: %v", err)
		}
		var schemaMap struct {
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(schemaBytes, &schemaMap); err != nil {
			t.Fatalf("unmarshal inputSchema failed: %v", err)
		}
		for _, prop := range expectedProps {
			if _, exists := schemaMap.Properties[prop]; !exists {
				t.Errorf("tool %q schema missing property %q. Schema: %s", tool.Name, prop, string(schemaBytes))
			}
		}
	}

	expected := []string{
		"path", "file_path", "format", "type", "quality", "full_page", "max_width", "max_size_kb",
	}
	checkToolSchemaProperties(screenshotTool, append(expected, "grid", "major_step", "minor_step")...)
}

func TestScreenshotToolCallSchemaValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping tool execution schema test in short mode")
	}

	origURL, origPort, origAttach := getConfig()
	dummyPort := 59999
	dummyAttach := true
	setConfig("", dummyPort, &dummyAttach)
	defer setConfig(origURL, origPort, &origAttach)

	cs := setupMCPWithToolsets(t, "full")

	// Call screenshot with various client aliases and parameters
	_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "screenshot",
		Arguments: map[string]any{
			"path":        "/tmp/test.png",
			"file_path":   "/tmp/test.png",
			"format":      "png",
			"type":        "png",
			"quality":     90,
			"full_page":   false,
			"max_width":   1200,
			"grid":        false,
			"major_step":  100,
			"minor_step":  25,
			"max_size_kb": 1000,
		},
	})
	if err != nil {
		if strings.Contains(err.Error(), "-32602") || strings.Contains(err.Error(), "unexpected additional properties") {
			t.Fatalf("screenshot failed schema validation with -32602: %v", err)
		}
	}

	// Call screenshot with full_page=true
	_, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "screenshot",
		Arguments: map[string]any{
			"path":        "/tmp/test-full.jpg",
			"file_path":   "/tmp/test-full.jpg",
			"format":      "jpeg",
			"type":        "jpeg",
			"quality":     85,
			"full_page":   true,
			"max_width":   1200,
			"grid":        false,
			"major_step":  100,
			"minor_step":  25,
			"max_size_kb": 1500,
		},
	})
	if err != nil {
		if strings.Contains(err.Error(), "-32602") || strings.Contains(err.Error(), "unexpected additional properties") {
			t.Fatalf("screenshot full_page failed schema validation with -32602: %v", err)
		}
	}
}
