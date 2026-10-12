package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestScreenshotToolInputFields(t *testing.T) {
	inputJSON := `{
		"selector": "#main-content",
		"ref": "ax:15",
		"save_to_disk": true,
		"disk": true,
		"format": "png",
		"quality": 85
	}`

	var input screenshotInput
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		t.Fatalf("failed to unmarshal screenshotInput: %v", err)
	}

	if input.Selector != "#main-content" {
		t.Errorf("got selector %q, want '#main-content'", input.Selector)
	}
	if input.Ref != "ax:15" {
		t.Errorf("got ref %q, want 'ax:15'", input.Ref)
	}
	if !input.SaveToDisk {
		t.Errorf("got save_to_disk false, want true")
	}
	if !input.Disk {
		t.Errorf("got disk false, want true")
	}

	var fpInput screenshotFullPageInput
	if err := json.Unmarshal([]byte(inputJSON), &fpInput); err != nil {
		t.Fatalf("failed to unmarshal screenshotFullPageInput: %v", err)
	}
	if !fpInput.SaveToDisk || !fpInput.Disk {
		t.Errorf("expected SaveToDisk and Disk to be true in fullpage input")
	}
}

func TestScreenshot_FullPageAliasMetadata(t *testing.T) {
	cases := []struct {
		name     string
		json     string
		wantFull bool
	}{
		{"snake_case true", `{"full_page": true}`, true},
		{"camelCase true", `{"fullPage": true}`, true},
		{"snake_case false, camelCase true", `{"full_page": false, "fullPage": true}`, true},
		{"snake_case true, camelCase false", `{"full_page": true, "fullPage": false}`, true},
		{"both false", `{"full_page": false, "fullPage": false}`, false},
		{"omitted", `{}`, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var in screenshotInput
			if err := json.Unmarshal([]byte(tc.json), &in); err != nil {
				t.Fatalf("unmarshal %s failed: %v", tc.name, err)
			}
			fullPage := in.FullPage || in.FullPageCamel
			if fullPage != tc.wantFull {
				t.Errorf("got fullPage=%v, want %v", fullPage, tc.wantFull)
			}
		})
	}
}

func TestScreenshotClipProperties(t *testing.T) {
	clip := &ScreenshotClip{
		X:      10.5,
		Y:      20.5,
		Width:  300.0,
		Height: 150.0,
		Scale:  1.0,
	}

	if clip.Width != 300.0 || clip.Height != 150.0 {
		t.Errorf("unexpected clip dimensions: %+v", clip)
	}
}

func TestResolveElementClipEmptySelector(t *testing.T) {
	_, _, err := resolveElementClip("ws://mock", "")
	if err == nil {
		t.Errorf("expected error for empty selector")
	}
}

func TestScreenshotDiskStorageModePayload(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "argus-screenshot-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testPNG := createTestPNG(200, 100)
	destPath := filepath.Join(tmpDir, "test.jpg")

	imgData, mimeType, err := writeScreenshot(testPNG, destPath, "jpeg", 80, 0, false)
	if err != nil {
		t.Fatalf("writeScreenshot failed: %v", err)
	}

	meta := map[string]any{
		"url":           "https://example.com/test",
		"title":         "Test",
		"path":          destPath,
		"file_path":     destPath,
		"bytes":         len(imgData),
		"size_bytes":    len(imgData),
		"mime_type":     mimeType,
		"saved_to_disk": true,
		"clipped":       true,
		"selector":      "#my-card",
	}

	metaText, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatalf("marshal indent failed: %v", err)
	}

	res := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(metaText)},
		},
	}

	if len(res.Content) != 1 {
		t.Fatalf("expected exactly 1 content item (TextContent), got %d", len(res.Content))
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected Content[0] to be *mcp.TextContent, got %T", res.Content[0])
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	if parsed["saved_to_disk"] != true {
		t.Errorf("expected saved_to_disk=true, got %v", parsed["saved_to_disk"])
	}
	if parsed["file_path"] != destPath {
		t.Errorf("expected file_path=%q, got %v", destPath, parsed["file_path"])
	}
	if parsed["selector"] != "#my-card" {
		t.Errorf("expected selector=#my-card, got %v", parsed["selector"])
	}

	// Verify file actually exists on disk
	fi, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("screenshot file not found on disk: %v", err)
	}
	if fi.Size() == 0 {
		t.Errorf("screenshot file on disk is empty")
	}
}

func TestScreenshot_TargetDisambiguation(t *testing.T) {
	// Case 1: target specified as tab ID without selector -> targetSelector must be empty
	inTabOnly := screenshotInput{Target: "TAB_12345"}
	targetUsedAsTabID := false
	if targetID := resolveFirstNonEmpty(inTabOnly.TargetID, inTabOnly.TabID); targetID != "" {
	} else if inTabOnly.Target != "" {
		targetUsedAsTabID = true
	}
	var elemTarget string
	if !targetUsedAsTabID {
		elemTarget = inTabOnly.Target
	}
	sel := resolveSelector(inTabOnly.Ref, inTabOnly.Selector, "", elemTarget, "", "")
	if sel != "" {
		t.Fatalf("expected empty targetSelector, got %q", sel)
	}

	// Case 2: target_id specified for tab, target specified for element
	inBoth := screenshotInput{TargetID: "TAB_12345", Target: ".my-element"}
	targetUsedAsTabID2 := false
	if targetID := resolveFirstNonEmpty(inBoth.TargetID, inBoth.TabID); targetID != "" {
	} else if inBoth.Target != "" {
		targetUsedAsTabID2 = true
	}
	var elemTarget2 string
	if !targetUsedAsTabID2 {
		elemTarget2 = inBoth.Target
	}
	sel2 := resolveSelector(inBoth.Ref, inBoth.Selector, "", elemTarget2, "", "")
	if sel2 != ".my-element" {
		t.Fatalf("expected .my-element, got %q", sel2)
	}
}
